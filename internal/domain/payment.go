package domain

import (
	"time"

	"gorm.io/gorm"
)

type PaymentCategory string

const (
	PaymentCatEWallet        PaymentCategory = "E-Wallet"
	PaymentCatVirtualAccount PaymentCategory = "Virtual Account"
	PaymentCatQRIS           PaymentCategory = "QRIS"
	PaymentCatConvenience    PaymentCategory = "Convenience Store"
	PaymentCatBalance        PaymentCategory = "Saldo Akun"
	PaymentCatManual         PaymentCategory = "Manual Transfer"
)

type PaymentMethod struct {
	ID           uint            `gorm:"primaryKey" json:"id"`
	Code         string          `gorm:"size:50;uniqueIndex;not null" json:"code"` // e.g. "QRIS", "BCAVA", "OVO", "SALDO"
	Name         string          `gorm:"size:100;not null" json:"name"`
	Category     PaymentCategory `gorm:"size:50;not null" json:"category"`
	FixedFee     float64         `gorm:"type:decimal(15,2);default:0" json:"fixed_fee"`
	PercentFee   float64         `gorm:"type:decimal(5,2);default:0" json:"percent_fee"`
	MinAmount    float64         `gorm:"type:decimal(15,2);default:1000" json:"min_amount"`
	MaxAmount    float64         `gorm:"type:decimal(15,2);default:50000000" json:"max_amount"`
	ImageURL     string          `gorm:"size:255" json:"image_url"`
	Instructions string          `gorm:"type:text" json:"instructions"`
	IsActive     bool            `gorm:"default:true" json:"is_active"`
	SortOrder    int             `gorm:"default:0" json:"sort_order"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	DeletedAt    gorm.DeletedAt  `gorm:"index" json:"-"`
}

func (p *PaymentMethod) CalculateFee(amount float64) float64 {
	percentFee := amount * (p.PercentFee / 100)
	return p.FixedFee + percentFee
}
