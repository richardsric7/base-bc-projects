package services

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/kyc/models"
	usersModels "wallet-backend/internal/components/users/models"
)

// ListSumsubLevels returns the configured catalog of level names a client
// may request (see models.SumsubLevel's doc comment for why this exists).
func (s *Service) ListSumsubLevels() ([]models.SumsubLevel, error) {
	var levels []models.SumsubLevel
	if err := s.DB.Order("id ASC").Find(&levels).Error; err != nil {
		return nil, apperrors.Internal("failed to load KYC levels")
	}
	return levels, nil
}

// GetSumsubProgress returns the caller's Sumsub progress, creating an
// empty (all-false) row on first read rather than requiring one to already
// exist - mirrors the original's "get-or-zero-value" GetUserKYCProgress.
func (s *Service) GetSumsubProgress(address string) (*models.UserSumsubProgress, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	return s.loadOrInitSumsubProgress(user.ID)
}

func (s *Service) loadOrInitSumsubProgress(userID uint) (*models.UserSumsubProgress, error) {
	var progress models.UserSumsubProgress
	err := s.DB.Where("user_id = ?", userID).First(&progress).Error
	if err == nil {
		return &progress, nil
	}
	if !isRecordNotFound(err) {
		return nil, apperrors.Internal("failed to load KYC progress")
	}
	progress = models.UserSumsubProgress{UserID: userID}
	return &progress, nil
}

func isRecordNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}

// InitiateSumsubLevel starts (or resumes) verification at levelName for the
// caller: it enforces the original's level-ordering rule (levels 1-3 must
// be completed in order), then creates a Sumsub applicant and returns an
// SDK access token the client's Sumsub mobile/web SDK uses to run the
// actual document/liveness capture. levelName is matched by substring
// ("level-1", "level-2", "level-3") exactly as the original did, since
// Sumsub level *names* are freeform strings configured in that project's
// Sumsub dashboard, not a fixed enum this codebase controls.
func (s *Service) InitiateSumsubLevel(address, levelName string) (token string, applicant models.SumsubApplicant, err error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return "", applicant, err
	}

	progress, err := s.loadOrInitSumsubProgress(user.ID)
	if err != nil {
		return "", applicant, err
	}

	if err := applySumsubLevelOrdering(progress, levelName, true); err != nil {
		return "", applicant, err
	}

	applicant = models.SumsubApplicant{
		ExternalUserID: user.Username,
		FixedInfo: models.SumsubInfo{
			FirstName: user.Username,
		},
	}
	applicant, err = s.sumsubCreateApplicant(applicant, levelName)
	if err != nil {
		return "", applicant, err
	}
	applicant, err = s.sumsubGetApplicantInfo(applicant.ID)
	if err != nil {
		return "", applicant, err
	}
	accessToken, err := s.sumsubGenerateAccessToken(user.Username, levelName)
	if err != nil {
		return "", applicant, err
	}

	if err := s.DB.Save(progress).Error; err != nil {
		return "", applicant, apperrors.Internal("failed to save KYC progress")
	}

	return accessToken.Token, applicant, nil
}

// applySumsubLevelOrdering mutates progress in place to mark a level
// initiated (or, from processWebhookResult, done) and enforces that levels
// 1-3 are only touched in order - porting the original's inline checks in
// InitiateUserKYCProgressForSumsub/CompleteUserKYCProgressForSumsub as one
// shared helper instead of duplicating the three near-identical blocks.
func applySumsubLevelOrdering(progress *models.UserSumsubProgress, levelName string, initiating bool) error {
	switch {
	case strings.Contains(levelName, "level-1"):
		if progress.Level1Done {
			return apperrors.Conflict("KYC level already done")
		}
		if initiating {
			progress.Level1Initiated = true
		} else {
			progress.Level1Done = true
		}
	case strings.Contains(levelName, "level-2"):
		if progress.Level2Done {
			return apperrors.Conflict("KYC level already done")
		}
		if !progress.Level1Done {
			return apperrors.BadRequest("KYC levels must be completed in order (1, 2, 3)")
		}
		if initiating {
			progress.Level2Initiated = true
		} else {
			progress.Level2Done = true
		}
	case strings.Contains(levelName, "level-3"):
		if progress.Level3Done {
			return apperrors.Conflict("KYC level already done")
		}
		if !progress.Level1Done || !progress.Level2Done {
			return apperrors.BadRequest("KYC levels must be completed in order (1, 2, 3)")
		}
		if initiating {
			progress.Level3Initiated = true
		} else {
			progress.Level3Done = true
		}
	default:
		return apperrors.BadRequest("unrecognized KYC level name")
	}
	return nil
}

// resetSumsubLevel clears both the initiated and done flags for whichever
// level levelName names - used when a review comes back RED, mirroring the
// original's ResetUserKYCProgressForSumsub.
func resetSumsubLevel(progress *models.UserSumsubProgress, levelName string) {
	switch {
	case strings.Contains(levelName, "level-1"):
		progress.Level1Initiated, progress.Level1Done = false, false
	case strings.Contains(levelName, "level-2"):
		progress.Level2Initiated, progress.Level2Done = false, false
	case strings.Contains(levelName, "level-3"):
		progress.Level3Initiated, progress.Level3Done = false, false
	}
}

// sumsubLevelNumber returns the numeric level (1-3) levelName names, or 0
// if it names none of them - used to update User.KYCVerifiedLevel.
func sumsubLevelNumber(levelName string) int {
	switch {
	case strings.Contains(levelName, "level-1"):
		return 1
	case strings.Contains(levelName, "level-2"):
		return 2
	case strings.Contains(levelName, "level-3"):
		return 3
	default:
		return 0
	}
}

// VerifySumsubWebhookSignature reports whether digestHeader is a valid
// HMAC-SHA256-hex digest of payload under SumsubSecretKey - ported from the
// original's VerifySumSubWebhook, ***with one fix***: the original computed
// this same digest but a caller-side bug meant the webhook handler never
// actually compared it against the request before trusting the payload (see
// PLAN.md §4.4 for detail). Every caller of this method must reject the
// request when it returns false.
func (s *Service) VerifySumsubWebhookSignature(payload []byte, digestHeader, algHeader string) bool {
	if s.SumsubSecretKey == "" {
		return false
	}
	if !strings.EqualFold(algHeader, "HMAC_SHA256_HEX") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.SumsubSecretKey))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(digestHeader)))
}

// ProcessSumsubWebhook records the review result and, on a GREEN answer,
// marks the corresponding level done and raises the user's
// KYCVerifiedLevel; on RED it resets that level's progress so the user can
// retry. Ported from the original's ProcessSumsubwebhook. The original also
// exposed a client-callable "complete level" endpoint that let an
// authenticated caller mark their own KYC as done directly, with no server
// verification at all - this port deliberately drops that endpoint, since
// it let any caller self-approve KYC; this webhook path (GREEN/RED from
// Sumsub itself, signature-verified by the caller before this is invoked)
// is the only way KYCVerifiedLevel advances.
func (s *Service) ProcessSumsubWebhook(input *models.SumsubWebhookInput) error {
	user, err := s.getUserByUsername(input.ExternalUserID)
	if err != nil {
		return err
	}

	logEntry := models.SumsubWebhookLog{
		ApplicantID:    input.ApplicantID,
		InspectionID:   input.InspectionID,
		CorrelationID:  input.CorrelationID,
		ExternalUserID: input.ExternalUserID,
		LevelName:      input.LevelName,
		Type:           input.Type,
		ReviewAnswer:   input.ReviewResult.ReviewAnswer,
		ReviewStatus:   input.ReviewStatus,
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}

		var progress models.UserSumsubProgress
		err := tx.Where("user_id = ?", user.ID).First(&progress).Error
		if err != nil {
			if !isRecordNotFound(err) {
				return err
			}
			progress = models.UserSumsubProgress{UserID: user.ID}
		}

		switch strings.ToUpper(input.ReviewResult.ReviewAnswer) {
		case "GREEN":
			if err := applySumsubLevelOrdering(&progress, input.LevelName, false); err != nil {
				// Already done, or out of order - the webhook is
				// informational at that point; nothing to update.
				return nil
			}
			if err := tx.Save(&progress).Error; err != nil {
				return err
			}
			if level := sumsubLevelNumber(input.LevelName); level > 0 {
				if err := tx.Model(&usersModels.User{}).
					Where("id = ? AND kyc_verified_level < ?", user.ID, level).
					Update("kyc_verified_level", level).Error; err != nil {
					return err
				}
			}
		case "RED":
			resetSumsubLevel(&progress, input.LevelName)
			if err := tx.Save(&progress).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Sumsub REST client -----------------------------------------------
//
// The three methods below call Sumsub's actual API
// (https://docs.sumsub.com/reference) using the request-signing scheme
// Sumsub documents: X-App-Token, plus an X-App-Access-Sig HMAC-SHA256 over
// timestamp+method+path+body, plus X-App-Access-Ts. Ported near-verbatim
// from the original's sumsubmethods.go.

func (s *Service) sumsubCreateApplicant(applicant models.SumsubApplicant, levelName string) (models.SumsubApplicant, error) {
	body, err := json.Marshal(applicant)
	if err != nil {
		return applicant, apperrors.Internal("failed to encode Sumsub applicant")
	}
	respBody, err := s.sumsubRequest("POST", "/resources/applicants?levelName="+levelName, "application/json", body)
	if err != nil {
		return applicant, err
	}
	var created models.SumsubApplicant
	if err := json.Unmarshal(respBody, &created); err != nil {
		return applicant, apperrors.Internal("failed to decode Sumsub applicant response")
	}
	return created, nil
}

func (s *Service) sumsubGetApplicantInfo(applicantID string) (models.SumsubApplicant, error) {
	var applicant models.SumsubApplicant
	respBody, err := s.sumsubRequest("GET", fmt.Sprintf("/resources/applicants/%s/one", applicantID), "application/json", nil)
	if err != nil {
		return applicant, err
	}
	if err := json.Unmarshal(respBody, &applicant); err != nil {
		return applicant, apperrors.Internal("failed to decode Sumsub applicant response")
	}
	return applicant, nil
}

func (s *Service) sumsubGenerateAccessToken(externalUserID, levelName string) (models.SumsubAccessToken, error) {
	var token models.SumsubAccessToken
	path := "/resources/accessTokens?userId=" + externalUserID + "&levelName=" + levelName
	respBody, err := s.sumsubRequest("POST", path, "application/json", []byte(""))
	if err != nil {
		return token, err
	}
	if err := json.Unmarshal(respBody, &token); err != nil {
		return token, apperrors.Internal("failed to decode Sumsub access token response")
	}
	return token, nil
}

func (s *Service) sumsubRequest(method, path, contentType string, body []byte) ([]byte, error) {
	if s.SumsubToken == "" || s.SumsubSecretKey == "" {
		return nil, apperrors.Internal("Sumsub is not configured (SUMSUB_TOKEN/SUMSUB_SECRET_KEY)")
	}

	req, err := http.NewRequest(method, s.SumsubBaseURL+path, bytes.NewBuffer(body))
	if err != nil {
		return nil, apperrors.Internal("failed to build Sumsub request")
	}

	ts := fmt.Sprintf("%d", time.Now().Unix())
	req.Header.Set("X-App-Token", s.SumsubToken)
	req.Header.Set("X-App-Access-Sig", sumsubSign(ts, s.SumsubSecretKey, method, path, body))
	req.Header.Set("X-App-Access-Ts", ts)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, apperrors.Internal("failed to reach Sumsub: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, apperrors.Internal("failed to read Sumsub response")
	}
	if resp.StatusCode >= 300 {
		return nil, apperrors.Internal(fmt.Sprintf("Sumsub returned %d: %s", resp.StatusCode, string(respBody)))
	}
	return respBody, nil
}

func sumsubSign(ts, secret, method, path string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + method + path))
	if body != nil {
		mac.Write(body)
	}
	return hex.EncodeToString(mac.Sum(nil))
}
