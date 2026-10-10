package digiflazz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

type fakeBuyer struct {
	create, check           *service.DigiflazzTransactionResponse
	createErr, checkErr     error
	callback                *service.DigiflazzCallbackPayload
	callbackErr             error
	createCalls, checkCalls int
	callbackSignature       string
	lastCustomerNo          string
}

func (f *fakeBuyer) GetPriceList() ([]service.DigiflazzPriceListItem, error) { return nil, nil }
func (f *fakeBuyer) CheckBalance() (float64, error)                          { return 42, nil }
func (f *fakeBuyer) CreateTransaction(_ string, _ string, customerNo string, _ bool) (*service.DigiflazzTransactionResponse, error) {
	f.createCalls++
	f.lastCustomerNo = customerNo
	return f.create, f.createErr
}
func (f *fakeBuyer) CheckTransactionStatus(string, string, string) (*service.DigiflazzTransactionResponse, error) {
	f.checkCalls++
	return f.check, f.checkErr
}
func (f *fakeBuyer) ProcessCallback(_ []byte, signature string) (*service.DigiflazzCallbackPayload, error) {
	f.callbackSignature = signature
	return f.callback, f.callbackErr
}

func response(status, rc, message string) *service.DigiflazzTransactionResponse {
	r := &service.DigiflazzTransactionResponse{}
	r.Data.RefID, r.Data.Status, r.Data.RC, r.Data.Message, r.Data.SN = "DF-1", status, rc, message, "SN-DF"
	return r
}

func TestPurchaseMapsDigiflazzResponsesByRC(t *testing.T) {
	cases := []struct {
		name, status, rc, message string
		want                      provider.Status
		wantPS                    string
	}{
		{"rc 00 sukses", "Sukses", "00", "ok", provider.StatusSuccess, "Sukses"},
		{"rc 03 pending", "Pending", "03", "wait", provider.StatusPending, "Pending"},
		{"rc 02 gagal", "Gagal", "02", "transaksi gagal", provider.StatusFailedFinal, "Gagal"},
		{"rc 51 gagal", "Gagal", "51", "nomor diblokir", provider.StatusFailedFinal, "Gagal"},
		{"rc 54 gagal", "Gagal", "54", "nomor tujuan salah", provider.StatusFailedFinal, "Gagal"},
		{"rc lain jadi pending", "Gagal", "44", "saldo tidak cukup", provider.StatusPending, "Pending (RC 44)"},
		{"rc kosong jadi pending", "Gagal", "", "tidak jelas", provider.StatusPending, "Pending (RC -)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buyer := &fakeBuyer{create: response(tc.status, tc.rc, tc.message)}
			result, err := New(buyer).Purchase(context.Background(), provider.PurchaseRequest{RefID: "REF", ProductCode: "SKU", CustomerID: "USER"})
			if err != nil || result.Status != tc.want || result.ProviderStatus != tc.wantPS || result.ProviderOrderID != "DF-1" || result.SN != "SN-DF" || len(result.Raw) == 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestPurchaseErrorAndExistingOrderUseProviderContract(t *testing.T) {
	buyer := &fakeBuyer{createErr: errors.New("connection timeout"), check: response("Sukses", "00", "ok")}
	adapter := New(buyer)
	result, err := adapter.Purchase(context.Background(), provider.PurchaseRequest{RefID: "REF", ProductCode: "SKU", CustomerID: "USER"})
	if result != nil {
		t.Fatalf("expected nil result, got %+v", result)
	}
	providerErr, ok := err.(*provider.ProviderError)
	if !ok || providerErr.Status != provider.StatusFailedHold || providerErr.ProviderStatus != "Pending" || providerErr.Kind != provider.ErrorTimeout {
		t.Fatalf("err=%#v", err)
	}
	result, err = adapter.Purchase(context.Background(), provider.PurchaseRequest{RefID: "REF", ProductCode: "SKU", CustomerID: "USER", ExistingProviderOrderID: "DF-EXISTING"})
	if err != nil || result == nil || buyer.createCalls != 1 || buyer.checkCalls != 1 {
		t.Fatalf("result=%+v err=%v create=%d check=%d", result, err, buyer.createCalls, buyer.checkCalls)
	}
}

func TestPurchaseCombinesCustomerAndServerIDForDigiflazz(t *testing.T) {
	buyer := &fakeBuyer{create: response("Pending", "03", "wait")}
	_, err := New(buyer).Purchase(context.Background(), provider.PurchaseRequest{RefID: "REF", ProductCode: "SKU", CustomerID: "12345678", ServerID: "2001"})
	if err != nil || buyer.lastCustomerNo != "12345678(2001)" {
		t.Fatalf("err=%v customer_no=%q", err, buyer.lastCustomerNo)
	}
}

func TestParseCallbackUsesLegacyHeaderFallback(t *testing.T) {
	payload := &service.DigiflazzCallbackPayload{}
	payload.Data.RefID, payload.Data.Status, payload.Data.RC, payload.Data.Message = "REF-CB", "Gagal", "44", "saldo provider habis"
	buyer := &fakeBuyer{callback: payload}
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{}`))
	req.Header.Set("X-Digiflazz-Delivery", "fallback-signature")
	result, refID, err := New(buyer).ParseCallback(req)
	if err != nil || refID != "REF-CB" || result.Status != provider.StatusPending || result.ProviderStatus != "Pending (RC 44)" || buyer.callbackSignature != "fallback-signature" {
		t.Fatalf("result=%+v ref=%q err=%v sig=%q", result, refID, err, buyer.callbackSignature)
	}
}
