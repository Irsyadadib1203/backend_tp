package otomax

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
)

type fakeProviderRepo struct{ record *domain.Provider }

func (r *fakeProviderRepo) GetByCode(string) (*domain.Provider, error) { return r.record, nil }
func (r *fakeProviderRepo) GetByID(uint) (*domain.Provider, error)     { return r.record, nil }
func (*fakeProviderRepo) List() ([]domain.Provider, error)             { return nil, nil }
func (*fakeProviderRepo) Update(*domain.Provider) error                { return nil }
func (*fakeProviderRepo) UpdateBalance(uint, float64) error            { return nil }
func (*fakeProviderRepo) LogWebhook(*domain.WebhookLog) error          { return nil }
func (*fakeProviderRepo) ListWebhookLogs(int, int, string) ([]domain.WebhookLog, int64, error) {
	return nil, 0, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testConfig() string {
	return `{
		"protocol":"otomax_http",
		"base_url":"https://api.ffzstore.example",
		"purchase":{"method":"GET","path":"/v1/otomax/order","query":{"apikey":"{{secret.apikey}}","product_code":"{{product_code}}","user_id":"{{customer_id}}","server_id":"{{server_id}}","trx_id":"{{ref_id}}","callback_url":"{{callback_url}}"}},
		"status":{"method":"GET","path":"/v1/otomax/status/{{provider_order_id}}","query":{"apikey":"{{secret.apikey}}"}},
		"balance":{"method":"GET","path":"/v1/otomax/user","query":{"apikey":"{{secret.apikey}}"}}
	}`
}

func newTestAdapter(roundTripper http.RoundTripper) *Adapter {
	return New(FFZStoreCode, &fakeProviderRepo{record: &domain.Provider{Code: FFZStoreCode, APIKey: "secret-key", Config: testConfig(), IsActive: true}}, &http.Client{Transport: roundTripper})
}

func response(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestPurchaseParsesPendingAndSendsDocumentedFields(t *testing.T) {
	adapter := newTestAdapter(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query := request.URL.Query()
		if request.URL.Path != "/v1/otomax/order" || query.Get("apikey") != "secret-key" || query.Get("trx_id") != "LOCAL-REF" || query.Get("product_code") != "S1_1187" {
			t.Fatalf("unexpected request URL: %s", request.URL)
		}
		return response("R#123424324 S1_1187.123456789|1234, status PENDING. RefId : ML_1680405885_1234 . Sisa saldo 12345"), nil
	}))

	result, err := adapter.Purchase(context.Background(), provider.PurchaseRequest{RefID: "LOCAL-REF", ProductCode: "S1_1187", CustomerID: "123456789", ServerID: "1234", CallbackURL: "https://merchant.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != provider.StatusPending || result.ProviderOrderID != "ML_1680405885_1234" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPurchaseExistingOrderWaitsForInvoiceNumberInsteadOfNewOrder(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return response("R#123 API S1_1187.123456789(1234), status SUCCESS. Nickname . RefId: ML_1680405885_1234 . Sisa saldo 12345"), nil
	}))

	result, err := adapter.Purchase(context.Background(), provider.PurchaseRequest{RefID: "LOCAL-REF", ProductCode: "S1_1187", CustomerID: "123456789", ExistingProviderOrderID: "ML_1680405885_1234"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != provider.StatusPending || calls != 0 {
		t.Fatalf("result=%+v calls=%d; RefId must not be used as invoice_number", result, calls)
	}
}

func TestStatusCheckUsesCallbackInvoiceNumberNotRefID(t *testing.T) {
	adapter := newTestAdapter(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/otomax/status/INV-456" {
			t.Fatalf("expected documented invoice_number path, got %s", request.URL.Path)
		}
		return response("R#123 API S1_1187.123456789(1234), status SUCCESS. Nickname . RefId: REF-123 . Sisa saldo 12345"), nil
	}))

	result, err := adapter.CheckStatus(context.Background(), provider.StatusRequest{
		RefID: "LOCAL-REF", ProviderOrderID: "REF-123", ProviderData: `{"trx_id":"LOCAL-REF","invoice_number":"INV-456"}`,
	})
	if err != nil || result.Status != provider.StatusSuccess || result.ProviderOrderID != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestBalanceParsesDocumentedPlainText(t *testing.T) {
	adapter := newTestAdapter(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response("TopupKuy. topupkuy@topupkuy.id. plan Platinum Member. balance 1234344"), nil
	}))
	balance, err := adapter.Balance(context.Background())
	if err != nil || balance != 1234344 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
}

func TestCallbackStatusesAreSafe(t *testing.T) {
	adapter := newTestAdapter(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("unexpected") }))
	cases := []struct {
		status string
		want   provider.Status
	}{
		{"SUCCESS", provider.StatusSuccess},
		{"PENDING", provider.StatusPending},
		{"PARTIAL_SUCCESS", provider.StatusPending},
		{"REFUNDED", provider.StatusFailedFinal},
		{"FAILED", provider.StatusFailedFinal},
		{"UNRECOGNIZED", provider.StatusPending},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://merchant.example/callback?trx_id=LOCAL-REF&invoice_number=REMOTE-INVOICE&status="+tc.status+"&sn=Nickname", nil)
			if err != nil {
				t.Fatal(err)
			}
			result, refID, err := adapter.ParseCallback(req)
			if err != nil || refID != "LOCAL-REF" || result.Status != tc.want || result.ProviderOrderID != "REMOTE-INVOICE" {
				t.Fatalf("result=%+v refID=%q err=%v", result, refID, err)
			}
		})
	}
}

func TestResponseStatusMappingUsesFinalOtoMaxDecision(t *testing.T) {
	cases := []struct {
		status string
		want   provider.Status
	}{
		{"SUCCESS", provider.StatusSuccess},
		{"PENDING", provider.StatusPending},
		{"PARTIAL_SUCCESS", provider.StatusPending},
		{"FAILED", provider.StatusFailedFinal},
		{"REFUNDED", provider.StatusFailedFinal},
		{"UNRECOGNIZED", provider.StatusPending},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			result := resultFromStatus(tc.status, "REF-123", "", "provider response", nil, false)
			if result.Status != tc.want {
				t.Fatalf("status=%s mapped=%v want=%v", tc.status, result.Status, tc.want)
			}
		})
	}
}

func TestCallbackWithoutTrxIDDoesNotTreatInvoiceNumberAsRefID(t *testing.T) {
	adapter := newTestAdapter(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("unexpected") }))
	req, err := http.NewRequest(http.MethodGet, "https://merchant.example/callback?invoice_number=INV-456&status=SUCCESS", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, refID, err := adapter.ParseCallback(req); err == nil || result != nil || refID != "" {
		t.Fatalf("result=%+v refID=%q err=%v", result, refID, err)
	}
}

func TestTimeoutIsHeldAndDoesNotRetryPurchase(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, context.DeadlineExceeded
	}))
	result, err := adapter.Purchase(context.Background(), provider.PurchaseRequest{RefID: "LOCAL-REF", ProductCode: "S1_1187", CustomerID: "123", ServerID: "1", CallbackURL: "https://merchant.example/callback"})
	if result != nil || err == nil || calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
	providerErr, ok := err.(*provider.ProviderError)
	if !ok || providerErr.Status != provider.StatusPending || providerErr.Kind != provider.ErrorTimeout {
		t.Fatalf("unexpected provider error: %#v", err)
	}
}

func TestTransportErrorMasksAPIKeyInUnderlyingCause(t *testing.T) {
	adapter := newTestAdapter(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("Get %q: %w", request.URL.String(), context.DeadlineExceeded)
	}))
	_, err := adapter.Purchase(context.Background(), provider.PurchaseRequest{RefID: "LOCAL-REF", ProductCode: "S1_1187", CustomerID: "123", ServerID: "1"})
	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.(*provider.ProviderError).Cause.Error(), "secret-key") {
		t.Fatalf("transport error leaked API key: %#v", err)
	}
}
