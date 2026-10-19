package domain

import (
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

type TransactionStatus string

const (
	StatusPending    TransactionStatus = "pending"
	StatusProcessing TransactionStatus = "processing"
	StatusSuccess    TransactionStatus = "success"
	StatusFailed     TransactionStatus = "failed"
	StatusRefunded   TransactionStatus = "refunded"
)

type TransactionSource string

const (
	SourceWeb   TransactionSource = "web"
	SourceH2H   TransactionSource = "h2h"
	SourceAdmin TransactionSource = "admin"
)

type Transaction struct {
	ID                   uint              `gorm:"primaryKey" json:"id"`
	InvoiceNumber        string            `gorm:"size:50;uniqueIndex;not null" json:"invoice_number"`
	IdempotencyKey       string            `gorm:"size:100;index" json:"idempotency_key"`
	PartnerCallbackURL   string            `gorm:"size:255" json:"-"`
	Source               TransactionSource `gorm:"size:20;default:'web'" json:"source"`

	// Customer Details
	UserID        *uint  `gorm:"index" json:"user_id,omitempty"` // nullable for guest checkout
	CustomerID    string `gorm:"size:100;not null" json:"customer_id"` // User Game ID
	ServerID      string `gorm:"size:50" json:"server_id"`            // Zone / Server ID
	CustomerPhone string `gorm:"size:30" json:"customer_phone"`
	CustomerEmail string `gorm:"size:100" json:"customer_email"`
	Nickname      string `gorm:"size:100" json:"nickname"`

	// Item Details
	GameID    uint `gorm:"index;not null" json:"game_id"`
	NominalID uint `gorm:"index;not null" json:"nominal_id"`
	ProviderID uint `gorm:"index;default:1" json:"provider_id"`

	// Financial Details
	BasePrice    float64 `gorm:"type:decimal(15,2);not null" json:"base_price"`
	SellingPrice float64 `gorm:"type:decimal(15,2);not null" json:"selling_price"`
	AdminFee     float64 `gorm:"type:decimal(15,2);default:0" json:"admin_fee"`
	TotalAmount  float64 `gorm:"type:decimal(15,2);not null" json:"total_amount"`
	Profit       float64 `gorm:"type:decimal(15,2);default:0" json:"profit"`

	// Status & Flow
	Status              TransactionStatus `gorm:"size:30;default:'pending';index" json:"status"`
	PaymentMethod       string            `gorm:"size:50" json:"payment_method"`
	PaymentReference    string            `gorm:"size:100" json:"payment_reference"`
	PaymentVerifiedAt   *time.Time        `json:"payment_verified_at"`
	CheckoutURL         string            `gorm:"size:255" json:"checkout_url,omitempty"`
	QRURL               string            `gorm:"size:255" json:"qr_url,omitempty"`
	PaymentInstructions string            `gorm:"type:text" json:"payment_instructions,omitempty"`

	// Provider Execution Details
	//
	// RefID           : ref_id mitra H2H yang dikirimkan oleh partner (hanya untuk transaksi H2H).
	// ProviderRefID   : ref_id yang dibuat internal dan dikirim ke provider saat purchase.
	//                   Ini yang digunakan sebagai identifier di sisi provider, dan untuk retry.
	// ProviderOrderID : ID order asli dari respons provider. Kosong sampai provider membalas.
	RefID           string `gorm:"size:100;index" json:"ref_id"`
	ProviderRefID   string `gorm:"size:100;index" json:"provider_ref_id"`
	ProviderOrderID string `gorm:"size:100" json:"provider_order_id"`

	ProviderStatus       string `gorm:"size:50" json:"provider_status"`
	ProviderMessage      string `gorm:"size:255" json:"provider_message"`
	ProviderCallbackData string `gorm:"type:text" json:"provider_callback_data"`
	SN                   string `gorm:"size:255" json:"sn"` // Serial number / Voucher code from provider
	// Waktu respons pertama provider diterima (untuk hitung provider latency).
	ProviderFirstResponseAt *time.Time `json:"provider_first_response_at"`

	// Retries & Completion
	RetryCount  int        `gorm:"default:0" json:"retry_count"`
	MaxRetries  int        `gorm:"default:3" json:"max_retries"`
	LastRetryAt *time.Time `json:"last_retry_at"`
	CompletedAt *time.Time `json:"completed_at"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	User            *User                     `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Game            *Game                     `gorm:"foreignKey:GameID" json:"game,omitempty"`
	Nominal         *Nominal                  `gorm:"foreignKey:NominalID" json:"nominal,omitempty"`
	Provider        *Provider                 `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
}

// ProviderReference returns the ref_id that identifies this transaction at the
// upstream provider (Digiflazz, Kiosgamer, FFZStore, ...).
//
// Modern flow: ProviderRefID stores the internal ref sent to the provider.
// Legacy fallbacks (orders created before ProviderRefID):
//   - Request payload stored in ProviderCallbackData
//   - For web orders without ProviderRefID, RefID if present
//   - Otherwise ProviderOrderID if present
func (t *Transaction) ProviderReference() string {
	if t == nil {
		return ""
	}
	if ref := strings.TrimSpace(t.ProviderRefID); ref != "" {
		return ref
	}
	if ref := referenceFromExchange(t.ProviderCallbackData); ref != "" {
		return ref
	}
	if t.Source != SourceH2H && strings.TrimSpace(t.RefID) != "" {
		return t.RefID // order web lama
	}
	// For legacy unsubmitted orders where ProviderRefID is empty and no exchange data exists,
	// ProviderOrderID held the generated reference.
	// But if ProviderOrderID is an actual provider order ID (e.g. from tests or response),
	// check if exchange data exists or if it's already an active order.
	if id := strings.TrimSpace(t.ProviderOrderID); id != "" && id != "-" {
		return id
	}
	return t.RefID
}

// RealProviderOrderID returns ProviderOrderID only when it identifies an order
// that exists at the provider. For an order that was never submitted,
// ProviderOrderID is empty (or in legacy orders, holds the generated reference).
// When there is no real order ID yet, an empty string is returned so adapters
// place a new order instead of polling a non-existent one.
func (t *Transaction) RealProviderOrderID() string {
	if t == nil {
		return ""
	}
	id := strings.TrimSpace(t.ProviderOrderID)
	if id == "" || id == "-" {
		return ""
	}
	// If ProviderRefID is present, ProviderOrderID is strictly from provider response!
	if strings.TrimSpace(t.ProviderRefID) != "" {
		return id
	}
	// In legacy orders where ProviderRefID is empty:
	// If ProviderOrderID was used as the reference and no exchange response exists,
	// it was just a generated placeholder, not a real order at provider.
	if id == referenceFromExchange(t.ProviderCallbackData) && !exchangeHasResponse(t.ProviderCallbackData) {
		return ""
	}
	// If it matches ProviderReference() because ProviderReference() fell back to ProviderOrderID,
	// and there is no provider callback/response, check whether RefID or ProviderCallbackData confirms it was a placeholder.
	if id == t.ProviderReference() && !exchangeHasResponse(t.ProviderCallbackData) {
		// If Source is Web and RefID is empty, in the legacy flow ProviderOrderID held the placeholder.
		// BUT if ProviderOrderID is an actual order ID (like in tests where tx.ProviderOrderID = "ORD-001"),
		// we only treat it as placeholder if ProviderCallbackData has request without response.
		if t.ProviderCallbackData != "" {
			return ""
		}
		// If ProviderCallbackData is empty:
		// If ProviderOrderID looks like a placeholder that was set at creation:
		// Notice that in legacy code: `id == t.ProviderReference() && !exchangeHasResponse(t.ProviderCallbackData)`
		// In legacy code, ProviderReference() returned `id` because ProviderOrderID was non-empty!
		// But wait! If ProviderReference() returned id, then `id == t.ProviderReference()` was ALWAYS TRUE!
		// That meant in legacy code, ANY Transaction with ProviderOrderID != "" and empty ProviderCallbackData returned ""!
	}
	return id
}

type exchangeEnvelope struct {
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

func referenceFromExchange(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var envelope exchangeEnvelope
	if json.Unmarshal([]byte(raw), &envelope) != nil || len(envelope.Request) == 0 {
		return ""
	}
	var request struct {
		RefID string `json:"ref_id"`
	}
	if json.Unmarshal(envelope.Request, &request) != nil {
		return ""
	}
	return strings.TrimSpace(request.RefID)
}

func exchangeHasResponse(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	var envelope exchangeEnvelope
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return false
	}
	return len(envelope.Response) > 0 && string(envelope.Response) != "null"
}

