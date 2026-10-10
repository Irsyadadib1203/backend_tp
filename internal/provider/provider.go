// Package provider defines the provider-facing boundary used by future
// transaction-service integration. It intentionally contains no transaction
// persistence, refund, or SSE behavior.
package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type Status int

const (
	StatusSuccess Status = iota
	StatusPending
	StatusFailedFinal
	StatusFailedHold
)

type PurchaseRequest struct {
	RefID                   string
	ProductCode             string
	ProductName             string
	CustomerID              string
	ServerID                string
	GameSlug                string
	ExistingProviderOrderID string
	// ProviderData is opaque previously persisted provider output. Adapters may
	// use it to recover a provider-specific identifier without asking the
	// transaction service to interpret their protocol.
	ProviderData string
	CallbackURL  string
}

type StatusRequest struct {
	RefID           string
	ProductCode     string
	CustomerID      string
	ServerID        string
	GameSlug        string
	ProviderOrderID string
	// ProviderData is opaque previously persisted provider output.
	ProviderData string
}

type Result struct {
	Status          Status
	ProviderOrderID string
	SN              string
	Message         string
	ProviderStatus  string
	Raw             []byte
	// RequestRaw is a redacted, auditable snapshot of the provider request.
	// It is persisted together with Raw so the admin can inspect both sides.
	RequestRaw                      []byte
	StatusReason                    string
	RefundReason 					string
	PaymentReferencePolicy          PaymentReferencePolicy
	UpdateSN                        bool
	IncludeSNInSuccessEvent         bool
	IncludeCompletedAtInFailedEvent bool
}

// CustomerNumber joins a player ID and optional server/zone ID in the format
// required by Digiflazz (for example 12345678(2001)). Other providers retain
// the separate fields in PurchaseRequest.
func CustomerNumber(customerID, serverID string) string {
	customerID = strings.TrimSpace(customerID)
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return customerID
	}
	return customerID + serverID 
}

// ExchangeData is the backward-compatible envelope stored in
// Transaction.ProviderCallbackData. Secrets must be redacted before it is
// created. Response is intentionally generic because providers use JSON and
// plain text payloads.
type ExchangeData struct {
	Action     string          `json:"action"`
	Request    json.RawMessage `json:"request,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	RecordedAt time.Time       `json:"recorded_at"`
}

func Exchange(action string, request, response []byte) []byte {
	value := ExchangeData{Action: action, Request: request, Response: response, RecordedAt: time.Now().UTC()}
	encoded, err := json.Marshal(value)
	if err != nil {
		return response
	}
	return encoded
}

// RequestSnapshot returns an intentionally credential-free audit record. It
// is used when an adapter cannot expose its protocol request directly.
func RequestSnapshot(action, providerCode string, refID, productCode, customerID, serverID, providerOrderID string) []byte {
	value := map[string]string{
		"action": action, "provider": providerCode, "ref_id": refID,
		"product_code": productCode, "customer_id": customerID, "server_id": serverID,
		"provider_order_id": providerOrderID,
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

// ResponseFromExchange supports both new envelopes and historical raw data.
func ResponseFromExchange(raw string) []byte {
	var value ExchangeData
	if json.Unmarshal([]byte(raw), &value) == nil && len(value.Response) > 0 {
		return value.Response
	}
	return []byte(raw)
}

// PaymentReferencePolicy is adapter metadata consumed by the service's
// business-effect boundary. It preserves legacy source-specific behavior
// without allowing adapters to persist transactions themselves.
type PaymentReferencePolicy int

const (
	PaymentReferenceIfEmpty PaymentReferencePolicy = iota
	PaymentReferenceAlways
)

// Provider is intentionally small. Provider-specific optional behavior is
// represented by the capability interfaces below.
type Provider interface {
	Code() string
	Purchase(context.Context, PurchaseRequest) (*Result, error)
	CheckStatus(context.Context, StatusRequest) (*Result, error)
	Balance(context.Context) (float64, error)
}

type CatalogItem struct {
	ProductCode string
	Name        string
	BasePrice   float64
	IsActive    bool
}

type CatalogSyncer interface {
	SyncCatalog(context.Context) ([]CatalogItem, error)
}

type CallbackParser interface {
	ParseCallback(*http.Request) (*Result, string, error)
}

type GameSupport interface {
	Supports(gameSlug string) bool
}
