package services

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/users/models"
	"wallet-backend/internal/network"
	"wallet-backend/internal/notify"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// ensurePrimaryWalletGroup (DeployPrimaryWallet) writes sharedaccess's
	// ClosedGroup/GroupMember rows directly (the same established pattern
	// recovery.go already uses to delete them on signer rotation) -
	// needed here too so those tests don't fail on a missing table.
	if err := db.AutoMigrate(sharedaccessModels.Models...); err != nil {
		t.Fatalf("migrate sharedaccess models: %v", err)
	}
	return db
}

// errBoom is a sentinel error tests use to force a submission failure.
var errBoom = errors.New("boom")

// fakeBlockchain is a no-op BlockchainClient - DeployPrimaryWallet's own
// tests care about what it's called with, but every other test in this
// package only needs Register/lookup behavior and never submits a
// transaction at all. Widened for wallet-recovery Branch B (PLAN.md §15):
// deployContractAddr/safeNonce/safeOwners/waitReceiptFail let a test
// steer the platform-deployment and self-management-transaction paths
// without needing a live or simulated chain, the same fake-first posture
// every other component's tests in this codebase already take.
type fakeBlockchain struct {
	submitted []fakeSubmission
	err       error

	deployContractAddr string
	deployContractErr  error

	safeNonce    *big.Int
	safeNonceErr error

	safeOwners    []common.Address
	safeOwnersErr error

	waitReceiptFail bool
	waitReceiptErr  error

	nativeTransferErr error
}

type fakeSubmission struct {
	to   common.Address
	data []byte
}

func (f *fakeBlockchain) SignAndSubmitTx(_ context.Context, _ *ecdsa.PrivateKey, to *common.Address, _ *big.Int, data []byte, _ *uint64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.submitted = append(f.submitted, fakeSubmission{to: *to, data: append([]byte{}, data...)})
	return "0xtxhash", nil
}

func (f *fakeBlockchain) DeployContract(_ context.Context, _ *ecdsa.PrivateKey, data []byte) (string, string, error) {
	if f.deployContractErr != nil {
		return "", "", f.deployContractErr
	}
	f.submitted = append(f.submitted, fakeSubmission{data: append([]byte{}, data...)})
	addr := f.deployContractAddr
	if addr == "" {
		addr = "0x9999999999999999999999999999999999999999"
	}
	return addr, "0xdeploytxhash", nil
}

func (f *fakeBlockchain) SafeNonce(_ context.Context, _ string) (*big.Int, error) {
	if f.safeNonceErr != nil {
		return nil, f.safeNonceErr
	}
	if f.safeNonce != nil {
		return f.safeNonce, nil
	}
	return big.NewInt(0), nil
}

func (f *fakeBlockchain) SafeOwners(_ context.Context, _ string) ([]common.Address, error) {
	if f.safeOwnersErr != nil {
		return nil, f.safeOwnersErr
	}
	return f.safeOwners, nil
}

func (f *fakeBlockchain) WaitForReceipt(_ context.Context, _ string) (bool, error) {
	if f.waitReceiptErr != nil {
		return false, f.waitReceiptErr
	}
	return !f.waitReceiptFail, nil
}

func (f *fakeBlockchain) BuildNativeTransferTx(_ context.Context, from, to string, amountWei *big.Int, _ *uint64) (*network.UnsignedTx, error) {
	if f.nativeTransferErr != nil {
		return nil, f.nativeTransferErr
	}
	return &network.UnsignedTx{To: to, Value: amountWei.String(), Type: "0x2"}, nil
}

// newTestService builds a Service backed by a fresh in-memory DB, a
// console mailer, and a no-op blockchain, for tests that don't care about
// the recovery salt/TTL or deployment specifically (those use their own
// setup in recovery_test.go and deploy_test.go).
func newTestService(t *testing.T) *Service {
	t.Helper()
	return New(newTestDB(t), notify.NewConsoleMailer(), "test-recovery-salt", 15*time.Minute, &fakeBlockchain{}, "test-deployer-salt")
}

// randomAddress generates a fresh, syntactically valid EVM address for tests
// that just need "some address," not a specific one.
func randomAddress(t *testing.T) string {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex()
}

func TestRegister_Success(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)

	user, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if user.Username != "alice" || user.SignerAddress != signerAddress {
		t.Fatalf("unexpected user: %+v", user)
	}
	wantAddress, _, err := computePrimaryWalletAddress(common.HexToAddress(signerAddress))
	if err != nil {
		t.Fatalf("computePrimaryWalletAddress: %v", err)
	}
	if user.Address != wantAddress.Hex() {
		t.Fatalf("user.Address = %s, want the computed Safe address %s", user.Address, wantAddress.Hex())
	}
	if user.Address == user.SignerAddress {
		t.Fatal("Address must be the computed Safe address, not the raw signer EOA")
	}
	if user.PrimaryWalletDeployed {
		t.Fatal("a freshly registered wallet must not be marked deployed")
	}

	var wallet models.UserWallet
	if err := svc.DB.Where("user_id = ?", user.ID).First(&wallet).Error; err != nil {
		t.Fatalf("expected a primary wallet to be created: %v", err)
	}
	if !wallet.IsPrimary || wallet.Address != wantAddress.Hex() {
		t.Fatalf("unexpected primary wallet: %+v", wallet)
	}
	if wallet.Tag != "primary" || wallet.Alias != "alice" {
		t.Fatalf("expected primary wallet Tag=primary Alias=alice, got %+v", wallet)
	}
}

func TestRegisterWalletForAddress_DerivesAliasFromOwnerAndTag(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)
	user, err := svc.Register(RegisterInput{Username: "bob", Email: "bob@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	subWalletAddress := randomAddress(t)

	wallet, err := svc.RegisterWalletForAddress(user.Address, subWalletAddress, "savings", "for a rainy day", models.WalletTypeNormal)
	if err != nil {
		t.Fatalf("RegisterWalletForAddress returned error: %v", err)
	}
	if wallet.Alias != "bob_savings" {
		t.Fatalf("expected alias bob_savings, got %q", wallet.Alias)
	}
	if wallet.WalletType != models.WalletTypeNormal {
		t.Fatalf("expected WalletTypeNormal, got %v", wallet.WalletType)
	}
	if wallet.Tag != "savings" || wallet.Description != "for a rainy day" || wallet.IsPrimary {
		t.Fatalf("unexpected wallet: %+v", wallet)
	}
}

func TestRegisterWalletForAddress_PersistsNonDefaultWalletType(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)
	user, err := svc.Register(RegisterInput{Username: "erin", Email: "erin@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	issuingWalletAddress := randomAddress(t)

	wallet, err := svc.RegisterWalletForAddress(user.Address, issuingWalletAddress, "issuer", "", models.WalletTypeAssetIssuing)
	if err != nil {
		t.Fatalf("RegisterWalletForAddress returned error: %v", err)
	}
	if wallet.WalletType != models.WalletTypeAssetIssuing {
		t.Fatalf("expected WalletTypeAssetIssuing, got %v", wallet.WalletType)
	}

	var reloaded models.UserWallet
	if err := svc.DB.Where("address = ?", issuingWalletAddress).First(&reloaded).Error; err != nil {
		t.Fatalf("failed to reload wallet: %v", err)
	}
	if reloaded.WalletType != models.WalletTypeAssetIssuing {
		t.Fatalf("expected persisted WalletTypeAssetIssuing, got %v", reloaded.WalletType)
	}
}

func TestUpdateWalletMetadata_OnlyOwnerMayEdit(t *testing.T) {
	svc := newTestService(t)
	ownerSigner := randomAddress(t)
	owner, err := svc.Register(RegisterInput{Username: "carol", Email: "carol@example.com", SignerAddress: ownerSigner})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	otherSigner := randomAddress(t)
	other, err := svc.Register(RegisterInput{Username: "dave", Email: "dave@example.com", SignerAddress: otherSigner})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	newTag, newAlias := "renamed", "carol_renamed"
	updated, err := svc.UpdateWalletMetadata(owner.Address, owner.Address, &newTag, nil, &newAlias)
	if err != nil {
		t.Fatalf("owner UpdateWalletMetadata returned error: %v", err)
	}
	if updated.Tag != "renamed" || updated.Alias != "carol_renamed" {
		t.Fatalf("unexpected wallet after update: %+v", updated)
	}

	if _, err := svc.UpdateWalletMetadata(other.Address, owner.Address, &newTag, nil, nil); err == nil {
		t.Fatal("expected a non-owner to be rejected")
	}
}

func TestResolveRecipient_AddressAliasUsernameEmail(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)
	user, err := svc.Register(RegisterInput{Username: "erin", Email: "erin@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	rawAddress := randomAddress(t)

	if got, err := svc.ResolveRecipient(rawAddress); err != nil || got != rawAddress {
		t.Fatalf("address passthrough: got (%q, %v)", got, err)
	}
	if got, err := svc.ResolveRecipient("erin"); err != nil || got != user.Address {
		t.Fatalf("alias resolution: got (%q, %v)", got, err)
	}
	if got, err := svc.ResolveRecipient("ERIN"); err != nil || got != user.Address {
		t.Fatalf("username resolution (case-insensitive): got (%q, %v)", got, err)
	}
	if got, err := svc.ResolveRecipient("erin@example.com"); err != nil || got != user.Address {
		t.Fatalf("email resolution: got (%q, %v)", got, err)
	}
	if _, err := svc.ResolveRecipient("nobody-by-this-name"); err == nil {
		t.Fatal("expected an unresolvable identifier to error")
	}
}

func TestLookupWalletDirectoryEntry_UnregisteredAddressStillReturnsEntry(t *testing.T) {
	svc := newTestService(t)
	rawAddress := randomAddress(t)

	entry, err := svc.LookupWalletDirectoryEntry(rawAddress)
	if err != nil {
		t.Fatalf("LookupWalletDirectoryEntry returned error: %v", err)
	}
	if entry.Address != rawAddress || entry.Alias != "" || entry.IsPrimary {
		t.Fatalf("expected a bare entry for an unregistered address, got %+v", entry)
	}
}

func TestRegister_InvalidAddress(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", SignerAddress: "not-an-address"})
	if err == nil {
		t.Fatal("expected an error for an invalid address")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", SignerAddress: randomAddress(t)}); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}

	_, err := svc.Register(RegisterInput{Username: "alice", Email: "someone-else@example.com", SignerAddress: randomAddress(t)})
	if err == nil {
		t.Fatal("expected a conflict error for a duplicate username")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 409 {
		t.Fatalf("expected a 409 AppError, got %#v", err)
	}
}

func TestGetByAddress(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)
	registered, err := svc.Register(RegisterInput{Username: "carol", Email: "carol@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	user, err := svc.GetByAddress(registered.Address)
	if err != nil {
		t.Fatalf("GetByAddress returned error: %v", err)
	}
	if user.Username != "carol" {
		t.Fatalf("unexpected user: %+v", user)
	}

	if _, err := svc.GetByAddress(randomAddress(t)); err == nil {
		t.Fatal("expected a not-found error for an unregistered address")
	}
}

func TestResolveUsernameToPrimaryWalletAddress(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)
	registered, err := svc.Register(RegisterInput{Username: "frank", Email: "frank@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	address, err := svc.ResolveUsernameToPrimaryWalletAddress("frank")
	if err != nil {
		t.Fatalf("ResolveUsernameToPrimaryWalletAddress returned error: %v", err)
	}
	if address != registered.Address {
		t.Fatalf("expected %s, got %s", registered.Address, address)
	}

	if _, err := svc.ResolveUsernameToPrimaryWalletAddress("nobody"); err == nil {
		t.Fatal("expected a not-found error for an unregistered username")
	}
}

// Regression test for a real bug: GetByAddress(CtxSubject) looked correct
// for GET /v1/users/me but silently 404'd on its own primary use case - a
// self-signed SignatureAuth request (the only way to call it before the
// wallet's Safe address is known at all) resolves CtxSubject to the
// signer's own address, never to User.Address (a distinct Safe address
// once a profile exists). GetBySignerAddress is what getMe actually calls
// now (keyed on CtxSigner instead).
func TestGetBySignerAddress(t *testing.T) {
	svc := newTestService(t)
	signerAddress := randomAddress(t)
	registered, err := svc.Register(RegisterInput{Username: "dave", Email: "dave@example.com", SignerAddress: signerAddress})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if registered.Address == signerAddress {
		t.Fatal("test setup invalid: the Safe address must differ from the signer address")
	}

	user, err := svc.GetBySignerAddress(signerAddress)
	if err != nil {
		t.Fatalf("GetBySignerAddress returned error: %v", err)
	}
	if user.Username != "dave" {
		t.Fatalf("unexpected user: %+v", user)
	}

	if _, err := svc.GetBySignerAddress(randomAddress(t)); err == nil {
		t.Fatal("expected a not-found error for an unregistered signer address")
	}
}

func TestSecurityAnswer_SetAndVerify(t *testing.T) {
	svc := newTestService(t)
	user, err := svc.Register(RegisterInput{Username: "bob", Email: "bob@example.com", SignerAddress: randomAddress(t)})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	question := models.SecurityQuestion{Question: "What is your favorite testnet faucet?"}
	if err := svc.DB.Create(&question).Error; err != nil {
		t.Fatalf("create question: %v", err)
	}

	if err := svc.SetSecurityAnswer(user.ID, question.ID, "Base Sepolia"); err != nil {
		t.Fatalf("SetSecurityAnswer returned error: %v", err)
	}

	match, err := svc.VerifySecurityAnswer(user.ID, question.ID, "Base Sepolia")
	if err != nil {
		t.Fatalf("VerifySecurityAnswer returned error: %v", err)
	}
	if !match {
		t.Fatal("expected the correct answer to match")
	}

	noMatch, err := svc.VerifySecurityAnswer(user.ID, question.ID, "Ethereum Mainnet")
	if err != nil {
		t.Fatalf("VerifySecurityAnswer returned error: %v", err)
	}
	if noMatch {
		t.Fatal("expected an incorrect answer not to match")
	}
}
