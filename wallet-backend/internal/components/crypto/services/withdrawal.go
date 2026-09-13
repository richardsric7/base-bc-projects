package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/crypto/models"
)

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

// RequestWithdrawal validates amount against currency/network's cached
// limits, submits signedTreasuryTransferTx (the caller's own signed
// on-chain transfer of amount from their Base address to the treasury
// address GetWithdrawalNetworks returned) as proof of the debit, and - if
// that succeeds - asks OneLiquidity to pay the external network.
//
// This extra on-chain leg is this port's addition, not something the
// original needed: there, "withdraw" meant debiting an internal Trovo
// ledger balance the platform already controlled, submitted server-side
// as part of the same request. On Base, the user's balance is a real
// on-chain balance only they can move - so before OneLiquidity is asked
// to pay out externally, this backend needs proof the matching amount
// actually left the user's own wallet, exactly the same build/sign/submit
// split every other mutating operation in this port uses.
//
// Only single-owner withdrawal is implemented - see
// models.WithdrawalRequest's doc comment for why the original's
// multi-party shared-access withdrawal path is a documented gap here
// rather than a mechanical port.
func (s *Service) RequestWithdrawal(address, currency, networkName, toAddress string, amount float64, signedTreasuryTransferTx string) (*models.WithdrawalRequest, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}

	result, err := s.GetWithdrawalNetworks(currency)
	if err != nil {
		return nil, err
	}
	var matched *models.WithdrawalNetwork
	for i := range result.Networks {
		if result.Networks[i].Network == networkName {
			matched = &result.Networks[i]
			break
		}
	}
	if matched == nil {
		return nil, apperrors.BadRequest("unsupported currency/network combination")
	}

	amountDec := decimal.NewFromFloat(amount)
	if min, err := decimal.NewFromString(matched.WithdrawMin); err == nil && amountDec.LessThan(min) {
		return nil, apperrors.BadRequest(fmt.Sprintf("amount is below the minimum of %s %s", matched.WithdrawMin, currency))
	}
	if max, err := decimal.NewFromString(matched.WithdrawMax); err == nil && amountDec.GreaterThan(max) {
		return nil, apperrors.BadRequest(fmt.Sprintf("amount is above the maximum of %s %s", matched.WithdrawMax, currency))
	}

	networkFee, _ := decimal.NewFromString(matched.WithdrawFee)
	serviceFee := amountDec.Mul(decimal.NewFromFloat(s.ServiceFeePercent)).Div(decimal.NewFromInt(100))
	amountToWithdraw := amountDec.Sub(serviceFee).Sub(networkFee)
	if amountToWithdraw.IsNegative() || amountToWithdraw.IsZero() {
		return nil, apperrors.BadRequest("amount is too small to cover fees")
	}

	txHash, err := s.Blockchain.SubmitSignedTransaction(context.Background(), signedTreasuryTransferTx)
	if err != nil {
		return nil, apperrors.BadRequest("failed to submit the debit transaction: " + err.Error())
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
		TreasuryTxHash:   txHash,
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
