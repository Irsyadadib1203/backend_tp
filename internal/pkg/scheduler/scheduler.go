package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"topup-backend/internal/service"
)

type AutoSyncScheduler struct {
	gameService service.GameService
	interval    time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	stopOnce    sync.Once
}

func NewAutoSyncScheduler(gameService service.GameService, interval time.Duration) *AutoSyncScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &AutoSyncScheduler{
		gameService: gameService,
		interval:    interval,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins the periodic auto-sync in the background
func (s *AutoSyncScheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("[Scheduler] Background Auto-Sync Scheduler started (Interval: %v)", s.interval)

		// Wait 1 minute after server boot before running the first sync to allow DB & network to warm up
		select {
		case <-time.After(1 * time.Minute):
			s.runSync()
		case <-s.ctx.Done():
			return
		}

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.runSync()
			case <-s.ctx.Done():
				log.Println("[Scheduler] Background Auto-Sync Scheduler stopped.")
				return
			}
		}
	}()
}

func (s *AutoSyncScheduler) runSync() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Scheduler] Recovered from panic in auto-sync: %v", r)
		}
	}()

	count, err := s.gameService.AutoSyncAllPrices()
	if err != nil {
		// Log warning silently without stopping the scheduler
		log.Printf("[Scheduler] Auto-sync skipped/warn: %v", err)
		return
	}

	if count > 0 {
		log.Printf("[Scheduler] Auto-sync completed successfully: %d products updated with latest base prices & status.", count)
	}
}

// Stop gracefully terminates the scheduler
func (s *AutoSyncScheduler) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
	})
}

// ---------------------------------------------------------------------------
// KiosgamerKeepAliveScheduler
// Memanggil KeepAlive secara berkala untuk menjaga sesi & cookie Kiosgamer
// tetap aktif tanpa harus input manual setiap hari.
// ---------------------------------------------------------------------------

type KiosgamerKeepAliveScheduler struct {
	kiosgamerService service.KiosgamerService
	interval         time.Duration
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	stopOnce         sync.Once
}

// ProviderBalanceRetryService is deliberately narrow so the scheduler cannot
// perform arbitrary transaction mutations.
type ProviderBalanceRetryService interface {
	RetryDigiflazzBalanceHolds() (int, error)
}

type ProviderBalanceRetryScheduler struct {
	service  ProviderBalanceRetryService
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func NewProviderBalanceRetryScheduler(service ProviderBalanceRetryService, interval time.Duration) *ProviderBalanceRetryScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProviderBalanceRetryScheduler{service: service, interval: interval, ctx: ctx, cancel: cancel}
}

func (s *ProviderBalanceRetryScheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("[ProviderBalanceRetry] Scheduler started (interval: %s)", s.interval)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				count, err := s.service.RetryDigiflazzBalanceHolds()
				if err != nil {
					log.Printf("[ProviderBalanceRetry] Digiflazz check skipped: %v", err)
				} else if count > 0 {
					log.Printf("[ProviderBalanceRetry] Resubmitted %d Digiflazz balance-held transaction(s)", count)
				}
			case <-s.ctx.Done():
				log.Println("[ProviderBalanceRetry] Scheduler stopped.")
				return
			}
		}
	}()
}

func (s *ProviderBalanceRetryScheduler) Stop() { s.stopOnce.Do(func() { s.cancel(); s.wg.Wait() }) }

func NewKiosgamerKeepAliveScheduler(kiosgamerService service.KiosgamerService, interval time.Duration) *KiosgamerKeepAliveScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &KiosgamerKeepAliveScheduler{
		kiosgamerService: kiosgamerService,
		interval:         interval,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start begins the periodic keep-alive heartbeat in the background
func (k *KiosgamerKeepAliveScheduler) Start() {
	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		log.Printf("[KiosgamerKeepAlive] Scheduler started (Interval: %v)", k.interval)

		// Jalankan langsung saat server boot (tanpa delay) agar cookie langsung ter-sync
		k.runKeepAlive()

		ticker := time.NewTicker(k.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				k.runKeepAlive()
			case <-k.ctx.Done():
				log.Println("[KiosgamerKeepAlive] Scheduler stopped.")
				return
			}
		}
	}()
}

func (k *KiosgamerKeepAliveScheduler) runKeepAlive() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[KiosgamerKeepAlive] Recovered from panic: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(k.ctx, 60*time.Second)
	defer cancel()

	if err := k.kiosgamerService.KeepAlive(ctx); err != nil {
		log.Printf("[KiosgamerKeepAlive] Warning: %v", err)
		return
	}
}

// Stop gracefully terminates the keep-alive scheduler
func (k *KiosgamerKeepAliveScheduler) Stop() {
	k.stopOnce.Do(func() {
		k.cancel()
		k.wg.Wait()
	})
}

// ---------------------------------------------------------------------------
// PriceSyncScheduler
// Sinkron harga per provider ke provider_products.cost_price, dengan interval
// masing-masing provider (env PRICE_SYNC_INTERVALS, mis. "DIGIFLAZZ:1m,KIOSGAMER:15m").
// Satu goroutine per provider; di dalamnya berjalan berurutan, jadi sinkron
// provider yang sama tidak pernah tumpang tindih.
// ---------------------------------------------------------------------------

type PriceSyncScheduler struct {
	svc       service.PriceSyncService
	intervals map[string]time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	stopOnce  sync.Once
}

func NewPriceSyncScheduler(svc service.PriceSyncService, intervals map[string]time.Duration) *PriceSyncScheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &PriceSyncScheduler{svc: svc, intervals: intervals, ctx: ctx, cancel: cancel}
}

func (s *PriceSyncScheduler) Start() {
	if len(s.intervals) == 0 {
		log.Println("[PriceSync] PRICE_SYNC_INTERVALS kosong, scheduler sinkron harga tidak dijalankan")
		return
	}
	for code, every := range s.intervals {
		s.wg.Add(1)
		go s.loop(code, every)
	}
}

func (s *PriceSyncScheduler) loop(code string, every time.Duration) {
	defer s.wg.Done()
	log.Printf("[PriceSync] %s dijadwalkan tiap %v", code, every)

	// Beri jeda setelah server menyala, lalu sinkron pertama.
	select {
	case <-time.After(30 * time.Second):
		s.runOnce(code, every)
	case <-s.ctx.Done():
		return
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runOnce(code, every)
		case <-s.ctx.Done():
			log.Printf("[PriceSync] %s dihentikan", code)
			return
		}
	}
}

func (s *PriceSyncScheduler) runOnce(code string, every time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PriceSync] %s: panic dipulihkan: %v", code, r)
		}
	}()

	timeout := 2 * time.Minute
	if every < timeout {
		timeout = every
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()

	if err := s.svc.SyncProvider(ctx, code); err != nil {
		log.Printf("[PriceSync] %s gagal: %v", code, err)
	}
}

// Stop menghentikan semua goroutine sinkron harga dengan rapi.
func (s *PriceSyncScheduler) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
	})
}
