package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBurstThenDeny(t *testing.T) {
	l := New(1, 3)
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return base }

	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d within the burst must be allowed", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Error("the fourth request must be denied; the burst is 3")
	}
}

func TestRefillOverTime(t *testing.T) {
	l := New(2, 2) // 2/sec
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	l.Allow("ip")
	l.Allow("ip")
	if l.Allow("ip") {
		t.Fatal("bucket should be empty")
	}

	now = now.Add(time.Second) // +2 tokens
	if !l.Allow("ip") || !l.Allow("ip") {
		t.Error("tokens must refill with elapsed time")
	}
	if l.Allow("ip") {
		t.Error("refill must be capped at the burst size")
	}
}

// One caller must not be able to exhaust another's budget.
func TestBucketsArePerKey(t *testing.T) {
	l := New(1, 1)
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return base }

	if !l.Allow("1.1.1.1") {
		t.Fatal("first caller must be allowed")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("first caller is now out of budget")
	}
	if !l.Allow("2.2.2.2") {
		t.Error("a different caller must have its own bucket")
	}
}

// The bucket map is keyed by client IP, so without a sweep it is an unbounded
// allocation the internet controls.
func TestSweepDropsIdleBuckets(t *testing.T) {
	l := New(1, 1)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	l.Allow("old")
	now = now.Add(time.Hour)
	l.Allow("fresh")

	if l.Len() != 2 {
		t.Fatalf("tracking %d buckets, want 2", l.Len())
	}
	dropped := l.Sweep(30 * time.Minute)
	if dropped != 1 {
		t.Errorf("swept %d buckets, want 1", dropped)
	}
	if l.Len() != 1 {
		t.Errorf("%d buckets remain, want 1", l.Len())
	}
}

func TestMiddlewareReturns429(t *testing.T) {
	l := New(0.0001, 1)
	l.now = func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) }

	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	req.RemoteAddr = "9.9.9.9:1234"

	first := httptest.NewRecorder()
	h.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, req)
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("a 429 must tell the caller when to retry")
	}
}

// The FIRST X-Forwarded-For entry is caller-supplied and trivially spoofed.
// Keying on it would let one scraper mint unlimited identities and defeat the
// limit entirely, so the LAST entry — appended by the nearest trusted proxy —
// is the one used.
func TestClientIPUsesTheLastForwardedEntryNotTheFirst(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	req.RemoteAddr = "10.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2, 3.3.3.3")

	if got := ClientIP(req); got != "3.3.3.3" {
		t.Errorf("ClientIP = %q, want 3.3.3.3 (the proxy-appended entry)", got)
	}
}

func TestClientIPSpoofingCannotMintIdentities(t *testing.T) {
	l := New(0.0001, 1)
	l.now = func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) }
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// Same real client, varying the spoofable leading entry each time.
	var denied bool
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
		req.RemoteAddr = "10.0.0.1:5000"
		req.Header.Set("X-Forwarded-For", string(rune('a'+i))+".fake, 3.3.3.3")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			denied = true
			break
		}
	}
	if !denied {
		t.Error("spoofing the leading X-Forwarded-For entry defeated the rate limit")
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	req.RemoteAddr = "192.0.2.5:41234"

	if got := ClientIP(req); got != "192.0.2.5" {
		t.Errorf("ClientIP = %q, want 192.0.2.5", got)
	}
}

// A per-address bucket bounds nobody over IPv6: the smallest allocation anyone
// gets is a /64, so rotating the low 64 bits is free and mints an unlimited
// supply of identities — for the request limiter and for the SSE stream cap,
// which keys on the same function.
func TestClientIPCollapsesIPv6ToItsPrefix(t *testing.T) {
	cases := []struct {
		name, remote, xff, want string
	}{
		{"ipv4 is untouched", "203.0.113.9:443", "", "203.0.113.9"},
		{"ipv4 via xff is untouched", "10.0.0.1:443", "1.2.3.4, 203.0.113.9", "203.0.113.9"},
		{"ipv6 is masked to /64", "[2001:db8:1:2:3:4:5:6]:443", "", "2001:db8:1:2::/64"},
		{"ipv6 via xff is masked to /64", "10.0.0.1:443", "2001:db8:1:2:aaaa:bbbb:cccc:dddd", "2001:db8:1:2::/64"},
		{"an unparseable value is kept as the key", "not-an-address", "", "not-an-address"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(req); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}

	// The point of the mask: two addresses in one allocation must share a bucket.
	a := httptest.NewRequest(http.MethodGet, "/", nil)
	a.RemoteAddr = "[2001:db8:1:2::1]:443"
	b := httptest.NewRequest(http.MethodGet, "/", nil)
	b.RemoteAddr = "[2001:db8:1:2:ffff:ffff:ffff:ffff]:443"
	if ClientIP(a) != ClientIP(b) {
		t.Error("two addresses in the same /64 must land in one bucket, or the limit is free to evade")
	}

	// ...and a different /64 must not, or one caller's burst would throttle
	// unrelated visitors.
	c := httptest.NewRequest(http.MethodGet, "/", nil)
	c.RemoteAddr = "[2001:db8:1:3::1]:443"
	if ClientIP(a) == ClientIP(c) {
		t.Error("different /64s must be different buckets")
	}
}
