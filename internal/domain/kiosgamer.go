package domain

import (
	"time"

	"gorm.io/gorm"
)

// KiosgamerCredential stores the rotating Kiosgamer session and optional
// authenticator secret. Sensitive fields are encrypted before persistence.
type KiosgamerCredential struct {
	ID                  uint           `gorm:"primaryKey" json:"id"`
	ProviderID          uint           `gorm:"uniqueIndex;not null" json:"provider_id"`
	SessionKeyEncrypted string         `gorm:"type:text" json:"-"`
	TOTPSecretEncrypted string         `gorm:"type:text" json:"-"`
	AccountUsername     string         `gorm:"size:100" json:"account_username"`
	AccountUID          string         `gorm:"size:100" json:"account_uid"`
	OAuthExpiryTime     int64          `json:"oauth_expiry_time"`
	SessionStatus       string         `gorm:"size:30;default:'UNKNOWN'" json:"session_status"`
	LastCheckedAt       *time.Time     `json:"last_checked_at"`
	LastRecoveredAt     *time.Time     `json:"last_recovered_at"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`

	Provider *Provider `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
}

const (
	KiosgamerStatusUnknown           = "UNKNOWN"
	KiosgamerStatusActive            = "ACTIVE"
	KiosgamerStatusExpired           = "EXPIRED"
	KiosgamerStatusReauthRequired    = "REAUTH_REQUIRED"
	KiosgamerStatusChallengeRequired = "CHALLENGE_REQUIRED"
)
