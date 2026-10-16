package domain

import (
	"time"
	"math"

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
	PriceVIP            float64        `gorm:"column:price_vip;type:decimal(15,2);not null" json:"price_vip"`
	PriceReseller       float64        `gorm:"type:decimal(15,2);not null" json:"price_reseller"` // for H2H Seller
	
	MarginPercent       float64        `gorm:"type:decimal(5,2);default:0" json:"margin_percent"`
	MarginFlat          float64        `gorm:"type:decimal(15,2);default:0" json:"margin_flat"`
	
	ProviderProductCode string         `gorm:"size:100;index;not null" json:"provider_product_code"` // buyer_sku_code in Digiflazz
	KiosgamerProductCode string        `gorm:"size:100;index" json:"kiosgamer_product_code"`         // SKU / item_id in Kiosgamer
	SellerProductCode   string         `gorm:"size:100;index" json:"seller_product_code"`           // buyer_sku_code when clients order from us
	
	IsActive            bool           `gorm:"default:true" json:"is_active"`
	SortOrder           int            `gorm:"default:0" json:"sort_order"`
	
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`

	// Relation
	Game             *Game             `gorm:"foreignKey:GameID" json:"game,omitempty"`
	Provider         *Provider         `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
	ProviderProducts []ProviderProduct `gorm:"foreignKey:NominalID" json:"provider_products,omitempty"`
}

func (n *Nominal) CalculatePrices() {
	if n.MarginPercent <= 0 && n.MarginFlat <= 0 {
		n.ForceCalculatePrices()
		return
	}
	if n.PricePublic > 0 && n.PriceMember > 0 && n.PriceVIP > 0 && n.PriceReseller > 0 {
		return
	}
	n.calculateMissingPrices()
}

func (n *Nominal) ForceCalculatePrices() {
	n.PricePublic = 0
	n.PriceMember = 0
	n.PriceVIP = 0
	n.PriceReseller = 0
	n.calculateMissingPrices()
}

func (n *Nominal) calculateMissingPrices() {
	margin := 0.0
	switch {
	case n.MarginPercent > 0:
		margin = n.BasePrice * (n.MarginPercent / 100)
	case n.MarginFlat > 0:
		margin = n.MarginFlat
	}
	if n.PricePublic <= 0 {
		n.PricePublic = math.Round(n.BasePrice + margin)
	}
	if n.PriceMember <= 0 {
		n.PriceMember = math.Round(n.BasePrice + margin*0.85)
	}
	if n.PriceVIP <= 0 {
		n.PriceVIP = math.Round(n.BasePrice + margin*0.70)
	}
	if n.PriceReseller <= 0 {
		n.PriceReseller = math.Round(n.BasePrice + margin*0.50)
	}
}

func (n *Nominal) ApplyBasePrice(base float64) {
	n.BasePrice = base
	n.ForceCalculatePrices()
}