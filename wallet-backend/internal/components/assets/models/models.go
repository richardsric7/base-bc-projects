package models

import "time"

// CuratedAsset is an entry in the wallet's supported-asset catalog - the
// list shown in an asset picker, as opposed to arbitrary Stellar assets a
// user could still opt into a trustline for directly.
type CuratedAsset struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Code      string    `gorm:"size:12;not null" json:"code"`
	Issuer    string    `gorm:"size:56;not null" json:"issuer"`
	Name      string    `gorm:"size:128" json:"name"`
	ImageURL  string    `gorm:"size:512" json:"imageUrl"`
	IsActive  bool      `gorm:"default:true" json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&CuratedAsset{},
}
