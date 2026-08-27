package domain

import "time"

// Article adalah model untuk artikel berita, promo, event, dan patch notes.
type Article struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Title       string    `gorm:"size:300;not null" json:"title"`
	Slug        string    `gorm:"size:320;uniqueIndex;not null" json:"slug"`
	Category    string    `gorm:"size:50;default:'Promo'" json:"category"` // Promo | Update | Event | Patch Notes
	Excerpt     string    `gorm:"type:text" json:"excerpt"`
	Content     string    `gorm:"type:text" json:"content"`
	ImageURL    string    `gorm:"size:500" json:"image_url"`
	ReadTime    string    `gorm:"size:30;default:'3 min read'" json:"read_time"`
	IsPublished bool      `gorm:"default:true" json:"is_published"`
	SortOrder   int       `gorm:"default:0" json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
