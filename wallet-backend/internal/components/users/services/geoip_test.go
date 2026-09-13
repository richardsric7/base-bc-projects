package services

import (
	"context"
	"testing"

	referenceModels "wallet-backend/internal/components/reference/models"
)

// fakeGeoIPProvider returns a fixed country code (or an error) without
// touching the network, so Register's risk-field wiring is testable
// without a real geo-IP vendor.
type fakeGeoIPProvider struct {
	countryCode string
	err         error
}

func (f fakeGeoIPProvider) Lookup(context.Context, string) (string, error) {
	return f.countryCode, f.err
}

func newTestServiceWithReferenceModels(t *testing.T) *Service {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(referenceModels.Models...); err != nil {
		t.Fatalf("migrate reference models: %v", err)
	}
	svc := New(db, nil, "test-recovery-salt", 0)
	return svc
}

func TestRegister_SetsRegistrationCountryCodeFromGeoIP(t *testing.T) {
	svc := newTestServiceWithReferenceModels(t)
	svc.GeoIP = fakeGeoIPProvider{countryCode: "NG"}

	user, err := svc.Register(RegisterInput{Username: "alice", Email: "alice@example.com", Address: randomAddress(t), RegistrationIP: "1.2.3.4"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.RegistrationCountryCode != "NG" {
		t.Fatalf("expected RegistrationCountryCode to be NG, got %q", user.RegistrationCountryCode)
	}
	if user.RegistrationHighRisk {
		t.Fatalf("expected RegistrationHighRisk to be false with no CountryConfig row for NG")
	}
}

func TestRegister_FlagsHighRiskCountry(t *testing.T) {
	svc := newTestServiceWithReferenceModels(t)
	svc.GeoIP = fakeGeoIPProvider{countryCode: "XX"}
	if err := svc.DB.Create(&referenceModels.CountryConfig{CountryCode: "XX", HighRisk: true}).Error; err != nil {
		t.Fatalf("seed country config: %v", err)
	}

	user, err := svc.Register(RegisterInput{Username: "bob", Email: "bob@example.com", Address: randomAddress(t), RegistrationIP: "5.6.7.8"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !user.RegistrationHighRisk {
		t.Fatalf("expected RegistrationHighRisk to be true for a country flagged HighRisk")
	}
}

// A geo-IP failure must never block registration - it's an advisory
// signal, not a gate (PLAN.md §4.13).
func TestRegister_SucceedsWhenGeoIPLookupFails(t *testing.T) {
	svc := newTestServiceWithReferenceModels(t)
	svc.GeoIP = fakeGeoIPProvider{err: context.DeadlineExceeded}

	user, err := svc.Register(RegisterInput{Username: "carol", Email: "carol@example.com", Address: randomAddress(t), RegistrationIP: "9.9.9.9"})
	if err != nil {
		t.Fatalf("expected Register to succeed despite a geo-IP failure, got: %v", err)
	}
	if user.RegistrationCountryCode != "" || user.RegistrationHighRisk {
		t.Fatalf("expected empty risk fields when geo-IP lookup fails")
	}
}
