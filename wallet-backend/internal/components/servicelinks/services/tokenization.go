package services

import (
	"context"

	"github.com/shopspring/decimal"

	tokenizationModels "wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/network"
)

// This file is a thin CanManageTokenization-gated passthrough onto the
// tokenization component for a service link's own onboarded users (see
// controllers for the capability gate). It adds nothing beyond the
// requireOwnedUser scoping every servicelinks route applies - the
// tokenization component's own services already implement every rule
// (offering access, subscription caps, sale-status checks).

// AssetInfo looks up a tokenized asset's public details - not scoped to an
// owned user since it's the same information CanLookupTokenInfo/the public
// tokenization endpoints already expose.
func (s *Service) AssetInfo(assetID uint) (*tokenizationModels.TokenizedAsset, error) {
	return s.Tokenization.GetByID(assetID)
}

// BuildPartnerTokenPurchase builds an unsigned on-chain purchase for an
// owned user buying into an active primary or secondary sale.
func (s *Service) BuildPartnerTokenPurchase(ctx context.Context, serviceLinkID, userID, assetID uint, quantity decimal.Decimal) (*network.UnsignedTx, decimal.Decimal, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, decimal.Zero, err
	}
	return s.Tokenization.BuildCryptoPurchase(ctx, userID, assetID, quantity)
}

// RecordPartnerTokenPurchase records a purchase the partner already had
// signed and submitted on the owned user's behalf.
func (s *Service) RecordPartnerTokenPurchase(serviceLinkID, userID, assetID uint, quantity decimal.Decimal, txHash string) (*tokenizationModels.TokenizedAssetSubscription, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.Tokenization.RecordCryptoPurchase(userID, assetID, quantity, txHash)
}

// PartnerTokenSubscriptions lists an owned user's tokenization
// subscriptions across every asset.
func (s *Service) PartnerTokenSubscriptions(serviceLinkID, userID uint) ([]tokenizationModels.TokenizedAssetSubscription, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.Tokenization.ListMySubscriptions(userID)
}
