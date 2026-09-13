// Package models defines the persisted shapes owned by the users component.
package models

import "time"

// User is an account holder. The wallet is non-custodial: Address is the
// EVM address recovered from a successful SIWE sign-in (see
// internal/components/auth) - the server never sees, let alone stores, the
// matching private key.
type User struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Username  string `gorm:"uniqueIndex;size:32;not null" json:"username"`
	Email     string `gorm:"uniqueIndex;size:255;not null" json:"email"`
	Address   string `gorm:"uniqueIndex;size:42;not null" json:"address"`
	KYCStatus string `gorm:"size:32;default:pending" json:"kycStatus"`
	// KYCVerifiedLevel is the highest identity-verification level this user
	// has completed with either vendor (see internal/components/kyc) - 0
	// means unverified. Ported from the original's User.KYCVerified; other
	// phases (fiat activation limits, tokenization purchase limits) gate on
	// this field, exactly as they did upstream.
	KYCVerifiedLevel       int  `gorm:"default:0" json:"kycVerifiedLevel"`
	AccountRecoveryEnabled bool `gorm:"default:false" json:"accountRecoveryEnabled"`
	// Activated records whether this user has completed the one-time paid
	// activation flow (see internal/components/fiat) that dispenses
	// starter gas and a reward token - checked so the payout can never be
	// claimed twice, even if the provider's webhook is delivered more than
	// once for the same successful charge.
	Activated bool      `gorm:"default:false" json:"activated"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// CreatedByServiceLinkID marks an account onboarded by a servicelinks
	// partner (see internal/components/servicelinks) rather than
	// self-registered - nil for an organic registration. Every servicelinks
	// route that acts on a specific user's wallet (payments, KYC overrides,
	// tokenization actions) requires this to equal the calling service
	// link's own ID, checked centrally rather than per-route (PLAN.md
	// §4.11 findings 5 and 6 - the original had this scoping only on some
	// routes, and its KYC-override check treated a nil value here as "skip
	// the check" instead of "deny").
	CreatedByServiceLinkID *uint `gorm:"index" json:"createdByServiceLinkId,omitempty"`
	// RegistrationCountryCode/RegistrationHighRisk are a best-effort
	// signal captured at registration time via internal/geoip resolving
	// the caller's IP, and reference.CountryConfig.HighRisk for that
	// country (see internal/components/reference, PLAN.md §4.13). Empty/
	// false whenever no geo-IP provider is configured or the lookup
	// fails - registration never blocks on this, it's a review flag, not
	// a gate.
	RegistrationCountryCode string `gorm:"size:2" json:"registrationCountryCode,omitempty"`
	RegistrationHighRisk    bool   `gorm:"default:false" json:"registrationHighRisk,omitempty"`
}

// UserWallet lets a user register additional EVM addresses they control
// (e.g. a hardware-wallet address) alongside their primary wallet.
type UserWallet struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	Address   string    `gorm:"uniqueIndex;size:42;not null" json:"address"`
	Label     string    `gorm:"size:64" json:"label"`
	IsPrimary bool      `gorm:"default:false" json:"isPrimary"`
	CreatedAt time.Time `json:"createdAt"`
}

// SecurityQuestion is one entry in the fixed catalog users pick from when
// setting up account-recovery questions.
type SecurityQuestion struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Question string `gorm:"size:255;not null" json:"question"`
}

// UserSecurityAnswer stores a hashed answer to one of the user's chosen
// security questions, used as one recovery factor alongside email OTP.
type UserSecurityAnswer struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	UserID             uint   `gorm:"index;not null" json:"userId"`
	SecurityQuestionID uint   `gorm:"not null" json:"securityQuestionId"`
	AnswerHash         string `gorm:"size:255;not null" json:"-"`
}

// AccountRecoveryEmailVerification stores a one-time recovery OTP sent to a
// user's registered email. Recovery is deliberately public/unauthenticated
// (see services/recovery.go) since its entire purpose is helping someone
// who can no longer sign in - this record plus a correct set of security
// answers are the two factors that stand in for a signature.
type AccountRecoveryEmailVerification struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	Code      string    `gorm:"size:16;not null" json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// AccountRecoveryLog is a permanent audit record of every completed
// recovery. AuthoritySignature is an EIP-191 signature over a description
// of this exact recovery, produced by a recovery-authority key derived via
// cryptoutil.DeriveKey (see services/recovery.go) - a tamper-evident
// attestation that this specific recovery was authorized by the server,
// standing in for the on-chain co-signature the original's native
// Stellar multi-sig recovery flow used (there's nothing to co-sign
// on-chain here, since re-pointing which address controls a username is a
// purely application-level change, not a chain operation - see PLAN.md §4.3).
type AccountRecoveryLog struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Username           string    `gorm:"index;size:32;not null" json:"username"`
	OldAddress         string    `gorm:"size:42;not null" json:"oldAddress"`
	NewAddress         string    `gorm:"size:42;not null" json:"newAddress"`
	AuthoritySignature string    `gorm:"size:132;not null" json:"authoritySignature"`
	CreatedAt          time.Time `json:"createdAt"`
}

// ReservedName is a username no one may register (staff handles, brand
// names, obviously-impersonation-prone strings). Checked at registration.
type ReservedName struct {
	ID   uint   `gorm:"primaryKey" json:"id"`
	Name string `gorm:"uniqueIndex;size:32;not null" json:"name"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&User{},
	&UserWallet{},
	&SecurityQuestion{},
	&UserSecurityAnswer{},
	&AccountRecoveryEmailVerification{},
	&AccountRecoveryLog{},
	&ReservedName{},
}
