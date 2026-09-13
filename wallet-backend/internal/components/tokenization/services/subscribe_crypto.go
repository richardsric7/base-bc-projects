package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"time"

	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
	"wallet-backend/internal/network"
)

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

// BuildCryptoPurchase builds the unsigned Sale.buy(assetAmount) call for
// the buyer to sign. Sale.sol (Phase 8) is the atomic one-transaction
// purchase upstream achieved via a PathPaymentStrictSend against its own
// standing DEX offer - the buyer must have already approve()'d the sale
// contract for the payment amount (built via the assets component's
// existing generic approve endpoint; this component builds only the buy()
// call itself, matching Phase 7 market's precedent of never pre-checking
// allowance server-side - an under-approved buy() simply reverts on-chain,
// which is the buyer's own problem to fix and retry).
func (s *Service) BuildCryptoPurchase(ctx context.Context, buyerUserID uint, assetID uint, quantity decimal.Decimal) (*network.UnsignedTx, decimal.Decimal, error) {
	asset, err := s.getAsset(assetID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	if asset.SaleContractAddress == nil {
		return nil, decimal.Zero, apperrors.Conflict("this asset has no active sale contract")
	}
	if quantity.LessThanOrEqual(decimal.Zero) {
		return nil, decimal.Zero, apperrors.BadRequest("quantity must be positive")
	}

	pricePerToken, err := decimal.NewFromString(asset.PricePerToken)
	if err != nil {
		return nil, decimal.Zero, apperrors.Internal("invalid pricePerToken on this asset")
	}
	paymentAmount := quantity.Mul(pricePerToken)

	if err := s.enforceCapAndStatus(asset, buyerUserID, quantity, paymentAmount); err != nil {
		return nil, decimal.Zero, err
	}

	buyer, err := s.getUserByID(buyerUserID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	if err := s.enforceOfferingAccess(asset, buyer.Address); err != nil {
		return nil, decimal.Zero, err
	}

	data, err := contracts.EncodeBuy(toBaseUnits(quantity, asset.AssetDecimals))
	if err != nil {
		return nil, decimal.Zero, apperrors.Internal("failed to encode purchase")
	}
	tx, err := s.Blockchain.BuildContractCallTx(ctx, buyer.Address, *asset.SaleContractAddress, big.NewInt(0), data, nil)
	if err != nil {
		return nil, decimal.Zero, apperrors.Internal("failed to build purchase transaction: " + err.Error())
	}
	return tx, paymentAmount, nil
}

// RecordCryptoPurchase records a self-submitted Sale.buy() as a completed
// subscription once the buyer reports its transaction hash. Trusting a
// client-submitted hash after building the exact calldata server-side is
// the same posture this codebase already takes for every other
// self-signed on-chain action (see market/crypto components).
func (s *Service) RecordCryptoPurchase(buyerUserID, assetID uint, quantity decimal.Decimal, txHash string) (*models.TokenizedAssetSubscription, error) {
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
		TxHash:             txHash,
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
