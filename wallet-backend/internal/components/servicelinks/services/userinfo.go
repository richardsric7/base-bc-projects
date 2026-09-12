package services

import (
	"errors"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	usersModels "wallet-backend/internal/components/users/models"
)

// UserInfo returns an owned user's profile - the same shape the wallet
// app's own /v1/users/:username endpoint returns, scoped by
// requireOwnedUser rather than left open the way the original's user-info
// route was.
func (s *Service) UserInfo(serviceLinkID, userID uint) (*usersModels.User, error) {
	return s.requireOwnedUser(serviceLinkID, userID)
}

// SendPush relays a push notification to an owned user's registered
// device, on the partner's behalf. Push delivery itself goes through
// notify.PushProvider - see sharedconfig for which implementation is wired
// in (NoopPushProvider drops it silently if no provider is configured, the
// same fallback behavior every other component's push call already uses).
func (s *Service) SendPush(serviceLinkID, userID uint, deviceToken, title, body string) error {
	if _, err := s.requireOwnedUser(serviceLinkID, userID); err != nil {
		return err
	}
	if err := s.Push.Send(deviceToken, title, body); err != nil {
		return apperrors.Internal("failed to send push notification")
	}
	return nil
}

// TokenInfo looks up a curated token by symbol - mirrors
// tokenization.Service's own curatedToken helper, which queries the
// shared DB directly rather than going through assets.Service, since every
// component in this port shares one database.
func (s *Service) TokenInfo(symbol string) (*assetsModels.CuratedToken, error) {
	var token assetsModels.CuratedToken
	if err := s.DB.Where("symbol = ? AND is_active = ?", symbol, true).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("unknown token symbol")
		}
		return nil, apperrors.Internal("failed to resolve token")
	}
	return &token, nil
}
