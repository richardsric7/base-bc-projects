// Package services implements the servicelinks partner API: an
// API-key-authenticated surface letting a third-party service act on
// behalf of wallet users with scoped permissions. See PLAN.md §4.11 for
// the full design and the authorization bugs found in the original and
// fixed here (plaintext API keys, missing status checks, an overloaded
// permission flag, missing wallet-ownership scoping, a broken KYC-override
// check, and an unscoped document store).
//
// This component is a facade sitting in front of users/payments/assets/
// tokenization - unlike the KYC-to-Stablerail or fiat-to-tokenization
// cross-component hooks elsewhere in this port (which use function-field
// callbacks specifically to avoid two peer components depending on each
// other), servicelinks is *designed* to depend on all of them, so it
// imports their services packages directly rather than inventing a hook
// for every one of the dozen calls it needs to make.
package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsServices "wallet-backend/internal/components/assets/services"
	paymentsServices "wallet-backend/internal/components/payments/services"
	"wallet-backend/internal/components/servicelinks/models"
	shortlinkServices "wallet-backend/internal/components/shortlink/services"
	tokenizationServices "wallet-backend/internal/components/tokenization/services"
	usersModels "wallet-backend/internal/components/users/models"
	usersServices "wallet-backend/internal/components/users/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/storage"
)

type Service struct {
	DB           *gorm.DB
	Users        *usersServices.Service
	Payments     *paymentsServices.Service
	Assets       *assetsServices.Service
	Tokenization *tokenizationServices.Service
	Storage      storage.Blob
	Push         notify.PushProvider
	JWTSecret    string
	JWTExpiry    time.Duration
	ApprovalTTL  time.Duration // default validity for an authorize/event request

	// Shortlink is assigned post-construction in main.go, the same
	// pattern as paymentsSvc.Alerts/usersSvc.GeoIP - shortlink.Init runs
	// after servicelinks.Init (main.go's existing component order), and
	// this keeps New's signature stable rather than reordering every
	// other call site (PLAN.md §14.2 item 2). A nil Shortlink makes
	// mintDeepLink a no-op instead of panicking, so existing tests that
	// don't need QR minting are unaffected.
	Shortlink *shortlinkServices.Service
}

func New(db *gorm.DB, users *usersServices.Service, payments *paymentsServices.Service, assets *assetsServices.Service, tokenization *tokenizationServices.Service, blob storage.Blob, push notify.PushProvider, jwtSecret string, jwtExpiry, approvalTTL time.Duration) *Service {
	return &Service{
		DB:           db,
		Users:        users,
		Payments:     payments,
		Assets:       assets,
		Tokenization: tokenization,
		Storage:      blob,
		Push:         push,
		JWTSecret:    jwtSecret,
		JWTExpiry:    jwtExpiry,
		ApprovalTTL:  approvalTTL,
	}
}

// generateAPIKey returns a fresh random API key (raw, shown once) and its
// SHA-256 hash (persisted). Never store the raw value.
func generateAPIKey() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", apperrors.Internal("failed to generate API key")
	}
	raw = "sl_" + hex.EncodeToString(buf)
	return raw, middleware.HashAPIKey(raw), nil
}

func (s *Service) getServiceLink(id uint) (*models.ServiceLink, error) {
	var link models.ServiceLink
	if err := s.DB.First(&link, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("service link not found")
		}
		return nil, apperrors.Internal("failed to load service link")
	}
	return &link, nil
}

// requireOwnedUser fetches a user by ID and requires it was created by
// serviceLinkID - deny-by-default (a nil CreatedByServiceLinkID or a
// mismatch are both rejected), fixing the original's KYC-override check
// which only rejected on a non-nil mismatch and silently allowed acting on
// any organically-registered user (PLAN.md §4.11 finding 6). Every route
// that acts on a specific user's wallet uses this same check (finding 5).
func (s *Service) requireOwnedUser(serviceLinkID uint, userID uint) (*usersModels.User, error) {
	user, err := s.Users.GetByID(userID)
	if err != nil {
		return nil, err
	}
	if user.CreatedByServiceLinkID == nil || *user.CreatedByServiceLinkID != serviceLinkID {
		return nil, apperrors.Forbidden("this user was not created by your service link")
	}
	return user, nil
}
