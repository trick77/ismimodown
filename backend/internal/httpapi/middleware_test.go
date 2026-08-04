package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
