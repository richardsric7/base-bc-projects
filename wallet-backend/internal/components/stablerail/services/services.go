// Package services implements the stablerail component's business logic:
// BVN onboarding, NGN->stablecoin on-ramp, and the automatic Base-address
// withdrawal that completes a funded on-ramp - all real HTTP calls against
// Stablerail's actual API. See PLAN.md §4.6 for the full design; this
// subsystem is close to chain-agnostic already (Stablerail's own API takes
// a destination address and network code per request), so it's ported
// close to verbatim with "base" in place of the original's "xbn" network
// code.
package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	usersModels "wallet-backend/internal/components/users/models"
)

// network is the value sent in every Stablerail request's "network" field,
// replacing the original's Stellar-specific "xbn". Stablerail may use a
// different literal for Base in its own dashboard/API - if so, this is the
// one line that needs to change.
const network = "base"

type Service struct {
	DB         *gorm.DB
	HTTPClient *http.Client
	APIKey     string
	BaseURL    string
	Enabled    bool
}

func New(db *gorm.DB, apiKey, baseURL string, enabled bool) *Service {
	if baseURL == "" {
		baseURL = "https://beta.stablesrail.io/v1"
	}
	return &Service{
		DB:         db,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Enabled:    enabled,
	}
}

func (s *Service) requireEnabled() error {
	if !s.Enabled || s.APIKey == "" {
		return apperrors.New(202, "stablerail_disabled", "Stablerail is not enabled on this deployment")
	}
	return nil
}

func (s *Service) getUserByAddress(address string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("address = ?", address).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("user profile not found - register first")
	}
	return &user, nil
}

func (s *Service) getUserByUsername(username string) (*usersModels.User, error) {
	var user usersModels.User
	if err := s.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, apperrors.NotFound("unknown username: " + username)
	}
	return &user, nil
}

func (s *Service) doRequest(method, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apperrors.Internal("failed to encode Stablerail request")
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, s.BaseURL+path, reqBody)
	if err != nil {
		return apperrors.Internal("failed to build Stablerail request")
	}
	req.Header.Set("x-api-key", s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return apperrors.Internal("failed to reach Stablerail: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return apperrors.Internal("failed to read Stablerail response")
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return apperrors.Internal("failed to decode Stablerail response")
	}
	if resp.StatusCode != http.StatusOK {
		return apperrors.Internal(fmt.Sprintf("Stablerail returned %d: %s", resp.StatusCode, string(respBody)))
	}
	return nil
}

// logPollError is the shared log line for both background pollers -
// failures here are expected occasionally (a transient network blip) and
// simply retried on the next tick, so they're logged rather than
// propagated anywhere synchronous.
func logPollError(job, requestID string, err error) {
	log.Printf("[stablerail:%s] error polling request %s: %v", job, requestID, err)
}
