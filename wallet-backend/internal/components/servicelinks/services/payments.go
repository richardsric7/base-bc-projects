package services

import (
	"context"

	"wallet-backend/internal/components/payments/models"
	"wallet-backend/internal/components/payments/services"
	usersModels "wallet-backend/internal/components/users/models"
)

// BuildPartnerPayment proposes a real Safe transaction from a service
// link's own onboarded user's primary wallet and returns the digest that
// must be personal_sign'd to approve it (see payments.PaymentProposal).
// requireOwnedUser is the fix for the original, which had no
// wallet-ownership check on this route at all and instead trusted
// unverified request headers - any partner holding any valid API key
// could build a payment transaction against an arbitrary user's wallet
// (PLAN.md §4.11 finding 5). The digest is still unsigned: this port's
// wallets are non-custodial, so signing happens wherever the partner's
// own embedded-wallet infrastructure holds the user's signer key, never
// on this server.
func (s *Service) BuildPartnerPayment(ctx context.Context, serviceLinkID, userID uint, to, tokenAddress, amount string) (*services.PaymentProposal, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return nil, err
	}
	return s.Payments.BuildPaymentTx(ctx, user.Address, user.SignerAddress, to, tokenAddress, amount)
}

// SubmitPartnerPayment approves the proposal from BuildPartnerPayment with
// the partner-held signature and records it in payment history.
func (s *Service) SubmitPartnerPayment(ctx context.Context, serviceLinkID, userID uint, idempotencyKey string, actionID uint, signature, to, tokenAddress, amount string) (*models.PaymentHistory, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return nil, err
	}
	return s.Payments.SubmitPayment(ctx, idempotencyKey, actionID, user.SignerAddress, signature, user.Address, to, tokenAddress, amount)
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
func (s *Service) RegisterSubWallet(serviceLinkID, userID uint, address, tag string) (*usersModels.UserWallet, error) {
	user, err := s.requireOwnedUser(serviceLinkID, userID)
	if err != nil {
		return nil, err
	}
	return s.Users.RegisterWalletForAddress(user.Address, address, tag, "", usersModels.WalletTypeNormal)
}
