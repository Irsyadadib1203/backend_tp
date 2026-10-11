package digiflazz

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"topup-backend/config"
	"topup-backend/internal/domain"
	"topup-backend/internal/pkg/crypto"
	"topup-backend/internal/repository"
)

type fakeProviderRepo struct {
	repository.ProviderRepository
	record      *domain.Provider
	balanceID   uint
	balance     float64
	loggedWebhk []*domain.WebhookLog
}

func (r *fakeProviderRepo) GetByCode(string) (*domain.Provider, error) { return r.record, nil }
func (r *fakeProviderRepo) UpdateBalance(id uint, balance float64) error {
	r.balanceID, r.balance = id, balance
	return nil
}
func (r *fakeProviderRepo) LogWebhook(l *domain.WebhookLog) error {
	r.loggedWebhk = append(r.loggedWebhk, l)
	return nil
}

func newTestClient(t *testing.T, handler http.HandlerFunc, secret string) (Client, *fakeProviderRepo) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	repo := &fakeProviderRepo{record: &domain.Provider{ID: 7, BaseURL: srv.URL + "/", Username: "user", APIKey: "key", Secret: secret}}
	return NewClient(repo, &config.Config{}), repo
}

func readBody(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body bukan JSON: %v", err)
	}
	return body
}

func TestGetPriceListSignsAndParses(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readBody(t, r)
		if r.URL.Path != "/price-list" || body["cmd"] != "prepaid" || body["username"] != "user" || body["sign"] != crypto.MD5Hash("userkeypricelist") {
			t.Errorf("request tidak sesuai: path=%s body=%v", r.URL.Path, body)
		}
		_, _ = w.Write([]byte(`{"data":[{"product_name":"ML 86","brand":"MOBILE LEGENDS","price":19500,"buyer_sku_code":"ML86","buyer_product_status":true,"seller_product_status":true}]}`))
	}, "")
	items, err := c.GetPriceList()
	if err != nil || len(items) != 1 || items[0].BuyerSkuCode != "ML86" || items[0].Price != 19500 || !items[0].BuyerProductStatus {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestCheckBalanceUpdatesProviderBalance(t *testing.T) {
	c, repo := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readBody(t, r)
		if r.URL.Path != "/cek-saldo" || body["cmd"] != "deposit" || body["sign"] != crypto.MD5Hash("userkeydepo") {
			t.Errorf("request tidak sesuai: path=%s body=%v", r.URL.Path, body)
		}
		_, _ = w.Write([]byte(`{"data":{"deposit":125000}}`))
	}, "")
	balance, err := c.CheckBalance()
	if err != nil || balance != 125000 || repo.balanceID != 7 || repo.balance != 125000 {
		t.Fatalf("balance=%v err=%v repo=%d/%v", balance, err, repo.balanceID, repo.balance)
	}
}

func TestCreateTransactionPayloadAndWebhookLog(t *testing.T) {
	c, repo := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readBody(t, r)
		if r.URL.Path != "/transaction" || body["ref_id"] != "REF-9" || body["buyer_sku_code"] != "ML86" || body["customer_no"] != "123456782001" ||
			body["sign"] != crypto.MD5Hash("userkeyREF-9") || body["cb_url"] != "https://api1235.irxplay.com/api/v1/callback/digiflazz" {
			t.Errorf("payload tidak sesuai: %v", body)
		}
		if _, ok := body["testing"]; ok {
			t.Errorf("testing tidak boleh dikirim saat false: %v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"ref_id":"REF-9","status":"Pending","rc":"03","sn":""}}`))
	}, "")
	resp, err := c.CreateTransaction("REF-9", "ML86", "123456782001", false)
	if err != nil || resp.Data.RefID != "REF-9" || resp.Data.RC != "03" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if len(repo.loggedWebhk) != 1 || repo.loggedWebhk[0].Direction != domain.WebhookOutgoing || repo.loggedWebhk[0].ProviderName != "DIGIFLAZZ_BUYER" {
		t.Fatalf("webhook log = %+v", repo.loggedWebhk)
	}
	// CheckTransactionStatus memakai endpoint & sign yang sama dengan CreateTransaction.
	if _, err := c.CheckTransactionStatus("REF-9", "ML86", "123456782001"); err != nil {
		t.Fatalf("CheckTransactionStatus: %v", err)
	}
}

func TestMissingCredentialsReturnError(t *testing.T) {
	repo := &fakeProviderRepo{record: &domain.Provider{BaseURL: "http://127.0.0.1:1"}}
	c := NewClient(repo, &config.Config{})
	if _, err := c.GetPriceList(); err == nil {
		t.Error("GetPriceList harus error tanpa kredensial")
	}
	if _, err := c.CheckBalance(); err == nil {
		t.Error("CheckBalance harus error tanpa kredensial")
	}
	if _, err := c.CreateTransaction("R", "S", "C", false); err == nil {
		t.Error("CreateTransaction harus error tanpa kredensial")
	}
}

func TestProcessCallbackSignatures(t *testing.T) {
	payloadMD5 := func(sign string) []byte {
		return []byte(`{"data":{"ref_id":"REF-1","status":"Sukses","rc":"00","sign":"` + sign + `"}}`)
	}
	c, repo := newTestClient(t, func(http.ResponseWriter, *http.Request) {}, "whsecret")

	raw := payloadMD5("")
	good := "sha1=" + crypto.HMACSHA1("whsecret", raw)
	if p, err := c.ProcessCallback(raw, good); err != nil || p.Data.RefID != "REF-1" {
		t.Fatalf("HMAC valid ditolak: %v", err)
	}
	if p, err := c.ProcessCallback(raw, strings.TrimPrefix(good, "sha1=")); err != nil || p == nil {
		t.Fatalf("HMAC tanpa prefix sha1= harus diterima: %v", err)
	}
	if _, err := c.ProcessCallback(raw, "sha1=deadbeef"); err == nil {
		t.Error("HMAC salah tanpa sign MD5 harus ditolak")
	}
	md5Raw := payloadMD5(crypto.MD5Hash("userkeyREF-1"))
	if _, err := c.ProcessCallback(md5Raw, "sha1=deadbeef"); err != nil {
		t.Errorf("fallback MD5 harus diterima: %v", err)
	}
	if _, err := c.ProcessCallback([]byte(`{"data":{"ref_id":""}}`), good); err == nil {
		t.Error("ref_id kosong harus ditolak")
	}
	if _, err := c.ProcessCallback([]byte(`bukan json`), good); err == nil {
		t.Error("JSON rusak harus ditolak")
	}
	if len(repo.loggedWebhk) == 0 || repo.loggedWebhk[0].Direction != domain.WebhookIncoming {
		t.Errorf("callback masuk harus dicatat: %+v", repo.loggedWebhk)
	}

	// Tanpa secret: sign MD5 di payload wajib valid bila ada.
	c2, _ := newTestClient(t, func(http.ResponseWriter, *http.Request) {}, "")
	if _, err := c2.ProcessCallback(payloadMD5("salah"), ""); err == nil {
		t.Error("sign MD5 salah harus ditolak")
	}
	if _, err := c2.ProcessCallback(payloadMD5(crypto.MD5Hash("userkeyREF-1")), ""); err != nil {
		t.Errorf("sign MD5 benar harus diterima: %v", err)
	}
}
