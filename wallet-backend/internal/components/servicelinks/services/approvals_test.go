package services

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"wallet-backend/internal/components/servicelinks/models"
)

func TestRequestApproval_LoginFlowEndToEnd(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanLogin: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)

	approval, err := svc.RequestApproval(link.ID, RequestApprovalInput{
		Kind:           models.ApprovalLogin,
		TargetUsername: target.Username,
		Description:    "Sign in to Acme",
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if approval.Authorized {
		t.Fatalf("a freshly requested approval must not start authorized")
	}

	approved, err := svc.Approve(approval.ID, target.Address)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !approved.Authorized {
		t.Fatalf("expected the approval to be marked authorized")
	}

	redeemed, sessionToken, err := svc.VerifyApproval(link.ID, approval.ID)
	if err != nil {
		t.Fatalf("VerifyApproval: %v", err)
	}
	if redeemed.ID != approval.ID {
		t.Fatalf("expected the same approval to be returned")
	}
	if sessionToken == "" {
		t.Fatalf("expected a LOGIN approval to mint a wallet-session token")
	}

	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(sessionToken, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(svc.JWTSecret), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("expected a valid JWT, got err=%v valid=%v", err, parsed != nil && parsed.Valid)
	}
	if claims.Subject != target.Address {
		t.Fatalf("expected the session token's subject to be the target user's address, got %q", claims.Subject)
	}

	// Single-use: redeeming the same approval a second time must fail.
	if _, _, err := svc.VerifyApproval(link.ID, approval.ID); err == nil {
		t.Fatalf("expected a second VerifyApproval on the same approval ID to fail")
	}
}

func TestApprove_RejectsWrongCaller(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanRequestAuthorization: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)
	impersonator := createTestUser(t, db, "impersonator", "0x7777777777777777777777777777777777777777", nil)

	approval, err := svc.RequestApproval(link.ID, RequestApprovalInput{
		Kind:           models.ApprovalAuthorize,
		TargetUsername: target.Username,
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	_, err = svc.Approve(approval.ID, impersonator.Address)
	if err == nil {
		t.Fatalf("expected Approve to reject a caller who is not the approval's target user")
	}
	if got := statusOf(t, err); got != 403 {
		t.Fatalf("expected 403, got %d", got)
	}
}

func TestVerifyApproval_RejectsUnauthorizedRequest(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanRegisterEvents: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)

	approval, err := svc.RequestApproval(link.ID, RequestApprovalInput{
		Kind:           models.ApprovalEvent,
		TargetUsername: target.Username,
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}

	if _, _, err := svc.VerifyApproval(link.ID, approval.ID); err == nil {
		t.Fatalf("expected VerifyApproval to reject an approval the target user has not authorized yet")
	}
}

func TestVerifyApproval_RejectsWrongServiceLink(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	linkA, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanLogin: true})
	linkB, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "globex", CanLogin: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)

	approval, err := svc.RequestApproval(linkA.ID, RequestApprovalInput{Kind: models.ApprovalLogin, TargetUsername: target.Username})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if _, err := svc.Approve(approval.ID, target.Address); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if _, _, err := svc.VerifyApproval(linkB.ID, approval.ID); err == nil {
		t.Fatalf("expected VerifyApproval to reject a different service link redeeming this approval")
	}
}
