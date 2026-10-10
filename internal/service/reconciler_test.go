package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

// ---- Fakes ----

// fakeReconcilerTxRepo is a minimal TransactionRepository fake for reconciler tests.
type fakeReconcilerTxRepo struct {
	processing []domain.Transaction
	queryErr   error
	updates    []uint
	mu         sync.Mutex
	claims     map[uint]bool
}

func (r *fakeReconcilerTxRepo) FindByRefIDAndUserID(
    refID string,
    userID uint,
) (*domain.Transaction, error) {
    return nil, nil
}

func (r *fakeReconcilerTxRepo) Create(*domain.Transaction) error { return nil }
func (r *fakeReconcilerTxRepo) FindByID(id uint) (*domain.Transaction, error) {
	for i := range r.processing {
		if r.processing[i].ID == id {
			return &r.processing[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (r *fakeReconcilerTxRepo) FindByInvoiceNumber(string) (*domain.Transaction, error) {
	return nil, errors.New("not found")
}
func (r *fakeReconcilerTxRepo) FindByRefID(string) (*domain.Transaction, error) {
	return nil, errors.New("not found")
}
func (r *fakeReconcilerTxRepo) FindByIdempotencyKey(string) (*domain.Transaction, error) {
	return nil, errors.New("not found")
}
func (r *fakeReconcilerTxRepo) Update(tx *domain.Transaction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, tx.ID)
	return nil
}
func (r *fakeReconcilerTxRepo) UpdateStatus(uint, domain.TransactionStatus, string) error {
	return nil
}
func (r *fakeReconcilerTxRepo) MarkAsProcessingIfPending(uint, string, time.Time) (bool, error) {
	return false, nil
}
func (r *fakeReconcilerTxRepo) FindProcessingOlderThan(minAge time.Duration, batchSize int) ([]domain.Transaction, error) {
	if r.queryErr != nil {
		return nil, r.queryErr
	}
	if batchSize > 0 && len(r.processing) > batchSize {
		return r.processing[:batchSize], nil
	}
	return r.processing, nil
}
func (r *fakeReconcilerTxRepo) ClaimProcessingForReconciliation(id uint, _ time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claims == nil {
		r.claims = make(map[uint]bool)
	}
	if r.claims[id] {
		return false, nil
	}
	r.claims[id] = true
	return true, nil
}
func (r *fakeReconcilerTxRepo) FindProcessingBalanceHolds(uint, int) ([]domain.Transaction, error) {
	return nil, nil
}
func (r *fakeReconcilerTxRepo) ListRecent(int) ([]domain.Transaction, error) { return nil, nil }
func (r *fakeReconcilerTxRepo) ListByUser(uint, int, int) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (r *fakeReconcilerTxRepo) ListAdmin(int, int, string, string, string, string) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (r *fakeReconcilerTxRepo) GetDashboardStats() (map[string]interface{}, error) { return nil, nil }

// reconcilerTestSvc is a minimal TransactionService fake for reconciler tests.
// Only CheckProviderStatus tracking is meaningful; all other methods are stubs.
type reconcilerTestSvc struct {
	mu         sync.Mutex
	checkCalls []uint
	checkErr   error
}

func (s *reconcilerTestSvc) CreateOrder(*CreateOrderRequest) (*domain.Transaction, error) {
	return nil, nil
}
func (s *reconcilerTestSvc) GetByInvoice(string) (*domain.Transaction, error) { return nil, nil }
func (s *reconcilerTestSvc) ListRecent(int) ([]domain.Transaction, error)     { return nil, nil }
func (s *reconcilerTestSvc) ListUserTransactions(uint, int, int) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (s *reconcilerTestSvc) ListAdminTransactions(int, int, string, string, string, string) ([]domain.Transaction, int64, error) {
	return nil, 0, nil
}
func (s *reconcilerTestSvc) GetDashboardStats() (map[string]interface{}, error) { return nil, nil }
func (s *reconcilerTestSvc) FulfillOrder(*domain.Transaction) error             { return nil }
func (s *reconcilerTestSvc) HandleDigiflazzCallback(*DigiflazzCallbackPayload) error {
	return nil
}
func (s *reconcilerTestSvc) HandleProviderCallback(string, *http.Request) (*domain.Transaction, *provider.Result, error) {
	return nil, nil, nil
}
func (s *reconcilerTestSvc) HandlePaymentSuccess(string, string, float64) error { return nil }
func (s *reconcilerTestSvc) ManualRetry(uint) error                             { return nil }
func (s *reconcilerTestSvc) CheckProviderStatus(id uint) (*domain.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkCalls = append(s.checkCalls, id)
	if s.checkErr != nil {
		return nil, s.checkErr
	}
	return &domain.Transaction{ID: id, Status: domain.StatusSuccess}, nil
}
func (s *reconcilerTestSvc) ReconcileProcessingTransaction(id uint) (*domain.Transaction, error) {
	return s.CheckProviderStatus(id)
}
func (s *reconcilerTestSvc) ManualSetSuccess(uint, string, string) error { return nil }
func (s *reconcilerTestSvc) ManualRefund(uint, string) error             { return nil }
func (s *reconcilerTestSvc) SetTripayService(TripayChannelService)       {}

// Verify that reconcilerTestSvc satisfies the service interface at compile time.
var _ TransactionService = (*reconcilerTestSvc)(nil)

// ---- Helpers ----

func newReconcilerWithFakes(txs []domain.Transaction, svc TransactionService) (*ReconcilerWorker, *fakeReconcilerTxRepo) {
	repo := &fakeReconcilerTxRepo{processing: txs}
	reg := provider.NewRegistry()
	cfg := ReconcilerConfig{
		MinAge:            5 * time.Minute,
		Interval:          time.Hour, // long so auto-tick never fires in test
		BatchSize:         20,
		Concurrency:       2,
		ProviderRateLimit: 0, // no rate limit in tests
	}
	w := NewReconcilerWorker(cfg, repo, reg, svc)
	return w, repo
}

func digiflazzTx(id uint) domain.Transaction {
	return domain.Transaction{
		ID:            id,
		InvoiceNumber: "INV-" + string(rune('0'+id)),
		RefID:         "REF-" + string(rune('0'+id)),
		Status:        domain.StatusProcessing,
		Provider:      &domain.Provider{Code: "DIGIFLAZZ"},
	}
}

// ---- Tests ----

// TestReconciler_DisabledFlagYieldsNoEffect verifies that when reconcilerEnabled=false
// the worker is never started and has zero effect (PRD §5 criterion 2).
func TestReconciler_DisabledFlagYieldsNoEffect(t *testing.T) {
	// If no reconciler is started, nothing calls CheckProviderStatus.
	// This is enforced in main.go: reconciler goroutine is not launched when disabled.
	// Here we test that runOnce with an empty registry does nothing harmful.
	svc := &reconcilerTestSvc{}
	w, _ := newReconcilerWithFakes([]domain.Transaction{digiflazzTx(1)}, svc)
	// Set registry to nil to simulate disabled state
	w.registry = nil

	w.runOnce(context.Background())

	if len(svc.checkCalls) != 0 {
		t.Fatalf("expected no CheckProviderStatus calls when registry=nil, got %d", len(svc.checkCalls))
	}
}

// TestReconciler_SuccessCase verifies that a stale processing transaction gets
// reconciled via CheckProviderStatus (PRD §5 criterion 1).
func TestReconciler_SuccessCase(t *testing.T) {
	svc := &reconcilerTestSvc{}
	txs := []domain.Transaction{digiflazzTx(10)}
	w, _ := newReconcilerWithFakes(txs, svc)

	w.runOnce(context.Background())

	if len(svc.checkCalls) != 1 || svc.checkCalls[0] != 10 {
		t.Fatalf("expected CheckProviderStatus called for tx 10, calls=%v", svc.checkCalls)
	}
}

// TestReconciler_SkipsHargaNaik verifies that a transaction held with "Pending (Harga Naik)"
// is not reconciled (PRD §5: "Lewati Pending (Harga Naik)").
func TestReconciler_SkipsHargaNaik(t *testing.T) {
	svc := &reconcilerTestSvc{}
	tx := digiflazzTx(20)
	tx.ProviderStatus = "Pending (Harga Naik)"
	w, _ := newReconcilerWithFakes([]domain.Transaction{tx}, svc)

	w.runOnce(context.Background())

	if len(svc.checkCalls) != 0 {
		t.Fatalf("expected no check for Harga Naik tx, got %d calls", len(svc.checkCalls))
	}
}

// TestReconciler_SkipsKonfigurasiError verifies that a transaction in "Konfigurasi Error"
// is never reconciled (PRD §5: "Lewati Konfigurasi Error").
func TestReconciler_SkipsKonfigurasiError(t *testing.T) {
	svc := &reconcilerTestSvc{}
	tx := digiflazzTx(21)
	tx.ProviderStatus = "Konfigurasi Error"
	w, _ := newReconcilerWithFakes([]domain.Transaction{tx}, svc)

	w.runOnce(context.Background())

	if len(svc.checkCalls) != 0 {
		t.Fatalf("expected no check for Konfigurasi Error tx, got %d calls", len(svc.checkCalls))
	}
}

// TestReconciler_SkipsKiosgamerWithoutOrderID verifies that a Kiosgamer transaction
// without ProviderOrderID is never reconciled (PRD §5).
func TestReconciler_SkipsKiosgamerWithoutOrderID(t *testing.T) {
	svc := &reconcilerTestSvc{}
	tx := domain.Transaction{
		ID:              30,
		Status:          domain.StatusProcessing,
		Provider:        &domain.Provider{Code: "KIOSGAMER"},
		ProviderOrderID: "", // no order ID → skip
	}
	w, _ := newReconcilerWithFakes([]domain.Transaction{tx}, svc)

	w.runOnce(context.Background())

	if len(svc.checkCalls) != 0 {
		t.Fatalf("expected no check for Kiosgamer without OrderID, got %d calls", len(svc.checkCalls))
	}
}

// TestReconciler_KiosgamerWithOrderIDIsReconciled verifies that a Kiosgamer transaction
// WITH a ProviderOrderID IS reconciled.
func TestReconciler_KiosgamerWithOrderIDIsReconciled(t *testing.T) {
	svc := &reconcilerTestSvc{}
	tx := domain.Transaction{
		ID:              31,
		Status:          domain.StatusProcessing,
		Provider:        &domain.Provider{Code: "KIOSGAMER"},
		ProviderOrderID: "KG-ORDER-123", // has order ID → reconcile
	}
	w, _ := newReconcilerWithFakes([]domain.Transaction{tx}, svc)

	w.runOnce(context.Background())

	if len(svc.checkCalls) != 1 || svc.checkCalls[0] != 31 {
		t.Fatalf("expected check for Kiosgamer tx 31, calls=%v", svc.checkCalls)
	}
}

// TestReconciler_NetworkErrorDoesNotRefund verifies that a network error from
// CheckProviderStatus does NOT cause a refund — the transaction remains in processing.
// PRD §5: "Timeout/ambiguity tidak pernah refund."
func TestReconciler_NetworkErrorDoesNotRefund(t *testing.T) {
	svc := &reconcilerTestSvc{
		checkErr: errors.New("connection timeout"),
	}
	txs := []domain.Transaction{digiflazzTx(40)}
	w, repo := newReconcilerWithFakes(txs, svc)

	w.runOnce(context.Background())

	// CheckProviderStatus was called but returned an error
	if len(svc.checkCalls) != 1 {
		t.Fatalf("expected 1 checkCalls, got %d", len(svc.checkCalls))
	}
	// The repo.updates should NOT include a status change to failed/refund
	// (we can't check refund in unit test, but we verify no Update was called on tx)
	if len(repo.updates) != 0 {
		t.Fatalf("expected no repo updates on network error (hold behavior), got %d", len(repo.updates))
	}
}

func TestReconciler_ProviderErrorDoesNotStopBatch(t *testing.T) {
	svc := &reconcilerTestSvc{checkErr: errors.New("provider unavailable")}
	w, _ := newReconcilerWithFakes([]domain.Transaction{digiflazzTx(41), digiflazzTx(42)}, svc)

	w.runOnce(context.Background())

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.checkCalls) != 2 {
		t.Fatalf("expected both transactions to be attempted after provider errors, calls=%v", svc.checkCalls)
	}
}

// TestReconciler_QueryErrorIsHandledGracefully verifies that a DB error on
// FindProcessingOlderThan does not panic and is logged gracefully.
func TestReconciler_QueryErrorIsHandledGracefully(t *testing.T) {
	svc := &reconcilerTestSvc{}
	repo := &fakeReconcilerTxRepo{queryErr: errors.New("database connection lost")}
	reg := provider.NewRegistry()
	cfg := ReconcilerConfig{
		MinAge:      5 * time.Minute,
		Interval:    time.Hour,
		BatchSize:   20,
		Concurrency: 2,
	}
	w := NewReconcilerWorker(cfg, repo, reg, svc)

	// Should not panic
	w.runOnce(context.Background())

	if len(svc.checkCalls) != 0 {
		t.Fatalf("expected no check on query error, got %d calls", len(svc.checkCalls))
	}
}

// TestReconciler_BatchSizeIsRespected verifies that at most batchSize transactions
// are processed per cycle.
func TestReconciler_BatchSizeIsRespected(t *testing.T) {
	svc := &reconcilerTestSvc{}
	txs := make([]domain.Transaction, 10)
	for i := range txs {
		txs[i] = digiflazzTx(uint(100 + i))
	}
	repo := &fakeReconcilerTxRepo{processing: txs}
	reg := provider.NewRegistry()
	cfg := ReconcilerConfig{
		MinAge:      5 * time.Minute,
		Interval:    time.Hour,
		BatchSize:   3, // only 3 per cycle
		Concurrency: 2,
	}
	w := NewReconcilerWorker(cfg, repo, reg, svc)

	w.runOnce(context.Background())

	if len(svc.checkCalls) > 3 {
		t.Fatalf("expected at most 3 CheckProviderStatus calls (batchSize=3), got %d", len(svc.checkCalls))
	}
}

func TestReconcilerConcurrentRunsClaimTransactionOnce(t *testing.T) {
	svc := &reconcilerTestSvc{}
	w, _ := newReconcilerWithFakes([]domain.Transaction{digiflazzTx(77)}, svc)

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runOnce(context.Background())
		}()
	}
	wg.Wait()

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.checkCalls) != 1 || svc.checkCalls[0] != 77 {
		t.Fatalf("expected exactly one claimed reconciliation, calls=%v", svc.checkCalls)
	}
}

// TestReconciler_ContextCancellationStopsWorker verifies that cancelling context
// stops the Run loop promptly.
func TestReconciler_ContextCancellationStopsWorker(t *testing.T) {
	svc := &reconcilerTestSvc{}
	w, _ := newReconcilerWithFakes(nil, svc)
	// Use a very short interval so it would tick quickly
	w.cfg.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Cancel after a brief moment
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK: Run exited after cancel
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop within 2s after context cancel")
	}
}

// TestReconcilerShouldSkip_AllCases is a table-driven test for shouldSkip logic.
func TestReconcilerShouldSkip_AllCases(t *testing.T) {
	svc := &reconcilerTestSvc{}
	w, _ := newReconcilerWithFakes(nil, svc)

	cases := []struct {
		name     string
		tx       domain.Transaction
		wantSkip bool
	}{
		{
			name:     "normal digiflazz tx",
			tx:       domain.Transaction{ID: 1, Status: domain.StatusProcessing, Provider: &domain.Provider{Code: "DIGIFLAZZ"}},
			wantSkip: false,
		},
		{
			name:     "harga naik",
			tx:       domain.Transaction{ID: 2, ProviderStatus: "Pending (Harga Naik)", Provider: &domain.Provider{Code: "DIGIFLAZZ"}},
			wantSkip: true,
		},
		{
			name:     "konfigurasi error",
			tx:       domain.Transaction{ID: 3, ProviderStatus: "Konfigurasi Error", Provider: &domain.Provider{Code: "KIOSGAMER"}},
			wantSkip: true,
		},
		{
			name:     "kiosgamer no order id",
			tx:       domain.Transaction{ID: 4, Provider: &domain.Provider{Code: "KIOSGAMER"}, ProviderOrderID: ""},
			wantSkip: true,
		},
		{
			name:     "kiosgamer with order id",
			tx:       domain.Transaction{ID: 5, Provider: &domain.Provider{Code: "KIOSGAMER"}, ProviderOrderID: "ORD-001"},
			wantSkip: false,
		},
		{
			name:     "no provider preloaded",
			tx:       domain.Transaction{ID: 6},
			wantSkip: false, // no skip rule applies; provider code="" which is not KIOSGAMER
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := w.shouldSkip(&tc.tx)
			if got != tc.wantSkip {
				t.Errorf("shouldSkip(%q) = %v, want %v", tc.name, got, tc.wantSkip)
			}
		})
	}
}

// Verify that fakeReconcilerTxRepo satisfies the repository interface at compile time.
var _ repository.TransactionRepository = (*fakeReconcilerTxRepo)(nil)
