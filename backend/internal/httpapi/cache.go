package httpapi

import (
	"sync"
	"time"
)

// responseCache holds rendered JSON keyed by request shape.
//
// The data only changes once per probe cycle — every five minutes — so
// recomputing a 3-month percentile sweep per request would mean the database is
// hit at whatever rate the internet feels like. With the cache the DB is
// touched once per distinct query per TTL, and a scraper costs a map lookup.
//
// Deliberately a plain map with a TTL rather than an LRU: the key space is the
// cross product of a fixed window allow-list, a fixed model list and a fixed
// metric allow-list — a few dozen entries at most, all of them bounded by
// server-side allow-lists rather than by caller input.
type responseCache struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	body      []byte
	expiresAt time.Time
}

func newResponseCache(ttl time.Duration) *responseCache {
	return &responseCache{ttl: ttl, now: time.Now, entries: map[string]cacheEntry{}}
}

// get returns a cached body if it is still fresh.
func (c *responseCache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[key]
	if !ok || c.now().After(e.expiresAt) {
		return nil, false
	}
	return e.body, true
}

func (c *responseCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{body: body, expiresAt: c.now().Add(c.ttl)}
}

// invalidate drops everything. Called when a cycle lands, so the dashboard
// reflects a new measurement immediately rather than up to a TTL later —
// which matters most during an incident, when the page is being reloaded.
func (c *responseCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}
