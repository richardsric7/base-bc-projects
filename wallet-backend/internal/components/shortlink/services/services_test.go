package services

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/components/shortlink/models"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(models.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(db, "https://trov.to")
}

func TestCreateLink_GeneratesUniqueShortCode(t *testing.T) {
	svc := newTestService(t)
	linkA, err := svc.CreateLink("https://example.com/a", "")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	linkB, err := svc.CreateLink("https://example.com/b", "")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if linkA.ShortCode == linkB.ShortCode {
		t.Fatalf("expected distinct short codes, got %q for both", linkA.ShortCode)
	}
	if len(linkA.ShortCode) != 8 {
		t.Fatalf("expected an 8-character short code, got %q", linkA.ShortCode)
	}
}

func TestCreateLink_RejectsEmptyTargetURL(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateLink("", ""); err == nil {
		t.Fatalf("expected an empty targetUrl to be rejected")
	}
}

func TestResolve_IncrementsClickCount(t *testing.T) {
	svc := newTestService(t)
	link, err := svc.CreateLink("https://example.com/a", `{"kind":"referral"}`)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	resolved, err := svc.Resolve(link.ShortCode)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.TargetURL != "https://example.com/a" {
		t.Fatalf("unexpected target URL: %q", resolved.TargetURL)
	}

	again, err := svc.Resolve(link.ShortCode)
	if err != nil {
		t.Fatalf("Resolve (second): %v", err)
	}
	if again.ClickCount != 2 {
		t.Fatalf("expected click count 2 after two resolves, got %d", again.ClickCount)
	}
}

func TestGetLink_DoesNotIncrementClickCount(t *testing.T) {
	svc := newTestService(t)
	link, err := svc.CreateLink("https://example.com/a", "")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	if _, err := svc.GetLink(link.ShortCode); err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	reloaded, err := svc.GetLink(link.ShortCode)
	if err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	if reloaded.ClickCount != 0 {
		t.Fatalf("expected GetLink to never increment click count, got %d", reloaded.ClickCount)
	}
}

func TestResolve_UnknownCode(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Resolve("NOTREAL1"); err == nil {
		t.Fatalf("expected an unknown short code to be rejected")
	}
}

func TestShortURL_PrefixesBaseURL(t *testing.T) {
	svc := newTestService(t)
	if got := svc.ShortURL("AB12CD34"); got != "https://trov.to/s/AB12CD34" {
		t.Fatalf("unexpected short URL: %q", got)
	}
}

func TestQRCodePNG_ReturnsValidPNG(t *testing.T) {
	svc := newTestService(t)
	png, err := svc.QRCodePNG("AB12CD34", 128)
	if err != nil {
		t.Fatalf("QRCodePNG: %v", err)
	}
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		t.Fatalf("expected a PNG file signature, got %d bytes", len(png))
	}
}
