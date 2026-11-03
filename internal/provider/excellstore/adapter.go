package excellstore

import (
	"context"
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
	Code = "EXCELLSTORE"
	// BaseURL sudah termasuk prefix /api, jadi path endpoint cukup "/order", dst.
	BaseURL = "https://excellstore.id/api"

	// ExcellStore mewajibkan field "kontak" (nomor HP, awali 62). Request internal
	// belum membawa nomor pelanggan, jadi dipakai nomor default ini.
	// GANTI dengan nomor milik toko Anda.
	defaultContact = "628000000000"
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
// Tipe response
// ---------------------------------------------------------------------------

// flexFloat menerima angka JSON maupun string angka ("10000"), karena
// ExcellStore mengirim harga dan saldo sebagai string.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	s = strings.TrimSpace(strings.Trim(s, `"`))
	if s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("angka tidak valid: %q", s)
	}
	*f = flexFloat(v)
	return nil
}

// Semua endpoint memakai envelope {status, msg, data}. Data dibiarkan raw
// karena bentuknya berbeda-beda (object / array / string) dan bisa kosong saat gagal.
type apiEnvelope struct {
	Status bool            `json:"status"`
	Msg    string          `json:"msg"`
	Data   json.RawMessage `json:"data"`
}

type orderData struct {
	ID          string `json:"id"` // order ID ExcellStore
	ServiceName string `json:"service_name"`
	ServiceID   string `json:"service_id"`
	Target      string `json:"target"`
	Kontak      string `json:"kontak"`
	Keterangan  string `json:"keterangan"` // SN / catatan
	Status      string `json:"status"`
}

type serviceItem struct {
	ID          string    `json:"id"`
	NamaLayanan string    `json:"nama_layanan"`
	Kategori    string    `json:"kategori"`
	Harga       flexFloat `json:"harga"`
	Status      string    `json:"status"`
}

// ---------------------------------------------------------------------------
// Kredensial & HTTP
// ---------------------------------------------------------------------------

type apiCredentials struct {
	BaseURL string
	APIKey  string
}

func (a *Adapter) credentials() (*apiCredentials, error) {
	if a.providers == nil {
		return nil, errors.New("ExcellStore: provider repository belum dikonfigurasi")
	}

	record, err := a.providers.GetByCode(Code)
	if err != nil || record == nil {
		return nil, errors.New("ExcellStore: provider belum terdaftar di database")
	}
	if !record.IsActive {
		return nil, errors.New("ExcellStore: provider tidak aktif")
	}
	apiKey := strings.TrimSpace(record.APIKey)
	if apiKey == "" {
		return nil, errors.New("ExcellStore: API Key belum dikonfigurasi")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(record.BaseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ExcellStore: BaseURL harus berupa URL HTTPS yang valid")
	}

	return &apiCredentials{BaseURL: baseURL, APIKey: apiKey}, nil
}

func (a *Adapter) ReconcileMinAge() time.Duration { return 10 * time.Second }

var _ provider.PollingProvider = (*Adapter)(nil)

// request mengirim POST application/x-www-form-urlencoded ke endpoint ExcellStore
// (sesuai dokumentasi: Method POST, Type Urlencoded).
func (a *Adapter) request(
	ctx context.Context,
	creds *apiCredentials,
	method, path string,
	form url.Values,
) ([]byte, error) {
	endpoint := creds.BaseURL + path

	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, errors.New("ExcellStore: gagal membuat request")
	}
	req.Header.Set("Accept", "application/json")
	if reader != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		// *url.Error memuat URL lengkap, jadi hanya error aslinya yang diteruskan.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("ExcellStore: request gagal: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("ExcellStore: baca response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ExcellStore: HTTP %d dari provider", resp.StatusCode)
	}
	return raw, nil
}

// decodeData memeriksa envelope lalu men-decode field data ke T.
func decodeData[T any](raw []byte) (T, error) {
	var zero T

	var envelope apiEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return zero, fmt.Errorf("ExcellStore: response JSON tidak valid: %w", err)
	}
	if !envelope.Status {
		msg := strings.TrimSpace(envelope.Msg)
		if msg == "" {
			msg = "provider mengembalikan status=false"
		}
		return zero, fmt.Errorf("ExcellStore: %s", msg)
	}

	var data T
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return zero, fmt.Errorf("ExcellStore: format data tidak valid: %w", err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Purchase
// ---------------------------------------------------------------------------

func (a *Adapter) Purchase(
	ctx context.Context,
	request provider.PurchaseRequest,
) (*provider.Result, error) {
	// Order sudah pernah dibuat di provider: jangan buat ulang, cukup cek status.
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

	serviceID := strings.TrimSpace(request.ProductCode)
	refID := strings.TrimSpace(request.RefID)
	customerID := strings.TrimSpace(request.CustomerID)
	if serviceID == "" || refID == "" || customerID == "" {
		return nil, configurationError(errors.New("ExcellStore: product code, reference ID, dan customer ID wajib diisi"))
	}

	// Format target: "DataID|ZoneID", atau "DataID" saja bila tanpa zone.
	target := customerID
	if serverID := strings.TrimSpace(request.ServerID); serverID != "" {
		target = customerID + "|" + serverID
	}

	form := url.Values{
		"api_key":    {creds.APIKey},
		"action":     {"order"},
		"service_id": {serviceID},
		"target":     {target},
		"kontak":     {defaultContact},
		"PartnerID":  {refID},
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/orderv2", form)
	if err != nil {
		return nil, temporaryError(err)
	}

	order, err := decodeData[orderData](raw)
	if err != nil {
		return nil, temporaryError(err)
	}
	if strings.TrimSpace(order.ID) == "" {
		return nil, temporaryError(errors.New("ExcellStore: response order tidak memiliki id"))
	}

	result := resultFromOrder(order, raw)
	if result.Status == provider.StatusSuccess {
		result.SN = provider.BuildManualSN(request.Nickname, customerID, request.InvoiceNumber)
		result.UpdateSN = true
	}
	// api_key sengaja tidak disimpan di request log.
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"action":     "order",
		"service_id": serviceID,
		"target":     target,
		"kontak":     defaultContact,
		"PartnerID":  refID,
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

	// Endpoint /status membutuhkan order ID ExcellStore, bukan ref ID kita.
	orderID := strings.TrimSpace(request.ProviderOrderID)
	if orderID == "" || orderID == "-" {
		return nil, temporaryError(errors.New("ExcellStore: provider order ID belum tersedia untuk cek status"))
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/status", url.Values{
		"api_key":  {creds.APIKey},
		"action":   {"status"},
		"order_id": {orderID},
	})
	if err != nil {
		return nil, temporaryError(err)
	}

	order, err := decodeData[orderData](raw)
	if err != nil {
		return nil, temporaryError(err)
	}
	// Response /status bisa saja tidak menyertakan id; pakai yang kita kirim.
	if strings.TrimSpace(order.ID) == "" {
		order.ID = orderID
	}

	result := resultFromOrder(order, raw)
	if result.Status == provider.StatusSuccess {
		result.SN = provider.BuildManualSN(request.Nickname, request.CustomerID, request.InvoiceNumber)
		result.UpdateSN = true
	}
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"action":   "check_status",
		"order_id": orderID,
	})
	result.Raw = raw
	return result, nil
}

// ---------------------------------------------------------------------------
// Balance & Catalog
// ---------------------------------------------------------------------------

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	creds, err := a.credentials()
	if err != nil {
		return 0, err
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/saldo", url.Values{"api_key": {creds.APIKey}})
	if err != nil {
		return 0, err
	}

	// data berisi string angka langsung, contoh: "data": "10000000".
	data, err := decodeData[flexFloat](raw)
	if err != nil {
		return 0, err
	}
	saldo := float64(data)
	if math.IsNaN(saldo) || math.IsInf(saldo, 0) {
		return 0, errors.New("ExcellStore: saldo tidak valid")
	}
	return saldo, nil
}

func (a *Adapter) SyncCatalog(ctx context.Context) ([]provider.CatalogItem, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, err
	}

	raw, err := a.request(ctx, creds, http.MethodPost, "/service", url.Values{"api_key": {creds.APIKey}})
	if err != nil {
		return nil, err
	}

	services, err := decodeData[[]serviceItem](raw)
	if err != nil {
		return nil, err
	}

	items := make([]provider.CatalogItem, 0, len(services))
	for _, svc := range services {
		id := strings.TrimSpace(svc.ID)
		if id == "" {
			continue
		}
		items = append(items, provider.CatalogItem{
			ProductCode: id,
			Name:        strings.TrimSpace(svc.NamaLayanan),
			BasePrice:   float64(svc.Harga),
			IsActive:    strings.EqualFold(strings.TrimSpace(svc.Status), "aktif"),
		})
	}

	if len(items) == 0 {
		return nil, errors.New("ExcellStore: katalog tidak berisi produk")
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func resultFromOrder(order orderData, raw []byte) *provider.Result {
	status := strings.ToLower(strings.TrimSpace(order.Status))
	result := &provider.Result{
		ProviderOrderID: strings.TrimSpace(order.ID),
		ProviderStatus:  status,
		Message:         "ExcellStore: status " + status,
		Raw:             raw,
	}

	// keterangan berisi SN dari provider (bisa kosong / berisi catatan).
	result.SN = strings.TrimSpace(order.Keterangan)
	result.UpdateSN = result.SN != ""

	switch status {
	case "success":
		result.Status = provider.StatusSuccess
		result.StatusReason = "ExcellStore: transaksi sukses"
		result.IncludeSNInSuccessEvent = true
	case "pending", "processing":
		result.Status = provider.StatusPending
		result.ProviderStatus = "processing"
		result.StatusReason = "ExcellStore: transaksi sedang diproses"
	case "cancel", "refund":
		result.Status = provider.StatusFailedFinal
		result.StatusReason = "ExcellStore: transaksi gagal final"
		if reason := strings.TrimSpace(order.Keterangan); reason != "" {
			result.RefundReason = reason
		}
		// Pada status gagal, keterangan adalah alasan, bukan SN.
		result.SN = ""
		result.UpdateSN = false
	default:
		// Status anomali: tidak di-refund dan tidak dianggap sukses.
		result.Status = provider.StatusPending
		result.ProviderStatus = "pending"
		result.Message = "ExcellStore: status tidak dikenal (" + status + "), ditahan di pending"
		result.StatusReason = "ExcellStore: status anomali, ditahan di pending"
		result.SN = ""
		result.UpdateSN = false
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
// (CallbackParser tidak diimplementasikan: ExcellStore dipakai tanpa callback.)
var (
	_ provider.Provider      = (*Adapter)(nil)
	_ provider.CatalogSyncer = (*Adapter)(nil)
)