package services

import (
	"testing"
	"time"

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

// newTestService builds a Service backed by a fresh in-memory DB and a
// console mailer, for tests that don't care about the recovery salt/TTL
// specifically (those use their own setup in recovery_test.go).
func newTestService(t *testing.T) *Service {
	t.Helper()
	return New(newTestDB(t), notify.NewConsoleMailer(), "test-recovery-salt", 15*time.Minute)
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
	address := randomAddress(t)

	user, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", Address: address})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if user.Username != "alice" || user.Address != address {
		t.Fatalf("unexpected user: %+v", user)
	}

	var wallet models.UserWallet
	if err := svc.DB.Where("user_id = ?", user.ID).First(&wallet).Error; err != nil {
		t.Fatalf("expected a primary wallet to be created: %v", err)
	}
	if !wallet.IsPrimary || wallet.Address != address {
		t.Fatalf("unexpected primary wallet: %+v", wallet)
	}
}

func TestRegister_InvalidAddress(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", Address: "not-an-address"})
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
	if _, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", Address: randomAddress(t)}); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}

	_, err := svc.Register(RegisterInput{Username: "alice", Email: "someone-else@example.com", Address: randomAddress(t)})
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
	address := randomAddress(t)
	if _, err := svc.Register(RegisterInput{Username: "carol", Email: "carol@example.com", Address: address}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	user, err := svc.GetByAddress(address)
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
	user, err := svc.Register(RegisterInput{Username: "bob", Email: "bob@example.com", Address: randomAddress(t)})
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
