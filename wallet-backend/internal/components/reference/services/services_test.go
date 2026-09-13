package services

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"wallet-backend/internal/components/reference/models"
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
	return New(db)
}

func TestUpsertCountry_CreateThenUpdate(t *testing.T) {
	svc := newTestService(t)
	country, err := svc.UpsertCountry("ng", "Nigeria", true)
	if err != nil {
		t.Fatalf("UpsertCountry (create): %v", err)
	}
	if country.CountryCode != "NG" {
		t.Fatalf("expected the country code to be upper-cased, got %q", country.CountryCode)
	}

	updated, err := svc.UpsertCountry("NG", "Nigeria (Federal Republic)", false)
	if err != nil {
		t.Fatalf("UpsertCountry (update): %v", err)
	}
	if updated.Enabled {
		t.Fatalf("expected Enabled to be updated to false")
	}

	countries, err := svc.ListCountries()
	if err != nil {
		t.Fatalf("ListCountries: %v", err)
	}
	if len(countries) != 1 {
		t.Fatalf("expected exactly one country row after an update, got %d", len(countries))
	}
}

func TestUpsertCountry_RejectsInvalidCode(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.UpsertCountry("NGR", "Nigeria", true); err == nil {
		t.Fatalf("expected a 3-letter code to be rejected")
	}
}

func TestGetCountryConfig_NilWhenNoRow(t *testing.T) {
	svc := newTestService(t)
	config, err := svc.GetCountryConfig("US")
	if err != nil {
		t.Fatalf("GetCountryConfig: %v", err)
	}
	if config != nil {
		t.Fatalf("expected a nil config for a country with no row, got %+v", config)
	}
}

func TestIsHighRiskCountry(t *testing.T) {
	svc := newTestService(t)
	if svc.IsHighRiskCountry("US") {
		t.Fatalf("expected a country with no config row to not be high-risk")
	}

	if _, err := svc.UpsertCountryConfig(models.CountryConfig{CountryCode: "us", HighRisk: true}); err != nil {
		t.Fatalf("UpsertCountryConfig: %v", err)
	}
	if !svc.IsHighRiskCountry("US") {
		t.Fatalf("expected the country to be reported high-risk after the config is set")
	}
}

func TestUpsertForm_BumpsVersionOnUpdate(t *testing.T) {
	svc := newTestService(t)
	form, err := svc.UpsertForm("kyc-intake", "KYC Intake", `{"type":"object"}`)
	if err != nil {
		t.Fatalf("UpsertForm (create): %v", err)
	}
	if form.Version != 1 {
		t.Fatalf("expected a new form to start at version 1, got %d", form.Version)
	}

	updated, err := svc.UpsertForm("kyc-intake", "KYC Intake v2", `{"type":"object","required":["name"]}`)
	if err != nil {
		t.Fatalf("UpsertForm (update): %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected the version to bump to 2, got %d", updated.Version)
	}
}

func TestDeactivateForm_HidesFromListAndGet(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.UpsertForm("kyc-intake", "KYC Intake", `{}`); err != nil {
		t.Fatalf("UpsertForm: %v", err)
	}

	if err := svc.DeactivateForm("kyc-intake"); err != nil {
		t.Fatalf("DeactivateForm: %v", err)
	}

	if _, err := svc.GetForm("kyc-intake"); err == nil {
		t.Fatalf("expected GetForm to fail for a deactivated form")
	}
	forms, err := svc.ListActiveForms()
	if err != nil {
		t.Fatalf("ListActiveForms: %v", err)
	}
	if len(forms) != 0 {
		t.Fatalf("expected no active forms after deactivation, got %d", len(forms))
	}
}

func TestDeactivateForm_NotFound(t *testing.T) {
	svc := newTestService(t)
	if err := svc.DeactivateForm("does-not-exist"); err == nil {
		t.Fatalf("expected an error for an unknown form id")
	}
}
