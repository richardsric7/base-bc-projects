// Package services implements the reference-data component: the country
// catalog/config and dynamic client-form definitions. Entirely
// chain-agnostic - see PLAN.md §4.13.
package services

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/reference/models"
)

type Service struct {
	DB *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{DB: db}
}

// ListCountries returns the full country catalog, alphabetically.
func (s *Service) ListCountries() ([]models.Country, error) {
	var countries []models.Country
	if err := s.DB.Order("country_code ASC").Find(&countries).Error; err != nil {
		return nil, apperrors.Internal("failed to load countries")
	}
	return countries, nil
}

// UpsertCountry creates or updates a country catalog entry.
func (s *Service) UpsertCountry(code, name string, enabled bool) (*models.Country, error) {
	code = strings.ToUpper(code)
	if len(code) != 2 {
		return nil, apperrors.BadRequest("countryCode must be a 2-letter ISO 3166-1 alpha-2 code")
	}
	if name == "" {
		return nil, apperrors.BadRequest("name is required")
	}
	country := models.Country{CountryCode: code, Name: name, Enabled: enabled}
	if err := s.DB.Save(&country).Error; err != nil {
		return nil, apperrors.Internal("failed to save country")
	}
	return &country, nil
}

// GetCountryConfig returns a country's config, or a nil pointer (not an
// error) when the country has no config row - callers fall back to a
// global default rather than treating "no per-country override yet" as a
// failure (see fiat.ActivationConfig's doc comment for the global-default
// this is layered on top of).
func (s *Service) GetCountryConfig(code string) (*models.CountryConfig, error) {
	var config models.CountryConfig
	err := s.DB.Where("country_code = ?", strings.ToUpper(code)).First(&config).Error
	if err == nil {
		return &config, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, apperrors.Internal("failed to load country config")
}

// UpsertCountryConfig creates or updates a country's config, keyed by
// country code.
func (s *Service) UpsertCountryConfig(config models.CountryConfig) (*models.CountryConfig, error) {
	config.CountryCode = strings.ToUpper(config.CountryCode)
	if len(config.CountryCode) != 2 {
		return nil, apperrors.BadRequest("countryCode must be a 2-letter ISO 3166-1 alpha-2 code")
	}
	if err := s.DB.Save(&config).Error; err != nil {
		return nil, apperrors.Internal("failed to save country config")
	}
	return &config, nil
}

// IsHighRiskCountry reports whether code has an explicit HighRisk config
// row - false (not high-risk) for any country with no config at all,
// deny-by-exception rather than deny-by-default, since most countries
// will never get an explicit CountryConfig row and that must not itself
// read as a risk signal.
func (s *Service) IsHighRiskCountry(code string) bool {
	config, err := s.GetCountryConfig(code)
	if err != nil || config == nil {
		return false
	}
	return config.HighRisk
}

// ListActiveForms returns every active form definition.
func (s *Service) ListActiveForms() ([]models.JsonForm, error) {
	var forms []models.JsonForm
	if err := s.DB.Where("is_active = ?", true).Order("name ASC").Find(&forms).Error; err != nil {
		return nil, apperrors.Internal("failed to load forms")
	}
	return forms, nil
}

// GetForm returns one active form by its stable ID.
func (s *Service) GetForm(id string) (*models.JsonForm, error) {
	var form models.JsonForm
	err := s.DB.Where("id = ? AND is_active = ?", id, true).First(&form).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("form not found")
		}
		return nil, apperrors.Internal("failed to load form")
	}
	return &form, nil
}

// UpsertForm creates or updates a form definition (matched by ID),
// bumping Version on every update so a client can detect it has a stale
// cached copy.
func (s *Service) UpsertForm(id, name, schema string) (*models.JsonForm, error) {
	if id == "" || name == "" || schema == "" {
		return nil, apperrors.BadRequest("id, name and schema are required")
	}
	var existing models.JsonForm
	err := s.DB.Where("id = ?", id).First(&existing).Error
	if err == nil {
		existing.Name = name
		existing.Schema = schema
		existing.Version++
		existing.IsActive = true
		if err := s.DB.Save(&existing).Error; err != nil {
			return nil, apperrors.Internal("failed to update form")
		}
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.Internal("failed to check for an existing form")
	}
	form := models.JsonForm{ID: id, Name: name, Schema: schema, Version: 1, IsActive: true}
	if err := s.DB.Create(&form).Error; err != nil {
		return nil, apperrors.Internal("failed to create form")
	}
	return &form, nil
}

// DeactivateForm hides a form from ListActiveForms/GetForm without
// deleting its history.
func (s *Service) DeactivateForm(id string) error {
	res := s.DB.Model(&models.JsonForm{}).Where("id = ?", id).Update("is_active", false)
	if res.Error != nil {
		return apperrors.Internal("failed to deactivate form")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("form not found")
	}
	return nil
}
