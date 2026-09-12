package services

import (
	"strings"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/stablerail/models"
)

// InitiateOnboarding starts BVN onboarding for the caller (identified by
// their verified session address) - the client-facing, manually-triggered
// path. Ported from the original's StablerailInitiateOnboardUser.
func (s *Service) InitiateOnboarding(address, bvn string) (string, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return "", err
	}
	return s.initiateOnboardingForUser(user.ID, bvn)
}

// InitiateOnboardingByUsername is the same flow, looked up by username -
// used by the KYC component's Doja Level-1-completion hook (see
// internal/components/kyc/services/doja.go's OnBVNVerified callback),
// which only has a username, not an authenticated request to read an
// address off of.
func (s *Service) InitiateOnboardingByUsername(username, bvn string) error {
	user, err := s.getUserByUsername(username)
	if err != nil {
		return err
	}
	_, err = s.initiateOnboardingForUser(user.ID, bvn)
	return err
}

func (s *Service) initiateOnboardingForUser(userID uint, bvn string) (string, error) {
	if err := s.requireEnabled(); err != nil {
		return "", err
	}

	var existing models.StablerailUser
	err := s.DB.Where("user_id = ?", userID).First(&existing).Error
	if err == nil {
		return "registration already done", nil
	}
	if !isRecordNotFound(err) {
		return "", apperrors.Internal("failed to check existing onboarding")
	}

	var resp models.OnboardResponse
	if err := s.doRequest("POST", "/onboarduser", models.OnboardRequest{BVN: bvn}, &resp); err != nil {
		return "", err
	}
	if resp.ResponseCode != "00" {
		return "", apperrors.Internal("Stablerail declined onboarding: " + resp.Message)
	}

	request := models.StablerailRequest{
		ID:          resp.Data.RequestID,
		UserID:      userID,
		RequestType: "Onboarding",
		Status:      resp.Data.Status,
	}
	if err := s.DB.Create(&request).Error; err != nil {
		return "", apperrors.Internal("failed to save onboarding request")
	}
	return resp.Data.Message, nil
}

func isRecordNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}

// PollPendingOnboarding checks every in-flight onboarding request against
// Stablerail's status endpoint and records a StablerailUser once
// Stablerail reports it complete - meant to be called on an interval by a
// background goroutine (see main.go), mirroring the original's
// ProcessUpdateStablerailOnboardingStatus.
func (s *Service) PollPendingOnboarding() {
	if err := s.requireEnabled(); err != nil {
		return
	}
	var pending []models.StablerailRequest
	if err := s.DB.Where("request_type = ? AND status = ?", "Onboarding", "processing").Find(&pending).Error; err != nil {
		return
	}
	for _, request := range pending {
		if err := s.pollOnboardingRequest(request); err != nil {
			// Best-effort background job: log and retry on the next tick
			// rather than surfacing anywhere synchronous.
			logPollError("onboarding", request.ID, err)
		}
	}
}

func (s *Service) pollOnboardingRequest(request models.StablerailRequest) error {
	var resp models.OnboardingStatusResponse
	if err := s.doRequest("POST", "/onboardstatus", map[string]string{"requestId": request.ID}, &resp); err != nil {
		return err
	}
	if resp.ResponseCode != "00" {
		return apperrors.Internal("could not check onboarding status")
	}

	request.Status = resp.Data.Status
	if err := s.DB.Save(&request).Error; err != nil {
		return err
	}

	if strings.EqualFold(resp.Data.Status, "completed") {
		return s.DB.Create(&models.StablerailUser{ID: resp.Data.UserID, UserID: request.UserID}).Error
	}
	return nil
}
