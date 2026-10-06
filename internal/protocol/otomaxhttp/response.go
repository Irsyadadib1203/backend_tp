package otomaxhttp

type Response struct {
	StatusCode int
	Body       []byte
}

// ParsedResponse contains protocol-level fields only. Mapping its Status to a
// transaction consequence belongs to the provider adapter.
type ParsedResponse struct {
	TransactionID   string
	ProductCode     string
	CustomerID      string
	ServerID        string
	Status          string
	ProviderOrderID string
	SerialNumber    string
	Balance         float64
	Raw             []byte
}

type UserInfo struct {
	Name    string
	Email   string
	Plan    string
	Balance float64
	Raw     []byte
}
