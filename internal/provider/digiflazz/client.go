package digiflazz

import (
	"bytes"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"log"

	"topup-backend/config"
	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
)

type PriceListItem struct {
	ProductName         string  `json:"product_name"`
	Category            string  `json:"category"`
	Brand               string  `json:"brand"`
	Type                string  `json:"type"`
	SellerName          string  `json:"seller_name"`
	Price               float64 `json:"price"`
	BuyerSkuCode        string  `json:"buyer_sku_code"`
	BuyerProductStatus  bool    `json:"buyer_product_status"`
	SellerProductStatus bool    `json:"seller_product_status"`
	UnlimitedStock      bool    `json:"unlimited_stock"`
	Stock               int     `json:"stock"`
	Multi               bool    `json:"multi"`
	StartCutOff         string  `json:"start_cut_off"`
	EndCutOff           string  `json:"end_cut_off"`
	Desc                string  `json:"desc"`
}

type TransactionResponse struct {
	Data struct {
		RefID        string  `json:"ref_id"`
		CustomerNo   string  `json:"customer_no"`
		BuyerSkuCode string  `json:"buyer_sku_code"`
		Message      string  `json:"message"`
		Status       string  `json:"status"` // "Sukses", "Pending", "Gagal"
		RC           string  `json:"rc"`
		SN           string  `json:"sn"`
		Price        float64 `json:"price"`
		Tele         string  `json:"tele"`
		Wa           string  `json:"wa"`
	} `json:"data"`
	RawRequest  []byte `json:"-"`
	RawResponse []byte `json:"-"`
}

type APIError struct {
	Err         error
	RawRequest  []byte
	RawResponse []byte
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "digiflazz api error"
}

func (e *APIError) Unwrap() error {
	return e.Err
}

type CallbackPayload struct {
	Data struct {
		RefID        string  `json:"ref_id"`
		CustomerNo   string  `json:"customer_no"`
		BuyerSkuCode string  `json:"buyer_sku_code"`
		Message      string  `json:"message"`
		Status       string  `json:"status"`
		RC           string  `json:"rc"`
		SN           string  `json:"sn"`
		Price        float64 `json:"price"`
		Sign         string  `json:"sign"`
	} `json:"data"`
}

// Client adalah klien HTTP ke API Digiflazz (price list, saldo, transaksi, callback).
// Dipakai oleh Adapter (jalur transaksi generik) dan oleh katalog/saldo di admin.
type Client interface {
	GetPriceList() ([]PriceListItem, error)
	CheckBalance() (float64, error)
	CreateTransaction(refID, buyerSkuCode, customerNo string, testing bool) (*TransactionResponse, error)
	CheckTransactionStatus(providerOrderID, buyerSkuCode, customerNo string) (*TransactionResponse, error)
	ProcessCallback(rawPayload []byte, signatureHeader string) (*CallbackPayload, error)
}

type client struct {
	providerRepo repository.ProviderRepository
	cfg          *config.Config
	httpClient   *http.Client
}

func NewClient(providerRepo repository.ProviderRepository, cfg *config.Config) Client {
	return &client{
		providerRepo: providerRepo,
		cfg:          cfg,
		httpClient: &http.Client{
			Timeout: 35 * time.Second,
		},
	}
}

func (s *client) getCredentials() (baseURL, username, apiKey, webhookSecret string) {
	record, err := s.providerRepo.GetByCode("DIGIFLAZZ")
	if err != nil || record == nil {
		log.Printf("[Digiflazz] kredensial tidak ditemukan di database: %v", err)
		return
	}
	baseURL = strings.TrimRight(strings.TrimSpace(record.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.digiflazz.com/v1"
	}
	username = strings.TrimSpace(record.Username)
	apiKey = strings.TrimSpace(record.APIKey)
	webhookSecret = strings.TrimSpace(record.Secret)
	return
}

func (s *client) GetPriceList() ([]PriceListItem, error) {
	baseURL, username, apiKey, _ := s.getCredentials()
	if username == "" || apiKey == "" {
		return nil, errors.New("digiflazz buyer username and API key must be configured")
	}

	signature := crypto.MD5Hash(username + apiKey + "pricelist")

	payload := map[string]interface{}{
		"cmd":      "prepaid",
		"username": username,
		"sign":     signature,
	}

	jsonPayload, _ := json.Marshal(payload)
	resp, err := s.httpClient.Post(baseURL+"/price-list", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Digiflazz price list: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data []PriceListItem `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid response from Digiflazz: %s", string(body))
	}

	return result.Data, nil
}

func (s *client) CheckBalance() (float64, error) {
	baseURL, username, apiKey, _ := s.getCredentials()
	if username == "" || apiKey == "" {
		return 0, errors.New("digiflazz credentials not configured")
	}

	signature := crypto.MD5Hash(username + apiKey + "depo")

	payload := map[string]interface{}{
		"cmd":      "deposit",
		"username": username,
		"sign":     signature,
	}

	jsonPayload, _ := json.Marshal(payload)
	resp, err := s.httpClient.Post(baseURL+"/cek-saldo", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Data struct {
			Deposit float64 `json:"deposit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse balance: %s", string(body))
	}

	// Update provider balance in database
	provider, err := s.providerRepo.GetByCode("DIGIFLAZZ")
	if err == nil && provider != nil {
		_ = s.providerRepo.UpdateBalance(provider.ID, result.Data.Deposit)
	}

	return result.Data.Deposit, nil
}

func (s *client) CreateTransaction(providerOrderID, buyerSkuCode, customerNo string, testing bool) (*TransactionResponse, error) {
	baseURL, username, apiKey, _ := s.getCredentials()
	if username == "" || apiKey == "" {
		return nil, errors.New("digiflazz credentials not configured")
	}

	signature := crypto.MD5Hash(username + apiKey + providerOrderID)

	payload := map[string]interface{}{
		"username":       username,
		"buyer_sku_code": buyerSkuCode,
		"customer_no":    customerNo,
		"ref_id":         providerOrderID,
		"sign":           signature,
		"cb_url":         "https://api1235.irxplay.com/api/v1/callback/digiflazz",
	}
	if testing {
		payload["testing"] = true
	}

	jsonPayload, _ := json.Marshal(payload)

	// Log outgoing webhook/request
	_ = s.providerRepo.LogWebhook(&domain.WebhookLog{
		Direction:    domain.WebhookOutgoing,
		ProviderName: "DIGIFLAZZ_BUYER",
		URL:          baseURL + "/transaction",
		Payload:      string(jsonPayload),
		CreatedAt:    time.Now(),
	})

	resp, err := s.httpClient.Post(baseURL+"/transaction", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, &APIError{Err: fmt.Errorf("failed to connect to Digiflazz: %w", err), RawRequest: jsonPayload}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &APIError{Err: err, RawRequest: jsonPayload}
	}

	var result TransactionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, &APIError{Err: fmt.Errorf("invalid json response from Digiflazz: %s", string(body)), RawRequest: jsonPayload, RawResponse: body}
	}

	result.RawRequest = jsonPayload
	result.RawResponse = body
	return &result, nil
}

func (s *client) CheckTransactionStatus(providerOrderID, buyerSkuCode, customerNo string) (*TransactionResponse, error) {
	return s.CreateTransaction(providerOrderID, buyerSkuCode, customerNo, false)
}

func (s *client) ProcessCallback(rawPayload []byte, signatureHeader string) (*CallbackPayload, error) {
	_, username, apiKey, webhookSecret := s.getCredentials()

	// Log incoming webhook
	_ = s.providerRepo.LogWebhook(&domain.WebhookLog{
		Direction:    domain.WebhookIncoming,
		ProviderName: "DIGIFLAZZ_CALLBACK",
		Payload:      string(rawPayload),
		CreatedAt:    time.Now(),
	})

	var payload CallbackPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return nil, errors.New("invalid JSON payload")
	}

	refID := payload.Data.RefID
	if refID == "" {
		return nil, errors.New("missing ref_id in callback")
	}

	// -------------------------------------------------------------------------
	// Digiflazz Webhook Signature Verification
	// -------------------------------------------------------------------------
	// 1. If webhookSecret is configured, verify X-Hub-Signature header (HMAC-SHA1)
	if webhookSecret != "" && signatureHeader != "" {
		expectedMac := "sha1=" + crypto.HMACSHA1(webhookSecret, rawPayload)
		trimmedHeader := strings.TrimSpace(signatureHeader)
		if !strings.HasPrefix(trimmedHeader, "sha1=") {
			trimmedHeader = "sha1=" + trimmedHeader
		}

		if !hmac.Equal([]byte(strings.ToLower(trimmedHeader)), []byte(strings.ToLower(expectedMac))) {
			// Also check if payload contains MD5 signature as fallback
			expectedMD5 := crypto.MD5Hash(username + apiKey + refID)
			if payload.Data.Sign == "" || !strings.EqualFold(payload.Data.Sign, expectedMD5) {
				return nil, errors.New("invalid Digiflazz callback HMAC signature")
			}
		}
	} else if payload.Data.Sign != "" {
		// 2. If payload contains MD5 sign: md5(username + apiKey + ref_id)
		expectedMD5 := crypto.MD5Hash(username + apiKey + refID)
		if !strings.EqualFold(payload.Data.Sign, expectedMD5) {
			return nil, errors.New("invalid Digiflazz callback MD5 signature")
		}
	}

	return &payload, nil
}