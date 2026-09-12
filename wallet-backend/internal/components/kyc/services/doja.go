package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/kyc/models"
)

// ListDojaWidgets returns the configured widget-ID -> level/type catalog
// (see models.DojaWidget's doc comment for why there are no seeded
// defaults).
func (s *Service) ListDojaWidgets() ([]models.DojaWidget, error) {
	var widgets []models.DojaWidget
	if err := s.DB.Order("level ASC, corporate ASC").Find(&widgets).Error; err != nil {
		return nil, apperrors.Internal("failed to load Doja widgets")
	}
	return widgets, nil
}

// GetDojaProgress returns the caller's Dojah progress, get-or-zero-value
// exactly like GetSumsubProgress.
func (s *Service) GetDojaProgress(address string) (*models.UserDojaProgress, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	var progress models.UserDojaProgress
	err = s.DB.Where("user_id = ?", user.ID).First(&progress).Error
	if err == nil {
		return &progress, nil
	}
	if !isRecordNotFound(err) {
		return nil, apperrors.Internal("failed to load KYC progress")
	}
	return &models.UserDojaProgress{UserID: user.ID}, nil
}

// VerifyDojaWebhookSignature reports whether signatureHeader is a valid
// HMAC-SHA256-hex digest of payload under DojaSecretKey.
//
// ***This fixes a real bug in the original***: postCallbacksDojaWebhookHandler
// computed this exact digest (expectedMAC) and read the incoming
// "x-dojah-signature" header, but never actually compared the two before
// processing the webhook - the only gate it applied was checking the
// request's source IP against a single hardcoded address, and even that
// check had no `return` in its failure branch, so an unrecognized IP was
// merely logged, not rejected. In effect, the original's Dojah webhook
// endpoint accepted and acted on a payload from anyone who could guess the
// URL, with no real verification at all. This port requires a valid
// signature; every caller of this method must reject the request when it
// returns false.
func (s *Service) VerifyDojaWebhookSignature(payload []byte, signatureHeader string) bool {
	if s.DojaSecretKey == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.DojaSecretKey))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(signatureHeader)))
}

// ProcessDojaWebhook resolves event's widget and user, advances the
// matching level's Submitted/Completed flags, and raises the user's
// KYCVerifiedLevel once a level reaches "Completed". Ported from the
// original's postCallbacksDojaWebhookHandler, with two deliberate
// omissions documented inline: push notifications (no device-token
// subsystem exists yet in this port - see PLAN.md §4.13) and the
// BVN-triggered Stablerail onboarding call (Phase 5, not yet built) both
// become no-ops here rather than being silently dropped from the design;
// wiring either back in later is a small, additive change at the marked
// points below.
func (s *Service) ProcessDojaWebhook(event *models.DojaWebhookEvent) error {
	var widget models.DojaWidget
	if err := s.DB.Where("id = ?", event.WidgetID).First(&widget).Error; err != nil {
		return apperrors.BadRequest("unrecognized Doja widget ID: " + event.WidgetID)
	}

	user, err := s.getUserByUsername(event.Metadata.UserID)
	if err != nil {
		// Matches the original's posture: an unrecognized username in an
		// otherwise-authentic webhook isn't a client error worth Dojah
		// retrying forever, so this is a soft no-op rather than a failure.
		log.Printf("[kyc:doja] webhook references unknown username %q, ignoring", event.Metadata.UserID)
		return nil
	}

	return s.DB.Transaction(func(tx *gorm.DB) error {
		var progress models.UserDojaProgress
		e := tx.Where("user_id = ?", user.ID).First(&progress).Error
		if e != nil {
			if !isRecordNotFound(e) {
				return e
			}
			progress = models.UserDojaProgress{UserID: user.ID}
		}

		switch strings.ToLower(event.VerificationStatus) {
		case "pending":
			markDojaSubmitted(&progress, widget.Level)
		case "completed":
			alreadyCompleted := dojaLevelCompleted(&progress, widget.Level)
			markDojaSubmitted(&progress, widget.Level)
			markDojaCompleted(&progress, widget.Level)
			if !alreadyCompleted {
				if err := tx.Model(&user).
					Where("kyc_verified_level < ?", widget.Level).
					Update("kyc_verified_level", widget.Level).Error; err != nil {
					return err
				}
				// Hook point for Phase 5 (Stablerail): the original
				// triggered BVN-based fiat-rail onboarding here when
				// level 1 completed with a BVN value present
				// (strings.EqualFold(event.IDType, "bvn")). Left as a
				// comment rather than a stub function since there is
				// nothing to call yet.
			}
			// Hook point for a future push-notification subsystem: the
			// original sent a "KYC level completed" push here.
		case "failed":
			resetDojaLevel(&progress, widget.Level)
		}

		return tx.Save(&progress).Error
	})
}

func markDojaSubmitted(p *models.UserDojaProgress, level int) {
	switch level {
	case 1:
		p.Level1Submitted = true
	case 2:
		p.Level2Submitted = true
	case 3:
		p.Level3Submitted = true
	case 4:
		p.Level4Submitted = true
	}
}

func markDojaCompleted(p *models.UserDojaProgress, level int) {
	switch level {
	case 1:
		p.Level1Completed = true
	case 2:
		p.Level2Completed = true
	case 3:
		p.Level3Completed = true
	case 4:
		p.Level4Completed = true
	}
}

func dojaLevelCompleted(p *models.UserDojaProgress, level int) bool {
	switch level {
	case 1:
		return p.Level1Completed
	case 2:
		return p.Level2Completed
	case 3:
		return p.Level3Completed
	case 4:
		return p.Level4Completed
	default:
		return false
	}
}

func resetDojaLevel(p *models.UserDojaProgress, level int) {
	switch level {
	case 1:
		p.Level1Submitted, p.Level1Completed = false, false
	case 2:
		p.Level2Submitted, p.Level2Completed = false, false
	case 3:
		p.Level3Submitted, p.Level3Completed = false, false
	case 4:
		p.Level4Submitted, p.Level4Completed = false, false
	}
}
