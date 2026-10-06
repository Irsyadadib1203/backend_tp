package provider

import (
	"testing"
)

func TestValidateProviderConfig_Valid(t *testing.T) {
	validJSON := `{
		"protocol": "otomax_http",
		"base_url": "https://api.ffzstore.com",
		"purchase": {
			"method": "GET",
			"path": "/v1/otomax/order",
			"timeout_seconds": 20,
			"query": {
				"apikey": "{{secret.apikey}}",
				"product_code": "{{product_code}}",
				"user_id": "{{customer_id}}",
				"server_id": "{{server_id}}",
				"trx_id": "{{ref_id}}",
				"callback_url": "{{callback_url}}"
			}
		},
		"parse": {
			"status_regex": "status\\s+([A-Za-z]+)",
			"provider_order_regex": "RefId\\s*:\\s*(\\S+)",
			"balance_regex": "Sisa saldo\\s+([0-9.,]+)"
		},
		"status_map": {
			"PENDING": "pending",
			"SUKSES": "success"
		}
	}`

	cfg, err := ValidateProviderConfig(validJSON)
	if err != nil {
		t.Fatalf("expected valid config, got err: %v", err)
	}
	if cfg.Protocol != "otomax_http" {
		t.Errorf("expected protocol otomax_http, got %q", cfg.Protocol)
	}
}

func TestValidateProviderConfig_AllowsProviderOrderIDStatusTemplate(t *testing.T) {
	raw := `{
		"protocol":"otomax_http",
		"base_url":"https://api.ffzstore.com",
		"status":{"method":"GET","path":"/v1/otomax/status/{{provider_order_id}}","query":{"apikey":"{{secret.apikey}}"}}
	}`
	if _, err := ValidateProviderConfig(raw); err != nil {
		t.Fatalf("expected provider-order status template to be valid: %v", err)
	}
}

func TestValidateProviderConfig_RejectsUnsupportedStatusMapping(t *testing.T) {
	raw := `{"protocol":"otomax_http","base_url":"https://api.ffzstore.com","status_map":{"SUCCESS":"refund_everything"}}`
	if _, err := ValidateProviderConfig(raw); err == nil {
		t.Fatal("expected unsupported status-map target to be rejected")
	}
}

func TestValidateProviderConfig_RejectsUnknownPlaceholder(t *testing.T) {
	invalidJSON := `{
		"protocol": "otomax_http",
		"base_url": "https://api.ffzstore.com",
		"purchase": {
			"method": "GET",
			"path": "/v1/order",
			"query": {
				"malicious": "{{arbitrary_code_exec}}"
			}
		}
	}`

	_, err := ValidateProviderConfig(invalidJSON)
	if err == nil {
		t.Fatal("expected error on unknown placeholder, got nil")
	}
}

func TestValidateProviderConfig_RejectsNonHTTPSAndSSRF(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"http scheme", "http://api.example.com"},
		{"localhost", "https://localhost:8080/order"},
		{"loopback IP", "https://127.0.0.1/order"},
		{"private IP", "https://192.168.1.1/order"},
		{"AWS metadata IP", "https://169.254.169.254/latest/meta-data/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jsonStr := `{"protocol": "otomax_http", "base_url": "` + tc.url + `"}`
			_, err := ValidateProviderConfig(jsonStr)
			if err == nil {
				t.Fatalf("expected rejection for %s (%s), got nil", tc.name, tc.url)
			}
		})
	}
}

func TestValidateProviderConfig_RejectsInvalidRegex(t *testing.T) {
	invalidJSON := `{
		"protocol": "otomax_http",
		"base_url": "https://api.ffzstore.com",
		"parse": {
			"status_regex": "[a-z"
		}
	}`

	_, err := ValidateProviderConfig(invalidJSON)
	if err == nil {
		t.Fatal("expected error on invalid regex, got nil")
	}
}

func TestEncryptDecryptConfigSecret(t *testing.T) {
	appSecret := "test-app-secret-12345"
	plain := "my-provider-api-key-999"

	enc, err := EncryptConfigSecret(plain, appSecret)
	if err != nil {
		t.Fatalf("encrypt err: %v", err)
	}
	if enc == plain || enc == "" {
		t.Fatalf("unexpected ciphertext: %q", enc)
	}

	dec, err := DecryptConfigSecret(enc, appSecret)
	if err != nil {
		t.Fatalf("decrypt err: %v", err)
	}
	if dec != plain {
		t.Fatalf("expected %q, got %q", plain, dec)
	}
}
