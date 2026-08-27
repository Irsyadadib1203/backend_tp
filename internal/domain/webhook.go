package domain

import (
	"time"

	"gorm.io/gorm"
)

type WebhookDirection string

const (
	WebhookIncoming WebhookDirection = "incoming"
	WebhookOutgoing WebhookDirection = "outgoing"
)

type WebhookLog struct {
	ID           uint             `gorm:"primaryKey" json:"id"`
	Direction    WebhookDirection `gorm:"size:10;not null;index" json:"direction"`
	ProviderName string           `gorm:"size:50;not null;index" json:"provider_name"` // "DIGIFLAZZ", "TRIPAY", "H2H_CLIENT"
	URL          string           `gorm:"size:255" json:"url"`
	Payload      string           `gorm:"type:text" json:"payload"`
	Response     string           `gorm:"type:text" json:"response"`
	StatusCode   int              `json:"status_code"`
	ErrorMessage string           `gorm:"size:255" json:"error_message,omitempty"`
	CreatedAt    time.Time        `gorm:"index" json:"created_at"`
	DeletedAt    gorm.DeletedAt   `gorm:"index" json:"-"`
}
