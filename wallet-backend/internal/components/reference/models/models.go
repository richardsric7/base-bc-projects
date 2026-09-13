// Package models defines the persisted shapes for cross-cutting reference
// data that isn't specific to any one subsystem: the country catalog and
// per-country config, and dynamic client-facing form definitions. See
// PLAN.md §4.13 - all chain-agnostic, direct schema/logic translation.
package models

import "time"

// Country is one entry in the supported-country catalog (ISO 3166-1
// alpha-2 code as the primary key). Enabled controls whether a country
// currently accepts new registrations at all - a harder gate than
// CountryConfig.HighRisk, which allows registration but flags it for
// review.
type Country struct {
	CountryCode string `gorm:"primaryKey;size:2" json:"countryCode"`
	Name        string `gorm:"size:128;not null" json:"name"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
}

// CountryConfig holds the per-country settings the original kept keyed by
// country code: fiat activation pricing (see fiat.ActivationConfig's doc
// comment - this is the per-country override that config was deferred
// pending this component), a regulator name for compliance-facing display,
// and HighRisk, which internal/geoip's registration hook reads to flag a
// new signup for manual review without blocking it outright.
type CountryConfig struct {
	CountryCode          string  `gorm:"primaryKey;size:2" json:"countryCode"`
	FiatCurrency         string  `gorm:"size:8;not null;default:USD" json:"fiatCurrency"`
	FiatActivationAmount float64 `gorm:"not null;default:10" json:"fiatActivationAmount"`
	RewardTokenPercent   float64 `gorm:"not null;default:50" json:"rewardTokenPercent"`
	RegulatorName        string  `gorm:"size:128" json:"regulatorName,omitempty"`
	HighRisk             bool    `gorm:"default:false" json:"highRisk"`
}

// JsonForm is a versioned, client-rendered form definition (a JSON Schema
// or an app-specific equivalent, stored as opaque text) - lets a client
// render a new onboarding/KYC/support form without an app-store release.
// Ported directly from the original's JsonForm; ID is the form's stable
// lookup key (e.g. "kyc-corporate-intake"), not an auto-increment surrogate,
// so a client can hardcode which form it wants without a discovery call.
type JsonForm struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Name      string    `gorm:"size:128;not null" json:"name"`
	Schema    string    `gorm:"type:text;not null" json:"schema"`
	Version   int       `gorm:"default:1" json:"version"`
	IsActive  bool      `gorm:"default:true" json:"isActive"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&Country{},
	&CountryConfig{},
	&JsonForm{},
}
