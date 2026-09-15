package services

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"wallet-backend/internal/components/tokenization/models"
)

func TestParseExitPercentage(t *testing.T) {
	cases := map[string]string{
		"5%":   "5",
		"5":    "5",
		" 3 ":  "3",
		"":     "0",
		"junk": "0",
	}
	for input, want := range cases {
		got := parseExitPercentage(input)
		if got.String() != want {
			t.Errorf("parseExitPercentage(%q) = %s, want %s", input, got.String(), want)
		}
	}
}

func TestRecordEarlyExit_ComputesPenaltyAdjustedPayout(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	holder := createTestUser(t, db, "holder", true)

	asset := models.TokenizedAsset{
		AssetQuoteCurrency:      "USDC",
		CurrentNAVPerToken:      "10",
		EarlyExitPenaltyPercent: "5%",
		EarlyExitFeePercent:     "2%",
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	svc.SharedAccess = readyGroupWalletExecutor("0xburntx")
	exit, err := svc.ConfirmEarlyExit(context.Background(), holder.ID, asset.ID, decimal.NewFromInt(100), 1, "0001", "Holder Name", 1, testSigner, "0xsig")
	if err != nil {
		t.Fatalf("ConfirmEarlyExit returned error: %v", err)
	}
	// payoutPricePerToken = 10 * (1 - 0.07) = 9.3; estimatedPayout = 9.3*100 = 930
	if exit.PayoutPricePerToken != "9.3" {
		t.Fatalf("expected payoutPricePerToken 9.3, got %s", exit.PayoutPricePerToken)
	}
	if exit.EstimatedPayoutAmount != "930" {
		t.Fatalf("expected estimatedPayoutAmount 930, got %s", exit.EstimatedPayoutAmount)
	}
}

func TestBuildEarlyExit_RejectsAfterMaturity(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	holder := createTestUser(t, db, "holder", true)

	issuerAddr := "0xissuer00000000000000000000000000000000"
	past := time.Now().Add(-24 * time.Hour)
	asset := models.TokenizedAsset{
		Status:                models.StatusPrimarySaleActive,
		IssuerContractAddress: &issuerAddr,
		MaturityDate:          &past,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	svc.SharedAccess = readyGroupWalletExecutor("0xtxhash")
	_, err := svc.BuildEarlyExit(context.Background(), holder.ID, asset.ID, decimal.NewFromInt(10), testSigner)
	if err == nil {
		t.Fatal("expected an error for early exit after maturity")
	}
	if status := appErrStatus(t, err); status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
}
