package domain

import "time"

// Banner adalah model untuk slide promo carousel di halaman utama frontend.
type Banner struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `gorm:"size:200;not null" json:"title"`
	Subtitle  string    `gorm:"type:text" json:"subtitle"`
	ImageURL  string    `gorm:"size:500;not null" json:"image_url"`
	LinkURL   string    `gorm:"size:500" json:"link_url"`
	BadgeText string    `gorm:"size:100" json:"badge_text"`
	SortOrder int       `gorm:"default:0" json:"sort_order"`
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
