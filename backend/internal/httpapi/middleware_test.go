package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trick77/mimostats/internal/samples"
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

	for _, path := range []string{"/healthz", "/api/models", "/api/summary?window=24h", "/api/nope", "/does-not-exist"} {
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
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
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
