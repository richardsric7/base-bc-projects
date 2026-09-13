// mirror.go holds read-only reflections of wallet-backend's own tables.
// This engine queries these tables to know which wallets to track and
// which token contracts are curated, but never migrates or writes to
// them - AutoMigrate is only ever called with the Models slice in
// models.go (PaymentHistory, TrackedWallet, IndexerCursor). Field tags
// here exist only so GORM resolves the same physical table/column names
// wallet-backend already owns; ownership of the schema stays with
// wallet-backend.
package models

import "time"

// UserWallet mirrors wallet-backend's internal/components/users/models.UserWallet.
// Table: user_wallets.
type UserWallet struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"index;not null"`
	Address   string    `gorm:"uniqueIndex;size:42;not null"`
	Label     string    `gorm:""`
	IsPrimary bool      `gorm:""`
	CreatedAt time.Time `gorm:""`
}

func (UserWallet) TableName() string { return "user_wallets" }

// CuratedToken mirrors wallet-backend's internal/components/assets/models.CuratedToken.
// Table: curated_tokens. Its ContractAddress list is what scopes
// network.Client.TransferLogs to a bounded, address-specific query
// instead of network-wide noise (PLAN.md §3.2).
type CuratedToken struct {
	ID              uint      `gorm:"primaryKey"`
	Symbol          string    `gorm:""`
	ContractAddress string    `gorm:"uniqueIndex;size:42;not null"`
	Decimals        uint8     `gorm:""`
	Name            string    `gorm:""`
	ImageURL        string    `gorm:""`
	IsActive        bool      `gorm:""`
	CreatedAt       time.Time `gorm:""`
}

func (CuratedToken) TableName() string { return "curated_tokens" }
