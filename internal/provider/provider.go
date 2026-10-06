// Package provider defines the provider-facing boundary used by future
// transaction-service integration. It intentionally contains no transaction
// persistence, refund, or SSE behavior.
package provider

import (
	"context"
	"net/http"
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
	GameSlug        string
	ProviderOrderID string
	// ProviderData is opaque previously persisted provider output.
	ProviderData string
}

type Result struct {
	Status                          Status
	ProviderOrderID                 string
	SN                              string
	Message                         string
	ProviderStatus                  string
	Raw                             []byte
	StatusReason                    string
	PaymentReferencePolicy          PaymentReferencePolicy
	UpdateSN                        bool
	IncludeSNInSuccessEvent         bool
	IncludeCompletedAtInFailedEvent bool
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
