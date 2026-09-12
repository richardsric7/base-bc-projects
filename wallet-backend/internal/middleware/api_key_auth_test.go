package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	servicelinksModels "wallet-backend/internal/components/servicelinks/models"
)

func newAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(servicelinksModels.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func runAPIKeyAuth(db *gorm.DB, rawKey string) (*httptest.ResponseRecorder, *servicelinksModels.ServiceLink) {
	router := gin.New()
	var captured *servicelinksModels.ServiceLink
	router.GET("/protected", APIKeyAuth(db), func(c *gin.Context) {
		captured = c.MustGet(CtxServiceLink).(*servicelinksModels.ServiceLink)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if rawKey != "" {
		req.Header.Set(APIKeyHeader, rawKey)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec, captured
}

func TestAPIKeyAuth_MissingHeader(t *testing.T) {
	db := newAuthTestDB(t)
	rec, _ := runAPIKeyAuth(db, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyAuth_UnknownKey(t *testing.T) {
	db := newAuthTestDB(t)
	rec, _ := runAPIKeyAuth(db, "sl_does-not-exist")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAPIKeyAuth_AcceptsVerifiedActiveLink(t *testing.T) {
	db := newAuthTestDB(t)
	raw := "sl_testkey"
	link := servicelinksModels.ServiceLink{
		OwnerUserID: 1,
		APIKeyHash:  HashAPIKey(raw),
		ShortName:   "partner-a",
		Verified:    true,
	}
	if err := db.Create(&link).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}

	rec, captured := runAPIKeyAuth(db, raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if captured == nil || captured.ID != link.ID {
		t.Fatalf("expected the matched service link to be attached to the context")
	}
}

// The plaintext key is never stored - only its hash is queryable, closing
// the original's plaintext-API-key-at-rest issue (PLAN.md §4.11 finding 1).
func TestAPIKeyAuth_RawKeyNeverMatchesStoredHash(t *testing.T) {
	db := newAuthTestDB(t)
	raw := "sl_testkey"
	link := servicelinksModels.ServiceLink{OwnerUserID: 1, APIKeyHash: HashAPIKey(raw), ShortName: "partner-a", Verified: true}
	db.Create(&link)

	var count int64
	db.Model(&servicelinksModels.ServiceLink{}).Where("api_key_hash = ?", raw).Count(&count)
	if count != 0 {
		t.Fatalf("the raw key must never equal the stored hash")
	}
}

func TestAPIKeyAuth_RejectsUnverifiedLink(t *testing.T) {
	db := newAuthTestDB(t)
	raw := "sl_unverified"
	link := servicelinksModels.ServiceLink{OwnerUserID: 1, APIKeyHash: HashAPIKey(raw), ShortName: "partner-b", Verified: false}
	db.Create(&link)

	rec, _ := runAPIKeyAuth(db, raw)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an unverified service link, got %d", rec.Code)
	}
}

func TestAPIKeyAuth_RejectsInactiveLink(t *testing.T) {
	db := newAuthTestDB(t)
	raw := "sl_inactive"
	link := servicelinksModels.ServiceLink{OwnerUserID: 1, APIKeyHash: HashAPIKey(raw), ShortName: "partner-c", Verified: true, Inactive: true}
	db.Create(&link)

	rec, _ := runAPIKeyAuth(db, raw)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a deactivated service link, got %d", rec.Code)
	}
}

// This is the fix for the original's status checks, which were
// reimplemented per-handler and not consistently applied everywhere
// (PLAN.md §4.11 finding 3) - here every route behind APIKeyAuth gets the
// same centralized enforcement, including suspension.
func TestAPIKeyAuth_RejectsSuspendedLink(t *testing.T) {
	db := newAuthTestDB(t)
	raw := "sl_suspended"
	link := servicelinksModels.ServiceLink{OwnerUserID: 1, APIKeyHash: HashAPIKey(raw), ShortName: "partner-d", Verified: true, Suspended: true, SuspensionReason: "fraud review"}
	db.Create(&link)

	rec, _ := runAPIKeyAuth(db, raw)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a suspended service link, got %d", rec.Code)
	}
}
