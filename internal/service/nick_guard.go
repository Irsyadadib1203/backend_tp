package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RateLimitError dikembalikan kalau sebuah pengirim terlalu sering memicu cek baru.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("terlalu sering, coba lagi dalam %d detik", int(e.RetryAfter.Seconds())+1)
}

// IsRateLimited memudahkan handler mengecek jenis error.
func IsRateLimited(err error) (*RateLimitError, bool) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return rl, true
	}
	return nil, false
}

type cacheEntry struct {
	res *NickResult
	exp time.Time
}

type inflightCall struct {
	done chan struct{}
	res  *NickResult
	err  error
}

// GuardedChecker membungkus NickChecker dengan:
//   - cache per userId:zoneId (cache hit tidak membuat order baru di mobapay)
//   - dedupe request yang sedang berjalan untuk key yang sama
//   - rate limit per kunci pemanggil (hanya cek yang benar-benar menembak mobapay dihitung)
//   - batas konkurensi global agar IP server tidak dibanjiri
type GuardedChecker struct {
	inner *NickChecker

	ttl         time.Duration // TTL hasil lengkap
	shortTTL    time.Duration // TTL kalau ada status Unknown (mobapay gagal sebagian)
	maxPerChat  int           // maks cek baru per window per kunci pemanggil
	window      time.Duration
	callTimeout time.Duration

	sem chan struct{}

	mu        sync.Mutex
	cache     map[string]cacheEntry
	inflight  map[string]*inflightCall
	hits      map[int64][]time.Time
	lastSweep time.Time
}

func NewGuardedChecker(inner *NickChecker) *GuardedChecker {
	return &GuardedChecker{
		inner:       inner,
		ttl:         45 * time.Second,
		shortTTL:    10 * time.Second,
		maxPerChat:  3,
		window:      time.Minute,
		callTimeout: 35 * time.Second,
		sem:         make(chan struct{}, 2),
		cache:       map[string]cacheEntry{},
		inflight:    map[string]*inflightCall{},
		hits:        map[int64][]time.Time{},
		lastSweep:   time.Now(),
	}
}

// Check dipanggil dari handler /cn. limitKey = ID pengirim Telegram.
func (g *GuardedChecker) Check(ctx context.Context, limitKey int64, userID, zoneID string) (*NickResult, error) {
	if !reDigits.MatchString(userID) || !reDigits.MatchString(zoneID) {
		return nil, fmt.Errorf("format ID/Server tidak valid")
	}
	key := userID + ":" + zoneID
	now := time.Now()

	g.mu.Lock()
	g.sweepLocked(now)

	// 1. Cache hit: tidak menyentuh mobapay, tidak kena rate limit.
	if e, ok := g.cache[key]; ok && now.Before(e.exp) {
		g.mu.Unlock()
		return e.res, nil
	}

	// 2. Sudah ada request yang sama berjalan: tunggu hasilnya.
	if c, ok := g.inflight[key]; ok {
		g.mu.Unlock()
		select {
		case <-c.done:
			return c.res, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// 3. Rate limit per pengirim (sliding window).
	recent := g.hits[limitKey][:0]
	for _, t := range g.hits[limitKey] {
		if now.Sub(t) < g.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= g.maxPerChat {
		g.hits[limitKey] = recent
		retry := g.window - now.Sub(recent[0])
		g.mu.Unlock()
		return nil, &RateLimitError{RetryAfter: retry}
	}
	g.hits[limitKey] = append(recent, now)

	c := &inflightCall{done: make(chan struct{})}
	g.inflight[key] = c
	g.mu.Unlock()

	// 4. Eksekusi sebenarnya. Context dilepas dari request pemanggil supaya
	// pengguna lain yang menunggu key sama tidak ikut gagal kalau pemanggil batal.
	c.res, c.err = g.run(userID, zoneID)

	g.mu.Lock()
	delete(g.inflight, key)
	if c.err == nil && c.res != nil {
		ttl := g.ttl
		if hasUnknown(c.res) {
			ttl = g.shortTTL
		}
		g.cache[key] = cacheEntry{res: c.res, exp: time.Now().Add(ttl)}
	}
	g.mu.Unlock()
	close(c.done)

	return c.res, c.err
}

func (g *GuardedChecker) run(userID, zoneID string) (*NickResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.callTimeout)
	defer cancel()

	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.inner.Check(ctx, userID, zoneID)
}

func hasUnknown(r *NickResult) bool {
	return r.WeeklyPass == LimitUnknown ||
		r.WeeklyElite == LimitUnknown ||
		r.MonthlyElite == LimitUnknown
}

// sweepLocked membersihkan entri kedaluwarsa paling sering sekali per menit.
func (g *GuardedChecker) sweepLocked(now time.Time) {
	if now.Sub(g.lastSweep) < time.Minute {
		return
	}
	g.lastSweep = now
	for k, e := range g.cache {
		if now.After(e.exp) {
			delete(g.cache, k)
		}
	}
	for id, ts := range g.hits {
		keep := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < g.window {
				keep = append(keep, t)
			}
		}
		if len(keep) == 0 {
			delete(g.hits, id)
		} else {
			g.hits[id] = keep
		}
	}
}