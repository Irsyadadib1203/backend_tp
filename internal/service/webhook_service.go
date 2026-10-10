package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
)

type WebhookService interface {
	DispatchH2HCallback(targetURL, apiKey string, data *H2HResponseData)
}

type webhookService struct {
	providerRepo repository.ProviderRepository
	httpClient   *http.Client
}

func NewWebhookService(providerRepo repository.ProviderRepository) WebhookService {
	return &webhookService{
		providerRepo: providerRepo,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// DispatchH2HCallback mengirim callback ke partner.
// sign = md5(ref_id + apikey + status)
func (s *webhookService) DispatchH2HCallback(targetURL, apiKey string, data *H2HResponseData) {
	if targetURL == "" || data == nil {
		return
	}

	sign := crypto.MD5Hash(data.RefID + apiKey + data.Status)
	data.Sign = sign

	payloadBytes, err := json.Marshal(map[string]interface{}{
		"data": data,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Callback-Signature", sign)

	resp, err := s.httpClient.Do(req)
	statusCode := 0
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else {
		statusCode = resp.StatusCode
		resp.Body.Close()
	}

	_ = s.providerRepo.LogWebhook(&domain.WebhookLog{
		Direction:    domain.WebhookOutgoing,
		ProviderName: "H2H_CLIENT_WEBHOOK",
		URL:          targetURL,
		Payload:      string(payloadBytes),
		StatusCode:   statusCode,
		ErrorMessage: errMsg,
		CreatedAt:    time.Now(),
	})

	if err != nil {
		fmt.Printf("[Webhook] Failed to dispatch callback to %s: %v\n", targetURL, err)
	}
}

// ---------------------------------------------------------------------------
// PartnerNotifier: callback status final transaksi H2H ke mitra
// (dipanggil dari transactionService saat status berubah menjadi final)
// ---------------------------------------------------------------------------

// PartnerNotifier mengirim callback status transaksi H2H ke mitra.
type PartnerNotifier interface {
	NotifyTransaction(tx *domain.Transaction)
}

// PartnerNotifierSetter adalah kemampuan opsional transactionService. Sengaja
// tidak dimasukkan ke interface TransactionService supaya fake di file tes
// tidak perlu diubah.
type PartnerNotifierSetter interface {
	SetPartnerNotifier(n PartnerNotifier)
}

type partnerNotifier struct {
	userRepo     repository.UserRepository
	nominalRepo  repository.NominalRepository
	providerRepo repository.ProviderRepository
	client       *http.Client
	retryDelays  []time.Duration // jeda sebelum percobaan ke-2, ke-3, dst.
}

// NewPartnerNotifier membuat pengirim callback ke mitra.
// allowPrivateTargets=true hanya untuk development (mengizinkan localhost / jaringan privat).
func NewPartnerNotifier(
	userRepo repository.UserRepository,
	nominalRepo repository.NominalRepository,
	providerRepo repository.ProviderRepository,
	allowPrivateTargets bool,
) PartnerNotifier {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Control dijalankan setelah DNS di-resolve, tepat sebelum koneksi dibuat,
		// jadi alamat internal tidak bisa lolos lewat nama domain (DNS rebinding).
		Control: func(network, address string, _ syscall.RawConn) error {
			if allowPrivateTargets {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || isBlockedWebhookIP(ip) {
				return fmt.Errorf("alamat tujuan webhook tidak diizinkan: %s", host)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil, // jangan lewat proxy environment
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
	}
	return &partnerNotifier{
		userRepo:     userRepo,
		nominalRepo:  nominalRepo,
		providerRepo: providerRepo,
		client: &http.Client{
			Timeout:   15 * time.Second,
			Transport: transport,
			// Jangan ikuti redirect: redirect bisa dipakai membelokkan ke alamat internal.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		retryDelays: []time.Duration{10 * time.Second, 60 * time.Second}, // total 3 percobaan
	}
}

func isBlockedWebhookIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// ValidatePartnerWebhookURL memeriksa bentuk URL webhook mitra (tanpa akses jaringan).
// Pemeriksaan alamat tujuan dilakukan saat pengiriman (lihat Control pada dialer).
func ValidatePartnerWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("url kosong")
	}
	if len(raw) > 255 {
		return errors.New("url terlalu panjang (maks 255 karakter)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("format url tidak valid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url harus diawali http:// atau https://")
	}
	if u.Hostname() == "" {
		return errors.New("host url kosong")
	}
	if u.User != nil {
		return errors.New("url tidak boleh memuat username/password")
	}
	return nil
}

// NotifyTransaction mengirim status final transaksi H2H ke mitra. Aman dipanggil
// untuk transaksi apa pun: yang bukan H2H atau belum final diabaikan.
// Payload dibuat saat ini juga; pengiriman (dengan retry) berjalan di goroutine.
func (n *partnerNotifier) NotifyTransaction(tx *domain.Transaction) {
	if tx == nil || tx.Source != domain.SourceH2H || tx.UserID == nil {
		return
	}
	if !isFinalTransactionStatus(tx.Status) {
		return
	}

	apiKey, err := n.userRepo.GetAPIKeyByUserID(*tx.UserID)
	if err != nil || apiKey == nil {
		return
	}

	target := strings.TrimSpace(tx.PartnerCallbackURL)
	if target == "" {
		target = strings.TrimSpace(apiKey.WebhookURL)
	}
	if target == "" {
		return
	}
	if err := ValidatePartnerWebhookURL(target); err != nil {
		log.Printf("[PartnerNotify] url dilewati invoice=%s: %v", tx.InvoiceNumber, err)
		return
	}

	data := n.buildPayload(tx, apiKey)
	go n.deliver(target, data, tx.InvoiceNumber)
}

func (n *partnerNotifier) buildPayload(tx *domain.Transaction, apiKey *domain.APIKey) *H2HResponseData {
	// ref_id milik mitra disimpan di idempotency key: "h2h_<userID>_<ref_id>".
	refID := strings.TrimPrefix(tx.IdempotencyKey, fmt.Sprintf("h2h_%d_", *tx.UserID))

	sku := ""
	if nominal, _ := n.nominalRepo.FindByID(tx.NominalID); nominal != nil {
		sku = nominal.SellerProductCode
		if sku == "" {
			sku = nominal.ProviderProductCode
		}
	}

	sn := tx.SN
	if sn == "" {
		sn = tx.PaymentReference
	}

	data := &H2HResponseData{
		RefID:         refID,
		InvoiceNumber: tx.InvoiceNumber,
		UserID:        tx.CustomerID,
		ServerID:      tx.ServerID,
		SKUCode:       sku,
		Message:       tx.ProviderMessage,
		Status:        statusToDigiflazz(tx.Status),
		RC:            statusToRC(tx.Status),
		SN:            sn,
		Price:         tx.SellingPrice,
	}
	// Sama seperti webhook awal: md5(ref_id + apikey + status).
	data.Sign = crypto.MD5Hash(data.RefID + apiKey.Key + data.Status)
	return data
}

func (n *partnerNotifier) deliver(target string, data *H2HResponseData, invoice string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PartnerNotify] panic dipulihkan invoice=%s: %v", invoice, r)
		}
	}()

	payload, err := json.Marshal(map[string]interface{}{"data": data})
	if err != nil {
		return
	}

	attempts := len(n.retryDelays) + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(n.retryDelays[attempt-2])
		}
		code, body, sendErr := n.post(target, payload, data.Sign)
		n.logAttempt(target, payload, code, body, sendErr)
		if sendErr == nil && code >= 200 && code < 300 {
			return
		}
	}
	log.Printf("[PartnerNotify] gagal terkirim setelah %d percobaan invoice=%s url=%s", attempts, invoice, target)
}

func (n *partnerNotifier) post(target string, payload []byte, sign string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Callback-Signature", sign)

	resp, err := n.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return resp.StatusCode, string(body), nil
}

func (n *partnerNotifier) logAttempt(target string, payload []byte, code int, body string, sendErr error) {
	errMsg := ""
	if sendErr != nil {
		errMsg = truncateManualText(sendErr.Error(), 255)
	}
	_ = n.providerRepo.LogWebhook(&domain.WebhookLog{
		Direction:    domain.WebhookOutgoing,
		ProviderName: "H2H_CLIENT_WEBHOOK",
		URL:          truncateManualText(target, 255),
		Payload:      string(payload),
		Response:     body,
		StatusCode:   code,
		ErrorMessage: errMsg,
		CreatedAt:    time.Now(),
	})
}