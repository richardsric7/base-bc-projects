package models

import "time"

// Announcement is an in-app notice shown to users (a banner, a maintenance
// notice, a feature callout).
type Announcement struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `gorm:"size:255;not null" json:"title"`
	Body      string    `gorm:"type:text;not null" json:"body"`
	IsActive  bool      `gorm:"default:true" json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
}

// Models is every GORM model this component owns.
var Models = []interface{}{
	&Announcement{},
}
