// Package models defines this engine's own persisted shapes (PaymentHistory,
// TrackedWallet, IndexerCursor - migrated and owned here) plus read-only
// mirrors of wallet-backend's tables this engine queries but never writes
// or migrates (mirror.go). See PLAN.md §3.6.
package models

import "time"

// PaymentHistory is the canonical, chain-observed payment record - the
// single table this engine writes to, and the table wallet-backend's own
// read-side query API is meant to be pointed at (PLAN.md §6's
// coordination note). One canonical unique key this time, not the two
// disagreeing composite keys PLAN.md §1.1/§4 found across the original's
// two repos: (TransactionHash, LogIndex) is a true dedup key for both a
// logged ERC-20 transfer (a real log index) and a native-ETH transfer (its
// LogIndex is synthesized as 0, and a transaction can only ever carry one
// native-value transfer).
type PaymentHistory struct {
	ID              string    `gorm:"primaryKey;size:36" json:"id"` // uuid
	TransactionHash string    `gorm:"size:66;not null;index:idx_payment_history_unique_key,unique" json:"transactionHash"`
	LogIndex        uint      `gorm:"not null;index:idx_payment_history_unique_key,unique" json:"logIndex"`
	BlockNumber     uint64    `gorm:"not null;index" json:"blockNumber"`
	TransactionDate time.Time `gorm:"index" json:"transactionDate"`
	// TransactionType is one of PAYMENT | MINT | BURN | "SWAP <A>><B>" -
	// see PLAN.md §3.3.
	TransactionType string  `gorm:"size:32;not null" json:"transactionType"`
	From            string  `gorm:"size:42;not null;index" json:"from"`
	To              string  `gorm:"size:42;not null;index" json:"to"`
	TokenAddress    *string `gorm:"size:42" json:"tokenAddress,omitempty"` // nil = native ETH
	AssetCode       string  `gorm:"size:12;not null" json:"assetCode"`
	// Amount is the base-unit decimal string (wei, or the token's own base
	// unit) - never a float, matching wallet-backend's own convention.
	Amount string `gorm:"not null" json:"amount"`
}

// TrackedWallet mirrors which addresses this engine watches, alongside the
// wallet-backend user ID that owns each one - used both to scope indexer
// queries and to resolve notify.Event.UserID without a second join.
// Deliberately holds no denormalized alias/name (PLAN.md §3.6's explicit
// improvement over the original: reading the current username at query
// time, not baking in whatever it was when the row was tracked, avoids
// showing a stale name on old history after a rename).
type TrackedWallet struct {
	Address string `gorm:"primaryKey;size:42" json:"address"`
	UserID  uint   `gorm:"not null;index" json:"userId"`
}

// IndexerCursor persists the live watcher's resume position - a single
// row, always ID=1. This is the fix for PLAN.md §1.2/§4's central bug:
// the original designed this exact mechanism (MonitoredCursor) but never
// actually called the save function that would have kept it current.
type IndexerCursor struct {
	ID        uint `gorm:"primaryKey"`
	LastBlock uint64
}

// Models is every GORM model this engine owns and migrates.
var Models = []interface{}{
	&PaymentHistory{},
	&TrackedWallet{},
	&IndexerCursor{},
}
