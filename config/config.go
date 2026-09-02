package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	AppPort     string
	AppEnv      string
	AppSecret   string
	FrontendURL string
	AdminURL    string

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

	// Digiflazz Seller (Our Open API H2H)
	DigiflazzSellerSecret string

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
		AppPort:     getEnv("APP_PORT", "8080"),
		AppEnv:      getEnv("APP_ENV", "development"),
		AppSecret:   getEnv("APP_SECRET", "super-secret-key-change-in-production-12345"),
		FrontendURL: getEnv("FRONTEND_URL", "http://localhost:3000"),
		AdminURL:    getEnv("ADMIN_URL", "http://localhost:3001"),

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

		DigiflazzSellerSecret: getEnv("DIGIFLAZZ_SELLER_SECRET", "h2h-seller-secret-999"),

		KiosgamerBaseURL: getEnv("KIOSGAMER_BASE_URL", "https://kiosgamer.co.id/api"),

		TripayAPIKey:       getEnv("TRIPAY_API_KEY", ""),
		TripayPrivateKey:   getEnv("TRIPAY_PRIVATE_KEY", ""),
		TripayMerchantCode: getEnv("TRIPAY_MERCHANT_CODE", ""),
		TripayBaseURL:      getEnv("TRIPAY_BASE_URL", "https://tripay.co.id/api-sandbox"),

		GenericWebhookSecrets: parseProviderSecrets(getEnv("GENERIC_WEBHOOK_SECRETS", "")),
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