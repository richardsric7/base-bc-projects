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
// transaction at all.
type fakeBlockchain struct {
	submitted []fakeSubmission
	err       error
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
