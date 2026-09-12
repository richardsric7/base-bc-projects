// Package models defines the persisted shapes owned by the fiat component:
// the decoupled build/pay/submit invoice pattern internal/fiat's doc
// comment describes, plus the "activation" flow that dispenses starter
// gas and a reward token to a newly-onboarded user once they pay a
// configured fiat amount. See PLAN.md §4.5 for the design and for why a
// few pieces of the original (per-country activation amounts, the
// asset-purchase leg's on-chain settlement) are trimmed or deferred here.
package models

import "time"

// ActivationConfig is a single-row table (there is always exactly one
// active row) holding the current fiat activation price and how it splits
// between starter gas and a reward token. The original kept this
// per-country (CountryConfig.FiatActivationAmount/TrovTokenActivationPercent),
// keyed by a user's country code; this port has no country/geo-IP
// subsystem yet (that's Phase 13's reference-data work), so it's a single
// global config for now - swapping in a per-country lookup later only
// requires adding a CountryCode column and a lookup key, not touching the
// dispensing logic itself.
type ActivationConfig struct {
	ID                   uint    `gorm:"primaryKey" json:"id"`
	FiatCurrency         string  `gorm:"size:8;not null;default:USD" json:"fiatCurrency"`
	FiatActivationAmount float64 `gorm:"not null;default:10" json:"fiatActivationAmount"`
	// RewardTokenPercent is the share of FiatActivationAmount converted to
	// the reward token (see ActivationRewardTokenSymbol config); the
	// remainder is converted to native ETH for starter gas.
	RewardTokenPercent float64 `gorm:"not null;default:50" json:"rewardTokenPercent"`
}

// FiatPaymentInvoice is a PENDING record of an intended fiat payment,
// created by the client (with a client-chosen idempotency reference as its
// ID) before it pays, and completed once the provider's webhook confirms
// success. Ported from the original's FiatPaymentInvoice; SignedTransaction
// replaces the original's separate unsigned-XDR + detached-signature pair
// with the single 0x-prefixed raw signed transaction Base's client-signing
// flow produces (see internal/network's UnsignedTx/SubmitSignedTransaction).
// PaymentType "ACTIVATION" is fully wired up in this phase; any other
// value (e.g. a future "ASSET_PURCHASE" from Phase 9) reuses this same
// generic PENDING -> submit-signed-tx -> COMPLETED path unchanged.
type FiatPaymentInvoice struct {
	ID                string    `gorm:"primaryKey;size:64" json:"id"`
	UserID            uint      `gorm:"index;not null" json:"userId"`
	ServiceProvider   string    `gorm:"size:32;not null" json:"serviceProvider"`
	PaymentType       string    `gorm:"size:32;not null" json:"paymentType"`
	Amount            float64   `gorm:"not null" json:"amount"`
	Currency          string    `gorm:"size:8;not null" json:"currency"`
	Status            string    `gorm:"size:16;not null;default:PENDING" json:"status"`
	SignedTransaction *string   `json:"-"`
	TransactionHash   *string   `gorm:"size:66" json:"transactionHash"`
	CreatedAt         time.Time `json:"createdAt"`
}

// FiatPayment is a permanent audit record of a completed fiat-triggered
// payout (activation dispense, or a future asset-purchase settlement).
// Ported from the original's FiatPayment.
type FiatPayment struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	UserID            uint      `gorm:"index;not null" json:"userId"`
	ServiceProvider   string    `gorm:"size:32;not null" json:"serviceProvider"`
	PaymentType       string    `gorm:"size:32;not null" json:"paymentType"`
	ProviderReference string    `gorm:"size:64" json:"providerReference"`
	Amount            float64   `json:"amount"`
	CreatedAt         time.Time `json:"createdAt"`
}

// FlutterwaveWebhookLog is a permanent audit record of every Flutterwave
// webhook received, mirroring the audit-log pattern already used for
// Sumsub (internal/components/kyc).
type FlutterwaveWebhookLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Event     string    `gorm:"size:64" json:"event"`
	TxRef     string    `gorm:"index;size:64" json:"txRef"`
	CreatedAt time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns, for the central
// migration list assembled in main.go.
var Models = []interface{}{
	&ActivationConfig{},
	&FiatPaymentInvoice{},
	&FiatPayment{},
	&FlutterwaveWebhookLog{},
}
