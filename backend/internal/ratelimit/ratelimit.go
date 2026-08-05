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

// Allow reports whether a request from key may proceed, consuming a token when
// it does.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refilled(key)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Permitted reports whether key has a token left WITHOUT consuming one.
//
// It exists for the misbehaviour limiter, where the request being gated is not
// the request being charged for: a caller pays for the 404s it caused, not for
// the dashboard it is loading. Consuming here instead would put a second,
// invisible budget on ordinary traffic — one page load is a dozen asset
// requests, and they would drain a bucket sized for a handful of 404s.
func (l *Limiter) Permitted(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.refilled(key).tokens >= 1
}

// Charge debits cost tokens from key whether or not any remain, letting the
// bucket run negative down to -burst.
//
// Debt is the point: a bucket floored at zero forgives a scanner after one
// refill interval no matter how many more paths it sprays, so the block would
// be the same length for the tenth probe as for the ten-thousandth. Letting it
// go negative makes continued spraying extend the block, and the floor keeps
// that bounded — the worst case is 2*burst/rate, not forever.
func (l *Limiter) Charge(key string, cost float64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refilled(key)
	b.tokens -= cost
	if b.tokens < -l.burst {
		b.tokens = -l.burst
	}
}

// refilled returns key's bucket, created full if new and topped up for the time
// elapsed since it was last touched. Caller must hold l.mu.
func (l *Limiter) refilled(key string) *bucket {
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
	return b
}

// Sweep drops buckets untouched for longer than idle that have refilled to
// full in the meantime.
//
// Without it the map is an unbounded, caller-controlled allocation: one IP per
// entry, and the internet has plenty of those. A full bucket is
// indistinguishable from a new one, so dropping it loses nothing — but a bucket
// still in debt is NOT, and deleting one would hand a scanner a clean slate
// simply for pausing. Hence the refill check rather than age alone.
func (l *Limiter) Sweep(idle time.Duration) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-idle)
	var n int
	for k, b := range l.buckets {
		if !b.last.Before(cutoff) {
			continue
		}
		if b.tokens+now.Sub(b.last).Seconds()*l.rate < l.burst {
			continue
		}
		delete(l.buckets, k)
		n++
	}
	return n
}

// MaxBlock is the longest a caller can be denied: the time it takes a bucket at
// the debt floor to climb back to one token.
//
// It exists so a Retry-After can be honest without repeating the limiter's
// numbers somewhere else, where the two would drift apart the first time one of
// them was tuned. A caller told to come back sooner than this comes back to
// another rejection.
func (l *Limiter) MaxBlock() time.Duration {
	if l.rate <= 0 {
		return 0
	}
	return time.Duration((l.burst + 1) / l.rate * float64(time.Second))
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
