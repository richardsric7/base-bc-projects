package services

import (
	"testing"

	"wallet-backend/internal/components/kyc/models"
	usersModels "wallet-backend/internal/components/users/models"
)

func createTestWidget(t *testing.T, svc *Service, id string, level int) {
	t.Helper()
	if err := svc.DB.Create(&models.DojaWidget{ID: id, Level: level}).Error; err != nil {
		t.Fatalf("create test widget: %v", err)
	}
}

func TestVerifyDojaWebhookSignature(t *testing.T) {
	svc, _ := newTestService(t, "")
	payload := []byte(`{"widget_id":"w1"}`)
	validSig := sumsubSignPayload(t, svc.DojaSecretKey, payload)

	if !svc.VerifyDojaWebhookSignature(payload, validSig) {
		t.Fatal("expected a matching signature to verify")
	}
	if svc.VerifyDojaWebhookSignature(payload, "deadbeef") {
		t.Fatal("expected a forged signature to be rejected")
	}
	if svc.VerifyDojaWebhookSignature([]byte(`{"tampered":true}`), validSig) {
		t.Fatal("expected a signature for different content to be rejected")
	}
}

func TestProcessDojaWebhook_RejectsUnknownWidget(t *testing.T) {
	svc, db := newTestService(t, "")
	createTestUser(t, db, "erin")

	event := &models.DojaWebhookEvent{WidgetID: "no-such-widget"}
	event.Metadata.UserID = "erin"

	err := svc.ProcessDojaWebhook(event)
	if err == nil {
		t.Fatal("expected an error for an unrecognized widget ID")
	}
	if status := appErrStatus(t, err); status != 400 {
		t.Fatalf("expected a 400 AppError, got %d", status)
	}
}

func TestProcessDojaWebhook_UnknownUserIsSoftNoOp(t *testing.T) {
	svc, _ := newTestService(t, "")
	createTestWidget(t, svc, "widget-l1", 1)

	event := &models.DojaWebhookEvent{WidgetID: "widget-l1", VerificationStatus: "Completed"}
	event.Metadata.UserID = "nobody"

	if err := svc.ProcessDojaWebhook(event); err != nil {
		t.Fatalf("expected a soft no-op (nil error) for an unknown username, got %v", err)
	}
}

func TestProcessDojaWebhook_CompletedRaisesVerifiedLevel(t *testing.T) {
	svc, db := newTestService(t, "")
	user := createTestUser(t, db, "frank")
	createTestWidget(t, svc, "widget-l2", 2)

	event := &models.DojaWebhookEvent{
		WidgetID:           "widget-l2",
		VerificationStatus: "Completed",
		IDType:             "BVN",
		Value:              "22222222222",
	}
	event.Metadata.UserID = "frank"

	if err := svc.ProcessDojaWebhook(event); err != nil {
		t.Fatalf("ProcessDojaWebhook returned error: %v", err)
	}

	progress, err := svc.GetDojaProgress(user.Address)
	if err != nil {
		t.Fatalf("GetDojaProgress returned error: %v", err)
	}
	if !progress.Level2Submitted || !progress.Level2Completed {
		t.Fatalf("expected level 2 submitted+completed, got %+v", progress)
	}

	var reloaded usersModels.User
	if err := db.Where("id = ?", user.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.KYCVerifiedLevel != 2 {
		t.Fatalf("expected KYCVerifiedLevel to be raised to 2, got %d", reloaded.KYCVerifiedLevel)
	}
}

func TestProcessDojaWebhook_PendingThenFailedResets(t *testing.T) {
	svc, db := newTestService(t, "")
	user := createTestUser(t, db, "grace")
	createTestWidget(t, svc, "widget-l1", 1)

	pending := &models.DojaWebhookEvent{WidgetID: "widget-l1", VerificationStatus: "Pending"}
	pending.Metadata.UserID = "grace"
	if err := svc.ProcessDojaWebhook(pending); err != nil {
		t.Fatalf("ProcessDojaWebhook (pending) returned error: %v", err)
	}

	progress, err := svc.GetDojaProgress(user.Address)
	if err != nil {
		t.Fatalf("GetDojaProgress returned error: %v", err)
	}
	if !progress.Level1Submitted || progress.Level1Completed {
		t.Fatalf("expected level 1 submitted but not completed, got %+v", progress)
	}

	failed := &models.DojaWebhookEvent{WidgetID: "widget-l1", VerificationStatus: "Failed"}
	failed.Metadata.UserID = "grace"
	if err := svc.ProcessDojaWebhook(failed); err != nil {
		t.Fatalf("ProcessDojaWebhook (failed) returned error: %v", err)
	}

	progress, err = svc.GetDojaProgress(user.Address)
	if err != nil {
		t.Fatalf("GetDojaProgress returned error: %v", err)
	}
	if progress.Level1Submitted || progress.Level1Completed {
		t.Fatalf("expected level 1 to be reset after Failed, got %+v", progress)
	}
}

func TestProcessDojaWebhook_DoesNotLowerVerifiedLevel(t *testing.T) {
	svc, db := newTestService(t, "")
	user := createTestUser(t, db, "heidi")
	if err := db.Model(&user).Update("kyc_verified_level", 3).Error; err != nil {
		t.Fatalf("seed KYCVerifiedLevel: %v", err)
	}
	createTestWidget(t, svc, "widget-l1", 1)

	event := &models.DojaWebhookEvent{WidgetID: "widget-l1", VerificationStatus: "Completed"}
	event.Metadata.UserID = "heidi"
	if err := svc.ProcessDojaWebhook(event); err != nil {
		t.Fatalf("ProcessDojaWebhook returned error: %v", err)
	}

	var reloaded usersModels.User
	if err := db.Where("id = ?", user.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.KYCVerifiedLevel != 3 {
		t.Fatalf("expected KYCVerifiedLevel to remain 3, got %d", reloaded.KYCVerifiedLevel)
	}
}
