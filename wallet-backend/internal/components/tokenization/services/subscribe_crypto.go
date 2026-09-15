package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
)

// PurchaseProposal is what BuildCryptoPurchase returns: the real on-chain
// SafeTxHash digest (see sharedaccess.DigestToSign) the buyer's signer key
// must personal_sign to approve the purchase, the PendingAction id that
// signature approves, and the fiat-equivalent payment amount this
// purchase will cost (informational, unchanged from before this
// component was reworked to delegate to sharedaccess - see
// GroupWalletExecutor's doc comment).
type PurchaseProposal struct {
	ActionID      uint            `json:"actionId"`
	DigestToSign  string          `json:"digestToSign"`
	PaymentAmount decimal.Decimal `json:"paymentAmount"`
}

// enforceCapAndStatus applies the two checks every purchase path (crypto
// and fiat) shares: the asset must be in a sale-open status, and - if a
// purchase cap is configured and still within its window - this purchase
// plus the buyer's prior purchases must not exceed it.
func (s *Service) enforceCapAndStatus(asset *models.TokenizedAsset, buyerUserID uint, quantity decimal.Decimal, paymentAmount decimal.Decimal) error {
	if asset.Status != models.StatusPrimarySaleActive && asset.Status != models.StatusSecondarySaleActive {
		return apperrors.Conflict("this asset is not currently open for purchase")
	}
	if !asset.CapOnPurchase || asset.SalesStart == nil {
		return nil
	}
	capEnd := asset.SalesStart.AddDate(0, 0, asset.CapDurationInDays)
	if time.Now().After(capEnd) {
		return nil
	}
	capAmount, err := decimal.NewFromString(asset.CapAmountInFiat)
	if err != nil || capAmount.LessThanOrEqual(decimal.Zero) {
		return nil
	}
	var priorTotal decimal.Decimal
	var subs []models.TokenizedAssetSubscription
	s.DB.Where("tokenized_asset_id = ? AND subscriber_user_id = ?", asset.ID, buyerUserID).Find(&subs)
	for _, sub := range subs {
		amt, err := decimal.NewFromString(sub.PaymentAmount)
		if err == nil {
			priorTotal = priorTotal.Add(amt)
		}
	}
	if priorTotal.Add(paymentAmount).GreaterThan(capAmount) {
		return apperrors.BadRequest("this purchase would exceed your cap for this asset")
	}
	return nil
}

// BuildCryptoPurchase proposes the Sale.buy(assetAmount) call as a real
// Safe transaction against the buyer's own primary wallet and returns the
// digest signerAddress must personal_sign to approve it (see
// GroupWalletExecutor's doc comment on why this can no longer build a
// plain unsigned transaction the way it originally did). Sale.sol
// (Phase 8) is the atomic one-transaction purchase upstream achieved via
// a PathPaymentStrictSend against its own standing DEX offer - the buyer
// must have already approve()'d the sale contract for the payment amount
// (built via the assets component's existing generic approve endpoint;
// this component proposes only the buy() call itself, matching Phase 7
// market's precedent of never pre-checking allowance server-side - an
// under-approved buy() simply reverts on-chain, which is the buyer's own
// problem to fix and retry).
func (s *Service) BuildCryptoPurchase(ctx context.Context, buyerUserID uint, assetID uint, quantity decimal.Decimal, signerAddress string) (*PurchaseProposal, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("crypto purchases are not available: shared-access wiring is missing")
	}
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	if asset.SaleContractAddress == nil {
		return nil, apperrors.Conflict("this asset has no active sale contract")
	}
	if quantity.LessThanOrEqual(decimal.Zero) {
		return nil, apperrors.BadRequest("quantity must be positive")
	}

	pricePerToken, err := decimal.NewFromString(asset.PricePerToken)
	if err != nil {
		return nil, apperrors.Internal("invalid pricePerToken on this asset")
	}
	paymentAmount := quantity.Mul(pricePerToken)

	if err := s.enforceCapAndStatus(asset, buyerUserID, quantity, paymentAmount); err != nil {
		return nil, err
	}

	buyer, err := s.getUserByID(buyerUserID)
	if err != nil {
		return nil, err
	}
	if err := s.requireBuyerKYC(buyer); err != nil {
		return nil, err
	}
	if err := s.enforceOfferingAccess(asset, buyer.Address); err != nil {
		return nil, err
	}
	// Every tokenized asset is a restricted security - the buyer's wallet
	// must be authorized to hold it before Sale.buy() can transfer any to
	// them, or the on-chain transfer will revert (TokenizedAsset.sol's own
	// doc comment). Authorizing here, now that KYC has just been
	// confirmed, mirrors the original's own subscription flow bundling
	// trustline authorization into the purchase.
	if err := s.authorizeHolder(ctx, asset, buyer.Address); err != nil {
		return nil, err
	}
	// The internal-balance/quote-currency asset the buyer's payment is
	// denominated in is itself restricted (PLAN.md §22.4, internal_balance.go)
	// - mirrors upstream's checkDistributionWalletHasQuoteCurrencyAuthorization
	// gate, extended to the buyer the same way tokenized-asset
	// authorization was. A no-op for a country with no internal-balance
	// asset deployed.
	if err := s.AuthorizeInternalBalanceHolder(ctx, asset.AssetCountryLocation, buyer.Address); err != nil {
		return nil, err
	}

	data, err := contracts.EncodeBuy(toBaseUnits(quantity, asset.AssetDecimals))
	if err != nil {
		return nil, apperrors.Internal("failed to encode purchase")
	}
	group, err := s.SharedAccess.GetGroupByAddress(buyer.Address)
	if err != nil {
		return nil, err
	}
	dataHex := "0x" + common.Bytes2Hex(data)
	action, err := s.SharedAccess.ProposeContractCall(ctx, signerAddress, group.ID, sharedaccessModels.ActionContractCall, "tokenization purchase", *asset.SaleContractAddress, "0", dataHex, "tokenization", uintToString(assetID))
	if err != nil {
		return nil, err
	}
	digest, err := s.SharedAccess.DigestToSign(action.ID, signerAddress)
	if err != nil {
		return nil, err
	}
	return &PurchaseProposal{ActionID: action.ID, DigestToSign: digest, PaymentAmount: paymentAmount}, nil
}

// ConfirmCryptoPurchase approves actionID (proposed via BuildCryptoPurchase)
// with signerAddress's personal_sign signature over its digest, executing
// the purchase immediately once the group's approval threshold is met -
// always true for an ordinary primary-wallet purchase (threshold 1, sole
// owner) - and records the resulting subscription using the real
// on-chain transaction hash from the executed action, rather than
// trusting a client-reported hash the way this component originally did
// (see GroupWalletExecutor's doc comment).
func (s *Service) ConfirmCryptoPurchase(ctx context.Context, buyerUserID, assetID uint, quantity decimal.Decimal, actionID uint, signerAddress, signature string) (*models.TokenizedAssetSubscription, error) {
	if s.SharedAccess == nil {
		return nil, apperrors.Internal("crypto purchases are not available: shared-access wiring is missing")
	}
	action, err := s.SharedAccess.ApproveAction(ctx, actionID, signerAddress, signature)
	if err != nil {
		return nil, err
	}
	if action.Status != sharedaccessModels.ActionExecuted {
		return nil, apperrors.BadRequest("purchase is not yet executed (status: " + string(action.Status) + ") - approve again once outstanding approvals are collected")
	}

	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, err
	}
	buyer, err := s.getUserByID(buyerUserID)
	if err != nil {
		return nil, err
	}
	pricePerToken, err := decimal.NewFromString(asset.PricePerToken)
	if err != nil {
		return nil, apperrors.Internal("invalid pricePerToken on this asset")
	}
	sub := models.TokenizedAssetSubscription{
		ID:                 randomID(),
		TokenizedAssetID:   assetID,
		SubscriberUserID:   buyerUserID,
		SubscriberAddress:  buyer.Address,
		Quantity:           quantity.String(),
		PaymentAmount:      quantity.Mul(pricePerToken).String(),
		PaymentAssetSymbol: asset.AssetQuoteCurrency,
		TxHash:             action.TxHash,
		Channel:            "CRYPTO",
	}
	if err := s.DB.Create(&sub).Error; err != nil {
		return nil, apperrors.Internal("failed to record purchase")
	}
	return &sub, nil
}

// randomID generates a short random hex identifier for records (like
// TokenizedAssetSubscription) that need a caller-agnostic primary key.
func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ListSubscriptions lists purchase records for one asset.
func (s *Service) ListSubscriptions(assetID uint) ([]models.TokenizedAssetSubscription, error) {
	var subs []models.TokenizedAssetSubscription
	if err := s.DB.Where("tokenized_asset_id = ?", assetID).Order("created_at DESC").Find(&subs).Error; err != nil {
		return nil, apperrors.Internal("failed to load subscriptions")
	}
	return subs, nil
}

// ListMySubscriptions lists one buyer's own purchase history across all assets.
func (s *Service) ListMySubscriptions(buyerUserID uint) ([]models.TokenizedAssetSubscription, error) {
	var subs []models.TokenizedAssetSubscription
	if err := s.DB.Where("subscriber_user_id = ?", buyerUserID).Order("created_at DESC").Find(&subs).Error; err != nil {
		return nil, apperrors.Internal("failed to load subscriptions")
	}
	return subs, nil
}
