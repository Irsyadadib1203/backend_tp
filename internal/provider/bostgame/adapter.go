package bostgame

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
	"strings"
	"time"

	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

const (
	Code    = "BOSTGAME"
	BaseURL = "https://api.bostgame.com"
)

type Adapter struct {
	providers       repository.ProviderRepository
	httpClient      *http.Client
	callbackBaseURL string
}

func New(providers repository.ProviderRepository, clients ...*http.Client) *Adapter {
	return newAdapter(providers, "", clients...)
}

// NewWithCallbackBaseURL mengisi callback_url otomatis saat membuat order.
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

// ---------- tipe data ----------

type apiEnvelope[T any] struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Data       T      `json:"data"`
}

type orderData struct {
	InvoiceNumber string  `json:"invoice_number"`
	TrxID         *string `json:"trx_id"`
	ResponseNote  string  `json:"response_note"`
	Product       struct {
		Name  string  `json:"name"`
		Code  string  `json:"code"`
		Price float64 `json:"price"`
	} `json:"product"`
	UserInput struct {
		UserID   string `json:"user_id"`
		ServerID string `json:"server_id"`
		Nickname string `json:"nickname"`
	} `json:"user_input"`
	Amount    float64         `json:"amount"`
	Status    string          `json:"status"`
	Voucher   json.RawMessage `json:"voucher"`
	CreatedAt int64           `json:"created_at"`
}

type productItem struct {
	ProductID    string  `json:"product_id"`
	ProductName  string  `json:"product_name"`
	ProductCode  string  `json:"product_code"`
	ProductPrice float64 `json:"product_price"`
	IsActive     bool    `json:"is_active"`
}

type userData struct {
	UserBalance float64 `json:"user_balance"`
}

type createOrderRequest struct {
	ProductCode string `json:"product_code"`
	UserID      string `json:"user_id"`
	ServerID    string `json:"server_id,omitempty"`
	TrxID       string `json:"trx_id"`
	CallbackURL string `json:"callback_url,omitempty"`
}

type apiCredentials struct {
	BaseURL string
	APIKey  string
}

// ---------- kredensial & HTTP ----------

func (a *Adapter) credentials() (*apiCredentials, error) {
	if a.providers == nil {
		return nil, errors.New("BostGame: provider repository belum dikonfigurasi")
	}

	record, err := a.providers.GetByCode(Code)
	if err != nil || record == nil {
		return nil, errors.New("BostGame: provider belum terdaftar di database")
	}
	if !record.IsActive {
		return nil, errors.New("BostGame: provider tidak aktif")
	}
	if strings.TrimSpace(record.APIKey) == "" {
		return nil, errors.New("BostGame: API Key belum dikonfigurasi")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(record.BaseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("BostGame: BaseURL harus berupa URL HTTPS yang valid")
	}

	// BaseURL boleh root domain atau yang sudah diakhiri /v1.
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}

	return &apiCredentials{
		BaseURL: baseURL,
		APIKey:  strings.TrimSpace(record.APIKey),
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
			return nil, fmt.Errorf("BostGame: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, creds.BaseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("BostGame: buat request: %w", err)
	}

	// BostGame memakai API key mentah di header Authorization (tanpa "Bearer").
	req.Header.Set("Authorization", creds.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("BostGame: request gagal: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("BostGame: baca response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Jangan menyertakan header autentikasi; message provider aman ditampilkan.
		var env apiEnvelope[json.RawMessage]
		if json.Unmarshal(raw, &env) == nil && strings.TrimSpace(env.Message) != "" {
			return nil, fmt.Errorf("BostGame: HTTP %d dari provider (%s)", resp.StatusCode, truncate(env.Message, 120))
		}
		return nil, fmt.Errorf("BostGame: HTTP %d dari provider", resp.StatusCode)
	}

	return raw, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func decodeEnvelope[T any](raw []byte) (T, error) {
	var envelope apiEnvelope[T]
	var zero T
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return zero, fmt.Errorf("BostGame: response JSON tidak valid: %w", err)
	}
	if envelope.StatusCode != 0 && envelope.StatusCode != http.StatusOK {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = fmt.Sprintf("provider mengembalikan statusCode %d", envelope.StatusCode)
		}
		return zero, fmt.Errorf("BostGame: %s", message)
	}
	return envelope.Data, nil
}

// ---------- Purchase ----------

func (a *Adapter) Purchase(
	ctx context.Context,
	request provider.PurchaseRequest,
) (*provider.Result, error) {
	// Retry dengan order yang sudah ada: cek status, jangan buat order baru.
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
			Nickname:        request.Nickname,
			InvoiceNumber:   request.InvoiceNumber,
		})
	}

	creds, err := a.credentials()
	if err != nil {
		return nil, configurationError(err)
	}

	code := strings.TrimSpace(request.ProductCode)
	refID := strings.TrimSpace(request.RefID)
	customerID := strings.TrimSpace(request.CustomerID)

	if code == "" || refID == "" || customerID == "" {
		return nil, configurationError(errors.New("BostGame: product code, reference ID, dan customer ID wajib diisi"))
	}

	payload := createOrderRequest{
		ProductCode: code,
		UserID:      customerID,
		ServerID:    strings.TrimSpace(request.ServerID),
		TrxID:       refID,
		CallbackURL: a.callbackURL(request.CallbackURL),
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/order", payload)
	if err != nil {
		return nil, temporaryError(err)
	}

	order, err := decodeEnvelope[orderData](raw)
	if err != nil {
		return nil, temporaryError(err)
	}
	if strings.TrimSpace(order.InvoiceNumber) == "" {
		return nil, temporaryError(errors.New("BostGame: response order tidak memiliki invoice_number"))
	}

	result := resultFromOrder(order, raw)
	applySuccessSN(result, order, request.Nickname, customerID, request.InvoiceNumber)
	result.RequestRaw, _ = json.Marshal(payload)
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

// ---------- CheckStatus ----------

func (a *Adapter) CheckStatus(
	ctx context.Context,
	request provider.StatusRequest,
) (*provider.Result, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, configurationError(err)
	}

	invoice := strings.TrimSpace(request.ProviderOrderID)
	if invoice == "" || invoice == "-" {
		// BostGame hanya menyediakan cek status via invoice_number, bukan via trx_id.
		return nil, temporaryError(errors.New("BostGame: invoice_number (provider order id) belum ada untuk cek status"))
	}

	order, raw, err := a.fetchOrder(ctx, creds, invoice)
	if err != nil {
		return nil, temporaryError(err)
	}

	result := resultFromOrder(order, raw)
	applySuccessSN(result, order, request.Nickname, request.CustomerID, request.InvoiceNumber)
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"action":         "check_status",
		"invoice_number": invoice,
	})
	result.Raw = raw
	return result, nil
}

func (a *Adapter) fetchOrder(ctx context.Context, creds *apiCredentials, invoice string) (orderData, []byte, error) {
	raw, err := a.request(ctx, creds, http.MethodGet, "/status/"+url.PathEscape(invoice), nil)
	if err != nil {
		return orderData{}, nil, err
	}
	order, err := decodeEnvelope[orderData](raw)
	if err != nil {
		return orderData{}, nil, err
	}
	if strings.TrimSpace(order.InvoiceNumber) == "" {
		return orderData{}, nil, errors.New("BostGame: response status tidak memiliki invoice_number")
	}
	return order, raw, nil
}

// ---------- error helpers ----------

func configurationError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorConfiguration,
		ProviderStatus: "Konfigurasi Error", Message: err.Error(), Cause: err,
	}
}

// Upstream tidak tersedia / response rusak ditahan (hold), bukan di-refund.
// Callback atau cek status berikutnya yang akan menyelesaikan order.
func temporaryError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary,
		ProviderStatus: "Provider Pending", Message: err.Error(), Cause: err,
	}
}

// ---------- Balance & katalog ----------

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	creds, err := a.credentials()
	if err != nil {
		return 0, err
	}

	raw, err := a.request(ctx, creds, http.MethodGet, "/user", nil)
	if err != nil {
		return 0, err
	}

	user, err := decodeEnvelope[userData](raw)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(user.UserBalance) || math.IsInf(user.UserBalance, 0) {
		return 0, errors.New("BostGame: saldo tidak valid")
	}
	return user.UserBalance, nil
}

func (a *Adapter) SyncCatalog(ctx context.Context) ([]provider.CatalogItem, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, err
	}

	raw, err := a.request(ctx, creds, http.MethodGet, "/products", nil)
	if err != nil {
		return nil, err
	}

	products, err := decodeEnvelope[[]productItem](raw)
	if err != nil {
		return nil, err
	}

	items := make([]provider.CatalogItem, 0, len(products))
	for _, item := range products {
		code := strings.TrimSpace(item.ProductCode)
		if code == "" {
			continue
		}
		items = append(items, provider.CatalogItem{
			ProductCode: code,
			Name:        strings.TrimSpace(item.ProductName),
			BasePrice:   item.ProductPrice,
			IsActive:    item.IsActive,
		})
	}

	if len(items) == 0 {
		return nil, errors.New("BostGame: katalog tidak berisi produk")
	}
	return items, nil
}

// ---------- mapping status ----------

func voucherText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(trimmed, &s) == nil {
		return strings.TrimSpace(s)
	}
	return string(trimmed)
}

func resultFromOrder(order orderData, raw []byte) *provider.Result {
	status := strings.ToUpper(strings.TrimSpace(order.Status))
	result := &provider.Result{
		ProviderOrderID: strings.TrimSpace(order.InvoiceNumber),
		ProviderStatus:  strings.ToLower(status),
		Message:         "BostGame: status " + strings.ToLower(status),
		Raw:             raw,
	}

	result.SN = voucherText(order.Voucher)
	result.UpdateSN = result.SN != ""

	switch status {
	case "SUCCESS":
		result.Status = provider.StatusSuccess
		result.StatusReason = "BostGame: transaksi sukses"
		result.IncludeSNInSuccessEvent = true
	case "PENDING", "PAID":
		// PAID = sudah dibayar ke provider, masih menunggu diproses.
		result.Status = provider.StatusPending
		result.ProviderStatus = "processing"
		result.StatusReason = "BostGame: transaksi sedang diproses"
	case "REFUNDED", "FAILED":
		result.Status = provider.StatusFailedFinal
		result.StatusReason = "BostGame: transaksi gagal final"
		result.RefundReason = "BostGame: " + strings.ToLower(status)
	case "PARTIAL_SUCCESS":
		// Sebagian terkirim: tidak dianggap sukses dan tidak di-refund, ditahan di pending.
		result.Status = provider.StatusPending
		result.ProviderStatus = "pending"
		result.Message = "BostGame: sukses sebagian, ditahan di pending"
		result.StatusReason = "BostGame: partial success, ditahan di pending"
	default:
		// Status anomali: tidak di-refund dan tidak dianggap sukses.
		result.Status = provider.StatusPending
		result.ProviderStatus = "pending"
		result.Message = "BostGame: status tidak dikenal (" + status + "), ditahan di pending"
		result.StatusReason = "BostGame: status anomali, ditahan di pending"
	}

	return result
}

// Untuk produk game: SN manual (nickname/ID/invoice). Untuk produk voucher
// yang mengembalikan kode di field voucher, kode itu tidak ditimpa.
func applySuccessSN(result *provider.Result, order orderData, nickname, customerID, invoiceNumber string) {
	if result.Status != provider.StatusSuccess || result.SN != "" {
		return
	}
	if strings.TrimSpace(nickname) == "" {
		nickname = order.UserInput.Nickname
	}
	result.SN = provider.BuildManualSN(nickname, customerID, invoiceNumber)
	result.UpdateSN = true
}

// ---------- callback ----------

// ParseCallback memverifikasi header X-Signature: HMAC-SHA256 atas raw body
// dengan API key sebagai secret. Nilai boleh berupa hex saja atau "sha256=<hex>".
func (a *Adapter) ParseCallback(
	req *http.Request,
) (*provider.Result, string, error) {
	if req == nil {
		return nil, "", errors.New("BostGame: callback request kosong")
	}

	creds, err := a.credentials()
	if err != nil {
		return nil, "", err
	}

	signature := strings.TrimSpace(req.Header.Get("X-Signature"))
	if len(signature) >= len("sha256=") && strings.EqualFold(signature[:len("sha256=")], "sha256=") {
		signature = signature[len("sha256="):]
	}
	if signature == "" {
		return nil, "", errors.New("BostGame: signature callback tidak ada")
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, 2<<20))
	if err != nil {
		return nil, "", errors.New("BostGame: gagal membaca callback body")
	}
	// Pertahankan body untuk handler lain jika diperlukan.
	req.Body = io.NopCloser(bytes.NewReader(body))

	providedSignature, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return nil, "", errors.New("BostGame: format signature tidak valid")
	}

	if !signatureMatches(body, providedSignature, []byte(creds.APIKey)) {
		return nil, "", errors.New("BostGame: signature callback tidak cocok")
	}

	// Callback berupa objek order langsung (tanpa envelope statusCode/data).
	var callback orderData
	if err := json.Unmarshal(body, &callback); err != nil {
		return nil, "", fmt.Errorf("BostGame: callback JSON tidak valid: %w", err)
	}
	invoice := strings.TrimSpace(callback.InvoiceNumber)
	if invoice == "" || strings.TrimSpace(callback.Status) == "" {
		return nil, "", errors.New("BostGame: callback tidak memiliki invoice_number atau status")
	}

	result := resultFromOrder(callback, body)
	applySuccessSN(result, callback, callback.UserInput.Nickname, callback.UserInput.UserID, "")
	result.RequestRaw = body
	result.Raw = body

	return result, invoice, nil
}
// signatureMatches mencocokkan HMAC-SHA256 atas raw body, lalu atas versi compact-nya
// (setara JSON.stringify di sisi provider).
func signatureMatches(body, provided, secret []byte) bool {
	candidates := [][]byte{body}
	var compact bytes.Buffer
	if json.Compact(&compact, body) == nil && !bytes.Equal(compact.Bytes(), body) {
		candidates = append(candidates, compact.Bytes())
	}
	for _, candidate := range candidates {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(candidate)
		if hmac.Equal(provided, mac.Sum(nil)) {
			return true
		}
	}
	return false
}

// Pastikan adapter memenuhi kontrak provider dan kapabilitas opsional.
var (
	_ provider.Provider       = (*Adapter)(nil)
	_ provider.CatalogSyncer  = (*Adapter)(nil)
	_ provider.CallbackParser = (*Adapter)(nil)
)