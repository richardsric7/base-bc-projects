package services

import (
	"context"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	"wallet-backend/internal/components/patron/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/rates"
)

type fakeBlockchain struct{}

func (f *fakeBlockchain) BuildNativeTransferTx(_ context.Context, from, to string, amountWei *big.Int, _ *uint64) (*network.UnsignedTx, error) {
	return &network.UnsignedTx{To: to, Value: amountWei.String()}, nil
}

func (f *fakeBlockchain) BuildERC20TransferTx(_ context.Context, from, tokenAddress, to string, amount *big.Int, _ *uint64) (*network.UnsignedTx, error) {
	return &network.UnsignedTx{To: tokenAddress, Data: common.Bytes2Hex(amount.Bytes())}, nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	for _, migrate := range [][]interface{}{models.Models, usersModels.Models, assetsModels.Models} {
		if err := db.AutoMigrate(migrate...); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

func newTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	ratesProvider := rates.NewStaticProvider(map[string]decimal.Decimal{
		"USD/USDC": decimal.NewFromInt(1),
	})
	svc := New(db, &fakeBlockchain{}, ratesProvider, "test-salt", 5)
	if err := db.Create(&models.PatronSubscriptionPaymentAsset{Symbol: "USDC"}).Error; err != nil {
		t.Fatalf("seed payment asset: %v", err)
	}
	return svc, db
}

func createTestUser(t *testing.T, db *gorm.DB, username string) usersModels.User {
	t.Helper()
	address := "0x" + username + "000000000000000000000000000000000"
	user := usersModels.User{
		Username:      username,
		Email:         username + "@example.com",
		Address:       address,
		SignerAddress: address,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func createGrade(t *testing.T, db *gorm.DB, packageID, tierID string, priceUSD float64) models.PatronMembershipGrade {
	t.Helper()
	grade := models.PatronMembershipGrade{PatronPackageID: packageID, PatronTierID: tierID, PriceUSD: priceUSD}
	if err := db.Create(&grade).Error; err != nil {
		t.Fatalf("create grade: %v", err)
	}
	return grade
}

func appErrStatus(t *testing.T, err error) int {
	t.Helper()
	appErr, ok := err.(*apperrors.AppError)
	if !ok {
		t.Fatalf("expected an *apperrors.AppError, got %#v", err)
	}
	return appErr.StatusCode()
}

func TestValidateUpgrade_BlocksHighestTierAndPackage(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	cases := []struct {
		name      string
		existing  *models.UserPatronMembership
		targetPkg string
		wantErr   bool
	}{
		{"diamond lifetime blocks everything", &models.UserPatronMembership{PatronPackageID: "DIAMOND", PatronTierID: "LIFETIME", ValidTill: future}, "GOLD", true},
		{"platinum lifetime blocks gold", &models.UserPatronMembership{PatronPackageID: "PLATINUM", PatronTierID: "LIFETIME", ValidTill: future}, "GOLD", true},
		{"platinum lifetime blocks platinum", &models.UserPatronMembership{PatronPackageID: "PLATINUM", PatronTierID: "LIFETIME", ValidTill: future}, "PLATINUM", true},
		{"platinum lifetime allows diamond", &models.UserPatronMembership{PatronPackageID: "PLATINUM", PatronTierID: "LIFETIME", ValidTill: future}, "DIAMOND", false},
		{"gold lifetime blocks gold", &models.UserPatronMembership{PatronPackageID: "GOLD", PatronTierID: "LIFETIME", ValidTill: future}, "GOLD", true},
		{"gold lifetime allows platinum", &models.UserPatronMembership{PatronPackageID: "GOLD", PatronTierID: "LIFETIME", ValidTill: future}, "PLATINUM", false},
		{"gold monthly allows gold", &models.UserPatronMembership{PatronPackageID: "GOLD", PatronTierID: "MONTHLY", ValidTill: future}, "GOLD", false},
		{"no existing membership", nil, "GOLD", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUpgrade(tc.existing, tc.targetPkg, "MONTHLY")
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestSchedule_NewMemberIsInstant(t *testing.T) {
	effectiveDate, validTill, instant := schedule(nil, "GOLD", "MONTHLY")
	if !instant {
		t.Fatal("expected a new subscription to activate instantly")
	}
	if effectiveDate.After(time.Now()) {
		t.Fatal("expected effective date to be now")
	}
	if !validTill.After(time.Now().Add(29*24*time.Hour)) || validTill.After(time.Now().Add(32*24*time.Hour)) {
		t.Fatalf("expected validTill roughly 1 month out, got %v", validTill)
	}
}

func TestSchedule_UpgradeFromLowerTierIsDeferred(t *testing.T) {
	existing := &models.UserPatronMembership{
		PatronPackageID: "GOLD",
		PatronTierID:    "MONTHLY",
		ValidTill:       time.Now().Add(10 * 24 * time.Hour),
	}
	effectiveDate, _, instant := schedule(existing, "PLATINUM", "MONTHLY")
	if instant {
		t.Fatal("expected the upgrade to be deferred, not instant")
	}
	expected := existing.ValidTill.AddDate(0, 0, 1)
	if effectiveDate.Sub(expected).Abs() > time.Second {
		t.Fatalf("expected effective date %v, got %v", expected, effectiveDate)
	}
}

func TestSchedule_ExpiredMembershipRenewsInstantly(t *testing.T) {
	existing := &models.UserPatronMembership{
		PatronPackageID: "GOLD",
		PatronTierID:    "MONTHLY",
		ValidTill:       time.Now().Add(-24 * time.Hour),
	}
	_, _, instant := schedule(existing, "GOLD", "MONTHLY")
	if !instant {
		t.Fatal("expected an expired membership to renew instantly")
	}
}

func TestSchedule_ExistingLifetimeAlwaysInstant(t *testing.T) {
	existing := &models.UserPatronMembership{
		PatronPackageID: "GOLD",
		PatronTierID:    "LIFETIME",
		ValidTill:       lifetimeDate,
	}
	_, _, instant := schedule(existing, "PLATINUM", "MONTHLY")
	if !instant {
		t.Fatal("expected an upgrade from an existing lifetime membership to be instant")
	}
}

func TestBuildSubscription_RejectsPendingSubscription(t *testing.T) {
	svc, db := newTestService(t)
	user := createTestUser(t, db, "alice")
	grade := createGrade(t, db, "GOLD", "MONTHLY", 10)

	if err := db.Create(&models.UserPatronSubscriptionLog{
		ID:              "pending-1",
		UserID:          user.ID,
		PatronPackageID: "PLATINUM",
		PatronTierID:    "MONTHLY",
		EffectiveDate:   time.Now().Add(48 * time.Hour),
		ValidTill:       time.Now().Add(30 * 24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed pending log: %v", err)
	}

	_, _, err := svc.BuildSubscription(context.Background(), user.ID, grade.ID, "USDC")
	if err == nil {
		t.Fatal("expected an error when a pending subscription already exists")
	}
	if status := appErrStatus(t, err); status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
}

func TestConfirmSubscription_InstantActivatesMembership(t *testing.T) {
	svc, db := newTestService(t)
	user := createTestUser(t, db, "alice")
	grade := createGrade(t, db, "GOLD", "MONTHLY", 10)

	logEntry, err := svc.ConfirmSubscription(context.Background(), user.ID, grade.ID, "USDC", "0xtxhash")
	if err != nil {
		t.Fatalf("ConfirmSubscription returned error: %v", err)
	}
	if logEntry.VATPaid != "0.5" {
		t.Fatalf("expected VAT of 0.5 (5%% of 10), got %s", logEntry.VATPaid)
	}

	membership, err := svc.GetMembership(user.ID)
	if err != nil {
		t.Fatalf("GetMembership returned error: %v", err)
	}
	if membership == nil || membership.PatronPackageID != "GOLD" {
		t.Fatalf("expected an instantly-activated GOLD membership, got %+v", membership)
	}
}

func TestConfirmSubscription_DeferredUpgradeDoesNotActivateYet(t *testing.T) {
	svc, db := newTestService(t)
	user := createTestUser(t, db, "alice")
	if err := db.Create(&models.UserPatronMembership{
		UserID:          user.ID,
		PatronPackageID: "GOLD",
		PatronTierID:    "MONTHLY",
		ValidTill:       time.Now().Add(10 * 24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	grade := createGrade(t, db, "PLATINUM", "MONTHLY", 20)

	_, err := svc.ConfirmSubscription(context.Background(), user.ID, grade.ID, "USDC", "0xtxhash")
	if err != nil {
		t.Fatalf("ConfirmSubscription returned error: %v", err)
	}

	membership, err := svc.GetMembership(user.ID)
	if err != nil {
		t.Fatalf("GetMembership returned error: %v", err)
	}
	if membership.PatronPackageID != "GOLD" {
		t.Fatalf("expected membership to remain GOLD until the deferred upgrade's effective date, got %s", membership.PatronPackageID)
	}
}

func TestPromotePendingMemberships_PromotesDueLogsOnly(t *testing.T) {
	svc, db := newTestService(t)
	user := createTestUser(t, db, "alice")

	if err := db.Create(&models.UserPatronSubscriptionLog{
		ID:              "future-log",
		UserID:          user.ID,
		PatronPackageID: "DIAMOND",
		PatronTierID:    "LIFETIME",
		EffectiveDate:   time.Now().Add(48 * time.Hour),
		ValidTill:       lifetimeDate,
	}).Error; err != nil {
		t.Fatalf("seed future log: %v", err)
	}
	if err := db.Create(&models.UserPatronSubscriptionLog{
		ID:              "due-log",
		UserID:          user.ID,
		PatronPackageID: "PLATINUM",
		PatronTierID:    "MONTHLY",
		EffectiveDate:   time.Now().Add(-time.Hour),
		ValidTill:       time.Now().Add(29 * 24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed due log: %v", err)
	}

	svc.PromotePendingMemberships()

	membership, err := svc.GetMembership(user.ID)
	if err != nil {
		t.Fatalf("GetMembership returned error: %v", err)
	}
	if membership == nil {
		t.Fatal("expected a promoted membership")
	}
	if membership.PatronPackageID != "PLATINUM" {
		t.Fatalf("expected the due (not future) log to be promoted, got %s", membership.PatronPackageID)
	}
}
