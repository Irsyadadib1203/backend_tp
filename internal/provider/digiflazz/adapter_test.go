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
}

func (f *fakeBuyer) GetPriceList() ([]service.DigiflazzPriceListItem, error) { return nil, nil }
func (f *fakeBuyer) CheckBalance() (float64, error)                          { return 42, nil }
func (f *fakeBuyer) CreateTransaction(string, string, string, bool) (*service.DigiflazzTransactionResponse, error) {
	f.createCalls++
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

func response(status, message string) *service.DigiflazzTransactionResponse {
	r := &service.DigiflazzTransactionResponse{}
	r.Data.RefID, r.Data.Status, r.Data.Message, r.Data.SN = "DF-1", status, message, "SN-DF"
	return r
}

func TestPurchaseMapsLegacyDigiflazzResponses(t *testing.T) {
	cases := []struct {
		name, status, message string
		want                  provider.Status
		wantPS                string
	}{
		{"success", "Sukses", "ok", provider.StatusSuccess, "Sukses"},
		{"pending", "Pending", "wait", provider.StatusPending, "Pending"},
		{"balance failure holds", "Gagal", "saldo provider habis", provider.StatusFailedHold, "Pending (Kendala Provider)"},
		{"final failure", "Gagal", "id salah", provider.StatusFailedFinal, "Gagal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buyer := &fakeBuyer{create: response(tc.status, tc.message)}
			result, err := New(buyer).Purchase(context.Background(), provider.PurchaseRequest{RefID: "REF", ProductCode: "SKU", CustomerID: "USER"})
			if err != nil || result.Status != tc.want || result.ProviderStatus != tc.wantPS || result.ProviderOrderID != "DF-1" || result.SN != "SN-DF" || len(result.Raw) == 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestPurchaseErrorAndExistingOrderUseProviderContract(t *testing.T) {
	buyer := &fakeBuyer{createErr: errors.New("connection timeout"), check: response("Sukses", "ok")}
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

func TestParseCallbackUsesLegacyHeaderFallback(t *testing.T) {
	payload := &service.DigiflazzCallbackPayload{}
	payload.Data.RefID, payload.Data.Status, payload.Data.Message = "REF-CB", "Gagal", "saldo provider habis"
	buyer := &fakeBuyer{callback: payload}
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{}`))
	req.Header.Set("X-Digiflazz-Delivery", "fallback-signature")
	result, refID, err := New(buyer).ParseCallback(req)
	if err != nil || refID != "REF-CB" || result.Status != provider.StatusFailedHold || result.ProviderStatus != "Pending (Kendala Provider)" || buyer.callbackSignature != "fallback-signature" {
		t.Fatalf("result=%+v ref=%q err=%v sig=%q", result, refID, err, buyer.callbackSignature)
	}
}
