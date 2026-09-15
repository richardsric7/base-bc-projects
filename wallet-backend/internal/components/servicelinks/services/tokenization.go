package services

import (
	"context"

	"github.com/shopspring/decimal"

	tokenizationModels "wallet-backend/internal/components/tokenization/models"
	tokenizationServices "wallet-backend/internal/components/tokenization/services"
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

// BuildPartnerTokenPurchase proposes the on-chain purchase, as a real Safe
// transaction against the owned user's own primary wallet, for an owned
// user buying into an active primary or secondary sale - signerAddress is
// still the owned user's own signer key (this servicelinks passthrough
// grants a partner no signing authority of its own; see
// tokenization.GroupWalletExecutor's doc comment for why a raw unsigned
// transaction can no longer be built here at all).
func (s *Service) BuildPartnerTokenPurchase(ctx context.Context, serviceLinkID, userID, assetID uint, quantity decimal.Decimal, signerAddress string) (*tokenizationServices.PurchaseProposal, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.Tokenization.BuildCryptoPurchase(ctx, userID, assetID, quantity, signerAddress)
}

// RecordPartnerTokenPurchase approves and executes a purchase proposed via
// BuildPartnerTokenPurchase, recording it once the on-chain transfer
// actually completes - see tokenization.ConfirmCryptoPurchase's doc
// comment.
func (s *Service) RecordPartnerTokenPurchase(ctx context.Context, serviceLinkID, userID, assetID uint, quantity decimal.Decimal, actionID uint, signerAddress, signature string) (*tokenizationModels.TokenizedAssetSubscription, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.Tokenization.ConfirmCryptoPurchase(ctx, userID, assetID, quantity, actionID, signerAddress, signature)
}

// PartnerTokenSubscriptions lists an owned user's tokenization
// subscriptions across every asset.
func (s *Service) PartnerTokenSubscriptions(serviceLinkID, userID uint) ([]tokenizationModels.TokenizedAssetSubscription, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.Tokenization.ListMySubscriptions(userID)
}
