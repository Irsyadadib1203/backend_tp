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
