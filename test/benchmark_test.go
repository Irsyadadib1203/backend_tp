package test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"topup-backend/internal/middleware"
	"topup-backend/internal/pkg/cache"
	"topup-backend/internal/pkg/worker"
)

// BenchmarkMemoryCache_Set tests the speed of writing to the in-memory cache
func BenchmarkMemoryCache_Set(b *testing.B) {
	c := cache.NewMemoryCache(5*time.Minute, 1*time.Minute)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key:%d", i%1000)
		c.Set(key, "sample_payload_game_data_nominal_12345", 5*time.Minute)
	}
}

// BenchmarkMemoryCache_Get tests the speed of reading from the in-memory cache (Target: < 100ns per op)
func BenchmarkMemoryCache_Get(b *testing.B) {
	c := cache.NewMemoryCache(5*time.Minute, 1*time.Minute)
	for i := 0; i < 1000; i++ {
		c.Set(fmt.Sprintf("key:%d", i), "sample_payload_game_data_nominal_12345", 5*time.Minute)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key:%d", i%1000)
			_, _ = c.Get(key)
			i++
		}
	})
}

// BenchmarkWorkerPool_Throughput tests concurrent task execution through the bounded worker pool
func BenchmarkWorkerPool_Throughput(b *testing.B) {
	pool := worker.NewWorkerPool(30, 2000)
	defer pool.Stop(1 * time.Second)

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		pool.Submit(func() {
			// Simulate quick CPU work (e.g. signature check / state validation)
			_ = 100 * 200
			wg.Done()
		})
	}
	wg.Wait()
}

// BenchmarkRateLimiter tests the speed of the in-memory token bucket rate limiter
func BenchmarkRateLimiter(b *testing.B) {
	limiter := middleware.NewIPRateLimiter(rate.Limit(10000), 20000)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		ip := "192.168.1.100"
		for pb.Next() {
			_ = limiter.Allow(ip)
		}
	})
}

