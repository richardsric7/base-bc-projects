package services

import (
	"context"
	"fmt"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/crypto/models"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/network"
)

// WithdrawalProposal is what BuildWithdrawal returns: the real on-chain
// SafeTxHash digest (see sharedaccess.DigestToSign) the caller's signer
// key must personal_sign to approve the treasury debit, and the
// PendingAction id that signature approves.
type WithdrawalProposal struct {
	ActionID     uint   `json:"actionId"`
	DigestToSign string `json:"digestToSign"`
}

// WithdrawalNetworksResult bundles a currency's cached withdrawal limits
// with the treasury address a caller must send their debit transfer to -
// a client needs this address to build the transfer RequestWithdrawal
// requires proof of (see that method's doc comment).
type WithdrawalNetworksResult struct {
	Networks        []models.WithdrawalNetwork `json:"networks"`
	TreasuryAddress string                     `json:"treasuryAddress"`
}

// GetWithdrawalNetworks returns currency's cached withdrawal
// limits/fee-per-network, refreshing from OneLiquidity if nothing is
// cached yet. Ported from the original's GetWithdrawalNetworks, minus its
// Redis caching layer (the DB row itself is the cache here - a
// call-time-refresh-if-empty policy rather than a timed TTL, since
// networks/limits change rarely enough that this is simpler for
// equivalent effect).
func (s *Service) GetWithdrawalNetworks(currency string) (*WithdrawalNetworksResult, error) {
	var networks []models.WithdrawalNetwork
	s.DB.Where("currency = ?", currency).Find(&networks)
	if len(networks) == 0 {
		if err := s.refreshWithdrawalNetworks(currency); err != nil {
			return nil, err
		}
		s.DB.Where("currency = ?", currency).Find(&networks)
	}

	treasury, err := s.treasuryAddress()
	if err != nil {
		return nil, apperrors.Internal("failed to derive treasury address")
	}
	return &WithdrawalNetworksResult{Networks: networks, TreasuryAddress: treasury}, nil
}

func (s *Service) refreshWithdrawalNetworks(currency string) error {
	var resp models.WithdrawalNetworksResponse
	if err := s.doRequest("GET", "/wallets/v1/withdrawal/networks?currency="+currency, nil, &resp); err != nil {
		return err
	}
	for _, item := range resp.Data {
		network := models.WithdrawalNetwork{
			Currency:    currency,
			Network:     item.Network,
			WithdrawMin: item.WithdrawMin,
			WithdrawMax: item.WithdrawMax,
			WithdrawFee: item.WithdrawFee,
		}
		s.DB.Where(models.WithdrawalNetwork{Currency: currency, Network: item.Network}).
			Assign(network).
			FirstOrCreate(&network)
	}
	return nil
}

// validateWithdrawalAmount is the currency/network limit and fee math
// BuildWithdrawal and ConfirmWithdrawal both need against the same
// GetWithdrawalNetworks lookup - split out so the two agree exactly on
// what a given amount actually costs.
func (s *Service) validateWithdrawalAmount(currency, networkName string, amount float64) (networkFee, serviceFee, amountToWithdraw decimal.Decimal, err error) {
	result, err := s.GetWithdrawalNetworks(currency)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, err
	}
	var matched *models.WithdrawalNetwork
	for i := range result.Networks {
		if result.Networks[i].Network == networkName {
			matched = &result.Networks[i]
			break
		}
	}
	if matched == nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, apperrors.BadRequest("unsupported currency/network combination")
	}

	amountDec := decimal.NewFromFloat(amount)
	if min, err := decimal.NewFromString(matched.WithdrawMin); err == nil && amountDec.LessThan(min) {
		return decimal.Zero, decimal.Zero, decimal.Zero, apperrors.BadRequest(fmt.Sprintf("amount is below the minimum of %s %s", matched.WithdrawMin, currency))
	}
	if max, err := decimal.NewFromString(matched.WithdrawMax); err == nil && amountDec.GreaterThan(max) {
		return decimal.Zero, decimal.Zero, decimal.Zero, apperrors.BadRequest(fmt.Sprintf("amount is above the maximum of %s %s", matched.WithdrawMax, currency))
	}

	networkFee, _ = decimal.NewFromString(matched.WithdrawFee)
	serviceFee = amountDec.Mul(decimal.NewFromFloat(s.ServiceFeePercent)).Div(decimal.NewFromInt(100))
	amountToWithdraw = amountDec.Sub(serviceFee).Sub(networkFee)
	if amountToWithdraw.IsNegative() || amountToWithdraw.IsZero() {
		return decimal.Zero, decimal.Zero, decimal.Zero, apperrors.BadRequest("amount is too small to cover fees")
	}
	return networkFee, serviceFee, amountToWithdraw, nil
}

// BuildWithdrawal validates amount against currency/network's cached
// limits and proposes the on-chain debit (a transfer of amount from the
// caller's own primary wallet to the treasury address GetWithdrawalNetworks
// returned) as a real Safe transaction, returning the digest signerAddress
// must personal_sign to approve it.
//
// This extra on-chain leg is this port's addition, not something the
// original needed: there, "withdraw" meant debiting an internal Trovo
// ledger balance the platform already controlled, submitted server-side
// as part of the same request. On Base, the user's balance is a real
// on-chain balance only a Safe transaction from that wallet can move - so
// before OneLiquidity is asked to pay out externally, this backend needs
// proof the matching amount actually left the user's own wallet. This can
// no longer build a plain unsigned transaction or trust a client-supplied
// signed one directly - see tokenization.GroupWalletExecutor's doc
// comment for the identical bug this component originally shared.
//
// Only single-owner withdrawal is implemented - see
// models.WithdrawalRequest's doc comment for why the original's
// multi-party shared-access withdrawal path is a documented gap here
// rather than a mechanical port.
func (s *Service) BuildWithdrawal(ctx context.Context, address, currency, networkName string, amount float64, signerAddress string) (*WithdrawalProposal, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("withdrawals are not available: shared-access wiring is missing")
	}
	if _, _, _, err := s.validateWithdrawalAmount(currency, networkName, amount); err != nil {
		return nil, err
	}

	treasury, err := s.treasuryAddress()
	if err != nil {
		return nil, apperrors.Internal("failed to derive treasury address")
	}

	group, err := s.SharedAccess.GetGroupByAddress(address)
	if err != nil {
		return nil, err
	}

	var contractAddress, dataHex, valueWei string
	if token := s.findCuratedToken(currency); token != nil {
		amountBase := toBaseUnits(decimal.NewFromFloat(amount), token.Decimals)
		data, err := network.EncodeERC20Transfer(treasury, amountBase)
		if err != nil {
			return nil, apperrors.Internal("failed to encode withdrawal debit")
		}
		contractAddress = token.ContractAddress
		dataHex = "0x" + ethcommon.Bytes2Hex(data)
		valueWei = "0"
	} else {
		// No curated ERC-20 for this currency - treat it as the chain's
		// native asset, a plain value transfer to the treasury address.
		amountBase := toBaseUnits(decimal.NewFromFloat(amount), 18)
		contractAddress = treasury
		dataHex = "0x"
		valueWei = amountBase.String()
	}

	action, err := s.SharedAccess.ProposeContractCall(ctx, signerAddress, group.ID, sharedaccessModels.ActionContractCall, "crypto withdrawal debit", contractAddress, valueWei, dataHex, "crypto-withdrawal", "")
	if err != nil {
		return nil, err
	}
	digest, err := s.SharedAccess.DigestToSign(action.ID, signerAddress)
	if err != nil {
		return nil, err
	}
	return &WithdrawalProposal{ActionID: action.ID, DigestToSign: digest}, nil
}

// ConfirmWithdrawal approves actionID (proposed via BuildWithdrawal) with
// signerAddress's personal_sign signature over its digest, executing the
// treasury debit immediately once the group's approval threshold is met,
// and - if that succeeds - asks OneLiquidity to pay the external network,
// exactly as this component did once its old self-submitted-transaction
// step succeeded.
func (s *Service) ConfirmWithdrawal(ctx context.Context, address, currency, networkName, toAddress string, amount float64, actionID uint, signerAddress, signature string) (*models.WithdrawalRequest, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("withdrawals are not available: shared-access wiring is missing")
	}
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	networkFee, serviceFee, amountToWithdraw, err := s.validateWithdrawalAmount(currency, networkName, amount)
	if err != nil {
		return nil, err
	}

	action, err := s.SharedAccess.ApproveAction(ctx, actionID, signerAddress, signature)
	if err != nil {
		return nil, err
	}
	if action.Status != sharedaccessModels.ActionExecuted {
		return nil, apperrors.BadRequest("withdrawal debit is not yet executed (status: " + string(action.Status) + ") - approve again once outstanding approvals are collected")
	}

	request := models.WithdrawalRequest{
		ID:               uuid.NewString(),
		UserID:           user.ID,
		Currency:         currency,
		Network:          networkName,
		ToAddress:        toAddress,
		AmountSubmitted:  amount,
		ServiceFeeAmount: serviceFee.InexactFloat64(),
		NetworkFeeAmount: networkFee.InexactFloat64(),
		AmountToWithdraw: amountToWithdraw.InexactFloat64(),
		TreasuryTxHash:   action.TxHash,
		Status:           "PENDING",
	}
	if err := s.DB.Create(&request).Error; err != nil {
		return nil, apperrors.Internal("failed to save withdrawal request")
	}

	var resp models.WithdrawalResponse
	payload := models.WithdrawalRequestPayload{
		Currency:  currency,
		Amount:    amountToWithdraw.InexactFloat64(),
		ToAddress: toAddress,
		Network:   networkName,
		Reference: request.ID,
	}
	if err := s.doRequest("POST", "/wallets/v1/withdrawal", payload, &resp); err != nil {
		// The on-chain debit already happened and is recorded - leave the
		// request PENDING for a manual/future retry against OneLiquidity
		// rather than losing track of it.
		return nil, err
	}

	request.OneLiquidityWithdrawalID = resp.Data.WithdrawalID
	request.Status = resp.Data.Status
	if err := s.DB.Save(&request).Error; err != nil {
		return nil, apperrors.Internal("failed to save withdrawal status")
	}
	return &request, nil
}

// ListWithdrawals returns the caller's withdrawal history.
func (s *Service) ListWithdrawals(address string) ([]models.WithdrawalRequest, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	var requests []models.WithdrawalRequest
	if err := s.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&requests).Error; err != nil {
		return nil, apperrors.Internal("failed to load withdrawal history")
	}
	return requests, nil
}
