// Package models defines the persisted shapes owned by the stablerail
// component: a chain-agnostic NGN<->stablecoin on-ramp vendor integration,
// same shape as kyc/fiat - see PLAN.md §4.6. Stablerail's own API already
// takes a destination wallet address and a network parameter per request
// rather than assuming Stellar, so porting this to Base is mostly a matter
// of sending "base" instead of the original's "xbn" network code and a
// 0x-address instead of a Stellar public key - there is very little here
// that is actually Stellar-specific.
package models

import "time"

// StablerailUser links a local user to the ID Stablerail's own onboarding
// API assigns them, once their BVN has been verified. Ported from the
// original's StablerailUser (renamed field: UserID uint FK, this port's
// established convention, rather than a Username string).
type StablerailUser struct {
	ID     string `gorm:"primaryKey;size:64" json:"id"` // Stablerail's own user ID
	UserID uint   `gorm:"uniqueIndex;not null" json:"userId"`
}

// StablerailRequest is an audit/poll-tracking row for any asynchronous
// Stablerail request (onboarding or an on-ramp) - RequestType and Status
// (whatever string Stablerail's API returns) decide which of the two
// background pollers picks it up. Ported from the original.
type StablerailRequest struct {
	ID             string    `gorm:"primaryKey;size:64" json:"id"` // Stablerail's own request ID
	UserID         uint      `gorm:"index;not null" json:"userId"`
	RequestType    string    `gorm:"size:32;not null" json:"requestType"` // "Onboarding" or "Onramp"
	Status         string    `gorm:"size:32" json:"status"`
	ResponseObject string    `json:"-"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// StablerailOnramp tracks one NGN->stablecoin on-ramp request end to end:
// WalletAddress is Stablerail's own internal custodial wallet for this
// request (where the user's bank transfer lands after conversion);
// DestinationAddress is the user's own Base address funds are ultimately
// withdrawn to. Ported from the original's StablerailOnramp
// (TrovoWalletAddress -> DestinationAddress).
type StablerailOnramp struct {
	ID                 string    `gorm:"primaryKey;size:64" json:"id"`
	UserID             uint      `gorm:"index;not null" json:"userId"`
	WalletAddress      string    `gorm:"size:128" json:"walletAddress"`
	DestinationAddress string    `gorm:"size:42" json:"destinationAddress"`
	TotalAmount        float64   `json:"totalAmount"`
	TargetAsset        string    `gorm:"size:16" json:"targetAsset"`
	Status             string    `gorm:"size:32" json:"status"`
	AutoSwapEnabled    bool      `gorm:"default:false" json:"autoSwapEnabled"`
	CreatedAt          time.Time `json:"createdAt"`
}

// StablerailAssetWithdrawal records Stablerail's own transfer of converted
// funds from its internal wallet to the user's Base address - triggered
// automatically once an on-ramp is funded (see services.go), never a
// direct client action. Ported from the original's
// StablerailAssetWithdrawalRequest.
type StablerailAssetWithdrawal struct {
	ID                string    `gorm:"primaryKey;size:64" json:"id"` // reuses the onramp request ID
	UserID            uint      `gorm:"index;not null" json:"userId"`
	InternalWallet    string    `gorm:"size:128" json:"internalWallet"`
	DestinationWallet string    `gorm:"size:42" json:"destinationWallet"`
	Amount            float64   `json:"amount"`
	Ticker            string    `gorm:"size:16" json:"ticker"`
	Network           string    `gorm:"size:16" json:"network"`
	Status            string    `gorm:"size:32" json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
}

// StablerailBank is one entry in Stablerail's supported-bank list for a
// country, synced periodically (see services.go) - ported directly from
// the original.
type StablerailBank struct {
	BankCode    string `gorm:"primaryKey;size:16" json:"bankCode"`
	BankName    string `gorm:"size:128" json:"bankName"`
	CountryCode string `gorm:"size:8" json:"countryCode"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&StablerailUser{},
	&StablerailRequest{},
	&StablerailOnramp{},
	&StablerailAssetWithdrawal{},
	&StablerailBank{},
}
