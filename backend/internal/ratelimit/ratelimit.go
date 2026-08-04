// Package ratelimit bounds what one caller can take from a public,
// unauthenticated API.
//
// Without it a single scraper pins the server: /api/* is open by design, and
// the SSE endpoint holds a connection open indefinitely, so the two limits here
// answer different questions — how fast may you ask, and how many of you may
// stay.
package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Limiter is a per-IP token bucket.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New builds a limiter allowing `burst` requests immediately and refilling at
// `rate` per second.
func New(rate, burst float64) *Limiter {
	return &Limiter{
		rate: rate, burst: burst,
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// Allow reports whether a request from key may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	// Refill for elapsed time, capped at burst.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Sweep drops buckets untouched for longer than idle.
//
// Without it the map is an unbounded, caller-controlled allocation: one IP per
// entry, and the internet has plenty of those. A full bucket is
// indistinguishable from a new one, so dropping it loses nothing.
func (l *Limiter) Sweep(idle time.Duration) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-idle)
	var n int
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
			n++
		}
	}
	return n
}

// Len reports how many buckets are tracked, for tests and diagnostics.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Middleware rejects over-rate requests with 429.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			// Set explicitly rather than via http.Error, which would label a
			// JSON body as text/plain and break a client that parses every
			// /api/* response as JSON.
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the caller's address.
//
// X-Forwarded-For is read because this runs behind Traefik, which sets it — but
// only the LAST entry, not the first. The first is caller-supplied and trivially
// spoofed, so keying the bucket on it would let one scraper mint unlimited
// identities and defeat the limit entirely. The last entry is the one the
// nearest trusted proxy appended.
// An IPv6 caller is keyed by its /64 rather than its full address. A single
// residential or hosting allocation is a /64 at minimum and usually far larger,
// so a per-address bucket bounds nobody over v6: rotating the low 64 bits is
// free and mints a fresh identity per request. /64 is the smallest unit that
// is not free to rotate. It is also the smallest unit routinely assigned to
// one subscriber, so it does not merge unrelated callers the way a /48 would.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := splitAndTrim(xff)
		if len(parts) > 0 {
			return normaliseIP(parts[len(parts)-1])
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return normaliseIP(host)
	}
	return normaliseIP(r.RemoteAddr)
}

// normaliseIP collapses an IPv6 address to its /64 prefix and leaves everything
// else alone.
//
// IPv4 is returned as-is: addresses there are scarce enough that one is a real
// identity, and masking would merge a whole /24 of unrelated callers into one
// bucket — punishing a shared office network for one bad neighbour.
//
// An unparseable value is returned unchanged. It cannot be a spoofed prefix
// (nothing parses it as an address downstream either) and keying on the literal
// string still bounds whoever keeps sending it.
func normaliseIP(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return raw
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	// The /64 prefix, rendered with the host bits zeroed so the key is stable
	// regardless of which address in the block arrived.
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func splitAndTrim(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
