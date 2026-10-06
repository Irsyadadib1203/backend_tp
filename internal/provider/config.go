package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"topup-backend/internal/pkg/crypto"
)

// Allowed placeholders in provider configuration templates (§3.5).
var allowedPlaceholders = map[string]bool{
	"product_code":      true,
	"customer_id":       true,
	"server_id":         true,
	"ref_id":            true,
	"provider_order_id": true,
	"callback_url":      true,
}

var placeholderRegex = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// EndpointConfig defines an HTTP call configuration for a provider action.
type EndpointConfig struct {
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Query          map[string]string `json:"query,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
}

// ParserConfig defines response parsing rules via whitelisted regexes.
type ParserConfig struct {
	StatusRegex        string `json:"status_regex,omitempty"`
	ProviderOrderRegex string `json:"provider_order_regex,omitempty"`
	BalanceRegex       string `json:"balance_regex,omitempty"`
	SNRegex            string `json:"sn_regex,omitempty"`
	MaxResponseBytes   int64  `json:"max_response_bytes,omitempty"`
}

// DeclarativeProviderConfig models declarative provider configuration (§3.5).
// Secrets referenced inside are encrypted with internal/pkg/crypto.
type DeclarativeProviderConfig struct {
	Protocol        string            `json:"protocol"`
	BaseURL         string            `json:"base_url"`
	Purchase        *EndpointConfig   `json:"purchase,omitempty"`
	Status          *EndpointConfig   `json:"status,omitempty"`
	Balance         *EndpointConfig   `json:"balance,omitempty"`
	Parse           *ParserConfig     `json:"parse,omitempty"`
	StatusMap       map[string]string `json:"status_map,omitempty"`
	HoldPatterns    []string          `json:"hold_message_patterns,omitempty"`
	UnknownResponse string            `json:"unknown_response,omitempty"`
	EncryptedSecret string            `json:"encrypted_secret,omitempty"`
	Reference       *ReferenceConfig  `json:"reference,omitempty"`
}

// ReferenceConfig controls transaction identifiers generated for a provider.
// Supported tokens: {date}, {time}, {timestamp}, {unix}, {random},
// {customer_id}, {server_id}, {nominal_id}, and {provider_code}.
type ReferenceConfig struct {
	InvoiceTemplate string `json:"invoice_template,omitempty"`
	RefIDTemplate   string `json:"ref_id_template,omitempty"`
}

func ParseReferenceConfig(raw string) ReferenceConfig {
	var cfg DeclarativeProviderConfig
	if json.Unmarshal([]byte(raw), &cfg) == nil && cfg.Reference != nil {
		return *cfg.Reference
	}
	return ReferenceConfig{}
}

func WithReferenceConfig(raw string, reference ReferenceConfig) (string, error) {
	var cfg DeclarativeProviderConfig
	if strings.TrimSpace(raw) != "" && json.Unmarshal([]byte(raw), &cfg) != nil {
		return "", errors.New("existing provider config is not valid JSON")
	}
	cfg.Reference = &reference
	encoded, err := json.Marshal(cfg)
	return string(encoded), err
}

func RenderReference(template, fallback, providerCode, customerID, serverID string, nominalID uint) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return fallback
	}
	now := time.Now()
	replacer := strings.NewReplacer(
		"{date}", now.Format("20060102"),
		"{time}", now.Format("150405"),
		"{timestamp}", now.Format("20060102150405"),
		"{unix}", fmt.Sprintf("%d", now.Unix()),
		"{random}", randomReferencePart(),
		"{customer_id}", sanitizeReferencePart(customerID),
		"{server_id}", sanitizeReferencePart(serverID),
		"{nominal_id}", fmt.Sprintf("%d", nominalID),
		"{provider_code}", sanitizeReferencePart(providerCode),
	)
	value := sanitizeReferencePart(replacer.Replace(template))
	// A template without {random} needs a random suffix to preserve database
	// uniqueness under simultaneous orders.
	if !strings.Contains(template, "{random}") {
		value += "-" + randomReferencePart()
	}
	if value == "" {
		return fallback
	}
	return value
}

func randomReferencePart() string {
	return strings.ToUpper(fmt.Sprintf("%x", time.Now().UnixNano()))[8:14]
}

func sanitizeReferencePart(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	var out strings.Builder
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '(' || character == ')' {
			out.WriteRune(character)
		}
	}
	return out.String()
}

// ValidateProviderConfig parses and strictly validates declarative provider configuration (§3.5).
func ValidateProviderConfig(rawJSON string) (*DeclarativeProviderConfig, error) {
	if strings.TrimSpace(rawJSON) == "" {
		return nil, errors.New("empty provider config")
	}

	var cfg DeclarativeProviderConfig
	if err := json.Unmarshal([]byte(rawJSON), &cfg); err != nil {
		return nil, fmt.Errorf("invalid json config: %w", err)
	}

	// 1. Validate Protocol
	switch strings.ToLower(strings.TrimSpace(cfg.Protocol)) {
	case "otomax_http", "rest_json", "custom":
		// Whitelisted protocols
	default:
		return nil, fmt.Errorf("unsupported protocol: %q", cfg.Protocol)
	}

	// 2. Validate BaseURL (SSRF defense & HTTPS requirement)
	if err := ValidateEndpointURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	// 3. Validate Endpoints
	for name, ep := range map[string]*EndpointConfig{
		"purchase": cfg.Purchase,
		"status":   cfg.Status,
		"balance":  cfg.Balance,
	} {
		if ep == nil {
			continue
		}
		if err := validateEndpoint(name, ep); err != nil {
			return nil, err
		}
	}

	// 4. Validate Parse regexes
	if cfg.Parse != nil {
		for name, pattern := range map[string]string{
			"status_regex":         cfg.Parse.StatusRegex,
			"provider_order_regex": cfg.Parse.ProviderOrderRegex,
			"balance_regex":        cfg.Parse.BalanceRegex,
			"sn_regex":             cfg.Parse.SNRegex,
		} {
			if pattern == "" {
				continue
			}
			if len(pattern) > 200 {
				return nil, fmt.Errorf("%s pattern too complex (exceeds 200 chars)", name)
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return nil, fmt.Errorf("invalid %s regex: %w", name, err)
			}
		}
	}

	for providerStatus, internalStatus := range cfg.StatusMap {
		if strings.TrimSpace(providerStatus) == "" {
			return nil, errors.New("status_map contains an empty provider status")
		}
		switch strings.ToLower(strings.TrimSpace(internalStatus)) {
		case "success", "pending", "hold", "failed_hold", "failed_final":
		default:
			return nil, fmt.Errorf("status_map has unsupported internal status %q", internalStatus)
		}
	}

	return &cfg, nil
}

// ValidateEndpointURL validates that a URL is HTTPS and does not point to internal/reserved networks (SSRF defense).
func ValidateEndpointURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed url: %w", err)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("https scheme is required, got %q", u.Scheme)
	}

	hostname := u.Hostname()
	if hostname == "" {
		return errors.New("empty hostname in url")
	}

	lowerHost := strings.ToLower(hostname)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".local") || strings.HasSuffix(lowerHost, ".internal") {
		return fmt.Errorf("prohibited internal host: %s", hostname)
	}

	// Check if hostname is an IP address
	if ip := net.ParseIP(hostname); ip != nil {
		if isPrivateOrLoopbackIP(ip) {
			return fmt.Errorf("prohibited ip target: %s", hostname)
		}
	}

	return nil
}

func isPrivateOrLoopbackIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// Also explicitly check AWS / GCP metadata IP 169.254.169.254
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}

func validateEndpoint(name string, ep *EndpointConfig) error {
	m := strings.ToUpper(strings.TrimSpace(ep.Method))
	if m != "GET" && m != "POST" && m != "PUT" {
		return fmt.Errorf("endpoint %s has invalid method %q (only GET/POST/PUT allowed)", name, ep.Method)
	}

	// Validate placeholders in Path
	if err := validatePlaceholders(ep.Path); err != nil {
		return fmt.Errorf("endpoint %s path: %w", name, err)
	}

	// Validate placeholders in Query
	for k, v := range ep.Query {
		if err := validatePlaceholders(k); err != nil {
			return fmt.Errorf("endpoint %s query key %q: %w", name, k, err)
		}
		if err := validatePlaceholders(v); err != nil {
			return fmt.Errorf("endpoint %s query value for %q: %w", name, k, err)
		}
	}

	// Validate placeholders in Body
	if err := validatePlaceholders(ep.Body); err != nil {
		return fmt.Errorf("endpoint %s body: %w", name, err)
	}

	return nil
}

func validatePlaceholders(template string) error {
	matches := placeholderRegex.FindAllStringSubmatch(template, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		ph := strings.TrimSpace(match[1])
		if strings.HasPrefix(ph, "secret.") {
			secretKey := strings.TrimPrefix(ph, "secret.")
			if strings.TrimSpace(secretKey) == "" {
				return errors.New("empty secret key in placeholder")
			}
			continue
		}
		if !allowedPlaceholders[ph] {
			return fmt.Errorf("unknown placeholder {{%s}}", ph)
		}
	}
	return nil
}

// EncryptConfigSecret encrypts a raw secret string using AES-GCM via internal/pkg/crypto.
func EncryptConfigSecret(plainSecret, appSecret string) (string, error) {
	return crypto.EncryptString(plainSecret, appSecret)
}

// DecryptConfigSecret decrypts an encrypted secret string using AES-GCM via internal/pkg/crypto.
func DecryptConfigSecret(encryptedSecret, appSecret string) (string, error) {
	return crypto.DecryptString(encryptedSecret, appSecret)
}
