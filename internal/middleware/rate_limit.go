package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"topup-backend/internal/pkg/response"
)

type ipVisitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type IPRateLimiter struct {
	mu       sync.RWMutex
	visitors map[string]*ipVisitor
	rate     rate.Limit
	burst    int
}

// NewIPRateLimiter creates a new rate limiter that allows `r` requests per second with a burst of `b`.
func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
	limiter := &IPRateLimiter{
		visitors: make(map[string]*ipVisitor),
		rate:     r,
		burst:    b,
	}

	// Background cleanup to prevent memory leaks from one-off IPs
	go limiter.cleanupLoop(5 * time.Minute)

	return limiter
}

func (i *IPRateLimiter) getVisitor(ip string) *rate.Limiter {
	i.mu.Lock()
	defer i.mu.Unlock()

	v, exists := i.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(i.rate, i.burst)
		i.visitors[ip] = &ipVisitor{
			limiter:  limiter,
			lastSeen: time.Now(),
		}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

// Allow checks if a request from the given IP is allowed under the rate limit
func (i *IPRateLimiter) Allow(ip string) bool {
	return i.getVisitor(ip).Allow()
}

func (i *IPRateLimiter) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		i.mu.Lock()
		for ip, v := range i.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(i.visitors, ip)
			}
		}
		i.mu.Unlock()
	}
}

// RateLimitMiddleware enforces rate limit per client IP
func RateLimitMiddleware(r rate.Limit, burst int) gin.HandlerFunc {
	limiter := NewIPRateLimiter(r, burst)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if ip == "" {
			ip = "unknown"
		}

		if !limiter.Allow(ip) {
			response.Error(c, http.StatusTooManyRequests, "Too many requests. Please slow down.")
			c.Abort()
			return
		}

		c.Next()
	}
}
