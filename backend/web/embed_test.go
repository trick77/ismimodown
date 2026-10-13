package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testDist is a built bundle in miniature: the repo embeds only .gitkeep, so
// the handler is tested against this rather than whatever the last UI build
// left in dist/.
func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                {Data: []byte(`<!doctype html><div id="root"></div>`)},
		"assets/index-C7Xl7PkL.js":  {Data: []byte("console.log(1)")},
		"assets/index-BGjmce_o.css": {Data: []byte("body{}")},
		"manifest.webmanifest":      {Data: []byte("{}")},
		"og.png":                    {Data: []byte("\x89PNG")},
	}
}

func serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(testDist()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHandlerServesIndex(t *testing.T) {
	rec := serve(t, "/")

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

// An unknown path is a 404, whether or not it carries a file extension.
//
// This is the inverse of the SPA fallback every Vite template ships, and it is
// deliberate: the dashboard keeps its state in the query string and has no
// router, so there is no deep link to protect, and serving the shell with a 200
// for /some/deep/link made every wrong URL on the host a duplicate of the one
// real page. Search engines call that a soft 404 and Bing takes it as a signal
// about the host rather than the URL. Add a client-side route and this test is
// the one to change — deliberately, not by accident.
//
// /index.html is in the list: the shell has one name, "/".
func TestUnknownPathsAreNotFound(t *testing.T) {
	for _, path := range []string{"/some/deep/link", "/admin", "/index.html", "/index.html.bak", "/nope.js"} {
		rec := serve(t, path)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("%s: served the shell instead of a 404 body", path)
		}
	}
}

// A missing ASSET must 404, not receive the shell. Falling back
// indiscriminately answers a stale hashed chunk request with index.html at
// 200/text-html; a browser holding an old shell across a deploy then parses
// HTML as JavaScript and white-screens on "Unexpected token '<'" instead of
// recovering on reload.
func TestHandlerDoesNotFallBackForAssets(t *testing.T) {
	for _, path := range []string{"/assets/index-abc123.js", "/assets/", "/favicon.ico", "/app.css"} {
		rec := serve(t, path)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (missing asset must not get the shell)", path, rec.Code)
		}
		// `id="root"` is in every shell and in no 404 body: a negative check
		// whose needle no longer exists passes forever.
		if strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("%s: served the shell body for a missing asset", path)
		}
	}
}

// The split that makes a year-long cache safe. Only Vite's hashed output may be
// held immutably; the shell is the file that NAMES the current bundle, so a
// cached one is a browser pinned to a build that no longer exists.
func TestCacheControlIsSetPerAsset(t *testing.T) {
	const immutable = "public, max-age=31536000, immutable"

	for path, want := range map[string]string{
		"/assets/index-C7Xl7PkL.js":  immutable,
		"/assets/index-BGjmce_o.css": immutable,
		"/":                          "no-cache",
	} {
		if got := serve(t, path).Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, want)
		}
	}
}

// The shell carries a validator, so a revisit costs a 304 rather than the body.
func TestShellRevalidatesWithAnETag(t *testing.T) {
	tag := serve(t, "/").Header().Get("ETag")
	if tag == "" {
		t.Fatal("no ETag on the shell")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", tag)
	handler(testDist()).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rec.Code)
	}
}

// Chrome refuses a manifest served as text/plain and silently drops the PWA
// icons and theme colour with it — a failure with no error anywhere the
// developer looks. Go's mime table has no .webmanifest entry; kit registers it.
func TestManifestIsServedWithItsOwnType(t *testing.T) {
	rec := serve(t, "/manifest.webmanifest")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/manifest+json") {
		t.Errorf("Content-Type = %q, want application/manifest+json", got)
	}
}

// A fresh checkout embeds only .gitkeep: the binary still builds and answers,
// with kit's placeholder rather than a blank page whose bundle 404s.
func TestUnbuiltDistServesThePlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{".gitkeep": {}}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// Handler wires the embedded dist, whatever is in it.
func TestHandlerEmbedsDist(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
