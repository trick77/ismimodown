package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "llmstats") {
		t.Errorf("body does not look like the shell: %s", rec.Body.String())
	}
}

// A deep link reloaded in the browser must land on the SPA shell, not a 404.
// The dashboard keeps its window filter in the query string and has no router,
// but this is the behaviour every future path depends on.
func TestHandlerFallsBackToTheShell(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/deep/link", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "llmstats") {
		t.Errorf("fallback did not serve the shell: %s", rec.Body.String())
	}
}

// A path that DOES exist is served by http.FileServer rather than rewritten to
// the shell. /index.html is the one such path in the placeholder bundle, and
// FileServer canonicalises it to "/" with a 301 — the point of the assertion is
// that the request took the file branch, not the fallback branch (which would
// have returned the shell body with a 200).
func TestHandlerServesAKnownPathWithoutFallback(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "./" {
		t.Errorf("Location = %q, want ./", got)
	}
}
