// Package services implements identity verification against the two
// vendors the original project integrated: Sumsub (outbound
// applicant-creation API + inbound review-result webhook) and Dojah
// (inbound webhook only, BVN-centric). This subsystem is entirely
// chain-agnostic - see PLAN.md §4.4 - so it's ported close to verbatim
// rather than redesigned for Base.
package services

import (
	"net/http"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	usersModels "wallet-backend/internal/components/users/models"
)

// Service holds every dependency both vendor integrations need. A single
// Service (rather than one per vendor) keeps the shared bits - DB access,
// user lookup - in one place; SumsubX/DojaX methods live in their own
// files (sumsub.go, doja.go) for readability.
type Service struct {
	DB              *gorm.DB
	HTTPClient      *http.Client
	SumsubBaseURL   string
	SumsubToken     string
	SumsubSecretKey string
	DojaSecretKey   string
}

func New(db *gorm.DB, sumsubBaseURL, sumsubToken, sumsubSecretKey, dojaSecretKey string) *Service {
	return &Service{
		DB:              db,
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
		SumsubBaseURL:   sumsubBaseURL,
		SumsubToken:     sumsubToken,
		SumsubSecretKey: sumsubSecretKey,
		DojaSecretKey:   dojaSecretKey,
	}
}

// getUserByAddress resolves the caller's User row from the Base address a
// verified session JWT carries - the same identity Register uses. Both
// vendor integrations need the full row (ID for progress rows, Username as
// the vendor-facing external ID, KYCVerifiedLevel to update).
func (s *Service) getUserByAddress(address string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("user profile not found - register before starting KYC")
	}
	return &user, nil
}

// getUserByUsername resolves a User row by username - used by webhook
// handlers, which only know the vendor's "externalUserId"/"user_id" value,
// which this project sets to the Trovo username (see InitiateSumsubLevel
// and the Doja widget's metadata.user_id convention it inherits from the
// original).
func (s *Service) getUserByUsername(username string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("unknown username in webhook payload: " + username)
	}
	return &user, nil
}
