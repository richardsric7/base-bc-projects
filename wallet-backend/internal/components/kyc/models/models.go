// Package models defines the persisted shapes owned by the kyc component -
// identity-verification progress against the two vendors the original
// project integrated (Sumsub and Dojah/Doja). Unlike most of this port,
// this subsystem is entirely chain-agnostic: neither vendor's API touches
// Base at all, so these models and the services built on them are close
// to a line-for-line port rather than an EVM redesign - see PLAN.md §4.4.
package models

import "time"

// SumsubLevel is one entry in the catalog of Sumsub verification levels a
// client may request (configured to match whatever levels exist in the
// project's Sumsub dashboard - Sumsub has no API to discover them). Ported
// from the original's KYCLevel; renamed and given a proper primary key
// since the original's "ID" was actually the level's name string.
type SumsubLevel struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Description string `gorm:"size:255" json:"description"`
}

// DojaWidget maps a Dojah-dashboard widget ID to the verification level and
// applicant type (individual/corporate) it represents - Dojah's webhook
// payload identifies a submission by widget ID only, so this is how a
// webhook is resolved back to "which level, for whom." Ported directly
// from the original; unlike SumsubLevel this has no seeded defaults; since
// widget IDs are vendor-dashboard-specific to whoever owns the Dojah
// account, shipping the original's actual widget IDs in this base template
// would be both meaningless to a different deployer and a minor
// information leak of the upstream account's configuration.
type DojaWidget struct {
	ID        string `gorm:"primaryKey;size:64" json:"id"`
	Level     int    `gorm:"not null" json:"level"`
	Corporate bool   `gorm:"default:false" json:"corporate"`
}

// UserSumsubProgress tracks one user's progress through Sumsub's 3 levels.
// Ported from the original's UserKYCProgress, keyed by UserID (this port's
// established convention - see UserSecurityAnswer) rather than the
// original's Username string.
type UserSumsubProgress struct {
	ID              uint `gorm:"primaryKey" json:"id"`
	UserID          uint `gorm:"uniqueIndex;not null" json:"userId"`
	Level1Initiated bool `gorm:"default:false" json:"level1Initiated"`
	Level1Done      bool `gorm:"default:false" json:"level1Done"`
	Level2Initiated bool `gorm:"default:false" json:"level2Initiated"`
	Level2Done      bool `gorm:"default:false" json:"level2Done"`
	Level3Initiated bool `gorm:"default:false" json:"level3Initiated"`
	Level3Done      bool `gorm:"default:false" json:"level3Done"`
}

// UserDojaProgress tracks one user's progress through Dojah's 4 levels.
// Ported from the original's UserDojaKYCProgress, same UserID-keying change
// as UserSumsubProgress.
type UserDojaProgress struct {
	ID              uint `gorm:"primaryKey" json:"id"`
	UserID          uint `gorm:"uniqueIndex;not null" json:"userId"`
	Level1Submitted bool `gorm:"default:false" json:"level1Submitted"`
	Level1Completed bool `gorm:"default:false" json:"level1Completed"`
	Level2Submitted bool `gorm:"default:false" json:"level2Submitted"`
	Level2Completed bool `gorm:"default:false" json:"level2Completed"`
	Level3Submitted bool `gorm:"default:false" json:"level3Submitted"`
	Level3Completed bool `gorm:"default:false" json:"level3Completed"`
	Level4Submitted bool `gorm:"default:false" json:"level4Submitted"`
	Level4Completed bool `gorm:"default:false" json:"level4Completed"`
}

// SumsubWebhookLog is a permanent audit record of every Sumsub review-result
// webhook received, ported from the original's SumSubReviewResult.
type SumsubWebhookLog struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ApplicantID    string    `gorm:"size:64" json:"applicantId"`
	InspectionID   string    `gorm:"size:64" json:"inspectionId"`
	CorrelationID  string    `gorm:"size:64" json:"correlationId"`
	ExternalUserID string    `gorm:"index;size:64" json:"externalUserId"`
	LevelName      string    `gorm:"size:64" json:"levelName"`
	Type           string    `gorm:"size:32" json:"type"`
	ReviewAnswer   string    `gorm:"size:16" json:"reviewAnswer"`
	ReviewStatus   string    `gorm:"size:32" json:"reviewStatus"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&SumsubLevel{},
	&DojaWidget{},
	&UserSumsubProgress{},
	&UserDojaProgress{},
	&SumsubWebhookLog{},
}
