// Package models defines the persisted shape for the shortlink/deep-link
// component: a self-hosted replacement for the original's Firebase
// Dynamic Links dependency (referrals, login/authorize/event deep links,
// payment requests, QR codes). See PLAN.md §4.13.
package models

import "time"

// DynamicLink is one generated short link: ShortCode resolves to
// TargetURL (typically the wallet app's own deep-link scheme or a web
// fallback), with Metadata as an opaque JSON blob a caller can stash
// alongside it (e.g. which referral code or servicelinks approval this
// link is for) and read back later without a second table per use case.
type DynamicLink struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ShortCode  string    `gorm:"uniqueIndex;size:16;not null" json:"shortCode"`
	TargetURL  string    `gorm:"size:2048;not null" json:"targetUrl"`
	Metadata   string    `gorm:"type:text" json:"metadata,omitempty"`
	ClickCount uint64    `gorm:"default:0" json:"clickCount"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&DynamicLink{},
}
