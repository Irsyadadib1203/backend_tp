package sipas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

const (
	Code    = "SIPAS"
	BaseURL = "https://h2h.kioson.my.id"
)

// Penanda pada teks respons topup. Urutan pengecekan: gagal -> proses -> sukses.
// Teks yang tidak cocok dengan semuanya ditahan di pending (tidak di-refund,
// tidak dianggap sukses). Lengkapi daftar ini setelah melihat respons nyata.
var (
	// Kosong dengan sengaja: respons gagal yang belum dikenal ditahan dulu
	// supaya admin yang memutuskan refund. Isi, misalnya "gagal", bila
	// sudah dipastikan respons itu selalu final.
	failedMarkers  = []string{}
	pendingMarkers = []string{"proses", "ditunggu", "pending", "antri"}
	successMarkers = []string{"sukses", "berhasil"}
)

var (
	rcPattern    = regexp.MustCompile(`(?i)^\s*RC\s*(\d+)`)
	saldoPattern = regexp.MustCompile(`(?i)Saldo(?:\s+Anda)?\s*:?\s*([0-9][0-9.,]*)`)
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

func (a *Adapter) Code() string { return Code }

// ---------------------------------------------------------------------------
// Kredensial & HTTP
// ---------------------------------------------------------------------------

type apiCredentials struct {
	BaseURL  string
	MemberID string // providers.username
	PIN      string // providers.api_key  (Pin Trx)
	Password string // providers.secret   (Password H2H)
}

func (a *Adapter) credentials() (*apiCredentials, error) {
	if a.providers == nil {
		return nil, errors.New("SIPAS: provider repository belum dikonfigurasi")
	}
	record, err := a.providers.GetByCode(Code)
	if err != nil || record == nil {
		return nil, errors.New("SIPAS: provider belum terdaftar di database")
	}
	if !record.IsActive {
		return nil, errors.New("SIPAS: provider tidak aktif")
	}

	memberID := strings.TrimSpace(record.Username)
	pin := strings.TrimSpace(record.APIKey)       // PIN Trx
	password := strings.TrimSpace(record.Secret)  // Password H2H
	if memberID == "" || password == "" || pin == "" {
		return nil, errors.New("SIPAS: Member ID, Password H2H, dan PIN Trx wajib dikonfigurasi")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(record.BaseURL), "/")
	if baseURL == "" {
		baseURL = BaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("SIPAS: BaseURL harus berupa URL HTTPS yang valid")
	}
	// Path endpoint sudah memuat /api, jadi buang bila admin mengisinya di BaseURL.
	baseURL = strings.TrimSuffix(baseURL, "/api")

	return &apiCredentials{BaseURL: baseURL, MemberID: memberID, Password: password, PIN: pin}, nil
}

// request memanggil endpoint GET. Kredensial ikut di query string, jadi URL
// tidak boleh pernah masuk ke log atau pesan error.
func (a *Adapter) request(
	ctx context.Context,
	creds *apiCredentials,
	path string,
	params url.Values,
) ([]byte, error) {
	q := url.Values{}
	q.Set("User", creds.MemberID)
	q.Set("Pin", creds.PIN)
	q.Set("Password", creds.Password)
	for key, values := range params {
		for _, v := range values {
			q.Set(key, v)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, creds.BaseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("SIPAS: gagal membuat request")
	}
	req.Header.Set("Accept", "*/*")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		// *url.Error memuat URL lengkap (termasuk kredensial); teruskan error aslinya saja.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("SIPAS: request gagal: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("SIPAS: baca response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("SIPAS: HTTP %d dari provider", resp.StatusCode)
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// Helper teks
// ---------------------------------------------------------------------------

func cleanText(raw []byte) string {
	return strings.Join(strings.Fields(string(raw)), " ")
}

func truncate(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

func containsAny(text string, markers []string) bool {
	for _, m := range markers {
		if m != "" && strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// parseRupiah memakai format Indonesia: titik = pemisah ribuan, koma = desimal
// ("50.006" -> 50006).
func parseRupiah(s string) (float64, error) {
	s = strings.TrimRight(strings.TrimSpace(s), ".,")
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	} else {
		s = strings.ReplaceAll(s, ".", "")
	}
	return strconv.ParseFloat(s, 64)
}

// ---------------------------------------------------------------------------
// Purchase
// ---------------------------------------------------------------------------

// target: untuk pulsa/data berisi MSISDN. Untuk produk game format Tujuan
// BELUM diverifikasi; sementara ID dan server digabung tanpa pemisah.
func target(customerID, serverID string) string {
	return strings.TrimSpace(customerID) + strings.TrimSpace(serverID)
}

func (a *Adapter) Purchase(
	ctx context.Context,
	request provider.PurchaseRequest,
) (*provider.Result, error) {
	// Order sudah dikirim sebelumnya: jangan kirim ulang (Trxid sama).
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
		return nil, configurationError(errors.New("SIPAS: product code, reference ID, dan customer ID wajib diisi"))
	}
	tujuan := target(customerID, request.ServerID)

	params := url.Values{}
	params.Set("Trxid", refID)
	params.Set("Produk", productCode)
	params.Set("Tujuan", tujuan)

	raw, err := a.request(ctx, creds, "/api/topup", params)
	if err != nil {
		// Order mungkin sudah masuk di provider; tahan, jangan refund.
		return nil, temporaryError(err)
	}
	text := cleanText(raw)
	if text == "" {
		return nil, temporaryError(errors.New("SIPAS: response topup kosong"))
	}

	result := resultFromText(text, refID, raw)
	if result.Status == provider.StatusSuccess {
		result.SN = provider.BuildManualSN(request.Nickname, customerID, request.InvoiceNumber)
		result.UpdateSN = true
	}
	// Kredensial sengaja tidak masuk ke log request.
	result.RequestRaw, _ = json.Marshal(map[string]string{
		"Trxid":  refID,
		"Produk": productCode,
		"Tujuan": tujuan,
	})
	result.Raw = raw
	return result, nil
}

func resultFromText(text, refID string, raw []byte) *provider.Result {
	lower := strings.ToLower(text)
	result := &provider.Result{
		ProviderOrderID: refID,
		Message:         "SIPAS: " + truncate(text, 200),
		Raw:             raw,
	}

	switch {
	case containsAny(lower, failedMarkers):
		result.Status = provider.StatusFailedFinal
		result.ProviderStatus = "failed"
		result.StatusReason = "SIPAS: transaksi gagal final"
		result.RefundReason = truncate(text, 200)
	case containsAny(lower, pendingMarkers):
		result.Status = provider.StatusPending
		result.ProviderStatus = "processing"
		result.StatusReason = "SIPAS: transaksi sedang diproses"
	case containsAny(lower, successMarkers):
		result.Status = provider.StatusSuccess
		result.ProviderStatus = "success"
		result.StatusReason = "SIPAS: transaksi sukses"
		result.IncludeSNInSuccessEvent = true
	default:
		// Respons tidak dikenal: tidak di-refund dan tidak dianggap sukses.
		result.Status = provider.StatusPending
		result.ProviderStatus = "pending"
		result.Message = "SIPAS: respons tidak dikenal, ditahan di pending: " + truncate(text, 200)
		result.StatusReason = "SIPAS: respons anomali, ditahan di pending"
	}
	return result
}

// ---------------------------------------------------------------------------
// CheckStatus
// ---------------------------------------------------------------------------

// Belum ada endpoint cek status yang terdokumentasi, jadi tidak ada yang bisa
// ditanyakan ke provider. Transaksi dibiarkan pending; selesaikan manual lewat
// "Manual Success" / "Manual Refund" di admin.
func (a *Adapter) CheckStatus(
	_ context.Context,
	request provider.StatusRequest,
) (*provider.Result, error) {
	refID := strings.TrimSpace(request.RefID)
	return &provider.Result{
		ProviderOrderID: refID,
		Status:          provider.StatusPending,
		ProviderStatus:  "pending",
		Message:         "SIPAS: cek status otomatis belum tersedia, selesaikan manual",
		StatusReason:    "SIPAS: menunggu konfirmasi manual",
	}, nil
}

// ---------------------------------------------------------------------------
// Balance & Catalog
// ---------------------------------------------------------------------------

func (a *Adapter) Balance(ctx context.Context) (float64, error) {
	creds, err := a.credentials()
	if err != nil {
		return 0, err
	}
	raw, err := a.request(ctx, creds, "/api/balance", nil)
	if err != nil {
		return 0, err
	}
	text := cleanText(raw)

	if m := rcPattern.FindStringSubmatch(text); m != nil {
		if code, _ := strconv.Atoi(m[1]); code != 0 {
			return 0, fmt.Errorf("SIPAS: %s", truncate(text, 200))
		}
	}
	m := saldoPattern.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("SIPAS: saldo tidak ditemukan di response: %s", truncate(text, 120))
	}
	saldo, err := parseRupiah(m[1])
	if err != nil || math.IsNaN(saldo) || math.IsInf(saldo, 0) {
		return 0, errors.New("SIPAS: saldo tidak valid")
	}
	return saldo, nil
}

type priceResponse struct {
	RC  string          `json:"rc"`
	Msg json.RawMessage `json:"msg"`
}

type priceGroup struct {
	Data string `json:"Data"`
	List []struct {
		Subs    string `json:"Subs"`
		Product []struct {
			ProductID   string `json:"ProductID"`
			ProductName string `json:"ProductName"`
			Describe    string `json:"Describe"`
			Price       string `json:"Price"`
			Status      string `json:"Status"`
		} `json:"product"`
	} `json:"List"`
}

func (a *Adapter) SyncCatalog(ctx context.Context) ([]provider.CatalogItem, error) {
	creds, err := a.credentials()
	if err != nil {
		return nil, err
	}
	raw, err := a.request(ctx, creds, "/api/prices", nil)
	if err != nil {
		return nil, err
	}

	var resp priceResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("SIPAS: response pricelist bukan JSON: %s", truncate(cleanText(raw), 120))
	}
	if strings.TrimSpace(resp.RC) != "00" {
		var msg string
		_ = json.Unmarshal(resp.Msg, &msg)
		if msg == "" {
			msg = "rc " + resp.RC
		}
		return nil, fmt.Errorf("SIPAS: %s", truncate(msg, 200))
	}

	var groups []priceGroup
	if err := json.Unmarshal(resp.Msg, &groups); err != nil {
		return nil, fmt.Errorf("SIPAS: format pricelist tidak valid: %w", err)
	}

	items := make([]provider.CatalogItem, 0, 1024)
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, sub := range group.List {
			for _, p := range sub.Product {
				code := strings.TrimSpace(p.ProductID)
				if code == "" || seen[code] {
					continue
				}
				seen[code] = true

				// Format harga: "8,488.00" (koma ribuan, titik desimal).
				price, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(p.Price), ",", ""), 64)
				if err != nil || math.IsNaN(price) || math.IsInf(price, 0) {
					price = 0
				}
				name := strings.TrimSpace(p.ProductName)
				if name == "" {
					name = strings.TrimSpace(p.Describe)
				}
				items = append(items, provider.CatalogItem{
					ProductCode: code,
					Name:        name,
					BasePrice:   price,
					// Harga 0 berarti belum ada harga modal; jangan diaktifkan.
					IsActive: strings.EqualFold(strings.TrimSpace(p.Status), "active") && price > 0,
				})
			}
		}
	}

	if len(items) == 0 {
		return nil, errors.New("SIPAS: katalog tidak berisi produk")
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Error helper
// ---------------------------------------------------------------------------

func configurationError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorConfiguration,
		ProviderStatus: "Konfigurasi Error", Message: err.Error(), Cause: err,
	}
}

// Response upstream yang gagal/tidak valid ditahan (hold), bukan di-refund.
func temporaryError(err error) *provider.ProviderError {
	return &provider.ProviderError{
		Status: provider.StatusFailedHold, Kind: provider.ErrorTemporary,
		ProviderStatus: "Provider Pending", Message: err.Error(), Cause: err,
	}
}

var (
	_ provider.Provider      = (*Adapter)(nil)
	_ provider.CatalogSyncer = (*Adapter)(nil)
)