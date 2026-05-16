package session

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

type cacheEntry struct {
	val       any
	expiresAt time.Time
}

// SessionCache stores ephemeral process-local values with TTL-based expiration.
//
// Values SHOULD be immutable or externally synchronized. SessionCache does
// NOT serialize its contents — values are lost on process exit.
type SessionCache struct {
	closed  atomic.Bool
	mu      sync.RWMutex
	entries map[model.ID]*cacheEntry
	stop    chan struct{}
	wg      sync.WaitGroup
}

// NewSessionCache creates a cache with periodic TTL sweep.
// If cleanupInterval is zero or negative, sweeping is disabled.
func NewSessionCache(cleanupInterval time.Duration) *SessionCache {
	c := &SessionCache{
		entries: make(map[model.ID]*cacheEntry),
		stop:    make(chan struct{}),
	}
	if cleanupInterval <= 0 {
		return c
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.sweep()
			}
		}
	}()
	return c
}

// CachePut stores a value with TTL. No-op after Close.
func (c *SessionCache) CachePut(key model.ID, val any, ttl time.Duration) {
	if c.closed.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &cacheEntry{
		val:       val,
		expiresAt: time.Now().Add(ttl),
	}
}

// CacheGet returns a value if present and not expired.
// Expired entries are reported as missing (lazy-expire).
// Actual cleanup happens in the sweep goroutine.
func (c *SessionCache) CacheGet(key model.ID) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.val, true
}

// Close stops the background sweep and waits for its completion.
// Idempotent — subsequent calls are no-ops.
// After Close, CachePut is a no-op.
func (c *SessionCache) Close() {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}
	close(c.stop)
	c.wg.Wait()
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

// sweep removes expired entries. Called from the background goroutine.
func (c *SessionCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	maps.DeleteFunc(c.entries, func(_ model.ID, e *cacheEntry) bool {
		return now.After(e.expiresAt)
	})
}
