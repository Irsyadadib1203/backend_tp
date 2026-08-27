package domain

import (
	"time"

	"gorm.io/gorm"
)

type APIKey struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	UserID     uint           `gorm:"uniqueIndex;not null" json:"user_id"`
	Key        string         `gorm:"size:64;uniqueIndex;not null" json:"key"`
	Secret     string         `gorm:"size:128;not null" json:"secret"`
	WebhookURL string         `gorm:"size:255" json:"webhook_url"`
	IsActive   bool           `gorm:"default:true" json:"is_active"`
	LastUsedAt *time.Time     `json:"last_used_at"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`

	User       *User          `gorm:"foreignKey:UserID" json:"user,omitempty"`
}
