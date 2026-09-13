package services

import (
	"net/http"
	"testing"

	"wallet-backend/internal/components/tokenization/models"
)

func TestSubmitApplication_RequiresKYC(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", false)

	_, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err == nil {
		t.Fatal("expected an error for a non-KYC-verified applicant")
	}
	if status := appErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", status)
	}
}

func TestSubmitApplication_RejectsUnsupportedQuoteCurrency(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)

	body := validDraftAsset()
	body.AssetQuoteCurrency = "NOTREAL"
	_, err := svc.SubmitApplication(user.ID, body)
	if err == nil {
		t.Fatal("expected an error for an unsupported quote currency")
	}
	if status := appErrStatus(t, err); status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
}

func TestSubmitApplication_CreatesDraft(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)

	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	if asset.Status != models.StatusDraft {
		t.Fatalf("expected status DRAFT, got %s", asset.Status)
	}
	if asset.InitiatorUserID != user.ID {
		t.Fatalf("expected initiator %d, got %d", user.ID, asset.InitiatorUserID)
	}
	if asset.AssetQuoteCurrency != "USDC" {
		t.Fatalf("expected default quote currency USDC, got %s", asset.AssetQuoteCurrency)
	}
}

// TestSubmitApplication_CannotSmuggleServerControlledFields is a security
// test: even if a client's JSON body sets Status, fee-snapshot fields, or
// stakeholder IDs directly, SubmitApplication must silently overwrite them
// rather than trust the client - see resetServerControlledFields.
func TestSubmitApplication_CannotSmuggleServerControlledFields(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)

	body := validDraftAsset()
	body.Status = models.StatusMinted
	body.VettingStatus = true
	body.AssetManagerID = 999
	body.SECFeePercent = "50"
	body.MintingApprovers = "0xattacker"
	contractAddr := "0xshouldnotbeset0000000000000000000000000"
	body.IssuerContractAddress = &contractAddr

	asset, err := svc.SubmitApplication(user.ID, body)
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	if asset.Status != models.StatusDraft {
		t.Fatalf("expected Status forced back to DRAFT, got %s", asset.Status)
	}
	if asset.VettingStatus {
		t.Fatal("expected VettingStatus forced to false")
	}
	if asset.AssetManagerID != 0 {
		t.Fatalf("expected AssetManagerID forced to 0, got %d", asset.AssetManagerID)
	}
	if asset.SECFeePercent != "" {
		t.Fatalf("expected SECFeePercent forced empty, got %q", asset.SECFeePercent)
	}
	if asset.MintingApprovers != "" {
		t.Fatalf("expected MintingApprovers forced empty, got %q", asset.MintingApprovers)
	}
	if asset.IssuerContractAddress != nil {
		t.Fatalf("expected IssuerContractAddress forced nil, got %v", *asset.IssuerContractAddress)
	}
}

func TestSubmitApplication_ResubmissionBlockedPastDraft(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)

	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	asset.Status = models.StatusApplicationConfirmed
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("failed to advance status: %v", err)
	}

	_, err = svc.SubmitApplication(user.ID, validDraftAsset())
	if err == nil {
		t.Fatal("expected an error resubmitting past draft")
	}
	if status := appErrStatus(t, err); status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
}

func TestVet_RejectsUnknownStakeholder(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}

	_, err = svc.Vet(asset.ID, VetInput{ApprovedAssetCustodianID: 999, AssetManagerID: 999, AssetIssuingHouseID: 999, LegalAndProfessionalPartnerID: 999, RatingAgencyID: 999, TrusteeID: 999})
	if err == nil {
		t.Fatal("expected an error for unknown stakeholders")
	}
	if status := appErrStatus(t, err); status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
}

func TestFailDueDiligence_RequiresReason(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}

	if _, err := svc.FailDueDiligence(asset.ID, "no"); err == nil {
		t.Fatal("expected an error for a too-short reason")
	}

	updated, err := svc.FailDueDiligence(asset.ID, "insufficient documentation")
	if err != nil {
		t.Fatalf("FailDueDiligence returned error: %v", err)
	}
	if !updated.DueDiligenceFailed || updated.Status != models.StatusDraft {
		t.Fatalf("expected due diligence failure recorded and status reset, got %+v", updated)
	}
}

func TestDelete_OnlyAllowedAtDraft(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	asset.Status = models.StatusMinted
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("failed to advance status: %v", err)
	}

	if err := svc.Delete(user.ID, asset.ID, true); err == nil {
		t.Fatal("expected delete to be rejected past draft")
	}

	asset.Status = models.StatusDraft
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("failed to reset status: %v", err)
	}
	if err := svc.Delete(user.ID, asset.ID, true); err != nil {
		t.Fatalf("expected delete to succeed at draft, got: %v", err)
	}
}
