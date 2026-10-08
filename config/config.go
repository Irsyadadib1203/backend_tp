package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppPort     string
	AppEnv      string
	AppSecret   string
	FrontendURL string
	AdminURL    string
	// PublicAPIURL is the externally reachable backend URL. Providers use it
	// for callbacks, so it must not be a private address or frontend origin.
	PublicAPIURL string

	// Database
	DBDriver string
	DBHost   string
	DBPort   string
	DBUser   string
	DBPass   string
	DBName   string
	DBSQLite string

	// JWT
	JWTSecret      string
	JWTExpiryHours int

	// Digiflazz Buyer
	DigiflazzBuyerBaseURL       string
	DigiflazzBuyerUsername      string
	DigiflazzBuyerAPIKey        string
	DigiflazzBuyerWebhookSecret string



	// Kiosgamer Provider
	KiosgamerBaseURL string

	// Payment Gateways (e.g. Tripay)
	TripayAPIKey       string
	TripayPrivateKey   string
	TripayMerchantCode string
	TripayBaseURL      string

	// Secrets for other/future payment gateways used by the generic
	// webhook route, keyed by lowercase provider name (the :provider path
	// param). Format in env: "provider1:secret1,provider2:secret2".
	// A provider with no entry here is never trusted by that route.
	GenericWebhookSecrets map[string]string

	// Reconciler (Fase 5) — disabled by default. Enable only after registry
	// mode has been running in production without discrepancy.
	ReconcilerEnabled bool
	ReconcilerMinAge  time.Duration
}

var AppConfig *Config

func LoadConfig() *Config {
	// Load .env if exists
	if err := godotenv.Load(); err != nil {
		_ = godotenv.Load("../.env")
	}

	expiryHours, err := strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "72"))
	if err != nil {
		expiryHours = 72
	}

	cfg := &Config{
		AppPort:      getEnv("APP_PORT", "8080"),
		AppEnv:       getEnv("APP_ENV", "development"),
		AppSecret:    getEnv("APP_SECRET", "super-secret-key-change-in-production-12345"),
		FrontendURL:  getEnv("FRONTEND_URL", "http://localhost:3000"),
		AdminURL:     getEnv("ADMIN_URL", "http://localhost:3001"),
		PublicAPIURL: getEnv("PUBLIC_API_URL", ""),

		DBDriver: getEnv("DB_DRIVER", "sqlite"),
		DBHost:   getEnv("DB_HOST", "127.0.0.1"),
		DBPort:   getEnv("DB_PORT", "3306"),
		DBUser:   getEnv("DB_USER", "root"),
		DBPass:   getEnv("DB_PASSWORD", ""),
		DBName:   getEnv("DB_DATABASE", "topup_db"),
		DBSQLite: getEnv("DB_SQLITE_PATH", "topup.db"),

		JWTSecret:      getEnv("JWT_SECRET", "jwt-secret-topup-system-secure-token-9988"),
		JWTExpiryHours: expiryHours,

		DigiflazzBuyerBaseURL:       getEnv("DIGIFLAZZ_BASE_URL", "https://api.digiflazz.com/v1"),
		DigiflazzBuyerUsername:      getEnv("DIGIFLAZZ_USERNAME", ""),
		DigiflazzBuyerAPIKey:        getEnv("DIGIFLAZZ_KEY", ""),
		DigiflazzBuyerWebhookSecret: getEnv("DIGIFLAZZ_WEBHOOK_SECRET", ""),

		KiosgamerBaseURL: getEnv("KIOSGAMER_BASE_URL", "https://kiosgamer.co.id/api"),

		TripayAPIKey:       getEnv("TRIPAY_API_KEY", ""),
		TripayPrivateKey:   getEnv("TRIPAY_PRIVATE_KEY", ""),
		TripayMerchantCode: getEnv("TRIPAY_MERCHANT_CODE", ""),
		TripayBaseURL:      getEnv("TRIPAY_BASE_URL", "https://tripay.co.id/api-sandbox"),

		GenericWebhookSecrets: parseProviderSecrets(getEnv("GENERIC_WEBHOOK_SECRETS", "")),

		ReconcilerEnabled: strings.ToLower(strings.TrimSpace(getEnv("RECONCILER_ENABLED", "false"))) == "true",
		ReconcilerMinAge:  parseReconcilerMinAge(getEnv("RECONCILER_MIN_AGE", "5m")),
	}

	AppConfig = cfg
	log.Printf("[Config] Loaded configuration. Environment: %s, Port: %s, DB Driver: %s", cfg.AppEnv, cfg.AppPort, cfg.DBDriver)
	return cfg
}

// parseProviderSecrets parses "provider1:secret1,provider2:secret2" into a
// lowercase-keyed map. Malformed entries are skipped rather than causing a
// startup failure — a bad entry just leaves that one provider unconfigured
// (and therefore rejected by the generic webhook handler) until fixed.
func parseProviderSecrets(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(parts[0]))
		secret := strings.TrimSpace(parts[1])
		if provider == "" || secret == "" {
			continue
		}
		result[provider] = secret
	}
	return result
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return fallback
}

// parseReconcilerMinAge parses a duration string (e.g. "5m", "10m", "1h").
// Falls back to 5 minutes on parse error so a misconfigured value can never
// accidentally cause zero-age reconciliation (which would reconcile fresh txns).
func parseReconcilerMinAge(raw string) time.Duration {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("[Config] Invalid RECONCILER_MIN_AGE=%q, falling back to 5m", raw)
		return 5 * time.Minute
	}
	return d
}
