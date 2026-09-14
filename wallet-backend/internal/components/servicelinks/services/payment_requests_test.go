package services

import (
	"strings"
	"testing"

	"wallet-backend/internal/components/servicelinks/models"
)

func TestRequestPaymentLink_MintsQRAndShortURL(t *testing.T) {
	svc, _ := newTestService(t)

	result, err := svc.RequestPaymentLink("0xAbc0000000000000000000000000000000000A", "0xToken00000000000000000000000000000000", "1000000000000000000", "invoice #42")
	if err != nil {
		t.Fatalf("RequestPaymentLink: %v", err)
	}
	if result.To == "" || result.Amount == "" {
		t.Fatalf("expected the request fields to be echoed back, got %+v", result)
	}
	if result.ShortURL == "" || result.QRURL == "" {
		t.Fatalf("expected a minted short URL and QR URL, got %+v", result)
	}
	if !strings.HasSuffix(result.QRURL, "/qr") {
		t.Fatalf("expected the QR URL to be the short URL with /qr appended, got %q", result.QRURL)
	}
}

func TestRequestPaymentLink_RequiresToAndAmount(t *testing.T) {
	svc, _ := newTestService(t)

	if _, err := svc.RequestPaymentLink("", "", "100", ""); err == nil {
		t.Fatalf("expected an error when to is missing")
	}
	if _, err := svc.RequestPaymentLink("0xAbc0000000000000000000000000000000000A", "", "", ""); err == nil {
		t.Fatalf("expected an error when amount is missing")
	}
}

func TestRequestPaymentLinkForOwnedUser_RejectsOrganicUser(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanSendPayments: true})
	organicUser := createTestUser(t, db, "organic", "0x2222222222222222222222222222222222222222", nil)

	if _, err := svc.RequestPaymentLinkForOwnedUser(link.ID, organicUser.ID, "0xdest", "", "100", ""); err == nil {
		t.Fatalf("expected RequestPaymentLinkForOwnedUser to reject a user not owned by this service link")
	}
}

func TestRequestPaymentLinkForOwnedUser_AcceptsOwnedUser(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanSendPayments: true})
	ownedUser := createTestUser(t, db, "owned", "0x4444444444444444444444444444444444444444", &link.ID)

	result, err := svc.RequestPaymentLinkForOwnedUser(link.ID, ownedUser.ID, "0xdest", "0xtoken", "100", "for coffee")
	if err != nil {
		t.Fatalf("RequestPaymentLinkForOwnedUser: %v", err)
	}
	if result.To != "0xdest" || result.Amount != "100" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRequestApproval_MintsShortURLAndQRURL(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanLogin: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)

	approval, err := svc.RequestApproval(link.ID, RequestApprovalInput{
		Kind:           models.ApprovalLogin,
		TargetUsername: target.Username,
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if approval.ShortURL == "" || approval.QRURL == "" {
		t.Fatalf("expected RequestApproval to mint a short URL and QR URL, got %+v", approval)
	}
}

func TestRequestApproval_NoShortlinkWiredIsANoop(t *testing.T) {
	svc, db := newTestService(t)
	svc.Shortlink = nil
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme", CanLogin: true})
	target := createTestUser(t, db, "targetuser", "0x6666666666666666666666666666666666666666", nil)

	approval, err := svc.RequestApproval(link.ID, RequestApprovalInput{
		Kind:           models.ApprovalLogin,
		TargetUsername: target.Username,
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if approval.ShortURL != "" || approval.QRURL != "" {
		t.Fatalf("expected no short URL/QR URL when Shortlink is nil, got %+v", approval)
	}
}
