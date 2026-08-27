package cache

import (
	"sync"
	"time"
)

type cacheItem struct {
	value      any
	expiration int64 // unix nano
}

func (item *cacheItem) isExpired() bool {
	if item.expiration == 0 {
		return false
	}
	return time.Now().UnixNano() > item.expiration
}

// MemoryCache is a high-performance, thread-safe, low-allocation in-memory cache
type MemoryCache struct {
	mu         sync.RWMutex
	items      map[string]*cacheItem
	defaultTTL time.Duration
	stopEvict  chan struct{}
}

// GlobalCache is the default shared instance for fast app-wide caching
var GlobalCache = NewMemoryCache(5*time.Minute, 1*time.Minute)

// NewMemoryCache creates a new in-memory cache
func NewMemoryCache(defaultTTL, cleanupInterval time.Duration) *MemoryCache {
	c := &MemoryCache{
		items:      make(map[string]*cacheItem),
		defaultTTL: defaultTTL,
		stopEvict:  make(chan struct{}),
	}

	go c.evictionLoop(cleanupInterval)
	return c
}

// Set adds or replaces an item in the cache with a specified TTL
func (c *MemoryCache) Set(key string, val any, ttl time.Duration) {
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).UnixNano()
	} else if c.defaultTTL > 0 {
		exp = time.Now().Add(c.defaultTTL).UnixNano()
	}

	c.mu.Lock()
	c.items[key] = &cacheItem{
		value:      val,
		expiration: exp,
	}
	c.mu.Unlock()
}

// Get retrieves an item from the cache
func (c *MemoryCache) Get(key string) (any, bool) {
	c.mu.RLock()
	item, found := c.items[key]
	c.mu.RUnlock()

	if !found {
		return nil, false
	}

	if item.isExpired() {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return nil, false
	}

	return item.value, true
}

// Delete removes a key from cache
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// DeletePrefix removes all keys starting with prefix
func (c *MemoryCache) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.items, k)
		}
	}
}

// Flush clears all cache items
func (c *MemoryCache) Flush() {
	c.mu.Lock()
	c.items = make(map[string]*cacheItem)
	c.mu.Unlock()
}

func (c *MemoryCache) evictionLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now().UnixNano()
			for k, item := range c.items {
				if item.expiration > 0 && now > item.expiration {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		case <-c.stopEvict:
			ticker.Stop()
			return
		}
	}
}
