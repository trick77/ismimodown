package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trick77/ismimodown/internal/ratelimit"
	"github.com/trick77/ismimodown/internal/samples"
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

	for i, path := range []string{"/wp-login.php", "/.env", "/.git/config"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404 while the budget lasts", i, path, code)
		}
	}
	if code := getFrom(t, h, "/phpmyadmin/", "9.9.9.9").Code; code != http.StatusTooManyRequests {
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
	if api.Header().Get("Retry-After") == "" {
		t.Error("a 429 must tell the caller when to retry")
	}
	if api.Header().Get("Content-Security-Policy") == "" {
		t.Error("a 429 must still carry the security headers")
	}

	page := getFrom(t, h, "/wp-login.php", "9.9.9.9")
	if ct := page.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("page Content-Type = %q, want text/plain", ct)
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
