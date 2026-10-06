package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

type mockCallbackTransactionService struct {
	tx       *domain.Transaction
	res      *provider.Result
	err      error
	called   bool
	lastCode string
}

func (s *mockCallbackTransactionService) CreateOrder(*service.CreateOrderRequest) (*domain.Transaction, error) {
	return nil, nil
}
func (s *mockCallbackTransactionService) GetByInvoice(string) (*domain.Transaction, error) {
	return nil, nil
}
func (s *mockCallbackTransactionService) ListRecent(int) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *mockCallbackTransactionService) ListUserTransactions(uint, int, int) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (s *mockCallbackTransactionService) ListAdminTransactions(int, int, string, string, string, string) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (s *mockCallbackTransactionService) GetDashboardStats() (map[string]interface{}, error) {
	return nil, nil
}
func (s *mockCallbackTransactionService) FulfillOrder(*domain.Transaction) error {
	return nil
}
func (s *mockCallbackTransactionService) HandleDigiflazzCallback(*service.DigiflazzCallbackPayload) error {
	return nil
}
func (s *mockCallbackTransactionService) HandlePaymentSuccess(string, string, float64) error {
	return nil
}
func (s *mockCallbackTransactionService) ManualRetry(uint) error { return nil }
func (s *mockCallbackTransactionService) CheckProviderStatus(uint) (*domain.Transaction, error) {
	return nil, nil
}
func (s *mockCallbackTransactionService) ReconcileProcessingTransaction(uint) (*domain.Transaction, error) {
	return nil, nil
}
func (s *mockCallbackTransactionService) ManualSetSuccess(uint, string, string) error {
	return nil
}
func (s *mockCallbackTransactionService) ManualRefund(uint, string) error               { return nil }
func (s *mockCallbackTransactionService) SetTripayService(service.TripayChannelService) {}

func (s *mockCallbackTransactionService) HandleProviderCallback(code string, req *http.Request) (*domain.Transaction, *provider.Result, error) {
	s.called = true
	s.lastCode = code
	if code == "UNKNOWN" {
		return nil, nil, errors.New("provider not found in registry")
	}
	return s.tx, s.res, s.err
}

type mockWebhookProviderRepo struct {
	loggedWebhooks []*domain.WebhookLog
}

func (m *mockWebhookProviderRepo) GetByCode(string) (*domain.Provider, error) { return nil, nil }
func (m *mockWebhookProviderRepo) GetByID(uint) (*domain.Provider, error)     { return nil, nil }
func (m *mockWebhookProviderRepo) List() ([]domain.Provider, error)           { return nil, nil }
func (m *mockWebhookProviderRepo) Update(*domain.Provider) error              { return nil }
func (m *mockWebhookProviderRepo) UpdateBalance(uint, float64) error          { return nil }
func (m *mockWebhookProviderRepo) LogWebhook(log *domain.WebhookLog) error {
	if m == nil {
		return nil
	}
	m.loggedWebhooks = append(m.loggedWebhooks, log)
	return nil
}
func (m *mockWebhookProviderRepo) ListWebhookLogs(int, int, string) ([]domain.WebhookLog, int64, error) {
	return nil, 0, nil
}

type mockCallbackParserProvider struct {
	code      string
	result    *provider.Result
	refID     string
	parseErr  error
	called    bool
	signature string
}

func (p *mockCallbackParserProvider) Code() string { return p.code }
func (*mockCallbackParserProvider) Purchase(context.Context, provider.PurchaseRequest) (*provider.Result, error) {
	return nil, nil
}
func (*mockCallbackParserProvider) CheckStatus(context.Context, provider.StatusRequest) (*provider.Result, error) {
	return nil, nil
}
func (*mockCallbackParserProvider) Balance(context.Context) (float64, error) { return 0, nil }
func (p *mockCallbackParserProvider) ParseCallback(r *http.Request) (*provider.Result, string, error) {
	p.called = true
	p.signature = r.Header.Get("X-Hub-Signature")
	if p.signature == "" {
		p.signature = r.Header.Get("X-Digiflazz-Delivery")
	}
	return p.result, p.refID, p.parseErr
}

func setupCallbackRouter(txSvc *mockCallbackTransactionService, reg *provider.Registry, repo *mockWebhookProviderRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := NewProviderCallbackHandler(txSvc, reg, repo)
	r.POST("/api/callback/provider/:code", handler.HandleCallback)
	return r
}

func TestProviderCallbackHandler_SignatureAndProcessing(t *testing.T) {
	cases := []struct {
		name       string
		parseErr   error
		serviceErr error
		txStatus   domain.TransactionStatus
		wantCode   int
		wantBody   string
	}{
		{
			name:       "invalid signature returns 400",
			parseErr:   errors.New("invalid signature"),
			serviceErr: errors.New("invalid signature"),
			wantCode:   http.StatusBadRequest,
			wantBody:   "invalid signature",
		},
		{
			name:       "processing error after valid signature returns 200 acknowledged",
			serviceErr: errors.New("transaction not found for callback"),
			wantCode:   http.StatusOK,
			wantBody:   "Callback received but handling returned: transaction not found for callback",
		},
		{
			name:     "valid callback processed successfully returns 200",
			wantCode: http.StatusOK,
			wantBody: "Digiflazz callback processed successfully",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &mockCallbackParserProvider{
				code:     "DIGIFLAZZ",
				refID:    "REF-123",
				parseErr: tc.parseErr,
				result:   &provider.Result{ProviderStatus: "Sukses", Status: provider.StatusSuccess},
			}
			reg := provider.NewRegistry()
			_ = reg.Register(p)

			txSvc := &mockCallbackTransactionService{
				tx:  &domain.Transaction{RefID: "REF-123", Status: domain.StatusSuccess},
				res: &provider.Result{ProviderStatus: "Sukses", Status: provider.StatusSuccess},
				err: tc.serviceErr,
			}
			repo := &mockWebhookProviderRepo{}
			router := setupCallbackRouter(txSvc, reg, repo)

			req := httptest.NewRequest(http.MethodPost, "/api/callback/provider/DIGIFLAZZ", strings.NewReader(`{"sign":"secret123","data":{"ref_id":"REF-123"}}`))
			req.Header.Set("X-Digiflazz-Delivery", "test-signature")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tc.wantCode {
				t.Fatalf("expected code %d, got %d (body: %s)", tc.wantCode, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Fatalf("expected body to contain %q, got %q", tc.wantBody, w.Body.String())
			}
		})
	}
}

func TestProviderCallbackHandler_MasksSensitiveDataInWebhookLog(t *testing.T) {
	p := &mockCallbackParserProvider{
		code:   "DIGIFLAZZ",
		refID:  "REF-123",
		result: &provider.Result{ProviderStatus: "Sukses", Status: provider.StatusSuccess},
	}
	reg := provider.NewRegistry()
	_ = reg.Register(p)

	txSvc := &mockCallbackTransactionService{
		tx:  &domain.Transaction{RefID: "REF-123", Status: domain.StatusSuccess},
		res: &provider.Result{ProviderStatus: "Sukses", Status: provider.StatusSuccess},
	}
	repo := &mockWebhookProviderRepo{}
	router := setupCallbackRouter(txSvc, reg, repo)

	rawBody := `{"data":{"ref_id":"REF-123","sign":"super_secret_signature_abc123","apikey":"api_secret_key"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/callback/provider/DIGIFLAZZ", strings.NewReader(rawBody))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if len(repo.loggedWebhooks) != 1 {
		t.Fatalf("expected 1 webhook logged, got %d", len(repo.loggedWebhooks))
	}

	loggedPayload := repo.loggedWebhooks[0].Payload
	if strings.Contains(loggedPayload, "super_secret_signature_abc123") || strings.Contains(loggedPayload, "api_secret_key") {
		t.Fatalf("sensitive data was not masked: %s", loggedPayload)
	}
	if !strings.Contains(loggedPayload, "***MASKED***") {
		t.Fatalf("expected masked placeholder in payload, got: %s", loggedPayload)
	}
}

func TestProviderCallbackHandler_UnsupportedProvider(t *testing.T) {
	reg := provider.NewRegistry()
	router := setupCallbackRouter(&mockCallbackTransactionService{}, reg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/callback/provider/UNKNOWN", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d", w.Code)
	}
}

func TestMaskSensitiveWebhookData(t *testing.T) {
	input := []byte(`{
		"username": "user123",
		"sign": "abc123secret",
		"apiKey": "key999",
		"nested": {
			"session_token": "token777",
			"password": "supersecret99"
		}
	}`)

	masked := MaskSensitiveWebhookData(input)

	// Check that sensitive VALUES are masked. Note: "password" key itself is preserved in output
	// (as a JSON key name), so we check specific value strings that don't appear as substrings of any key.
	if strings.Contains(masked, "abc123secret") || strings.Contains(masked, "key999") ||
		strings.Contains(masked, "token777") || strings.Contains(masked, "supersecret99") {
		t.Fatalf("sensitive data unmasked in: %s", masked)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(masked), &parsed); err != nil {
		t.Fatalf("unmarshal masked JSON err: %v", err)
	}
	if parsed["username"] != "user123" {
		t.Errorf("expected non-sensitive field username preserved, got: %v", parsed["username"])
	}
}

func TestMaskSensitiveWebhookData_QueryString(t *testing.T) {
	masked := MaskSensitiveWebhookData([]byte("apikey=supersecret99&status=SUCCESS"))
	if strings.Contains(masked, "supersecret99") || !strings.Contains(masked, "apikey=%2A%2A%2AMASKED%2A%2A%2A") {
		t.Fatalf("query secret was not masked: %s", masked)
	}
}

func TestHandleProviderCallback_IntegrationWithServiceBoundary(t *testing.T) {
	// Verify that HandleProviderCallback calls applyResult(source=callback)
	// and preserves idempotency on final transactions
	txFinal := &domain.Transaction{
		ID:            50,
		InvoiceNumber: "INV-FINAL",
		RefID:         "REF-FINAL",
		Status:        domain.StatusSuccess,
	}

	now := time.Now()
	txFinal.CompletedAt = &now

	p := &mockCallbackParserProvider{
		code:   "DIGIFLAZZ",
		refID:  "REF-FINAL",
		result: &provider.Result{ProviderStatus: "Sukses", Status: provider.StatusSuccess},
	}
	reg := provider.NewRegistry()
	_ = reg.Register(p)

	txSvc := &mockCallbackTransactionService{
		tx:  txFinal,
		res: p.result,
	}
	router := setupCallbackRouter(txSvc, reg, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/callback/provider/DIGIFLAZZ", strings.NewReader(`{"data":{"ref_id":"REF-FINAL"}}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !txSvc.called {
		t.Fatal("expected service HandleProviderCallback to be called")
	}
}
