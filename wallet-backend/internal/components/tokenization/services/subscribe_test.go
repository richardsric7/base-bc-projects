package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/shopspring/decimal"

	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"
)

func TestBuildCryptoPurchase_RejectsWhenNotOnSale(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	buyer := createTestUser(t, db, "buyer", true)

	saleAddr := "0xsale00000000000000000000000000000000000"
	asset := models.TokenizedAsset{
		Status:              models.StatusDraft,
		AssetQuoteCurrency:  "USDC",
		PricePerToken:       "2",
		AssetDecimals:       2,
		SaleContractAddress: &saleAddr,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	_, _, err := svc.BuildCryptoPurchase(context.Background(), buyer.ID, asset.ID, decimal.NewFromInt(10))
	if err == nil {
		t.Fatal("expected an error purchasing an asset not open for sale")
	}
	if status := appErrStatus(t, err); status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
}

func TestBuildCryptoPurchase_EnforcesPrivateOfferingMembership(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	buyer := createTestUser(t, db, "buyer", true)

	group := sharedaccessModels.ClosedGroup{Name: "private offering", Purpose: sharedaccessModels.PurposePrivateOffering}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("seed closed group: %v", err)
	}

	saleAddr := "0xsale00000000000000000000000000000000000"
	groupID := group.ID
	asset := models.TokenizedAsset{
		Status:              models.StatusPrimarySaleActive,
		OfferingType:        models.OfferingPrivate,
		ClosedGroupID:       &groupID,
		AssetQuoteCurrency:  "USDC",
		PricePerToken:       "2",
		AssetDecimals:       2,
		SaleContractAddress: &saleAddr,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	_, _, err := svc.BuildCryptoPurchase(context.Background(), buyer.ID, asset.ID, decimal.NewFromInt(10))
	if err == nil {
		t.Fatal("expected an error for a non-member buying into a private offering")
	}
	if status := appErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", status)
	}

	if err := db.Create(&sharedaccessModels.GroupMember{GroupID: group.ID, MemberAddress: buyer.Address, Role: sharedaccessModels.RoleViewOnly}).Error; err != nil {
		t.Fatalf("add buyer to group: %v", err)
	}
	tx, paymentAmount, err := svc.BuildCryptoPurchase(context.Background(), buyer.ID, asset.ID, decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("expected purchase to succeed once buyer is a group member, got: %v", err)
	}
	if tx == nil {
		t.Fatal("expected an unsigned transaction")
	}
	if !paymentAmount.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("expected payment amount 10*2=20, got %s", paymentAmount.String())
	}
}

func TestBuildFiatPurchase_RejectsWhenNotConfigured(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	buyer := createTestUser(t, db, "buyer", true)
	distributionAddr := "0xdist0000000000000000000000000000000000"
	issuerAddr := "0xissuer00000000000000000000000000000000"
	asset := models.TokenizedAsset{
		Status:                models.StatusPrimarySaleActive,
		AssetQuoteCurrency:    "USDC",
		PricePerToken:         "2",
		AssetDecimals:         2,
		DistributionAddress:   &distributionAddr,
		IssuerContractAddress: &issuerAddr,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	_, err := svc.BuildFiatPurchase(context.Background(), buyer.ID, asset.ID, decimal.NewFromInt(5), "invoice-1", "NGN")
	if err == nil {
		t.Fatal("expected an error when no fiat invoice hook is configured")
	}
	if status := appErrStatus(t, err); status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
}

func TestBuildFiatPurchase_CreatesInvoiceAndSubscription(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	buyer := createTestUser(t, db, "buyer", true)
	distributionAddr := "0xdist0000000000000000000000000000000000"
	issuerAddr := "0xissuer00000000000000000000000000000000"
	asset := models.TokenizedAsset{
		Status:                models.StatusPrimarySaleActive,
		OfferingType:          models.OfferingPublic,
		AssetQuoteCurrency:    "USDC",
		PricePerToken:         "2",
		AssetDecimals:         2,
		DistributionAddress:   &distributionAddr,
		IssuerContractAddress: &issuerAddr,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	var capturedAmount float64
	svc.CreateFiatInvoice = func(address, id, serviceProvider, paymentType string, amount float64, currency string, signedTransaction *string) error {
		capturedAmount = amount
		if signedTransaction == nil || *signedTransaction == "" {
			t.Fatal("expected a signed transaction to be attached to the invoice")
		}
		return nil
	}

	sub, err := svc.BuildFiatPurchase(context.Background(), buyer.ID, asset.ID, decimal.NewFromInt(5), "invoice-1", "NGN")
	if err != nil {
		t.Fatalf("BuildFiatPurchase returned error: %v", err)
	}
	if sub.Channel != "FIAT" {
		t.Fatalf("expected channel FIAT, got %s", sub.Channel)
	}
	if capturedAmount != 10 {
		t.Fatalf("expected payment amount 5*2=10, got %v", capturedAmount)
	}
}
