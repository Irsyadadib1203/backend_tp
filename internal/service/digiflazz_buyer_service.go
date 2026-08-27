package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"topup-backend/config"
	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
)

type DigiflazzPriceListItem struct {
	ProductName        string  `json:"product_name"`
	Category           string  `json:"category"`
	Brand              string  `json:"brand"`
	Type               string  `json:"type"`
	SellerName         string  `json:"seller_name"`
	Price              float64 `json:"price"`
	BuyerSkuCode       string  `json:"buyer_sku_code"`
	BuyerProductStatus bool    `json:"buyer_product_status"`
	SellerProductStatus bool   `json:"seller_product_status"`
	UnlimitedStock     bool    `json:"unlimited_stock"`
	Stock              int     `json:"stock"`
	Multi              bool    `json:"multi"`
	StartCutOff        string  `json:"start_cut_off"`
	EndCutOff          string  `json:"end_cut_off"`
	Desc               string  `json:"desc"`
}

type DigiflazzTransactionResponse struct {
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
}

type DigiflazzCallbackPayload struct {
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

type DigiflazzBuyerService interface {
	GetPriceList() ([]DigiflazzPriceListItem, error)
	CheckBalance() (float64, error)
	CreateTransaction(refID, buyerSkuCode, customerNo string, testing bool) (*DigiflazzTransactionResponse, error)
	CheckTransactionStatus(refID, buyerSkuCode, customerNo string) (*DigiflazzTransactionResponse, error)
	ProcessCallback(rawPayload []byte, signatureHeader string) (*DigiflazzCallbackPayload, error)
}

type digiflazzBuyerService struct {
	providerRepo repository.ProviderRepository
	cfg          *config.Config
	httpClient   *http.Client
}

func NewDigiflazzBuyerService(providerRepo repository.ProviderRepository, cfg *config.Config) DigiflazzBuyerService {
	return &digiflazzBuyerService{
		providerRepo: providerRepo,
		cfg:          cfg,
		httpClient: &http.Client{
			Timeout: 35 * time.Second,
		},
	}
}

func (s *digiflazzBuyerService) getCredentials() (baseURL, username, apiKey string) {
	baseURL = s.cfg.DigiflazzBuyerBaseURL
	username = s.cfg.DigiflazzBuyerUsername
	apiKey = s.cfg.DigiflazzBuyerAPIKey

	provider, err := s.providerRepo.GetByCode("DIGIFLAZZ")
	if err == nil && provider != nil {
		if provider.BaseURL != "" {
			baseURL = provider.BaseURL
		}
		if provider.Username != "" {
			username = provider.Username
		}
		if provider.APIKey != "" {
			apiKey = provider.APIKey
		}
	}
	return
}

func (s *digiflazzBuyerService) GetPriceList() ([]DigiflazzPriceListItem, error) {
	baseURL, username, apiKey := s.getCredentials()
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
		Data []DigiflazzPriceListItem `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid response from Digiflazz: %s", string(body))
	}

	return result.Data, nil
}

func (s *digiflazzBuyerService) CheckBalance() (float64, error) {
	baseURL, username, apiKey := s.getCredentials()
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

func (s *digiflazzBuyerService) CreateTransaction(refID, buyerSkuCode, customerNo string, testing bool) (*DigiflazzTransactionResponse, error) {
	baseURL, username, apiKey := s.getCredentials()
	if username == "" || apiKey == "" {
		return nil, errors.New("digiflazz credentials not configured")
	}

	signature := crypto.MD5Hash(username + apiKey + refID)

	payload := map[string]interface{}{
		"username":       username,
		"buyer_sku_code": buyerSkuCode,
		"customer_no":    customerNo,
		"ref_id":         refID,
		"sign":           signature,
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
		return nil, fmt.Errorf("failed to connect to Digiflazz: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result DigiflazzTransactionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid json response from Digiflazz: %s", string(body))
	}

	return &result, nil
}

func (s *digiflazzBuyerService) CheckTransactionStatus(refID, buyerSkuCode, customerNo string) (*DigiflazzTransactionResponse, error) {
	return s.CreateTransaction(refID, buyerSkuCode, customerNo, false)
}

func (s *digiflazzBuyerService) ProcessCallback(rawPayload []byte, signatureHeader string) (*DigiflazzCallbackPayload, error) {
	_, username, apiKey := s.getCredentials()

	// Log incoming webhook
	_ = s.providerRepo.LogWebhook(&domain.WebhookLog{
		Direction:    domain.WebhookIncoming,
		ProviderName: "DIGIFLAZZ_CALLBACK",
		Payload:      string(rawPayload),
		CreatedAt:    time.Now(),
	})

	var payload DigiflazzCallbackPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return nil, errors.New("invalid JSON payload")
	}

	// Validate signature: md5(username + apiKey + ref_id)
	refID := payload.Data.RefID
	if refID == "" {
		return nil, errors.New("missing ref_id in callback")
	}

	expectedSign := crypto.MD5Hash(username + apiKey + refID)
	receivedSign := payload.Data.Sign
	if receivedSign == "" {
		receivedSign = signatureHeader
	}

	// A missing signature is NOT valid — it must always match, never be
	// skipped. Omitting the "sign" field (or the header) is exactly what an
	// attacker forging a callback would do, so treating "empty" as "trust it"
	// was a bypass, not a fallback.
	if receivedSign == "" || receivedSign != expectedSign {
		return nil, errors.New("invalid or missing Digiflazz callback signature")
	}

	return &payload, nil
}