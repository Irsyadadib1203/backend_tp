// Package otomaxhttp implements the transport and plain-text response format
// shared by OtoMax-compatible providers. It has no transaction, refund, or
// provider-specific business rules.
package otomaxhttp

import "time"

type Endpoint struct {
	Method         string
	Path           string
	Timeout        time.Duration
	Query          map[string]string
	Headers        map[string]string
	Body           string
	MaxResponseLen int64
}

type TemplateValues struct {
	ProductCode     string
	CustomerID      string
	ServerID        string
	RefID           string
	ProviderOrderID string
	CallbackURL     string
	Secrets         map[string]string
}
