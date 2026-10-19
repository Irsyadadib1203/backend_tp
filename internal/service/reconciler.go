package service

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/provider"
	"topup-backend/internal/repository"
)

// ReconcilerConfig holds tunable parameters for the reconciler worker.
// All values must be set before calling Run; zero values are not safe.
type ReconcilerConfig struct {
	// MinAge is the minimum age of a processing transaction before it is
	// eligible for reconciliation. Maps to RECONCILER_MIN_AGE (default 5m).
	// MinAge ... (tetap)
	MinAge time.Duration

	// [TAMBAH] Batas bawah umur untuk query antrean. Isi dengan ReconcileMinAge
	// terkecil di antara provider PollingProvider (ASMarket = 10s). 0 = fitur mati.
	FastMinAge time.Duration

	// [TAMBAH] Berapa lama transaksi dipolling cepat (dihitung dari created_at).
	// Lewat dari ini kembali ke MinAge. 0 = tanpa batas.
	FastPollWindow time.Duration
	// Interval is how often the worker polls for stale transactions.
	// Default: 1 minute.
	Interval time.Duration

	// BatchSize is the maximum number of transactions processed per tick.
	// Default: 20.
	BatchSize int

	// Concurrency is the number of goroutines used to process a batch.
	// Default: 4.
	Concurrency int

	// ProviderRateLimit is the minimum gap between two consecutive CheckStatus
	// calls to the same provider code, to avoid hammering the upstream API.
	// Default: 500ms.
	ProviderRateLimit time.Duration
}

// DefaultReconcilerConfig returns production-safe defaults.
func DefaultReconcilerConfig(minAge time.Duration) ReconcilerConfig {
	return ReconcilerConfig{
		MinAge:            minAge,
		FastMinAge:        10 * time.Second, // [TAMBAH]
		FastPollWindow:    15 * time.Minute, // [TAMBAH]
		Interval:          5 * time.Second,
		BatchSize:         30,
		Concurrency:       4,
		ProviderRateLimit: 500 * time.Millisecond,
	}
}

// ReconcilerWorker polls for stale processing transactions and reconciles them
// by calling CheckProviderStatus (which routes to registry checkProviderStatusRegistry
// and applyResult internally).
//
// PRD §5 Fase 5 requirements implemented here:
//   - Default disabled; enable via RECONCILER_ENABLED=true
//   - Only processes transactions older than RECONCILER_MIN_AGE
//   - Skips "Pending (Harga Naik)", "Konfigurasi Error", Kiosgamer without ProviderOrderID
//   - Timeout/ambiguity → hold (never auto-refund from reconcile path directly)
//   - Rate-limited per provider, bounded concurrency, context-aware graceful shutdown
type ReconcilerWorker struct {
	cfg      ReconcilerConfig
	txRepo   repository.TransactionRepository
	registry *provider.Registry
	txSvc    TransactionService

	mu            sync.Mutex
	lastCallByPvd map[string]time.Time // provider code → last CheckStatus time
}

// NewReconcilerWorker creates a reconciler worker.
// txSvc is used to call CheckProviderStatus, which routes through the
// registry/applyResult boundary.
func NewReconcilerWorker(
	cfg ReconcilerConfig,
	txRepo repository.TransactionRepository,
	registry *provider.Registry,
	txSvc TransactionService,
) *ReconcilerWorker {
	return &ReconcilerWorker{
		cfg:           cfg,
		txRepo:        txRepo,
		registry:      registry,
		txSvc:         txSvc,
		lastCallByPvd: make(map[string]time.Time),
	}
}

// Run starts the reconciler loop. It blocks until ctx is cancelled.
// Call this in a goroutine: go worker.Run(ctx).
func (w *ReconcilerWorker) Run(ctx context.Context) {
	log.Printf("[Reconciler] Started. minAge=%s fastMinAge=%s fastWindow=%s interval=%s batchSize=%d concurrency=%d",
		w.cfg.MinAge, w.cfg.FastMinAge, w.cfg.FastPollWindow, w.cfg.Interval, w.cfg.BatchSize, w.cfg.Concurrency)

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Reconciler] Stopping (context cancelled).")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// queryMinAge: umur terkecil untuk query antrean. Filter per provider
// dilakukan di Go lewat minAgeFor.
func (w *ReconcilerWorker) queryMinAge() time.Duration {
	if w.cfg.FastMinAge > 0 && w.cfg.FastMinAge < w.cfg.MinAge {
		return w.cfg.FastMinAge
	}
	return w.cfg.MinAge
}

// minAgeFor menentukan jeda minimum antar pengecekan untuk satu transaksi.
func (w *ReconcilerWorker) minAgeFor(tx *domain.Transaction, now time.Time) time.Duration {
	// Tanpa order ID asli, jalur reconcile memanggil Purchase (bukan CheckStatus),
	// jadi jangan dipercepat (risiko order ganda).
	if !hasProviderOrder(tx) {
		return w.cfg.MinAge
	}
	code := resolveProviderCodeFromTx(tx)
	if code == "" {
		return w.cfg.MinAge
	}
	p, ok := w.registry.Get(code)
	if !ok {
		return w.cfg.MinAge
	}
	pp, ok := p.(provider.PollingProvider)
	if !ok {
		return w.cfg.MinAge // provider lain: tetap default 1 menit
	}
	age := pp.ReconcileMinAge()
	if age <= 0 || age >= w.cfg.MinAge {
		return w.cfg.MinAge
	}
	// Order yang menggantung lama tidak dicek tiap 10 detik selamanya.
	if w.cfg.FastPollWindow > 0 && now.Sub(tx.CreatedAt) > w.cfg.FastPollWindow {
		return w.cfg.MinAge
	}
	return age
}

type reconcileCandidate struct {
	tx        *domain.Transaction
	dueBefore time.Time
}

// runOnce executes one reconciliation cycle: query batch, fan-out, wait.
func (w *ReconcilerWorker) runOnce(ctx context.Context) {
	if w.registry == nil {
		return
	}

	// [UBAH] sebelumnya: FindReconcileQueue(w.cfg.MinAge, ...)
	queue, err := w.txRepo.FindReconcileQueue(w.queryMinAge(), w.cfg.BatchSize*5)
	if err != nil {
		log.Printf("[Reconciler] Error querying queue: %v", err)
		return
	}

	// [UBAH] Jendela hanya berisi transaksi yang SUDAH jatuh tempo menurut
	// min age provider masing-masing. Yang belum jatuh tempo dilewati tanpa
	// memakai slot.
	now := time.Now() // [TAMBAH]
	window := make([]reconcileCandidate, 0, w.cfg.BatchSize) // [UBAH] tipe slice
	for i := range queue {
		tx := &queue[i]
		if w.shouldSkip(tx) {
			continue
		}
		dueBefore := now.Add(-w.minAgeFor(tx, now)) // [TAMBAH]
		if tx.UpdatedAt.After(dueBefore) {          // [PINDAH] dari loop bawah
			continue
		}
		window = append(window, reconcileCandidate{tx: tx, dueBefore: dueBefore}) // [UBAH]
		if len(window) >= w.cfg.BatchSize {
			break
		}
	}
	if len(window) == 0 {
		return
	}

	// [HAPUS] baris ini: dueBefore := time.Now().Add(-w.cfg.MinAge)

	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup

	for _, c := range window { // [UBAH] sebelumnya: for _, tx := range window
		// [HAPUS] blok ini (pindah ke atas):
		//   if tx.UpdatedAt.After(dueBefore) { continue }

		// [UBAH] pakai c.tx.ID dan c.dueBefore (per transaksi)
		claimed, claimErr := w.txRepo.ClaimProcessingForReconciliation(c.tx.ID, c.dueBefore)
		if claimErr != nil {
			log.Printf("[Reconciler] Could not claim tx %d: %v", c.tx.ID, claimErr)
			continue
		}
		if !claimed {
			continue
		}

		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(tx *domain.Transaction) {
			defer wg.Done()
			defer func() { <-sem }()
			w.waitRateSlot(ctx, resolveProviderCodeFromTx(tx))
			w.reconcileOne(tx)
		}(c.tx) // [UBAH] sebelumnya: }(tx)
	}
	wg.Wait()
}

// waitRateSlot menjeda (bukan melewati) panggilan ke provider yang sama.
func (w *ReconcilerWorker) waitRateSlot(ctx context.Context, pvdCode string) {
	if pvdCode == "" || w.cfg.ProviderRateLimit <= 0 {
		return
	}
	w.mu.Lock()
	at := time.Now()
	if next := w.lastCallByPvd[pvdCode].Add(w.cfg.ProviderRateLimit); next.After(at) {
		at = next
	}
	w.lastCallByPvd[pvdCode] = at
	w.mu.Unlock()

	if d := time.Until(at); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	}
}

// shouldSkip returns true for transactions that must never be reconciled by
// this worker, per PRD §5:
//   - ProviderStatus == "Pending (Harga Naik)"  → waiting for price approval
//   - ProviderStatus == "Konfigurasi Error"       → config broken, no retry
//   - Kiosgamer transactions without ProviderOrderID → CheckRecentOrder heuristic
//     is unreliable for background workers; require manual admin review
func (w *ReconcilerWorker) shouldSkip(tx *domain.Transaction) bool {
	status := tx.ProviderStatus
	if strings.Contains(status, "Harga Naik") || strings.Contains(status, "Konfigurasi Error") {
		return true
	}

	// Determine if this is a Kiosgamer transaction
	pvdCode := resolveProviderCodeFromTx(tx)
	if strings.EqualFold(pvdCode, "KIOSGAMER") && !hasProviderOrder(tx) {
		return true
	}

	return false
}



// resolveProviderCodeFromTx returns the uppercased provider code for a transaction,
// reading from the preloaded Provider relation. Returns "" if not available.
func resolveProviderCodeFromTx(tx *domain.Transaction) string {
	if tx.Provider != nil {
		return strings.ToUpper(tx.Provider.Code)
	}
	return ""
}


// reconcileOne calls the registry-only reconciliation path for a single transaction.
// Errors from the registry/provider are logged but never directly trigger a
// refund — the existing applyResult logic inside CheckProviderStatus handles
// that only for StatusFailedFinal.
//
// PRD §5: "Timeout/ambiguity tidak pernah refund; refund hanya dari StatusFailedFinal."
// This is enforced inside applyResult (StatusFailedHold → processing, no refund).
func (w *ReconcilerWorker) reconcileOne(tx *domain.Transaction) {
	pvdCode := resolveProviderCodeFromTx(tx)
	log.Printf("[Reconciler] Checking tx %d (invoice=%s provider=%s providerOrderID=%q)",
		tx.ID, tx.InvoiceNumber, pvdCode, tx.ProviderOrderID)

	_, err := w.txSvc.ReconcileProcessingTransaction(tx.ID)
	if err != nil {
		log.Printf("[Reconciler] tx %d reconcile error (will remain processing / hold): %v", tx.ID, err)
		// Do NOT trigger refund here. PRD §5: error during reconcile is held,
		// not refunded. applyResult already handles StatusFailedHold → processing.
		return
	}
	log.Printf("[Reconciler] tx %d reconciled successfully.", tx.ID)
}
