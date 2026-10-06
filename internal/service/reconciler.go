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
	MinAge time.Duration

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
		Interval:          time.Minute,
		BatchSize:         20,
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
	log.Printf("[Reconciler] Started. minAge=%s interval=%s batchSize=%d concurrency=%d",
		w.cfg.MinAge, w.cfg.Interval, w.cfg.BatchSize, w.cfg.Concurrency)

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

// runOnce executes one reconciliation cycle: query batch, fan-out, wait.
func (w *ReconcilerWorker) runOnce(ctx context.Context) {
	if w.registry == nil {
		return // registry not initialized, skip silently
	}

	txs, err := w.txRepo.FindProcessingOlderThan(w.cfg.MinAge, w.cfg.BatchSize)
	if err != nil {
		log.Printf("[Reconciler] Error querying stale transactions: %v", err)
		return
	}
	if len(txs) == 0 {
		return
	}

	log.Printf("[Reconciler] Reconciling %d stale transactions.", len(txs))

	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup

	for i := range txs {
		tx := &txs[i]

		// Skip transactions that should not be reconciled (PRD §5)
		if w.shouldSkip(tx) {
			continue
		}

		// Per-provider rate limiting: enforce minimum gap between calls
		pvdCode := resolveProviderCodeFromTx(tx)
		if pvdCode != "" && !w.acquireRateSlot(pvdCode) {
			log.Printf("[Reconciler] Rate-limited provider %s, skipping tx %d this cycle.", pvdCode, tx.ID)
			continue
		}

		// Claim before the provider request. This is a short conditional update,
		// not a lock held across the network call, so another worker can neither
		// duplicate this check nor block normal transaction processing.
		claimed, claimErr := w.txRepo.ClaimProcessingForReconciliation(tx.ID, time.Now().Add(-w.cfg.MinAge))
		if claimErr != nil {
			log.Printf("[Reconciler] Could not claim tx %d: %v", tx.ID, claimErr)
			continue
		}
		if !claimed {
			log.Printf("[Reconciler] tx %d was already claimed or finalized; skipping.", tx.ID)
			continue
		}

		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(tx *domain.Transaction) {
			defer wg.Done()
			defer func() { <-sem }()
			w.reconcileOne(tx)
		}(tx)
	}

	wg.Wait()
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
		log.Printf("[Reconciler] Skipping tx %d (provider status: %q)", tx.ID, status)
		return true
	}

	// Determine if this is a Kiosgamer transaction
	pvdCode := resolveProviderCodeFromTx(tx)
	if strings.EqualFold(pvdCode, "KIOSGAMER") && tx.ProviderOrderID == "" {
		log.Printf("[Reconciler] Skipping Kiosgamer tx %d (no ProviderOrderID)", tx.ID)
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

// acquireRateSlot returns true if the caller may proceed with a CheckStatus
// call for pvdCode (i.e., enough time has elapsed since the last call).
// It updates the last-call timestamp atomically.
func (w *ReconcilerWorker) acquireRateSlot(pvdCode string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	last, seen := w.lastCallByPvd[pvdCode]
	if seen && time.Since(last) < w.cfg.ProviderRateLimit {
		return false
	}
	w.lastCallByPvd[pvdCode] = time.Now()
	return true
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
