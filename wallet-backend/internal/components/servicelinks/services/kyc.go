package services

import "wallet-backend/internal/apperrors"

// UpdateKYCStatus lets a partner report a KYC outcome it collected itself
// for one of its own onboarded users. requireOwnedUser is the fix for the
// original's broken ownership check here, which only rejected on a
// non-nil mismatch and so silently allowed a compromised or careless
// service link to override the KYC level of any organically-registered
// user (PLAN.md §4.11 finding 6).
func (s *Service) UpdateKYCStatus(serviceLinkID uint, userID uint, level int) error {
	if level < 0 {
		return apperrors.BadRequest("level must not be negative")
	}
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return err
	}
	return s.Users.SetKYCVerifiedLevel(userID, level)
}
