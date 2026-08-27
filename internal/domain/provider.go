package domain

import (
	"time"

	"gorm.io/gorm"
)

type Provider struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:100;not null" json:"name"`
	Code        string         `gorm:"size:50;uniqueIndex;not null" json:"code"` // "DIGIFLAZZ"
	BaseURL     string         `gorm:"size:255;not null" json:"base_url"`
	Username    string         `gorm:"size:100" json:"username"`
	APIKey      string         `gorm:"size:255" json:"api_key"`
	Balance     float64        `gorm:"type:decimal(15,2);default:0" json:"balance"`
	IsActive    bool           `gorm:"default:true" json:"is_active"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	Nominals     []Nominal     `gorm:"foreignKey:ProviderID" json:"nominals,omitempty"`
	Transactions []Transaction `gorm:"foreignKey:ProviderID" json:"transactions,omitempty"`
}
