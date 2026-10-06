package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
	"topup-backend/internal/service"
)

type characterizationCallbackDigiflazz struct {
	payload   *service.DigiflazzCallbackPayload
	err       error
	signature string
}

func (d *characterizationCallbackDigiflazz) GetPriceList() ([]service.DigiflazzPriceListItem, error) {
	return nil, nil
}
func (d *characterizationCallbackDigiflazz) CheckBalance() (float64, error) { return 0, nil }
func (d *characterizationCallbackDigiflazz) CreateTransaction(string, string, string, bool) (*service.DigiflazzTransactionResponse, error) {
	return nil, nil
}
func (d *characterizationCallbackDigiflazz) CheckTransactionStatus(string, string, string) (*service.DigiflazzTransactionResponse, error) {
	return nil, nil
}
func (d *characterizationCallbackDigiflazz) ProcessCallback(_ []byte, signature string) (*service.DigiflazzCallbackPayload, error) {
	d.signature = signature
	return d.payload, d.err
}

type characterizationCallbackTransactionService struct {
	handleErr error
	called    bool
}

func (s *characterizationCallbackTransactionService) CreateOrder(*service.CreateOrderRequest) (*domain.Transaction, error) {
	return nil, nil
}
func (s *characterizationCallbackTransactionService) GetByInvoice(string) (*domain.Transaction, error) {
	return nil, nil
}
func (s *characterizationCallbackTransactionService) ListRecent(int) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *characterizationCallbackTransactionService) ListUserTransactions(uint, int, int) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (s *characterizationCallbackTransactionService) ListAdminTransactions(int, int, string, string, string, string) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (s *characterizationCallbackTransactionService) GetDashboardStats() (map[string]interface{}, error) {
	return nil, nil
}
func (s *characterizationCallbackTransactionService) FulfillOrder(*domain.Transaction) error {
	return nil
}
func (s *characterizationCallbackTransactionService) HandleDigiflazzCallback(*service.DigiflazzCallbackPayload) error {
	s.called = true
	return s.handleErr
}
func (s *characterizationCallbackTransactionService) HandleProviderCallback(string, *http.Request) (*domain.Transaction, *provider.Result, error) {
	s.called = true
	return nil, nil, s.handleErr
}
func (s *characterizationCallbackTransactionService) HandlePaymentSuccess(string, string, float64) error {
	return nil
}
func (s *characterizationCallbackTransactionService) ManualRetry(uint) error { return nil }
func (s *characterizationCallbackTransactionService) CheckProviderStatus(uint) (*domain.Transaction, error) {
	return nil, nil
}
func (s *characterizationCallbackTransactionService) ReconcileProcessingTransaction(uint) (*domain.Transaction, error) {
	return nil, nil
}
func (s *characterizationCallbackTransactionService) ManualSetSuccess(uint, string, string) error {
	return nil
}
func (s *characterizationCallbackTransactionService) ManualRefund(uint, string) error               { return nil }
func (s *characterizationCallbackTransactionService) SetTripayService(service.TripayChannelService) {}

func callbackPayload() *service.DigiflazzCallbackPayload {
	p := &service.DigiflazzCallbackPayload{}
	p.Data.RefID, p.Data.Status = "REF-CALLBACK", "Sukses"
	return p
}

func TestDigiflazzBuyerHandler_LegacyCallbackHTTPResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		processErr error
		handleErr  error
		wantCode   int
		wantBody   string
		wantCalled bool
	}{
		{"signature invalid is bad request", errors.New("invalid signature"), nil, http.StatusBadRequest, "invalid signature", false},
		{"valid signature processing error is acknowledged", nil, errors.New("transaction not found for callback"), http.StatusOK, "Callback received but handling returned", true},
		{"valid callback success response", nil, nil, http.StatusOK, "Digiflazz callback processed successfully", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			digi := &characterizationCallbackDigiflazz{payload: callbackPayload(), err: tc.processErr}
			txSvc := &characterizationCallbackTransactionService{handleErr: tc.handleErr}
			router := gin.New()
			router.POST("/callback/digiflazz", NewDigiflazzBuyerHandler(digi, txSvc).HandleCallback)
			req := httptest.NewRequest(http.MethodPost, "/callback/digiflazz", strings.NewReader(`{"data":{}}`))
			req.Header.Set("X-Digiflazz-Delivery", "fallback-signature")
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.wantCode || !strings.Contains(res.Body.String(), tc.wantBody) || txSvc.called != tc.wantCalled || digi.signature != "fallback-signature" {
				t.Fatalf("code=%d body=%s called=%v signature=%q", res.Code, res.Body.String(), txSvc.called, digi.signature)
			}
		})
	}
}
