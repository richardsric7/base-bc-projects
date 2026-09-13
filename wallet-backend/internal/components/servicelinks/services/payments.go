package services

import (
	"context"

	"wallet-backend/internal/components/payments/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/network"
)

// BuildPartnerPayment builds an unsigned transfer from a service link's own
// onboarded user's wallet. requireOwnedUser is the fix for the original,
// which had no wallet-ownership check on this route at all and instead
// trusted unverified request headers - any partner holding any valid API
// key could build a payment transaction against an arbitrary user's wallet
// (PLAN.md §4.11 finding 5). The returned transaction is still unsigned:
// this port's wallets are non-custodial, so signing happens wherever the
// partner's own embedded-wallet infrastructure holds the user's key, never
// on this server.
func (s *Service) BuildPartnerPayment(ctx context.Context, serviceLinkID, userID uint, to, tokenAddress, amount string, nonce *uint64) (*network.UnsignedTx, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return nil, err
	}
	return s.Payments.BuildPaymentTx(ctx, user.Address, to, tokenAddress, amount, nonce)
}

// SubmitPartnerPayment submits a payment the partner already had signed on
// the user's behalf and records it in payment history.
func (s *Service) SubmitPartnerPayment(ctx context.Context, serviceLinkID, userID uint, idempotencyKey, signedTx, to, tokenAddress, amount string) (*models.PaymentHistory, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return nil, err
	}
	return s.Payments.SubmitPayment(ctx, idempotencyKey, signedTx, user.Address, to, tokenAddress, amount)
}

// Balance returns an owned user's balance of native ETH (empty
// tokenAddress) or a specific ERC-20 token.
func (s *Service) Balance(ctx context.Context, serviceLinkID, userID uint, tokenAddress string) (string, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return "", err
	}
	return s.Assets.Balance(ctx, user.Address, tokenAddress)
}

// PaymentHistory returns an owned user's payment history.
func (s *Service) PaymentHistory(serviceLinkID, userID uint) ([]models.PaymentHistory, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return nil, err
	}
	return s.Payments.History(user.Address)
}

// RegisterSubWallet lets a partner register an additional address an owned
// user controls (e.g. a second embedded-wallet address the partner
// provisioned for them) alongside their primary wallet.
func (s *Service) RegisterSubWallet(serviceLinkID, userID uint, address, label string) (*usersModels.UserWallet, error) {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return nil, err
	}
	return s.Users.RegisterWallet(userID, address, label)
}
