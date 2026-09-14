package services

import (
	usersModels "wallet-backend/internal/components/users/models"
	usersServices "wallet-backend/internal/components/users/services"
)

// OnboardUser creates a new user profile on a partner's behalf, tagged
// with CreatedByServiceLinkID so every later servicelinks action on this
// user's wallet passes requireOwnedUser (PLAN.md §4.11 finding 5). Callers
// gate this on the service link's CanCreateUsers capability; see
// controllers.
//
// Unlike the self-registration path (users.Service.Register's own doc
// comment), address is taken from the partner's request body rather than a
// verified signed request: a partner-onboarded user's wallet is provisioned
// and held by the partner's own embedded-wallet infrastructure, not signed
// into directly against this API, so there is no signed request to prove
// ownership with. This is a deliberate trust boundary - only a
// CanCreateUsers-authorized, verified service link may call this - see
// PLAN.md §4.11's "what ports directly" note on onboarding. address is
// used the same way a self-registering caller's own verified signer
// address is: as the sole owner Register computes a primary wallet Safe
// address from (PLAN.md §13.2), never as the wallet address itself.
func (s *Service) OnboardUser(serviceLinkID uint, username, email, address string) (*usersModels.User, error) {
	return s.Users.Register(usersServices.RegisterInput{
		Username:               username,
		Email:                  email,
		SignerAddress:          address,
		CreatedByServiceLinkID: &serviceLinkID,
	})
}
