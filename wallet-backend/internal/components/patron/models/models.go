// Package models defines the persisted shapes for patron/membership
// subscriptions - fully chain-agnostic pricing/membership logic ported
// directly from upstream. See PLAN.md §4.10 for what changed (the
// payment leg only) and the two activation-worker bugs found and fixed
// rather than reproduced.
package models

import "time"

// PatronPackage is a subscription package tier (e.g. GOLD, PLATINUM,
// DIAMOND), ordered by PriorityOrder (lower = more exclusive/valuable,
// matching upstream's convention).
type PatronPackage struct {
	ID            string `gorm:"primaryKey;size:32" json:"id"`
	Description   string `json:"description"`
	Inactive      bool   `gorm:"default:false" json:"inactive"`
	PriorityOrder int    `gorm:"default:1" json:"-"`
}

// PatronTier is a billing cadence (MONTHLY, ANNUAL, LIFETIME).
type PatronTier struct {
	ID            string `gorm:"primaryKey;size:32" json:"id"`
	CanExpire     bool   `gorm:"default:true" json:"canExpire"`
	Inactive      bool   `gorm:"default:false" json:"inactive"`
	PriorityOrder int    `gorm:"default:1" json:"-"`
}

// PatronMembershipGrade is one (package, tier) combination's USD price.
type PatronMembershipGrade struct {
	ID              uint    `gorm:"primaryKey" json:"id"`
	PatronPackageID string  `gorm:"size:32;uniqueIndex:idx_grade_unique" json:"patronPackageId"`
	PatronTierID    string  `gorm:"size:32;uniqueIndex:idx_grade_unique" json:"patronTierId"`
	PriceUSD        float64 `json:"priceUsd"`
}

// UserPatronMembership is a user's current active membership - "lifetime"
// is represented by ValidTill in the far future (year 9999), matching
// upstream's own convention rather than a separate boolean.
type UserPatronMembership struct {
	UserID          uint      `gorm:"primaryKey" json:"userId"`
	PatronPackageID string    `gorm:"size:32" json:"patronPackageId"`
	PatronTierID    string    `gorm:"size:32" json:"patronTierId"`
	ValidTill       time.Time `gorm:"not null" json:"validTill"`
}

// UserPatronSubscriptionLog is the append-only history of subscription
// changes, including future-dated (not-yet-effective) upgrades/renewals -
// the recurring activation worker (see services) promotes these to
// UserPatronMembership once EffectiveDate arrives.
type UserPatronSubscriptionLog struct {
	ID              string    `gorm:"primaryKey;size:64" json:"id"`
	CreatedAt       time.Time `json:"createdAt"`
	UserID          uint      `gorm:"index" json:"userId"`
	PatronPackageID string    `gorm:"size:32" json:"patronPackageId"`
	PatronTierID    string    `gorm:"size:32" json:"patronTierId"`
	EffectiveDate   time.Time `gorm:"not null" json:"effectiveDate"`
	ValidTill       time.Time `gorm:"not null" json:"validTill"`
	VATPaid         string    `gorm:"default:0" json:"vatPaid"`
	TxHash          string    `gorm:"size:66" json:"txHash"`
}

// PatronSubscriptionPaymentAsset is the allow-list of currencies a
// subscription can be paid in - a thin wrapper around a CuratedToken
// symbol, matching tokenization's TokenizationCurrency pattern (§4.9).
type PatronSubscriptionPaymentAsset struct {
	Symbol   string `gorm:"primaryKey;size:12" json:"symbol"`
	Inactive bool   `gorm:"default:false" json:"-"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&PatronPackage{},
	&PatronTier{},
	&PatronMembershipGrade{},
	&UserPatronMembership{},
	&UserPatronSubscriptionLog{},
	&PatronSubscriptionPaymentAsset{},
}
