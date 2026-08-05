package web

import (
	"mime"
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
	// The mount point, not the product name: this asserts that the SPA SHELL
	// was served, and a test that fingerprints on branding fails the day the
	// branding is reworded — which is exactly how it failed.
	if !strings.Contains(rec.Body.String(), `id="root"`) {
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
	if !strings.Contains(rec.Body.String(), `id="root"`) {
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

// A missing ASSET must 404, not receive the shell. Falling back
// indiscriminately answers a stale hashed chunk request with index.html at
// 200/text-html; a browser holding an old shell across a deploy then parses
// HTML as JavaScript and white-screens on "Unexpected token '<'" instead of
// recovering on reload.
func TestHandlerDoesNotFallBackForAssets(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	for _, path := range []string{"/assets/index-abc123.js", "/favicon.ico", "/app.css"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (missing asset must not get the shell)", path, rec.Code)
		}
		// The mount point, not the product name — the same reasoning as
		// TestHandlerServesIndex above, and for a sharper reason on a NEGATIVE
		// assertion. This used to match the product name — which has since been
		// renamed twice, first the site and then the code — and a
		// negative check whose needle no longer exists does not fail: it passes
		// forever, including on the day the shell really is served for a
		// missing asset. `id="root"` is in every shell and in no 404 body.
		if strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("%s: served the shell body for a missing asset", path)
		}
	}
}

// Chrome refuses a manifest served as text/plain and silently drops the PWA
// icons and theme colour with it — a failure with no error anywhere the
// developer looks. Go's mime table has no .webmanifest entry, so the
// registration in embed.go is the only thing standing between the manifest and
// that default.
//
// This one asserts the registration directly, and so runs everywhere. The
// serving test below needs a real build embedded, and backend/web/dist is
// gitignored apart from index.html — on a fresh checkout and in CI it skips,
// which would leave the registration with no cover that gates a merge.
func TestWebmanifestExtensionIsRegistered(t *testing.T) {
	if got := mime.TypeByExtension(".webmanifest"); !strings.HasPrefix(got, "application/manifest+json") {
		t.Errorf("mime.TypeByExtension(\".webmanifest\") = %q, want application/manifest+json", got)
	}
}

// The other half: that the registration actually reaches the FileServer, rather
// than being shadowed by the system mime table it merges into.
func TestManifestIsServedWithItsOwnType(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))

	// The placeholder dist in the repo carries only index.html, so a 404 here is
	// the fresh-checkout case rather than a regression. The type is what is
	// under test, and it is only observable when the file is present.
	if rec.Code == http.StatusNotFound {
		t.Skip("no manifest in the embedded dist — run the UI build first")
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/manifest+json") {
		t.Errorf("Content-Type = %q, want application/manifest+json", got)
	}
}
