package excellentstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

const (
	Code    = "EXCELLENTSTORE"
	BaseURL = "https://api.excellentstore.id/api/v1"
)

type Adapter struct {
	providers       repository.ProviderRepository
	httpClient      *http.Client
	callbackBaseURL string
}

func New(providers repository.ProviderRepository, clients ...*http.Client) *Adapter {
	return newAdapter(providers, "", clients...)
}

// NewWithCallbackBaseURL configures the generic callback endpoint sent with
// new orders. New remains available for callers that configure the webhook at
// ExcellentStore directly.
func NewWithCallbackBaseURL(providers repository.ProviderRepository, callbackBaseURL string, clients ...*http.Client) *Adapter {
	return newAdapter(providers, callbackBaseURL, clients...)
}

func newAdapter(providers repository.ProviderRepository, callbackBaseURL string, clients ...*http.Client) *Adapter {
	client := &http.Client{Timeout: 20 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &Adapter{
		providers:       providers,
		httpClient:      client,
		callbackBaseURL: strings.TrimRight(strings.TrimSpace(callbackBaseURL), "/"),
	}
}

func (a *Adapter) Code() string {
	return Code
}

type apiEnvelope[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type orderData struct {
	OrderID     string `json:"order_id"`
	ReferenceID string `json:"reference_id"`
	Status      string `json:"status"`
	Product     struct {
		SKU      string `json:"sku"`
		Name     string `json:"name"`
		Category string `json:"category"`
	} `json:"product"`
	Pricing struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"pricing"`
	Fulfillment struct {
		SerialNumber  *string `json:"serial_number"`
		FailureReason *string `json:"failure_reason"`
	} `json:"fulfillment"`
}

type productResponse struct {
	Categories []struct {
		Category struct {
			Code             string `json:"code"`
			Name             string `json:"name"`
			RequiresServerID bool   `json:"requires_server_id"`
		} `json:"category"`
		Items []struct {
			SKU      string  `json:"sku"`
			Name     string  `json:"name"`
			Price    float64 `json:"price"`
			IsActive bool    `json:"is_active"`
		} `json:"items"`
	} `json:"categories"`
}

type accountResponse struct {
	Balance float64 `json:"balance"`
}

type createOrderRequest struct {
	ProductSKU  string          `json:"product_sku"`
	Customer    customerRequest `json:"customer"`
	ReferenceID string          `json:"reference_id"`
	CallbackURL string          `json:"callback_url,omitempty"`
}

type customerRequest struct {
	UserID   string `json:"user_id"`
	ServerID string `json:"server_id,omitempty"`
}

type callbackPayload struct {
	Event      string    `json:"event"`
	DeliveryID string    `json:"delivery_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       orderData `json:"data"`
}

type apiCredentials struct {
	BaseURL string
	APIKey  string
	Secret  string
}

func (a *Adapter) credentials() (*apiCredentials, error) {
	if a.providers == nil {
		return nil, errors.New("ExcellentStore: provider repository belum dikonfigurasi")
	}

	record, err := a.providers.GetByCode(Code)
	if err != nil || record == nil {
		return nil, errors.New("ExcellentStore: provider belum terdaftar di database")
	}
	if !record.IsActive {
		return nil, errors.New("ExcellentStore: provider tidak aktif")
	}
	if strings.TrimSpace(record.APIKey) == "" {
		return nil, errors.New("ExcellentStore: API Key belum dikonfigurasi")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(record.BaseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ExcellentStore: BaseURL harus berupa URL HTTPS yang valid")
	}

	// BaseURL boleh berupa root API atau URL dasar yang diakhiri /api/v1.
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/api/v1") {
		baseURL += "/api/v1"
	}

	return &apiCredentials{
		BaseURL: baseURL,
		APIKey:  strings.TrimSpace(record.APIKey),
		Secret:  strings.TrimSpace(record.Secret),
	}, nil
}

func (a *Adapter) request(
	ctx context.Context,
	creds *apiCredentials,
	method, path string,
	body any,
) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("ExcellentStore: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		method,
		creds.BaseURL+path,
		reader,
	)
	if err != nil {
		return nil, fmt.Errorf("ExcellentStore: buat request: %w", err)
	}

	req.Header.Set("X-API-Key", creds.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ExcellentStore: request gagal: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("ExcellentStore: baca response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Hindari menampilkan header autentikasi atau kredensial.
		return nil, fmt.Errorf(
			"ExcellentStore: HTTP %d dari provider",
			resp.StatusCode,
		)
	}

	return raw, nil
}

func decodeEnvelope[T any](raw []byte) (T, error) {
	var envelope apiEnvelope[T]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		var zero T
		return zero, fmt.Errorf("ExcellentStore: response JSON tidak valid: %w", err)
	}
	if !envelope.Success {
		var zero T
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = "provider mengembalikan success=false"
		}
		return zero, fmt.Errorf("ExcellentStore: %s", message)
	}
	return envelope.Data, nil
}

func (a *Adapter) Purchase(
	ctx context.Context,
	request provider.PurchaseRequest,
) (*provider.Result, error) {
	if strings.TrimSpace(request.ExistingProviderOrderID) != "" &&
		request.ExistingProviderOrderID != "-" {
		return a.CheckStatus(ctx, provider.StatusRequest{
			RefID:           request.RefID,
			ProductCode:     request.ProductCode,
			CustomerID:      request.CustomerID,
			ServerID:        request.ServerID,
			GameSlug:        request.GameSlug,
			ProviderOrderID: request.ExistingProviderOrderID,
			ProviderData:    request.ProviderData,
		})
	}

	creds, err := a.credentials()
	if err != nil {
		return nil, configurationError(err)
	}

	sku := strings.TrimSpace(request.ProductCode)
	refID := strings.TrimSpace(request.RefID)
	customerID := strings.TrimSpace(request.CustomerID)

	if sku == "" || refID == "" || customerID == "" {
		return nil, configurationError(errors.New("ExcellentStore: SKU, reference ID, dan customer ID wajib diisi"))
	}

	payload := createOrderRequest{
		ProductSKU: sku,
		Customer: customerRequest{
			UserID:   customerID,
			ServerID: strings.TrimSpace(request.ServerID),
		},
		ReferenceID: refID,
		CallbackURL: a.callbackURL(request.CallbackURL),
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/orders", payload)
	if err != nil {
		return nil, temporaryError(err)
	}

	order, err := decodeEnvelope[orderData](raw)
	if err != nil {
		return nil, temporaryError(err)
	}
	if strings.TrimSpace(order.OrderID) == "" {
		return nil, temporaryError(errors.New("ExcellentStore: response create order tidak memiliki order_id"))
	}

	result := resultFromOrder(order, raw)
	result.RequestRaw, _ = json.Marshal(map[string]any{
		"product_sku":  sku,
		"customer":     payload.Customer,
		"reference_id": refID,
		"callback_url": payload.CallbackURL,
	})
	result.Raw = raw
	return result, nil
}

func (a *Adapter) callbackURL(requestURL string) string {
	if value := strings.TrimSpace(requestURL); value != "" {
		return value
	}
	if a.callbackBaseURL == "" {
		return ""
	}
	return a.callbackBaseURL + "/api/callback/provider/" + Code
}

func (a *Adapter) CheckStatus(
	ctx context.Context,
	request provider.StatusRequest,
) (*provider.Result, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, configurationError(err)
	}

	orderID := strings.TrimSpace(request.ProviderOrderID)
	if orderID == "" {
		return nil, configurationError(errors.New("ExcellentStore: ProviderOrderID diperlukan untuk cek status"))
	}

	raw, err := a.request(
		ctx,
		creds,
		http.MethodGet,
		"/orders/"+url.PathEscape(orderID),
		nil,
	)
	if err != nil {
		return nil, temporaryError(err)
	}

	order, err := decodeEnvelope[orderData](raw)
	if err != nil {
		return nil, temporaryError(err)
	}

	result := resultFromOrder(order, raw)
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"action":            "check_status",
		"provider_order_id": orderID,
	})
	result.Raw = raw
	return result, nil
}

func configurationError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorConfiguration,
		ProviderStatus: "Konfigurasi Error", Message: err.Error(), Cause: err,
	}
}

// An unavailable or malformed upstream response is deliberately held instead
// of refunded. A later callback/status check can safely settle the order.
func temporaryError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary,
		ProviderStatus: "Provider Pending", Message: err.Error(), Cause: err,
	}
}

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	creds, err := a.credentials()
	if err != nil {
		return 0, err
	}

	raw, err := a.request(ctx, creds, http.MethodGet, "/account/me", nil)
	if err != nil {
		return 0, err
	}

	account, err := decodeEnvelope[accountResponse](raw)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(account.Balance) || math.IsInf(account.Balance, 0) {
		return 0, errors.New("ExcellentStore: saldo tidak valid")
	}
	return account.Balance, nil
}

func (a *Adapter) SyncCatalog(ctx context.Context) ([]provider.CatalogItem, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, err
	}

	raw, err := a.request(ctx, creds, http.MethodGet, "/products/all", nil)
	if err != nil {
		return nil, err
	}

	catalog, err := decodeEnvelope[productResponse](raw)
	if err != nil {
		return nil, err
	}

	items := make([]provider.CatalogItem, 0)
	for _, category := range catalog.Categories {
		for _, item := range category.Items {
			sku := strings.TrimSpace(item.SKU)
			if sku == "" {
				continue
			}
			items = append(items, provider.CatalogItem{
				ProductCode: sku,
				Name:        strings.TrimSpace(item.Name),
				BasePrice:   item.Price,
				IsActive:    item.IsActive,
			})
		}
	}

	if len(items) == 0 {
		return nil, errors.New("ExcellentStore: katalog tidak berisi produk")
	}
	return items, nil
}

func resultFromOrder(order orderData, raw []byte) *provider.Result {
	status := strings.ToLower(strings.TrimSpace(order.Status))
	result := &provider.Result{
		ProviderOrderID: strings.TrimSpace(order.OrderID),
		ProviderStatus:  status,
		Message:         "ExcellentStore: status " + status,
		Raw:             raw,
	}

	if order.Fulfillment.SerialNumber != nil {
		result.SN = strings.TrimSpace(*order.Fulfillment.SerialNumber)
	}
	result.UpdateSN = result.SN != ""

	switch status {
	case "success", "successful", "completed":
		result.Status = provider.StatusSuccess
		result.StatusReason = "ExcellentStore: transaksi sukses"
		result.IncludeSNInSuccessEvent = true
	case "pending", "processing", "created":
		result.Status = provider.StatusPending
		result.StatusReason = "ExcellentStore: transaksi sedang diproses"
	case "failed", "refunded", "cancelled", "canceled":
		result.Status = provider.StatusFailedFinal
		result.StatusReason = "ExcellentStore: transaksi gagal final"
		if order.Fulfillment.FailureReason != nil &&
			strings.TrimSpace(*order.Fulfillment.FailureReason) != "" {
			result.RefundReason = strings.TrimSpace(*order.Fulfillment.FailureReason)
		}
	default:
		// Status baru/tidak dikenal tidak boleh menyebabkan refund otomatis.
		result.Status = provider.StatusFailedHold
		result.StatusReason = "ExcellentStore: status belum dikenali, perlu pemeriksaan"
	}

	return result
}

// ParseCallback memverifikasi HMAC-SHA256 atas raw body.
// X-Signature harus berbentuk sha256=<hex>.
func (a *Adapter) ParseCallback(
	req *http.Request,
) (*provider.Result, string, error) {
	if req == nil {
		return nil, "", errors.New("ExcellentStore: callback request kosong")
	}

	creds, err := a.credentials()
	if err != nil {
		return nil, "", err
	}
	if creds.Secret == "" {
		return nil, "", errors.New("ExcellentStore: webhook secret belum dikonfigurasi")
	}

	signature := strings.TrimSpace(req.Header.Get("X-Signature"))
	if !strings.HasPrefix(strings.ToLower(signature), "sha256=") {
		return nil, "", errors.New("ExcellentStore: signature callback tidak valid")
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, 2<<20))
	if err != nil {
		return nil, "", errors.New("ExcellentStore: gagal membaca callback body")
	}
	// Pertahankan body untuk handler lain jika diperlukan.
	req.Body = io.NopCloser(bytes.NewReader(body))

	providedSignature, err := hex.DecodeString(strings.TrimSpace(signature[len("sha256="):]))
	if err != nil {
		return nil, "", errors.New("ExcellentStore: format signature tidak valid")
	}

	mac := hmac.New(sha256.New, []byte(creds.Secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return nil, "", errors.New("ExcellentStore: signature callback tidak cocok")
	}

	var callback callbackPayload
	if err := json.Unmarshal(body, &callback); err != nil {
		return nil, "", fmt.Errorf("ExcellentStore: callback JSON tidak valid: %w", err)
	}
	if callback.Event != "order.status_updated" {
		return nil, "", fmt.Errorf("ExcellentStore: event callback tidak didukung")
	}
	if strings.TrimSpace(callback.DeliveryID) == "" ||
		strings.TrimSpace(callback.Data.ReferenceID) == "" ||
		strings.TrimSpace(callback.Data.OrderID) == "" {
		return nil, "", errors.New("ExcellentStore: callback tidak memiliki delivery_id, reference_id, atau order_id")
	}

	result := resultFromOrder(callback.Data, body)
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"event":       callback.Event,
		"delivery_id": callback.DeliveryID,
	})
	result.Raw = body

	// TransactionService mencocokkan nilai ini dengan RefID transaksi internal.
	return result, strings.TrimSpace(callback.Data.ReferenceID), nil
}

// Pastikan adapter memenuhi kontrak provider dan kapabilitas opsional.
var (
	_ provider.Provider       = (*Adapter)(nil)
	_ provider.CatalogSyncer  = (*Adapter)(nil)
	_ provider.CallbackParser = (*Adapter)(nil)
)

// Dipakai untuk memvalidasi field numerik bila nanti response API berubah.
var _ = strconv.ParseFloat
