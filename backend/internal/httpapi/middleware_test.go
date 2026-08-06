package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/trick77/ismimodown/internal/ratelimit"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ismimodown/web"
)

// The logging middleware wraps the ResponseWriter, and a wrapper that swallows
// Flush would leave every pushed cycle sitting in the buffer — the /api/events
// stream would appear to work while the dashboard only updated on reload. This
// is the one middleware behaviour phase 4 hard-depends on.
func TestStatusRecorderForwardsFlush(t *testing.T) {
	var flushed bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped writer must still be an http.Flusher; SSE depends on it")
		}
		_, _ = w.Write([]byte("event: cycle\n\n"))
		f.Flush()
		flushed = true
	})

	// A neutral path, not /api/events: that is a real route now, and this test
	// is about the middleware wrapper rather than the SSE handler.
	srv := New(Deps{DB: openTestDB(t), Static: handler})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream-probe", nil))

	if !flushed {
		t.Error("Flush was not reachable through the middleware chain")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// http.ResponseController walks Unwrap; without it, deadline control and other
// controller operations fail on any wrapped writer.
func TestStatusRecorderUnwraps(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner}

	if got := rec.Unwrap(); got != http.ResponseWriter(inner) {
		t.Error("Unwrap must return the underlying writer")
	}
}

// A handler that writes without calling WriteHeader still produced a 200, and
// the log line must say 200 rather than 0.
func TestStatusRecorderDefaultsToOKOnBareWrite(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}

	if _, err := rec.Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.status != http.StatusOK {
		t.Errorf("status = %d, want 200 inferred from a bare Write", rec.status)
	}
}

func TestStatusRecorderCapturesExplicitStatus(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}

	rec.WriteHeader(http.StatusTeapot)

	if rec.status != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.status, http.StatusTeapot)
	}
}

// /healthz is hit by the container healthcheck every 60s and would otherwise be
// most of the log volume on an idle server, drowning the requests that matter.
func TestHealthzIsNotLogged(t *testing.T) {
	// Exercised for its side-effect-free path: the handler must still answer
	// normally while bypassing the logging wrapper.
	srv := New(Deps{DB: openTestDB(t)})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// The site is a public page with no auth, no cookies and no user input, so
// these headers are hardening rather than a patch for a live hole — but they
// are the difference between "cannot be framed or sniffed" as a property and as
// a coincidence. Asserted on the API, the SPA and an error alike, because the
// middleware sits where all three pass through.
func TestSecurityHeadersAreOnEveryResponse(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{
		Version: "test", DB: db, Samples: samples.New(db),
		Models: []string{"mimo-v2.5"},
	})

	want := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	}

	for _, path := range []string{"/healthz", "/api/dashboard?window=24h", "/api/nope", "/does-not-exist"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		for name, value := range want {
			if got := rec.Header().Get(name); got != value {
				t.Errorf("%s: %s = %q, want %q", path, name, got, value)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("%s: CSP = %q, want frame-ancestors and script-src locked to self", path, csp)
		}
		// An inline-script escape hatch would undo the half of the policy that
		// matters: script-src is what leaves an injected string nowhere to run.
		if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") {
			t.Errorf("%s: CSP must not allow inline or eval'd script: %q", path, csp)
		}
		// Clarity's tag is the ONE third-party origin the policy admits, and it
		// is admitted by name. A second one arriving unremarked is the way a
		// policy like this rots, so pin the whole directive rather than probe
		// it for substrings.
		if !strings.Contains(csp, "script-src 'self' https://www.clarity.ms;") {
			t.Errorf("%s: script-src = %q, want 'self' plus clarity.ms and nothing else", path, csp)
		}
	}
}

// Clarity loads its tag from www.clarity.ms and beacons to whichever *.clarity.ms
// shard it is load-balanced onto, plus c.bing.com. Both halves are needed: with
// script-src alone the tag runs and every upload is blocked, which looks like a
// working install and produces an empty dashboard.
func TestClarityOriginsAreAllowed(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{Version: "test", DB: db, Samples: samples.New(db)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	csp := rec.Header().Get("Content-Security-Policy")

	for _, want := range []string{
		"script-src 'self' https://www.clarity.ms",
		"connect-src 'self' https://*.clarity.ms https://c.bing.com",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP = %q, want it to contain %q", csp, want)
		}
	}

	// default-src stays 'self'. Clarity's own documentation suggests widening
	// that instead, but every directive this policy cares about is set
	// explicitly, so a wildcard there would only loosen the ones nobody listed.
	if !strings.Contains(csp, "default-src 'self';") {
		t.Errorf("CSP = %q, want default-src left at 'self'", csp)
	}
}

// HSTS is the TLS terminator's to set: this process serves plain HTTP and does
// not know whether anything in front of it is doing TLS at all. Emitting it here
// would be wrong on a dev server and redundant in production.
func TestNoHSTSFromTheApplication(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{Version: "test", DB: db, Samples: samples.New(db)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q; it belongs on the TLS edge", got)
	}
}

// No CORS headers anywhere. The API is public to read in a browser tab, not to
// be embedded in someone else's page — and an Access-Control-Allow-Origin added
// for convenience is how that distinction gets lost.
func TestNoCORSHeaders(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{Version: "test", DB: db, Samples: samples.New(db), Models: []string{"mimo-v2.5"}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?window=24h", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)

	for _, name := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
	} {
		if got := rec.Header().Get(name); got != "" {
			t.Errorf("%s = %q, want unset", name, got)
		}
	}
}

// getFrom issues one request from a named caller and returns the recorder.
func getFrom(t *testing.T, h http.Handler, path, from string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = from + ":40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// scannerServer is a server whose every unknown path 404s (Static is nil), with
// a 404 budget of three and effectively no refill.
func scannerServer(t *testing.T) http.Handler {
	t.Helper()
	return NewServer(Deps{
		Version:         "test",
		DB:              openTestDB(t),
		NotFoundLimiter: ratelimit.New(0.0001, 3),
	})
}

// The point of the whole thing: a caller walking a wordlist is cut off, and it
// happens on the non-API surface the request limiter never sees.
func TestNotFoundsThrottleTheCaller(t *testing.T) {
	h := scannerServer(t)

	for i, path := range []string{"/wp-login.php", "/.env", "/config.json"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404 while the budget lasts", i, path, code)
		}
	}
	if code := getFrom(t, h, "/wp-config.php.bak", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth probe = %d, want 429", code)
	}
	// And being cut off means cut off, not merely barred from more 404s.
	if code := getFrom(t, h, "/", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the page itself = %d, want 429 for a throttled caller", code)
	}
	if code := getFrom(t, h, "/wp-login.php", "10.10.10.10").Code; code != http.StatusNotFound {
		t.Errorf("an unrelated caller = %d, want 404; budgets are per caller", code)
	}
}

// An unmatched /api path is a 404 like any other, and a scanner walking API
// names must pay for it too.
func TestUnknownAPIPathsAreCharged(t *testing.T) {
	h := scannerServer(t)

	for i := 0; i < 3; i++ {
		if code := getFrom(t, h, "/api/v1/users", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d = %d, want 404", i, code)
		}
	}
	if code := getFrom(t, h, "/api/v1/users", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth probe = %d, want 429", code)
	}
}

// The gate must never spend the budget it guards. A page load is a dozen asset
// requests, and if any of them cost a token the dashboard would throttle its
// own visitors on a budget sized for a handful of 404s.
func TestSuccessfulRequestsAreNotCharged(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	h := NewServer(Deps{
		Version:         "test",
		DB:              openTestDB(t),
		Static:          static,
		NotFoundLimiter: ratelimit.New(0.0001, 1), // a budget of exactly one 404
	})

	for i := 0; i < 50; i++ {
		if code := getFrom(t, h, "/assets/index-abc123.js", "9.9.9.9").Code; code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200; serving the site must cost nothing", i, code)
		}
	}
}

// A health probe that fails because someone else scanned the box restarts a
// healthy container.
func TestHealthzIsNeverThrottled(t *testing.T) {
	h := scannerServer(t)

	for i := 0; i < 6; i++ {
		getFrom(t, h, "/.env", "9.9.9.9")
	}
	if code := getFrom(t, h, "/", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Fatalf("caller = %d, want 429; the test needs a throttled caller", code)
	}
	if code := getFrom(t, h, "/healthz", "9.9.9.9").Code; code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 even for a throttled caller", code)
	}
}

// A 429 is a response like any other: it carries the security headers, it tells
// the caller when to come back, and on /api/* it stays JSON for a client that
// parses every API response as such.
func TestThrottledResponseShape(t *testing.T) {
	h := scannerServer(t)
	for i := 0; i < 4; i++ {
		getFrom(t, h, "/.env", "9.9.9.9")
	}

	api := getFrom(t, h, "/api/dashboard", "9.9.9.9")
	if api.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", api.Code)
	}
	if ct := api.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("/api/* Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(api.Body.String(), `"error"`) {
		t.Errorf("/api/* body = %q, want JSON", api.Body.String())
	}
	// The header carries the limiter's own answer, not a constant that would
	// send an obedient client back into a second 429: at 0.0001/s the wait is a
	// very long one indeed.
	if secs, err := strconv.Atoi(api.Header().Get("Retry-After")); err != nil || secs < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds",
			api.Header().Get("Retry-After"))
	}
	if api.Header().Get("Content-Security-Policy") == "" {
		t.Error("a 429 must still carry the security headers")
	}

	page := getFrom(t, h, "/wp-login.php", "9.9.9.9")
	if ct := page.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("page Content-Type = %q, want text/plain", ct)
	}
}

// The debt has to be reachable THROUGH the middleware, not only by calling
// Charge directly. The gate needs a whole token to let a request through, so a
// charge levied after the response can never take the bucket below zero: unless
// a blocked request is charged too, the block is one refill long no matter how
// long the spraying goes on, and every mention of debt is describing a state
// nothing can produce.
func TestKnockingWhileBlockedDeepensTheBlock(t *testing.T) {
	h := NewServer(Deps{
		Version:         "test",
		DB:              openTestDB(t),
		NotFoundLimiter: ratelimit.New(1, 2), // a token a second, floor -2
	})

	// Spend the budget on 404s, which lands the bucket at exactly zero.
	getFrom(t, h, "/wp-login.php", "9.9.9.9")
	getFrom(t, h, "/.env", "9.9.9.9")

	first := retryAfter(t, getFrom(t, h, "/one.php", "9.9.9.9"))
	second := retryAfter(t, getFrom(t, h, "/two.php", "9.9.9.9"))

	if second <= first {
		t.Errorf("Retry-After went %d -> %d; a caller that keeps knocking must "+
			"wait longer, or the debt floor and the refill rate are decoration",
			first, second)
	}
}

// retryAfter reads the header off a response that must be a 429.
func retryAfter(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After = %q: %v", rec.Header().Get("Retry-After"), err)
	}
	return secs
}

// A limiter that never refills has no wait to report, and "Retry-After: 0"
// reads as "retry immediately" — the opposite of a block that never lifts.
func TestRetryAfterIsNeverZero(t *testing.T) {
	h := NewServer(Deps{
		Version:         "test",
		DB:              openTestDB(t),
		NotFoundLimiter: ratelimit.New(0, 1), // no refill at all
	})

	getFrom(t, h, "/wp-login.php", "9.9.9.9") // spends the only token
	if secs := retryAfter(t, getFrom(t, h, "/.env", "9.9.9.9")); secs < 1 {
		t.Errorf("Retry-After = %d, want at least 1", secs)
	}
}

// Against the REAL SPA handler rather than a nil Static, because the two do not
// answer the same way and only one of them ships.
//
// web.spaHandler 404s a missing file with an extension and serves the shell for
// anything without one, so an extensionless probe — /admin, /.git/config — is a
// 200 that costs a scanner nothing. That is the SPA's deep-link fallback doing
// its job, and it is the boundary of what this limiter can see: pinned here so
// the next person to widen the throttle knows where to look.
func TestChargingFollowsTheRealSPAHandler(t *testing.T) {
	static, err := web.Handler()
	if err != nil {
		t.Fatalf("web.Handler: %v", err)
	}
	h := NewServer(Deps{
		Version:         "test",
		DB:              openTestDB(t),
		Static:          static,
		NotFoundLimiter: ratelimit.New(0.0001, 2),
	})

	// Extensionless: the shell, and no charge however many arrive.
	for i := 0; i < 20; i++ {
		if code := getFrom(t, h, "/admin", "9.9.9.9").Code; code != http.StatusOK {
			t.Fatalf("extensionless probe %d = %d, want the SPA shell", i, code)
		}
	}

	// Extensioned: a real 404, and the budget is gone in two.
	for i, path := range []string{"/wp-login.php", "/.env"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404", i, path, code)
		}
	}
	if code := getFrom(t, h, "/xmlrpc.php", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("third extensioned probe = %d, want 429", code)
	}
}

// Nil is the documented "no such limit" case, and the tests everywhere else in
// this package rely on it.
func TestNilNotFoundLimiterServesEverything(t *testing.T) {
	h := NewServer(Deps{Version: "test", DB: openTestDB(t)})

	for i := 0; i < 20; i++ {
		if code := getFrom(t, h, "/.env", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("request %d = %d, want 404 with no limiter wired", i, code)
		}
	}
}
