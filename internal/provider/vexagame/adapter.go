package vexagame

import (
	"bytes"
	"context"
	"encoding/json"
	"crypto/subtle"
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
	Code    = "VEXAGAME"
	BaseURL = "https://api.vexaagen.com/v2"
)

// Slug kategori yang disinkronkan sekaligus oleh SyncCatalog.
var catalogSlugs = []string{"free-fire", "mobile-legends"}

// Status transaksi Vexagame (huruf kecil). Status di luar daftar ini TIDAK
// di-refund dan tidak dianggap sukses: ditahan di pending supaya admin bisa
// memeriksa. Tambahkan nilai baru di sini bila Vexagame punya status lain.
var (
	successStatuses = map[string]bool{
		"berhasil": true, "sukses": true, "success": true, "selesai": true, "completed": true,
	}
	pendingStatuses = map[string]bool{
		"dalam proses": true, "diproses": true, "proses": true, "pending": true,
		"processing": true, "menunggu": true, "menunggu pembayaran": true,
	}
	failedStatuses = map[string]bool{
		"gagal": true, "failed": true, "dibatalkan": true, "batal": true,
		"cancelled": true, "canceled": true, "refund": true, "dikembalikan": true,
	}
)

type Adapter struct {
	providers  repository.ProviderRepository
	httpClient *http.Client
}

func New(providers repository.ProviderRepository, clients ...*http.Client) *Adapter {
	client := &http.Client{Timeout: 20 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &Adapter{providers: providers, httpClient: client}
}

func (a *Adapter) Code() string {
	return Code
}


// ---------------------------------------------------------------------------
// Tipe request/response
// ---------------------------------------------------------------------------

// Semua endpoint memakai envelope {request, code, message, payload}.
type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Payload json.RawMessage `json:"payload"`
}

type transactionData struct {
	Code         string `json:"code"` // kode transaksi Vexagame (mis. TRX987654)
	CustomerNo   string `json:"customer_no"`
	SerialNumber string `json:"serial_number"`
	Status       string `json:"status"`
	Note         any    `json:"note"`
	PartnerRefID string `json:"partner_ref_id"`
}

type productItem struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	FullName string  `json:"full_name"`
	Status   string  `json:"status"`
	PriceRaw float64 `json:"price_raw"`
}

type createTransactionRequest struct {
	Code          string `json:"code"`
	CustomerNo    string `json:"customer_no"`
	PaymentMethod string `json:"payment_method"`
	Qty           int    `json:"qty"`
	PartnerRefID  string `json:"partner_ref_id"`
}

// httpError membawa status HTTP supaya pemanggil bisa membedakan 404.
type httpError struct {
	Status  int
	Message string
}

func (e *httpError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Vexagame: HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Vexagame: HTTP %d dari provider", e.Status)
}

// ---------------------------------------------------------------------------
// Kredensial & HTTP
// ---------------------------------------------------------------------------

type apiCredentials struct {
	BaseURL string
	APIKey  string
	CallbackToken string
}

func (a *Adapter) credentials() (*apiCredentials, error) {
	if a.providers == nil {
		return nil, errors.New("Vexagame: provider repository belum dikonfigurasi")
	}

	record, err := a.providers.GetByCode(Code)
	if err != nil || record == nil {
		return nil, errors.New("Vexagame: provider belum terdaftar di database")
	}
	if !record.IsActive {
		return nil, errors.New("Vexagame: provider tidak aktif")
	}
	apiKey := strings.TrimSpace(record.APIKey)
	if apiKey == "" {
		return nil, errors.New("Vexagame: API Key belum dikonfigurasi")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(record.BaseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Vexagame: BaseURL harus berupa URL HTTPS yang valid")
	}
	// BaseURL boleh root domain atau yang sudah berakhiran /v2.
	if !strings.HasSuffix(baseURL, "/v2") {
		baseURL += "/v2"
	}

	return &apiCredentials{
		BaseURL: baseURL,
		APIKey:  apiKey,
		CallbackToken: strings.TrimSpace(record.Username),
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
			return nil, fmt.Errorf("Vexagame: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, creds.BaseURL+path, reader)
	if err != nil {
		return nil, errors.New("Vexagame: gagal membuat request")
	}
	req.Header.Set("Authorization", creds.APIKey)
	req.Header.Set("Accept", "application/json")
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Vexagame: request gagal: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("Vexagame: baca response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpError{Status: resp.StatusCode, Message: errorMessage(raw)}
	}
	return raw, nil
}

// errorMessage mengambil field message dari body error (dipotong 200 karakter).
func errorMessage(raw []byte) string {
	var env apiEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return ""
	}
	msg := strings.TrimSpace(env.Message)
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200])
	}
	return msg
}

// payloadOf memeriksa envelope (code 2xx) lalu mengembalikan payload mentah.
func payloadOf(raw []byte) (json.RawMessage, error) {
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("Vexagame: response JSON tidak valid: %w", err)
	}
	if env.Code < 200 || env.Code >= 300 {
		msg := strings.TrimSpace(env.Message)
		if msg == "" {
			msg = fmt.Sprintf("provider mengembalikan code %d", env.Code)
		}
		return nil, fmt.Errorf("Vexagame: %s", msg)
	}
	return env.Payload, nil
}

// decodeTransaction menerima payload berupa object, atau array (ambil elemen pertama).
func decodeTransaction(raw []byte) (transactionData, error) {
	var zero transactionData
	payload, err := payloadOf(raw)
	if err != nil {
		return zero, err
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return zero, errors.New("Vexagame: payload transaksi kosong")
	}
	if trimmed[0] == '[' {
		var list []transactionData
		if err := json.Unmarshal(trimmed, &list); err != nil || len(list) == 0 {
			return zero, errors.New("Vexagame: payload transaksi tidak valid")
		}
		return list[0], nil
	}
	var order transactionData
	if err := json.Unmarshal(trimmed, &order); err != nil {
		return zero, fmt.Errorf("Vexagame: format transaksi tidak valid: %w", err)
	}
	return order, nil
}

// lookupTransaction mengambil transaksi lewat kode Vexagame ATAU partner_ref_id.
func (a *Adapter) lookupTransaction(
	ctx context.Context,
	creds *apiCredentials,
	identifier string,
) (transactionData, []byte, error) {
	raw, err := a.request(ctx, creds, http.MethodGet, "/transaction/"+url.PathEscape(identifier), nil)
	if err != nil {
		return transactionData{}, nil, err
	}
	order, err := decodeTransaction(raw)
	if err != nil {
		return transactionData{}, nil, err
	}
	return order, raw, nil
}

// ---------------------------------------------------------------------------
// Purchase
// ---------------------------------------------------------------------------

// customerNo menggabungkan ID pemain dan server/zone tanpa pemisah
// (contoh dokumentasi: "123456781234"). Ubah di sini bila Vexagame
// meminta format lain untuk game tertentu.
func customerNo(customerID, serverID string) string {
	return strings.TrimSpace(customerID) + strings.TrimSpace(serverID)
}

func (a *Adapter) Purchase(
	ctx context.Context,
	request provider.PurchaseRequest,
) (*provider.Result, error) {
	// Order sudah ada di provider: cukup cek status, jangan buat ulang.
	if existing := strings.TrimSpace(request.ExistingProviderOrderID); existing != "" && existing != "-" {
		return a.CheckStatus(ctx, provider.StatusRequest{
			RefID:           request.RefID,
			ProductCode:     request.ProductCode,
			CustomerID:      request.CustomerID,
			ServerID:        request.ServerID,
			GameSlug:        request.GameSlug,
			ProviderOrderID: existing,
			ProviderData:    request.ProviderData,
			Nickname:        request.Nickname,
			InvoiceNumber:   request.InvoiceNumber,
		})
	}

	creds, err := a.credentials()
	if err != nil {
		return nil, configurationError(err)
	}

	productCode := strings.TrimSpace(request.ProductCode)
	refID := strings.TrimSpace(request.RefID)
	customerID := strings.TrimSpace(request.CustomerID)
	if productCode == "" || refID == "" || customerID == "" {
		return nil, configurationError(errors.New("Vexagame: product code, reference ID, dan customer ID wajib diisi"))
	}

	payload := createTransactionRequest{
		Code:          productCode,
		CustomerNo:    customerNo(customerID, request.ServerID),
		PaymentMethod: "balance",
		Qty:           1,
		PartnerRefID:  refID,
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/transaction", payload)
	var order transactionData
	if err == nil {
		order, err = decodeTransaction(raw)
	}
	if err == nil && strings.TrimSpace(order.Code) == "" {
		err = errors.New("Vexagame: response transaksi tidak memiliki code")
	}
	if err != nil {
		// Create gagal atau responsnya hilang/ambigu. Transaksi mungkin sudah
		// terbentuk di Vexagame, jadi cari dulu lewat partner_ref_id sebelum
		// menyerah. Dengan begitu retry tidak membuat order ganda.
		if found, foundRaw, lookupErr := a.lookupTransaction(ctx, creds, refID); lookupErr == nil &&
			strings.TrimSpace(found.Code) != "" {
			order, raw, err = found, foundRaw, nil
		} else {
			return nil, temporaryError(err)
		}
	}

	result := resultFromOrder(order, raw)
	if result.Status == provider.StatusSuccess {
		result.SN = provider.BuildManualSN(request.Nickname, customerID, request.InvoiceNumber)
		result.UpdateSN = true
	}
	// API key sengaja tidak masuk ke log request.
	result.RequestRaw, _ = json.Marshal(map[string]any{
		"code":           payload.Code,
		"customer_no":    payload.CustomerNo,
		"payment_method": payload.PaymentMethod,
		"qty":            payload.Qty,
		"partner_ref_id": payload.PartnerRefID,
	})
	result.Raw = raw
	return result, nil
}

// ---------------------------------------------------------------------------
// CheckStatus
// ---------------------------------------------------------------------------

func (a *Adapter) CheckStatus(
	ctx context.Context,
	request provider.StatusRequest,
) (*provider.Result, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, configurationError(err)
	}

	// Endpoint menerima kode Vexagame atau partner_ref_id. Pakai kode bila ada,
	// kalau belum ada pakai RefID kita.
	identifier := strings.TrimSpace(request.ProviderOrderID)
	if identifier == "" || identifier == "-" {
		identifier = strings.TrimSpace(request.RefID)
	}
	if identifier == "" {
		return nil, configurationError(errors.New("Vexagame: kode transaksi atau reference ID diperlukan untuk cek status"))
	}

	order, raw, err := a.lookupTransaction(ctx, creds, identifier)
	if err != nil {
		return nil, temporaryError(err)
	}

	result := resultFromOrder(order, raw)
	if result.Status == provider.StatusSuccess {
		result.SN = provider.BuildManualSN(request.Nickname, request.CustomerID, request.InvoiceNumber)
		result.UpdateSN = true
	}
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"action":     "check_status",
		"identifier": identifier,
	})
	result.Raw = raw
	return result, nil
}

// ---------------------------------------------------------------------------
// Callback
// ---------------------------------------------------------------------------

type callbackPayload struct {
	Code         string `json:"code"`
	PartnerRefID string `json:"partner_ref_id"`
	Status       string `json:"status"`
	CustomerNo   string `json:"customer_no"`
	Description  string `json:"description"`
	SN           string `json:"sn"`
}

// bearerToken mengambil token dari "Authorization: Bearer <token>".
// Token tanpa awalan "Bearer" juga diterima.
func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return header
}

// ParseCallback memverifikasi Callback Token (header Authorization: Bearer ...)
// lalu mengubah payload menjadi Result. Token yang dipakai adalah callback token
// yang disimpan; bila kosong, dipakai API key (di contoh dokumentasi keduanya sama).
func (a *Adapter) ParseCallback(req *http.Request) (*provider.Result, string, error) {
	if req == nil {
		return nil, "", errors.New("Vexagame: callback request kosong")
	}
	creds, err := a.credentials()
	if err != nil {
		return nil, "", err
	}

	expected := creds.CallbackToken
	if expected == "" {
		expected = creds.APIKey
	}
	provided := bearerToken(req.Header.Get("Authorization"))
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return nil, "", errors.New("Vexagame: token callback tidak valid")
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, 2<<20))
	if err != nil {
		return nil, "", errors.New("Vexagame: gagal membaca callback body")
	}
	req.Body = io.NopCloser(bytes.NewReader(body))

	var cb callbackPayload
	if err := json.Unmarshal(body, &cb); err != nil {
		return nil, "", fmt.Errorf("Vexagame: callback JSON tidak valid: %w", err)
	}
	cb.Code = strings.TrimSpace(cb.Code)
	cb.PartnerRefID = strings.TrimSpace(cb.PartnerRefID)
	if cb.Code == "" && cb.PartnerRefID == "" {
		return nil, "", errors.New("Vexagame: callback tidak memiliki code atau partner_ref_id")
	}

	result := resultFromOrder(transactionData{
		Code:         cb.Code,
		CustomerNo:   cb.CustomerNo,
		SerialNumber: cb.SN,
		Status:       cb.Status,
		Note:         cb.Description, // dipakai sebagai alasan refund bila gagal
		PartnerRefID: cb.PartnerRefID,
	}, body)
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"event":          "callback",
		"code":           cb.Code,
		"partner_ref_id": cb.PartnerRefID,
	})
	result.Raw = body

	// Dikembalikan partner_ref_id: nilai ini selalu cocok dengan ProviderRefID
	// transaksi kita, bahkan bila callback tiba sebelum kode Vexagame tersimpan.
	identifier := cb.PartnerRefID
	if identifier == "" {
		identifier = cb.Code
	}
	return result, identifier, nil
}

// ---------------------------------------------------------------------------
// Balance & Catalog
// ---------------------------------------------------------------------------

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	creds, err := a.credentials()
	if err != nil {
		return 0, err
	}

	raw, err := a.request(ctx, creds, http.MethodGet, "/me", nil)
	if err != nil {
		return 0, err
	}
	payload, err := payloadOf(raw)
	if err != nil {
		return 0, err
	}

	var me struct {
		LatestBalance float64 `json:"latest_balance"`
	}
	if err := json.Unmarshal(payload, &me); err != nil {
		return 0, fmt.Errorf("Vexagame: format saldo tidak valid: %w", err)
	}
	if math.IsNaN(me.LatestBalance) || math.IsInf(me.LatestBalance, 0) {
		return 0, errors.New("Vexagame: saldo tidak valid")
	}
	return me.LatestBalance, nil
}

// SyncCatalog mengambil semua slug sekaligus. Bila salah satu slug gagal,
// seluruh sinkron dibatalkan: katalog parsial akan membuat produk game lain
// dianggap tidak ada dan harganya dikosongkan oleh PriceSyncService.
func (a *Adapter) SyncCatalog(ctx context.Context) ([]provider.CatalogItem, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, err
	}

	items := make([]provider.CatalogItem, 0)
	seen := make(map[string]bool)

	for _, slug := range catalogSlugs {
		raw, err := a.request(ctx, creds, http.MethodGet,
			"/product-item?product_slug="+url.QueryEscape(slug), nil)
		if err != nil {
			return nil, fmt.Errorf("Vexagame: katalog %s: %w", slug, err)
		}
		payload, err := payloadOf(raw)
		if err != nil {
			return nil, fmt.Errorf("Vexagame: katalog %s: %w", slug, err)
		}
		var products []productItem
		if err := json.Unmarshal(payload, &products); err != nil {
			return nil, fmt.Errorf("Vexagame: format katalog %s tidak valid: %w", slug, err)
		}
		if len(products) == 0 {
			return nil, fmt.Errorf("Vexagame: katalog %s kosong", slug)
		}

		for _, p := range products {
			code := strings.TrimSpace(p.Code)
			if code == "" || seen[code] {
				continue
			}
			seen[code] = true

			name := strings.TrimSpace(p.FullName)
			if name == "" {
				name = strings.TrimSpace(p.Name)
			}
			items = append(items, provider.CatalogItem{
				ProductCode: code,
				Name:        name,
				BasePrice:   p.PriceRaw,
				IsActive:    strings.EqualFold(strings.TrimSpace(p.Status), "active"),
			})
		}
	}

	if len(items) == 0 {
		return nil, errors.New("Vexagame: katalog tidak berisi produk")
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func noteText(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func resultFromOrder(order transactionData, raw []byte) *provider.Result {
	status := strings.ToLower(strings.TrimSpace(order.Status))
	result := &provider.Result{
		ProviderOrderID: strings.TrimSpace(order.Code),
		ProviderStatus:  status,
		Message:         "Vexagame: status " + status,
		Raw:             raw,
	}

	switch {
	case successStatuses[status]:
		result.Status = provider.StatusSuccess
		result.StatusReason = "Vexagame: transaksi sukses"
		result.IncludeSNInSuccessEvent = true
		if sn := strings.TrimSpace(order.SerialNumber); sn != "" && sn != "-" {
			result.SN = sn
			result.UpdateSN = true
		}
	case pendingStatuses[status]:
		result.Status = provider.StatusPending
		result.ProviderStatus = "processing"
		result.StatusReason = "Vexagame: transaksi sedang diproses"
	case failedStatuses[status]:
		result.Status = provider.StatusFailedFinal
		result.StatusReason = "Vexagame: transaksi gagal final"
		if reason := noteText(order.Note); reason != "" {
			result.RefundReason = reason
		}
	default:
		// Status anomali: tidak di-refund dan tidak dianggap sukses.
		result.Status = provider.StatusPending
		result.ProviderStatus = "pending"
		result.Message = "Vexagame: status tidak dikenal (" + status + "), ditahan di pending"
		result.StatusReason = "Vexagame: status anomali, ditahan di pending"
	}

	return result
}

func configurationError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorConfiguration,
		ProviderStatus: "Konfigurasi Error", Message: err.Error(), Cause: err,
	}
}

// Response upstream yang gagal/tidak valid sengaja ditahan (hold), bukan
// di-refund, supaya cek status berikutnya bisa menyelesaikan order dengan aman.
func temporaryError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary,
		ProviderStatus: "Provider Pending", Message: err.Error(), Cause: err,
	}
}

// Pastikan adapter memenuhi kontrak provider dan kapabilitas opsional.
// (CallbackParser belum diimplementasikan: format payload callback belum ada.)
var (
	_ provider.Provider        = (*Adapter)(nil)
	_ provider.CatalogSyncer   = (*Adapter)(nil)
	_ provider.CallbackParser = (*Adapter)(nil)
)