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
