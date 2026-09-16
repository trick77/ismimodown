package httpapi

import (
	"errors"
	"sync"
	"time"
)

// errBuildAbandoned is what a waiter sees when the build it waited on
// panicked out from under it.
var errBuildAbandoned = errors.New("dashboard build abandoned")

// responseCache holds rendered JSON keyed by request shape.
//
// The data only changes once per probe cycle — every five minutes — so
// recomputing a 3-month percentile sweep per request would mean the database is
// hit at whatever rate the internet feels like. With the cache the DB is
// touched once per window per cycle, and a scraper costs a map lookup.
//
// Deliberately a plain map with a TTL rather than an LRU: the key space is the
// window allow-list — five entries, all of them bounded by server-side
// allow-lists rather than by caller input.
//
// A miss is coalesced, twice over. Concurrent misses on ONE key wait for the
// single build already running for it, and builds on DIFFERENT keys queue
// behind one semaphore. Both were measured missing on the live site: five
// concurrent cold requests for the 30d window each took 19 s where one took
// 2.8 s, because each ran the full sixty-query build on a container capped at
// one CPU. The natural trigger is not a scraper but the page itself — every
// cycle dropped the cache and then told every open tab to refetch in the same
// instant, so every tab was a cold miss and the cost scaled with the tab count.
type responseCache struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.RWMutex
	entries map[string]cacheEntry

	// flights are the builds in progress, one per key. A waiter blocks on the
	// call's done channel and reads the result the first caller wrote.
	flightMu sync.Mutex
	flights  map[string]*flight

	// build admits one build at a time, whatever the key. A second CPU would
	// not help: compose.yaml pins the container to one, and two 3mo sweeps
	// sharing it finish later than one after the other would.
	build chan struct{}
}

type cacheEntry struct {
	body      []byte
	expiresAt time.Time
}

type flight struct {
	done chan struct{}
	body []byte
	err  error
}

func newResponseCache(ttl time.Duration) *responseCache {
	return &responseCache{
		ttl: ttl, now: time.Now,
		entries: map[string]cacheEntry{},
		flights: map[string]*flight{},
		build:   make(chan struct{}, 1),
	}
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

// getOrBuild serves the cached body, or builds it exactly once however many
// callers ask at the same time.
//
// A caller that arrives while a build for its key is in flight waits for that
// build's result rather than starting its own. A caller that starts a build
// first queues for the build slot, then checks the cache AGAIN before doing
// any work: a Warm that landed while it queued has already answered it.
func (c *responseCache) getOrBuild(key string, build func() ([]byte, error)) ([]byte, error) {
	if body, ok := c.get(key); ok {
		return body, nil
	}

	c.flightMu.Lock()
	if f, ok := c.flights[key]; ok {
		c.flightMu.Unlock()
		<-f.done
		return f.body, f.err
	}
	f := &flight{done: make(chan struct{})}
	c.flights[key] = f
	c.flightMu.Unlock()

	c.build <- struct{}{}
	// Deferred, not sequenced after the build: a panic inside build() is
	// caught by the recovery middleware one frame up, and without this it
	// would leave the slot held and the flight open — every later miss on
	// any key blocks on the slot, and the next Warm blocks the scheduler.
	defer func() {
		<-c.build
		c.flightMu.Lock()
		delete(c.flights, key)
		c.flightMu.Unlock()
		close(f.done)
	}()

	if body, ok := c.get(key); ok {
		f.body = body
		return f.body, nil
	}
	// Overwritten by build() on every path but a panic, where the waiters
	// would otherwise read a nil body with a nil error and send an empty 200.
	f.err = errBuildAbandoned
	f.body, f.err = build()
	if f.err == nil {
		c.put(key, f.body)
	}
	return f.body, f.err
}

// invalidate drops everything. The fallback when a warm-up fails: the next
// request for each window builds it on demand, coalesced as above.
func (c *responseCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}
