package domain

import (
	"time"

	"gorm.io/gorm"
)

type Nominal struct {
	ID                  uint           `gorm:"primaryKey" json:"id"`
	GameID              uint           `gorm:"index;not null" json:"game_id"`
	ProviderID          uint           `gorm:"index;default:1" json:"provider_id"`
	Name                string         `gorm:"size:150;not null" json:"name"`
	Description         string         `gorm:"size:255" json:"description"`
	
	// Multi-Tier Pricing
	BasePrice           float64        `gorm:"type:decimal(15,2);not null" json:"base_price"`
	PricePublic         float64        `gorm:"type:decimal(15,2);not null" json:"price_public"`
	PriceMember         float64        `gorm:"type:decimal(15,2);not null" json:"price_member"`
	PriceVIP            float64        `gorm:"type:decimal(15,2);not null" json:"price_vip"`
	PriceReseller       float64        `gorm:"type:decimal(15,2);not null" json:"price_reseller"` // for H2H Seller
	
	MarginPercent       float64        `gorm:"type:decimal(5,2);default:0" json:"margin_percent"`
	MarginFlat          float64        `gorm:"type:decimal(15,2);default:0" json:"margin_flat"`
	
	ProviderProductCode string         `gorm:"size:100;index;not null" json:"provider_product_code"` // buyer_sku_code in Digiflazz
	SellerProductCode   string         `gorm:"size:100;index" json:"seller_product_code"`           // buyer_sku_code when clients order from us
	
	IsActive            bool           `gorm:"default:true" json:"is_active"`
	SortOrder           int            `gorm:"default:0" json:"sort_order"`
	
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`

	// Relation
	Game                *Game          `gorm:"foreignKey:GameID" json:"game,omitempty"`
	Provider            *Provider      `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
}

func (n *Nominal) CalculatePrices() {
	if n.MarginPercent > 0 {
		margin := n.BasePrice * (n.MarginPercent / 100)
		n.PricePublic = n.BasePrice + margin
		n.PriceMember = n.BasePrice + (margin * 0.85)
		n.PriceVIP = n.BasePrice + (margin * 0.70)
		n.PriceReseller = n.BasePrice + (margin * 0.50)
	} else if n.MarginFlat > 0 {
		n.PricePublic = n.BasePrice + n.MarginFlat
		n.PriceMember = n.BasePrice + (n.MarginFlat * 0.85)
		n.PriceVIP = n.BasePrice + (n.MarginFlat * 0.70)
		n.PriceReseller = n.BasePrice + (n.MarginFlat * 0.50)
	}
}
