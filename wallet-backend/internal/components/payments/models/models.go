package models

import "time"

// PaymentHistory records a submitted payment for lookup, independent of the
// chain's own history. IdempotencyKey lets Submit be safely retried by an
// app that went offline mid-flow (see PLAN.md §3): resubmitting the same
// key returns the original record instead of erroring or double-processing.
type PaymentHistory struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	IdempotencyKey string `gorm:"uniqueIndex;size:64;not null" json:"idempotencyKey"`
	FromAddress    string `gorm:"index;size:42;not null" json:"fromAddress"`
	ToAddress      string `gorm:"index;size:42;not null" json:"toAddress"`
	TokenAddress   string `gorm:"size:42" json:"tokenAddress"` // empty = native ETH
	Amount         string `gorm:"size:80;not null" json:"amount"`
	// Memo is the original's own free-text payment reference (the same
	// field its send-payment screen and payment-request deep link both
	// carried) - optional, purely a display string with nothing on-chain
	// to attach it to (Base ERC-20 transfers/native sends have no memo
	// field the way a Stellar transaction did), so it lives only here.
	// Restored after being dropped without documentation - see PLAN.md
	// §23.
	Memo      string    `gorm:"size:200" json:"memo,omitempty"`
	TxHash    string    `gorm:"uniqueIndex;size:66;not null" json:"txHash"`
	CreatedAt time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&PaymentHistory{},
}
