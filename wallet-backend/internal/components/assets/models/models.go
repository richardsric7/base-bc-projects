package models

import "time"

// CuratedToken is an entry in the wallet's supported-token catalog - the
// list shown in a token picker, as opposed to arbitrary ERC-20 tokens a
// user could still hold or approve directly (there is no EVM equivalent of
// a Stellar trustline gating which assets an address may hold - anyone can
// hold any ERC-20 balance with no opt-in step).
type CuratedToken struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Symbol          string    `gorm:"size:12;not null" json:"symbol"`
	ContractAddress string    `gorm:"uniqueIndex;size:42;not null" json:"contractAddress"`
	Decimals        uint8     `gorm:"not null" json:"decimals"`
	Name            string    `gorm:"size:128" json:"name"`
	ImageURL        string    `gorm:"size:512" json:"imageUrl"`
	IsActive        bool      `gorm:"default:true" json:"isActive"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&CuratedToken{},
}
