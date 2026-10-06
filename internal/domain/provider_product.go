package domain

import (
	"time"

	"gorm.io/gorm"
)

// ProviderProduct maps a nominal item to a specific provider's product SKU.
// It supports multi-provider routing and priority-based failover.
type ProviderProduct struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	NominalID   uint           `gorm:"index;not null;uniqueIndex:idx_nominal_provider" json:"nominal_id"`
	ProviderID  uint           `gorm:"index;not null;uniqueIndex:idx_nominal_provider" json:"provider_id"`
	ProductCode string         `gorm:"size:100;not null" json:"product_code"`
	CostPrice   *float64       `gorm:"type:decimal(15,2)" json:"cost_price,omitempty"`
	Priority    int            `gorm:"default:0" json:"priority"`
	IsActive    bool           `gorm:"default:true" json:"is_active"`
	Extra       string         `gorm:"type:text" json:"extra,omitempty"` // Data-only JSON metadata (e.g. {"requires_server_id":true})

	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	Nominal  *Nominal  `gorm:"foreignKey:NominalID" json:"nominal,omitempty"`
	Provider *Provider `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
}
