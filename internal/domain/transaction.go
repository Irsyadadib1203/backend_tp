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
	SourceWeb    TransactionSource = "web"
	SourceH2H    TransactionSource = "h2h"
	SourceAdmin  TransactionSource = "admin"
)

type Transaction struct {
	ID                   uint              `gorm:"primaryKey" json:"id"`
	InvoiceNumber        string            `gorm:"size:50;uniqueIndex;not null" json:"invoice_number"`
	IdempotencyKey       string            `gorm:"size:100;index" json:"idempotency_key"`
	PartnerCallbackURL   string            `gorm:"size:255" json:"-"`
	Source               TransactionSource `gorm:"size:20;default:'web'" json:"source"`
	
	// Customer Details
	UserID               *uint             `gorm:"index" json:"user_id,omitempty"` // nullable for guest checkout
	CustomerID           string            `gorm:"size:100;not null" json:"customer_id"` // User Game ID
	ServerID             string            `gorm:"size:50" json:"server_id"`            // Zone / Server ID
	CustomerPhone        string            `gorm:"size:30" json:"customer_phone"`
	CustomerEmail        string            `gorm:"size:100" json:"customer_email"`
	Nickname             string            `gorm:"size:100" json:"nickname"`
	
	// Item Details
	GameID               uint              `gorm:"index;not null" json:"game_id"`
	NominalID            uint              `gorm:"index;not null" json:"nominal_id"`
	ProviderID           uint              `gorm:"index;default:1" json:"provider_id"`
	
	// Financial Details
	BasePrice            float64           `gorm:"type:decimal(15,2);not null" json:"base_price"`
	SellingPrice         float64           `gorm:"type:decimal(15,2);not null" json:"selling_price"`
	AdminFee             float64           `gorm:"type:decimal(15,2);default:0" json:"admin_fee"`
	TotalAmount          float64           `gorm:"type:decimal(15,2);not null" json:"total_amount"`
	Profit               float64           `gorm:"type:decimal(15,2);default:0" json:"profit"`
	
	// Status & Flow
	Status               TransactionStatus `gorm:"size:30;default:'pending';index" json:"status"`
	PaymentMethod        string            `gorm:"size:50" json:"payment_method"`
	PaymentReference     string            `gorm:"size:100" json:"payment_reference"`
	PaymentVerifiedAt    *time.Time        `json:"payment_verified_at"`
	CheckoutURL          string            `gorm:"size:255" json:"checkout_url,omitempty"`
	QRURL                string            `gorm:"size:255" json:"qr_url,omitempty"`
	PaymentInstructions  string            `gorm:"type:text" json:"payment_instructions,omitempty"`
	
	// Provider (Digiflazz) Execution Details
	RefID                string            `gorm:"size:100;index" json:"ref_id"` // Reference passed to provider
	ProviderOrderID      string            `gorm:"size:100" json:"provider_order_id"`
	ProviderStatus       string            `gorm:"size:50" json:"provider_status"`
	ProviderMessage      string            `gorm:"size:255" json:"provider_message"`
	ProviderCallbackData string            `gorm:"type:text" json:"provider_callback_data"`
	SN                   string            `gorm:"size:255" json:"sn"` // Serial number / Voucher code from provider
	
	// Retries & Completion
	RetryCount           int               `gorm:"default:0" json:"retry_count"`
	MaxRetries           int               `gorm:"default:3" json:"max_retries"`
	LastRetryAt          *time.Time        `json:"last_retry_at"`
	CompletedAt          *time.Time        `json:"completed_at"`
	
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	DeletedAt            gorm.DeletedAt    `gorm:"index" json:"-"`

	// Relations
	User                 *User             `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Game                 *Game             `gorm:"foreignKey:GameID" json:"game,omitempty"`
	Nominal              *Nominal          `gorm:"foreignKey:NominalID" json:"nominal,omitempty"`
	Provider             *Provider         `gorm:"foreignKey:ProviderID" json:"provider,omitempty"`
	StatusHistories      []TransactionStatusHistory `gorm:"foreignKey:TransactionID" json:"status_histories,omitempty"`
}

type TransactionStatusHistory struct {
	ID            uint              `gorm:"primaryKey" json:"id"`
	TransactionID uint              `gorm:"index;not null" json:"transaction_id"`
	FromStatus    TransactionStatus `gorm:"size:30" json:"from_status"`
	ToStatus      TransactionStatus `gorm:"size:30;not null" json:"to_status"`
	Reason        string            `gorm:"size:255" json:"reason"`
	CreatedAt     time.Time         `json:"created_at"`
}

// ProviderReference returns the ref_id that identifies this transaction at the
// upstream provider (Digiflazz, Kiosgamer, FFZStore, ...).
//
// Model data: kolom ref_id hanya berisi ref_id milik mitra H2H. Ref yang dikirim
// ke provider disimpan di provider_order_id saat order dibuat (web maupun H2H),
// lalu tercatat juga sebagai "ref_id" pada request di provider_callback_data.
// Order web lama (sebelum model ini) menyimpan ref provider di ref_id.
func (t *Transaction) ProviderReference() string {
	if t == nil {
		return ""
	}
	if ref := referenceFromExchange(t.ProviderCallbackData); ref != "" {
		return ref
	}
	if t.Source != SourceH2H && strings.TrimSpace(t.RefID) != "" {
		return t.RefID // order web lama
	}
	if id := strings.TrimSpace(t.ProviderOrderID); id != "" && id != "-" {
		return id
	}
	return t.RefID
}

// RealProviderOrderID returns ProviderOrderID only when it identifies an order
// that exists at the provider. For an order that was never submitted,
// ProviderOrderID just holds the reference we generated, so it is not a real
// order id and an empty string is returned (adapters then place a new order
// instead of polling a non-existent one).
func (t *Transaction) RealProviderOrderID() string {
	if t == nil {
		return ""
	}
	id := strings.TrimSpace(t.ProviderOrderID)
	if id == "" || id == "-" {
		return ""
	}
	if id == t.ProviderReference() && !exchangeHasResponse(t.ProviderCallbackData) {
		return ""
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
