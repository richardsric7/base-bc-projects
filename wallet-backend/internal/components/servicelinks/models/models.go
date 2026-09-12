// Package models defines the persisted shapes for the servicelinks
// partner API - an API-key-authenticated surface letting a third-party
// service act on behalf of wallet users with scoped permissions. See
// PLAN.md §4.11 for the full design, including a cluster of real
// authorization bugs found in the original while porting this subsystem
// (plaintext API keys, no centralized suspended/inactive checks, an
// overloaded single permission flag, missing wallet-ownership scoping on
// money-moving routes, a broken KYC-override lookup, and an unscoped
// document store) and how each is fixed here rather than reproduced.
package models

import "time"

// ServiceLink is one partner's API credential and capability grant. APIKeyHash
// is a SHA-256 hash - the raw key is generated once at creation, shown to
// the partner exactly once, and never stored (fixing the original's
// plaintext-API-key storage).
type ServiceLink struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	OwnerUserID uint      `gorm:"index;not null" json:"ownerUserId"` // the partner's own users.User account
	APIKeyHash  string    `gorm:"uniqueIndex;size:64;not null" json:"-"`
	ShortName   string    `gorm:"uniqueIndex;size:50;not null" json:"shortName"`
	LongName    string    `gorm:"size:100" json:"longName"`

	// Capabilities - split from the original's single overloaded
	// CreateUsersPermission (which gated onboarding, KYC override,
	// minting, balance/history reads, payments, sub-wallets, and every
	// tokenization-management route with one flag) into independently
	// grantable scopes. See PLAN.md §4.11 finding 4.
	CanLogin                 bool `gorm:"default:false" json:"canLogin"`                 // LoginPermission
	CanRequestAuthorization  bool `gorm:"default:false" json:"canRequestAuthorization"`  // AuthorizationPermission
	CanRegisterEvents        bool `gorm:"default:false" json:"canRegisterEvents"`        // EventPermission
	CanViewUserInfo          bool `gorm:"default:false" json:"canViewUserInfo"`          // AllowUserInfo
	CanSendPushNotifications bool `gorm:"default:false" json:"canSendPushNotifications"` // PushNotificationPermission
	CanLookupTokenInfo       bool `gorm:"default:false" json:"canLookupTokenInfo"`       // TokenInfoPermission
	CanCreateUsers           bool `gorm:"default:false" json:"canCreateUsers"`
	CanUpdateKYC             bool `gorm:"default:false" json:"canUpdateKyc"`
	CanSendPayments          bool `gorm:"default:false" json:"canSendPayments"`
	CanReadBalances          bool `gorm:"default:false" json:"canReadBalances"`
	CanManageTokenization    bool `gorm:"default:false" json:"canManageTokenization"`

	AllowReferralForRegisteredUsers bool `gorm:"default:false" json:"allowReferralForRegisteredUsers"`

	// Status - checked centrally by middleware.APIKeyAuth for every route,
	// deny-by-default (PLAN.md §4.11 finding 3; the original only enforced
	// this trio on three of its ~40 routes).
	Verified         bool   `gorm:"default:false" json:"verified"`
	Inactive         bool   `gorm:"default:false" json:"inactive"`
	Suspended        bool   `gorm:"default:false" json:"suspended"`
	SuspensionReason string `json:"suspensionReason,omitempty"`
}

// ApprovalKind distinguishes what a ServiceLinkApproval is for - the
// original had three near-identical models and handler trios (login
// delegation, generic 2FA authorization, event registration) that differ
// only in this one dimension. Consolidated into one model/flow here
// (PLAN.md §4.11 finding 8).
type ApprovalKind string

const (
	ApprovalLogin     ApprovalKind = "LOGIN"
	ApprovalAuthorize ApprovalKind = "AUTHORIZE"
	ApprovalEvent     ApprovalKind = "EVENT"
)

// ServiceLinkApproval is one pending (or resolved) request/approve/verify
// exchange: a partner requests it for a specific target user, that user's
// own wallet app approves it (proven by their existing wallet-session JWT
// per PLAN.md §4.11 finding 9, not a bespoke per-request signature scheme),
// and the partner polls to collect the result - a session-issuing JWT for
// ApprovalLogin, a plain yes/no for the other two kinds.
type ServiceLinkApproval struct {
	ID            string       `gorm:"primaryKey;size:64" json:"id"`
	CreatedAt     time.Time    `json:"createdAt"`
	ServiceLinkID uint         `gorm:"index;not null" json:"serviceLinkId"`
	Kind          ApprovalKind `gorm:"size:16;not null" json:"kind"`
	TargetUserID  uint         `gorm:"index;not null" json:"targetUserId"`
	Description   string       `json:"description"`
	CallbackURL   string       `json:"-"`
	ExpiresAt     time.Time    `gorm:"not null" json:"expiresAt"`
	Authorized    bool         `gorm:"default:false" json:"authorized"`
}

// StakeholderDocument is a private document upload (e.g. a partner's KYB
// paperwork) - a separate object-storage channel from tokenization's own
// document uploads (§4.9), scoped by the uploading ServiceLinkID so one
// partner can never read or delete another's documents (PLAN.md §4.11
// finding 7 - the original had no such column at all, an IDOR).
type StakeholderDocument struct {
	ID               string    `gorm:"primaryKey;size:64" json:"id"` // random object key
	CreatedAt        time.Time `json:"createdAt"`
	ServiceLinkID    uint      `gorm:"index;not null" json:"serviceLinkId"`
	OriginalFilename string    `json:"originalFilename"`
	ContentType      string    `json:"contentType"`
	SHA256           string    `json:"sha256"`
	StorageKey       string    `json:"-"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&ServiceLink{},
	&ServiceLinkApproval{},
	&StakeholderDocument{},
}
