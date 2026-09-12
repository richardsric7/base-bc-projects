package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"wallet-backend/internal/components/kyc/models"
	usersModels "wallet-backend/internal/components/users/models"
)

// newFakeSumsubServer returns an httptest server that plays along with the
// three Sumsub endpoints InitiateSumsubLevel calls, so the ordering/progress
// logic can be tested without reaching the real Sumsub API.
func newFakeSumsubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/resources/applicants", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(models.SumsubApplicant{ID: "applicant-1", ExternalUserID: "alice"})
	})
	mux.HandleFunc("/resources/applicants/applicant-1/one", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(models.SumsubApplicant{ID: "applicant-1", ExternalUserID: "alice"})
	})
	mux.HandleFunc("/resources/accessTokens", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(models.SumsubAccessToken{Token: "sdk-token-xyz", UserID: "alice"})
	})
	return httptest.NewServer(mux)
}

func TestInitiateSumsubLevel_Level1Success(t *testing.T) {
	server := newFakeSumsubServer(t)
	defer server.Close()
	svc, db := newTestService(t, server.URL)
	user := createTestUser(t, db, "alice")

	token, applicant, err := svc.InitiateSumsubLevel(user.Address, "id-and-liveness-level-1")
	if err != nil {
		t.Fatalf("InitiateSumsubLevel returned error: %v", err)
	}
	if token != "sdk-token-xyz" {
		t.Fatalf("expected the mocked SDK token, got %q", token)
	}
	if applicant.ID != "applicant-1" {
		t.Fatalf("unexpected applicant: %+v", applicant)
	}

	progress, err := svc.GetSumsubProgress(user.Address)
	if err != nil {
		t.Fatalf("GetSumsubProgress returned error: %v", err)
	}
	if !progress.Level1Initiated || progress.Level1Done {
		t.Fatalf("expected level 1 initiated but not done, got %+v", progress)
	}
}

func TestInitiateSumsubLevel_RejectsOutOfOrderLevel(t *testing.T) {
	server := newFakeSumsubServer(t)
	defer server.Close()
	svc, db := newTestService(t, server.URL)
	user := createTestUser(t, db, "bob")

	_, _, err := svc.InitiateSumsubLevel(user.Address, "id-and-liveness-level-2")
	if err == nil {
		t.Fatal("expected an error initiating level 2 before level 1 is done")
	}
	if status := appErrStatus(t, err); status != 400 {
		t.Fatalf("expected a 400 AppError, got %d", status)
	}
}

func sumsubSignPayload(t *testing.T, secret string, payload []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySumsubWebhookSignature(t *testing.T) {
	svc, _ := newTestService(t, "")
	payload := []byte(`{"externalUserId":"alice"}`)
	validDigest := sumsubSignPayload(t, svc.SumsubSecretKey, payload)

	if !svc.VerifySumsubWebhookSignature(payload, validDigest, "HMAC_SHA256_HEX") {
		t.Fatal("expected a matching digest to verify")
	}
	if svc.VerifySumsubWebhookSignature(payload, validDigest, "SOME_OTHER_ALGO") {
		t.Fatal("expected an unrecognized algorithm to be rejected")
	}
	if svc.VerifySumsubWebhookSignature(payload, "deadbeef", "HMAC_SHA256_HEX") {
		t.Fatal("expected a forged digest to be rejected")
	}
	if svc.VerifySumsubWebhookSignature([]byte(`{"tampered":true}`), validDigest, "HMAC_SHA256_HEX") {
		t.Fatal("expected a digest for different content to be rejected")
	}
}

func TestProcessSumsubWebhook_GreenMarksLevelDoneAndRaisesVerifiedLevel(t *testing.T) {
	server := newFakeSumsubServer(t)
	defer server.Close()
	svc, db := newTestService(t, server.URL)
	user := createTestUser(t, db, "carol")

	if _, _, err := svc.InitiateSumsubLevel(user.Address, "id-and-liveness-level-1"); err != nil {
		t.Fatalf("InitiateSumsubLevel returned error: %v", err)
	}

	input := &models.SumsubWebhookInput{
		ExternalUserID: "carol",
		LevelName:      "id-and-liveness-level-1",
		ReviewResult:   models.SumsubReviewResult{ReviewAnswer: "GREEN"},
	}
	if err := svc.ProcessSumsubWebhook(input); err != nil {
		t.Fatalf("ProcessSumsubWebhook returned error: %v", err)
	}

	progress, err := svc.GetSumsubProgress(user.Address)
	if err != nil {
		t.Fatalf("GetSumsubProgress returned error: %v", err)
	}
	if !progress.Level1Done {
		t.Fatalf("expected level 1 to be marked done, got %+v", progress)
	}

	var reloaded usersModels.User
	if err := db.Where("id = ?", user.ID).First(&reloaded).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.KYCVerifiedLevel != 1 {
		t.Fatalf("expected KYCVerifiedLevel to be raised to 1, got %d", reloaded.KYCVerifiedLevel)
	}

	var logCount int64
	db.Model(&models.SumsubWebhookLog{}).Where("external_user_id = ?", "carol").Count(&logCount)
	if logCount != 1 {
		t.Fatalf("expected one audit log row, got %d", logCount)
	}
}

func TestProcessSumsubWebhook_RedResetsLevel(t *testing.T) {
	server := newFakeSumsubServer(t)
	defer server.Close()
	svc, db := newTestService(t, server.URL)
	user := createTestUser(t, db, "dave")

	if _, _, err := svc.InitiateSumsubLevel(user.Address, "id-and-liveness-level-1"); err != nil {
		t.Fatalf("InitiateSumsubLevel returned error: %v", err)
	}

	input := &models.SumsubWebhookInput{
		ExternalUserID: "dave",
		LevelName:      "id-and-liveness-level-1",
		ReviewResult:   models.SumsubReviewResult{ReviewAnswer: "RED"},
	}
	if err := svc.ProcessSumsubWebhook(input); err != nil {
		t.Fatalf("ProcessSumsubWebhook returned error: %v", err)
	}

	progress, err := svc.GetSumsubProgress(user.Address)
	if err != nil {
		t.Fatalf("GetSumsubProgress returned error: %v", err)
	}
	if progress.Level1Initiated || progress.Level1Done {
		t.Fatalf("expected level 1 to be reset after a RED review, got %+v", progress)
	}
}

func TestProcessSumsubWebhook_UnknownUserRejected(t *testing.T) {
	svc, _ := newTestService(t, "")
	input := &models.SumsubWebhookInput{ExternalUserID: "nobody", LevelName: "id-and-liveness-level-1"}
	if err := svc.ProcessSumsubWebhook(input); err == nil {
		t.Fatal("expected an error for a webhook referencing an unknown user")
	}
}
