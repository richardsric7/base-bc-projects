package services

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/models"
	"wallet-backend/internal/middleware"
)

func generateApprovalID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", apperrors.Internal("failed to generate approval id")
	}
	return hex.EncodeToString(buf), nil
}

// RequestApprovalInput is a partner's request for a named user's consent.
// One model with a Kind discriminator replaces the original's three
// near-identical login/authorize/event models and handlers (PLAN.md §4.11
// finding 7).
type RequestApprovalInput struct {
	Kind           models.ApprovalKind
	TargetUsername string
	Description    string
	CallbackURL    string
}

// RequestApproval records a pending consent request naming an existing
// user by username. It does not require the target user to already be
// scoped to this service link via requireOwnedUser: a LOGIN request in
// particular is how an organically-registered user first links to a
// partner. Every subsequent action a partner takes on the user's behalf
// still goes through requireOwnedUser regardless of what gets approved
// here.
func (s *Service) RequestApproval(serviceLinkID uint, input RequestApprovalInput) (*models.ServiceLinkApproval, error) {
	switch input.Kind {
	case models.ApprovalLogin, models.ApprovalAuthorize, models.ApprovalEvent:
	default:
		return nil, apperrors.BadRequest("kind must be LOGIN, AUTHORIZE or EVENT")
	}
	target, err := s.Users.GetByUsername(input.TargetUsername)
	if err != nil {
		return nil, err
	}
	id, err := generateApprovalID()
	if err != nil {
		return nil, err
	}
	approval := models.ServiceLinkApproval{
		ID:            id,
		ServiceLinkID: serviceLinkID,
		Kind:          input.Kind,
		TargetUserID:  target.ID,
		Description:   input.Description,
		CallbackURL:   input.CallbackURL,
		ExpiresAt:     time.Now().Add(s.ApprovalTTL),
	}
	if err := s.DB.Create(&approval).Error; err != nil {
		return nil, apperrors.Internal("failed to create approval request")
	}

	link, err := s.mintDeepLink(strings.ToLower(string(input.Kind)), map[string]string{"id": approval.ID}, map[string]string{
		"action":     strings.ToLower(string(input.Kind)),
		"approvalId": approval.ID,
	})
	if err != nil {
		return nil, err
	}
	approval.ShortURL = link.ShortURL
	approval.QRURL = link.QRURL

	return &approval, nil
}

func (s *Service) getApproval(approvalID string) (*models.ServiceLinkApproval, error) {
	var approval models.ServiceLinkApproval
	if err := s.DB.First(&approval, "id = ?", approvalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("approval request not found")
		}
		return nil, apperrors.Internal("failed to load approval request")
	}
	return &approval, nil
}

// GetApproval fetches a pending approval by ID - used by the wallet app to
// show the signed-in user what they're being asked to approve before they
// decide.
func (s *Service) GetApproval(approvalID string) (*models.ServiceLinkApproval, error) {
	return s.getApproval(approvalID)
}

// Approve marks a pending approval as authorized. callerAddress must be
// the wallet address SignatureAuth resolved for the caller (see
// controllers) - approving on behalf of anyone else is rejected, the same
// deny-by-default discipline as requireOwnedUser.
func (s *Service) Approve(approvalID string, callerAddress string) (*models.ServiceLinkApproval, error) {
	approval, err := s.getApproval(approvalID)
	if err != nil {
		return nil, err
	}
	target, err := s.Users.GetByID(approval.TargetUserID)
	if err != nil {
		return nil, err
	}
	if target.Address != callerAddress {
		return nil, apperrors.Forbidden("this approval request is not addressed to you")
	}
	if approval.Authorized {
		return nil, apperrors.Conflict("this approval request has already been authorized")
	}
	if time.Now().After(approval.ExpiresAt) {
		return nil, apperrors.BadRequest("this approval request has expired")
	}
	approval.Authorized = true
	if err := s.DB.Save(approval).Error; err != nil {
		return nil, apperrors.Internal("failed to record approval")
	}

	// Fire-and-forget: dispatchApprovalCallback (PLAN.md §14.2 item 3)
	// retries on its own, so Approve returns to the caller immediately
	// rather than waiting on a partner's callback endpoint.
	go dispatchApprovalCallback(*approval)

	return approval, nil
}

// VerifyApproval lets the requesting service link redeem an authorized
// approval, exactly once - deleting it afterward closes off replaying the
// same approval ID for a second session token. For a LOGIN request this
// mints a servicelink-session JWT (AudienceServiceLinkSession) for the
// target user - a token that authenticates the *partner*, not the user's
// own wallet operations, so it survives independently of PLAN.md §12's
// per-request signature scheme those operations use instead (PLAN.md
// §12.5, §14.1.1). AUTHORIZE and EVENT requests return no token: it's up
// to the partner's own next call - which still goes through
// requireOwnedUser - to act on the user's consent.
func (s *Service) VerifyApproval(serviceLinkID uint, approvalID string) (*models.ServiceLinkApproval, string, error) {
	approval, err := s.getApproval(approvalID)
	if err != nil {
		return nil, "", err
	}
	if approval.ServiceLinkID != serviceLinkID {
		return nil, "", apperrors.Forbidden("this approval request belongs to a different service link")
	}
	if !approval.Authorized {
		return nil, "", apperrors.BadRequest("this approval request has not been authorized yet")
	}

	var sessionToken string
	if approval.Kind == models.ApprovalLogin {
		target, err := s.Users.GetByID(approval.TargetUserID)
		if err != nil {
			return nil, "", err
		}
		sessionToken, err = middleware.IssueToken(s.JWTSecret, target.Address, middleware.AudienceServiceLinkSession, s.JWTExpiry)
		if err != nil {
			return nil, "", apperrors.Internal("failed to issue session token")
		}
	}

	if err := s.DB.Delete(approval).Error; err != nil {
		return nil, "", apperrors.Internal("failed to consume approval request")
	}
	return approval, sessionToken, nil
}
