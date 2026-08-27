package domain

import (
	"time"

	"gorm.io/gorm"
)

type IPWhitelist struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	UserID    *uint          `gorm:"index" json:"user_id,omitempty"` // null means global whitelist
	IPAddress string         `gorm:"size:45;not null;index" json:"ip_address"` // IPv4 or IPv6
	Label     string         `gorm:"size:100" json:"label"` // e.g. "Server Digiflazz Jakarta", "Mitra Topupku"
	IsActive  bool           `gorm:"default:true" json:"is_active"`
	CreatedBy string         `gorm:"size:100" json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	User      *User          `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

type IPAccessLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	IPAddress  string    `gorm:"size:45;not null;index" json:"ip_address"`
	Endpoint   string    `gorm:"size:255;not null" json:"endpoint"`
	Method     string    `gorm:"size:10;not null" json:"method"`
	StatusCode int       `gorm:"index" json:"status_code"`
	Status     string    `gorm:"size:50" json:"status"` // "ALLOWED", "BLOCKED", "SUSPICIOUS"
	Reason     string    `gorm:"size:255" json:"reason"`
	UserAgent  string    `gorm:"size:255" json:"user_agent"`
	Payload    string    `gorm:"type:text" json:"payload,omitempty"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

type IPWatchlist struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	IPAddress      string    `gorm:"size:45;uniqueIndex;not null" json:"ip_address"`
	FailedAttempts int       `gorm:"default:1" json:"failed_attempts"`
	LastAttemptAt  time.Time `json:"last_attempt_at"`
	IsBlocked      bool      `gorm:"default:false" json:"is_blocked"`
	BlockReason    string    `gorm:"size:255" json:"block_reason"`
	BlockedUntil   *time.Time `json:"blocked_until,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
