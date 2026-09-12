package models

import "time"

// PaymentHistory records a submitted payment for lookup, independent of
// Horizon's own (much larger, time-limited) operation history.
type PaymentHistory struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	FromPublicKey string    `gorm:"index;size:56;not null" json:"fromPublicKey"`
	ToPublicKey   string    `gorm:"index;size:56;not null" json:"toPublicKey"`
	AssetCode     string    `gorm:"size:12" json:"assetCode"`
	AssetIssuer   string    `gorm:"size:56" json:"assetIssuer"`
	Amount        string    `gorm:"size:32;not null" json:"amount"`
	TxHash        string    `gorm:"uniqueIndex;size:64;not null" json:"txHash"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&PaymentHistory{},
}
