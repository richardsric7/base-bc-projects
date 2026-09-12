package services

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stellar/go-stellar-sdk/keypair"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/users/models"
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

const testPublicKey = "GBTNUZDIUMWZEGTNQCL5F73PIABCBJ4YQA2VJS7HXTBRDDSTWCE6UNXE"

func TestRegister_Success(t *testing.T) {
	svc := New(newTestDB(t))

	user, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", PublicKey: testPublicKey})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if user.Username != "alice" || user.PublicKey != testPublicKey {
		t.Fatalf("unexpected user: %+v", user)
	}

	var wallet models.UserWallet
	if err := svc.DB.Where("user_id = ?", user.ID).First(&wallet).Error; err != nil {
		t.Fatalf("expected a primary wallet to be created: %v", err)
	}
	if !wallet.IsPrimary || wallet.PublicKey != testPublicKey {
		t.Fatalf("unexpected primary wallet: %+v", wallet)
	}
}

func TestRegister_InvalidPublicKey(t *testing.T) {
	svc := New(newTestDB(t))
	_, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", PublicKey: "not-a-key"})
	if err == nil {
		t.Fatal("expected an error for an invalid public key")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 400 {
		t.Fatalf("expected a 400 AppError, got %#v", err)
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc := New(newTestDB(t))
	if _, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", PublicKey: testPublicKey}); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}

	other, err := keypair.Random()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	_, err = svc.Register(RegisterInput{Username: "alice", Email: "someone-else@example.com", PublicKey: other.Address()})
	if err == nil {
		t.Fatal("expected a conflict error for a duplicate username")
	}
	appErr, ok := err.(*apperrors.AppError)
	if !ok || appErr.StatusCode() != 409 {
		t.Fatalf("expected a 409 AppError, got %#v", err)
	}
}

func TestSecurityAnswer_SetAndVerify(t *testing.T) {
	svc := New(newTestDB(t))
	user, err := svc.Register(RegisterInput{Username: "bob", Email: "bob@example.com", PublicKey: testPublicKey})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	question := models.SecurityQuestion{Question: "What is your favorite testnet asset?"}
	if err := svc.DB.Create(&question).Error; err != nil {
		t.Fatalf("create question: %v", err)
	}

	if err := svc.SetSecurityAnswer(user.ID, question.ID, "XLM"); err != nil {
		t.Fatalf("SetSecurityAnswer returned error: %v", err)
	}

	match, err := svc.VerifySecurityAnswer(user.ID, question.ID, "XLM")
	if err != nil {
		t.Fatalf("VerifySecurityAnswer returned error: %v", err)
	}
	if !match {
		t.Fatal("expected the correct answer to match")
	}

	noMatch, err := svc.VerifySecurityAnswer(user.ID, question.ID, "BTC")
	if err != nil {
		t.Fatalf("VerifySecurityAnswer returned error: %v", err)
	}
	if noMatch {
		t.Fatal("expected an incorrect answer not to match")
	}
}
