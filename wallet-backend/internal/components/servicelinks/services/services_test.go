package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	assetsModels "wallet-backend/internal/components/assets/models"
	assetsServices "wallet-backend/internal/components/assets/services"
	paymentsModels "wallet-backend/internal/components/payments/models"
	paymentsServices "wallet-backend/internal/components/payments/services"
	"wallet-backend/internal/components/servicelinks/models"
	shortlinkModels "wallet-backend/internal/components/shortlink/models"
	shortlinkServices "wallet-backend/internal/components/shortlink/services"
	tokenizationModels "wallet-backend/internal/components/tokenization/models"
	tokenizationServices "wallet-backend/internal/components/tokenization/services"
	usersModels "wallet-backend/internal/components/users/models"
	usersServices "wallet-backend/internal/components/users/services"
	"wallet-backend/internal/middleware"
	"wallet-backend/internal/network"
	"wallet-backend/internal/notify"
	"wallet-backend/internal/storage"
)

// fakeTokenizationBlockchain satisfies tokenizationServices.BlockchainClient
// without touching the network - none of the tests in this package
// exercise an on-chain path, they only need tokenization.Service to
// construct.
type fakeTokenizationBlockchain struct{}

func (fakeTokenizationBlockchain) DeployContract(context.Context, *ecdsa.PrivateKey, []byte) (string, string, error) {
	return "", "", nil
}
func (fakeTokenizationBlockchain) SignAndSubmitTx(context.Context, *ecdsa.PrivateKey, *common.Address, *big.Int, []byte, *uint64) (string, error) {
	return "", nil
}
func (fakeTokenizationBlockchain) SignTx(context.Context, *ecdsa.PrivateKey, *common.Address, *big.Int, []byte, *uint64) (string, error) {
	return "", nil
}
func (fakeTokenizationBlockchain) BuildContractCallTx(context.Context, string, string, *big.Int, []byte, *uint64) (*network.UnsignedTx, error) {
	return &network.UnsignedTx{}, nil
}
func (fakeTokenizationBlockchain) SubmitSignedTransaction(context.Context, string) (string, error) {
	return "", nil
}
func (fakeTokenizationBlockchain) ERC20BalanceOf(context.Context, string, string) (*big.Int, error) {
	return big.NewInt(0), nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	for _, migrate := range [][]interface{}{models.Models, usersModels.Models, assetsModels.Models, paymentsModels.Models, tokenizationModels.Models, shortlinkModels.Models} {
		if err := db.AutoMigrate(migrate...); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

func newTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	usersSvc := usersServices.New(db, notify.NewConsoleMailer(), "test-recovery-salt", time.Hour, nil, "test-deployer-salt")
	paymentsSvc := paymentsServices.New(db, (*network.Client)(nil))
	assetsSvc := assetsServices.New(db, (*network.Client)(nil))
	tokenizationSvc := tokenizationServices.New(db, fakeTokenizationBlockchain{}, nil, "issuer-salt", "distribution-salt", decimal.Zero)
	blob, err := storage.NewLocalDiskBlob(t.TempDir(), "/files")
	if err != nil {
		t.Fatalf("init local blob storage: %v", err)
	}
	svc := New(db, usersSvc, paymentsSvc, assetsSvc, tokenizationSvc, blob, notify.NewNoopPushProvider(), "test-jwt-secret", time.Hour, 10*time.Minute)
	svc.Shortlink = shortlinkServices.New(db, "https://test.example")
	return svc, db
}

func createTestUser(t *testing.T, db *gorm.DB, username, address string, createdByServiceLinkID *uint) usersModels.User {
	t.Helper()
	user := usersModels.User{
		Username:               username,
		Email:                  username + "@example.com",
		Address:                address,
		SignerAddress:          address,
		CreatedByServiceLinkID: createdByServiceLinkID,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	appErr, ok := err.(apperrors.GenericError)
	if !ok {
		t.Fatalf("expected an apperrors.GenericError, got %T: %v", err, err)
	}
	return appErr.StatusCode()
}

// --- admin: CreateServiceLink / verify / suspend / reactivate ---

func TestCreateServiceLink_ReturnsRawKeyOnceAndStoresOnlyItsHash(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)

	link, rawKey, err := svc.CreateServiceLink(CreateServiceLinkInput{
		OwnerUserID: owner.ID,
		ShortName:   "acme",
		CanLogin:    true,
	})
	if err != nil {
		t.Fatalf("CreateServiceLink: %v", err)
	}
	if rawKey == "" {
		t.Fatalf("expected a raw API key to be returned")
	}
	if link.APIKeyHash == rawKey {
		t.Fatalf("the raw key must never be stored verbatim")
	}
	if link.APIKeyHash != middleware.HashAPIKey(rawKey) {
		t.Fatalf("stored hash does not match the returned raw key's hash")
	}
	if link.Verified {
		t.Fatalf("a newly created service link must start unverified")
	}
}

func TestCreateServiceLink_DuplicateShortNameConflicts(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)

	if _, _, err := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, _, err := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})
	if err == nil {
		t.Fatalf("expected a conflict for a duplicate short name")
	}
	if got := statusOf(t, err); got != 409 {
		t.Fatalf("expected 409, got %d", got)
	}
}

func TestSuspendAndReactivateServiceLink(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})

	suspended, err := svc.SuspendServiceLink(link.ID, "fraud review")
	if err != nil {
		t.Fatalf("SuspendServiceLink: %v", err)
	}
	if !suspended.Suspended || suspended.SuspensionReason != "fraud review" {
		t.Fatalf("expected the link to be suspended with a reason recorded")
	}

	reactivated, err := svc.ReactivateServiceLink(link.ID)
	if err != nil {
		t.Fatalf("ReactivateServiceLink: %v", err)
	}
	if reactivated.Suspended || reactivated.SuspensionReason != "" {
		t.Fatalf("expected the suspension to be fully cleared")
	}
}

// --- requireOwnedUser: the core deny-by-default fix (PLAN.md §4.11 finding 6) ---

func TestRequireOwnedUser_RejectsOrganicUser(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})

	// An organically-registered user - CreatedByServiceLinkID is nil. The
	// original's equivalent check treated nil as "skip the check" and
	// silently allowed acting on this user; requireOwnedUser must reject it.
	organicUser := createTestUser(t, db, "organic", "0x2222222222222222222222222222222222222222", nil)

	_, err := svc.requireOwnedUser(link.ID, organicUser.ID)
	if err == nil {
		t.Fatalf("expected requireOwnedUser to reject a user with no owning service link")
	}
	if got := statusOf(t, err); got != 403 {
		t.Fatalf("expected 403, got %d", got)
	}
}

func TestRequireOwnedUser_RejectsUserOwnedByAnotherServiceLink(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	linkA, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})
	linkB, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "globex"})

	otherPartnersUser := createTestUser(t, db, "otherpartner", "0x3333333333333333333333333333333333333333", &linkB.ID)

	if _, err := svc.requireOwnedUser(linkA.ID, otherPartnersUser.ID); err == nil {
		t.Fatalf("expected requireOwnedUser to reject a user owned by a different service link")
	}
}

func TestRequireOwnedUser_AcceptsOwnedUser(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})

	ownedUser := createTestUser(t, db, "owned", "0x4444444444444444444444444444444444444444", &link.ID)

	user, err := svc.requireOwnedUser(link.ID, ownedUser.ID)
	if err != nil {
		t.Fatalf("requireOwnedUser: %v", err)
	}
	if user.ID != ownedUser.ID {
		t.Fatalf("expected the owned user to be returned")
	}
}

// --- onboarding + KYC override ---

func TestOnboardUser_TagsCreatedByServiceLinkID(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})

	user, err := svc.OnboardUser(link.ID, "partneruser", "partneruser@example.com", "0x5555555555555555555555555555555555555555")
	if err != nil {
		t.Fatalf("OnboardUser: %v", err)
	}
	if user.CreatedByServiceLinkID == nil || *user.CreatedByServiceLinkID != link.ID {
		t.Fatalf("expected the onboarded user to be tagged with the onboarding service link")
	}
}

func TestUpdateKYCStatus_RejectsOrganicUser(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})
	organicUser := createTestUser(t, db, "organic", "0x2222222222222222222222222222222222222222", nil)

	err := svc.UpdateKYCStatus(link.ID, organicUser.ID, 2)
	if err == nil {
		t.Fatalf("expected UpdateKYCStatus to reject overriding an organically-registered user's KYC level")
	}
	if got := statusOf(t, err); got != 403 {
		t.Fatalf("expected 403, got %d", got)
	}
}

func TestUpdateKYCStatus_AcceptsOwnedUser(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})
	ownedUser := createTestUser(t, db, "owned", "0x4444444444444444444444444444444444444444", &link.ID)

	if err := svc.UpdateKYCStatus(link.ID, ownedUser.ID, 2); err != nil {
		t.Fatalf("UpdateKYCStatus: %v", err)
	}

	updated, err := svc.Users.GetByID(ownedUser.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.KYCVerifiedLevel != 2 {
		t.Fatalf("expected KYCVerifiedLevel to be updated to 2, got %d", updated.KYCVerifiedLevel)
	}
}
