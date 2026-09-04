package domain

import (
	"time"

	"gorm.io/gorm"
)

type DepositStatus string

const (
	DepositPending   DepositStatus = "pending"
	DepositApproved  DepositStatus = "approved"
	DepositRejected  DepositStatus = "rejected"
	DepositCancelled DepositStatus = "cancelled"
)

type MutationType string

const (
	MutationDebit  MutationType = "debit"  // Saldo berkurang (belanja topup)
	MutationCredit MutationType = "credit" // Saldo bertambah (deposit, refund, bonus)
)

type DepositRequest struct {
	ID            uint          `gorm:"primaryKey" json:"id"`
	InvoiceNumber string        `gorm:"size:50;uniqueIndex;not null" json:"invoice_number"`
	UserID        uint          `gorm:"index;not null" json:"user_id"`
	Amount        float64       `gorm:"type:decimal(15,2);not null" json:"amount"`
	UniqueCode    int           `gorm:"default:0" json:"unique_code"`
	TotalAmount         float64       `gorm:"type:decimal(15,2);not null" json:"total_amount"`
	AdminFee            float64       `gorm:"type:decimal(15,2);default:0" json:"admin_fee"`
	PaymentType         string        `gorm:"size:20;default:'manual'" json:"payment_type"` // "instant" (Tripay) or "manual"
	PaymentMethod       string        `gorm:"size:50;not null" json:"payment_method"`
	PaymentReference    string        `gorm:"size:100" json:"payment_reference"` // PayCode / VA No
	TripayReference     string        `gorm:"size:100" json:"tripay_reference"`
	CheckoutURL         string        `gorm:"size:255" json:"checkout_url,omitempty"`
	QRURL               string        `gorm:"size:255" json:"qr_url,omitempty"`
	PaymentInstructions string        `gorm:"type:text" json:"payment_instructions,omitempty"`
	ProofImageURL       string        `gorm:"size:255" json:"proof_image_url"`
	Status        DepositStatus `gorm:"size:30;default:'pending';index" json:"status"`
	Notes         string        `gorm:"size:255" json:"notes"`
	ApprovedBy    *uint         `json:"approved_by,omitempty"`
	ApprovedAt    *time.Time    `json:"approved_at,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`

	User          *User         `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

type BalanceMutation struct {
	ID            uint         `gorm:"primaryKey" json:"id"`
	UserID        uint         `gorm:"index;not null" json:"user_id"`
	Type          MutationType `gorm:"size:10;not null" json:"type"` // debit / credit
	Amount        float64      `gorm:"type:decimal(15,2);not null" json:"amount"`
	BalanceBefore float64      `gorm:"type:decimal(15,2);not null" json:"balance_before"`
	BalanceAfter  float64      `gorm:"type:decimal(15,2);not null" json:"balance_after"`
	ReferenceType string       `gorm:"size:50" json:"reference_type"` // "TRANSACTION", "DEPOSIT", "REFUND", "ADJUSTMENT"
	ReferenceID   string       `gorm:"size:100;index" json:"reference_id"` // Invoice number
	Description   string       `gorm:"size:255;not null" json:"description"`
	CreatedAt     time.Time    `json:"created_at"`

	User          *User        `gorm:"foreignKey:UserID" json:"user,omitempty"`
}
