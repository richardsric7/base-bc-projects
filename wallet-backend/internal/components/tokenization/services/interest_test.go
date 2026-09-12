package services

import (
	"net/http"
	"testing"

	"wallet-backend/internal/components/tokenization/models"
)

func TestExpressInterest_RequiresMintedStatus(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	asset := models.TokenizedAsset{Status: models.StatusDraft}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	_, err := svc.ExpressInterest(user.ID, asset.ID, "100")
	if err == nil {
		t.Fatal("expected an error expressing interest before minting")
	}
	if status := appErrStatus(t, err); status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}

	asset.Status = models.StatusMinted
	if err := db.Save(&asset).Error; err != nil {
		t.Fatalf("advance status: %v", err)
	}
	interest, err := svc.ExpressInterest(user.ID, asset.ID, "100")
	if err != nil {
		t.Fatalf("ExpressInterest returned error: %v", err)
	}
	if interest.Amount != "100" {
		t.Fatalf("expected amount 100, got %s", interest.Amount)
	}

	// Re-expressing interest upserts rather than duplicating.
	if _, err := svc.ExpressInterest(user.ID, asset.ID, "200"); err != nil {
		t.Fatalf("second ExpressInterest returned error: %v", err)
	}
	interests, err := svc.ListExpressionsOfInterest(asset.ID)
	if err != nil {
		t.Fatalf("ListExpressionsOfInterest returned error: %v", err)
	}
	if len(interests) != 1 || interests[0].Amount != "200" {
		t.Fatalf("expected exactly one upserted interest with amount 200, got %+v", interests)
	}
}
