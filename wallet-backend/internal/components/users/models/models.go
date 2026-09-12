// Package models defines the persisted shapes owned by the users component.
package models

import "time"

// User is an account holder. The wallet is non-custodial: PublicKey is
// client-supplied at registration and the server never sees (let alone
// stores) the matching secret key.
type User struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Username  string    `gorm:"uniqueIndex;size:32;not null" json:"username"`
	Email     string    `gorm:"uniqueIndex;size:255;not null" json:"email"`
	PublicKey string    `gorm:"uniqueIndex;size:56;not null" json:"publicKey"`
	KYCStatus string    `gorm:"size:32;default:pending" json:"kycStatus"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// UserWallet lets a user register additional Stellar accounts they control
// (e.g. a hardware-wallet address) alongside their primary wallet.
type UserWallet struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	PublicKey string    `gorm:"uniqueIndex;size:56;not null" json:"publicKey"`
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

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&User{},
	&UserWallet{},
	&SecurityQuestion{},
	&UserSecurityAnswer{},
}
