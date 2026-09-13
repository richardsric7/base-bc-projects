package services

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/kyc/models"
	usersModels "wallet-backend/internal/components/users/models"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate kyc models: %v", err)
	}
	if err := db.AutoMigrate(usersModels.Models...); err != nil {
		t.Fatalf("migrate users models: %v", err)
	}
	return db
}

func createTestUser(t *testing.T, db *gorm.DB, username string) usersModels.User {
	t.Helper()
	user := usersModels.User{
		Username: username,
		Email:    username + "@example.com",
		Address:  "0x" + username + "0000000000000000000000000000000000",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

func newTestService(t *testing.T, sumsubBaseURL string) (*Service, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	svc := New(db, sumsubBaseURL, "test-token", "test-sumsub-secret", "test-doja-secret")
	return svc, db
}

func appErrStatus(t *testing.T, err error) int {
	t.Helper()
	appErr, ok := err.(*apperrors.AppError)
	if !ok {
		t.Fatalf("expected an *apperrors.AppError, got %#v", err)
	}
	return appErr.StatusCode()
}
