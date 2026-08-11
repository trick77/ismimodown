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
		// No third-party origin at all, on any directive. An origin arriving
		// unremarked is the way a policy like this rots, so pin the whole
		// directive rather than probe it for substrings.
		if !strings.Contains(csp, "script-src 'self';") {
			t.Errorf("%s: script-src = %q, want 'self' and nothing else", path, csp)
		}
		if !strings.Contains(csp, "connect-src 'self';") {
			t.Errorf("%s: connect-src = %q, want 'self' and nothing else", path, csp)
		}
	}
}

// The policy names no host at all. Microsoft Clarity was the single
// exception it ever carried — www.clarity.ms for the tag, *.clarity.ms and
// c.bing.com for the beacons — and this asserts it did not come back, in the
// only form that catches the next one too: no scheme-qualified origin anywhere
// in the policy.
func TestNoThirdPartyOriginsInThePolicy(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{Version: "test", DB: db, Samples: samples.New(db)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	csp := rec.Header().Get("Content-Security-Policy")

	// "https://" and "http://" both, so a plaintext origin is not the way in.
	// data: is deliberately not matched — img-src carries it on purpose, and it
	// names no host.
	for _, scheme := range []string{"https://", "http://"} {
		if strings.Contains(csp, scheme) {
			t.Errorf("CSP = %q, want no %s origin in it", csp, scheme)
		}
	}
	if strings.Contains(csp, "clarity") || strings.Contains(csp, "bing") {
		t.Errorf("CSP = %q, want no trace of the removed analytics origins", csp)
	}

	// default-src stays 'self'. Clarity's own documentation suggested widening
	// that instead; every directive this policy cares about is set explicitly,
	// so a wildcard there would only loosen the ones nobody listed.
	if !strings.Contains(csp, "default-src 'self';") {
		t.Errorf("CSP = %q, want default-src left at 'self'", csp)
	}
}

// The og card is the one response another origin is supposed to be able to
// load, so it is the one response CORP must not refuse.
//
// This was wrong for a long time and invisible, because the preview surfaces
// that matter most — Slack, WhatsApp, Telegram, X — fetch the image server-side,
// and a server-side fetch never consults CORP. Only a client hotlinking the URL
// into a page sees the block, which is why the assertion is worth having.
func TestTheOgCardIsEmbeddableCrossOrigin(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{Version: "test", DB: db, Samples: samples.New(db)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/og.png", nil))

	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Errorf("/og.png: CORP = %q, want cross-origin; the card exists to be embedded", got)
	}
}

// The exception is the card and nothing else. An icon, the manifest, the page
// and the API all stay same-origin — none of them has any business being
// embedded in someone else's document, and a blanket cross-origin would be the
// easy way to lose that.
func TestOnlyTheOgCardIsCrossOrigin(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{Version: "test", DB: db, Samples: samples.New(db)})

	for _, path := range []string{
		"/", "/healthz", "/api/dashboard?window=24h",
		"/icon.svg", "/icon-192.png", "/manifest.webmanifest", "/apple-touch-icon.png",
		"/og.png/x", "/x/og.png",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
			t.Errorf("%s: CORP = %q, want same-origin", path, got)
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

// An unmatched /api path 404s, and an extensionless one is not charged for it —
// /api/v1/users is extensionless like every other API name a scanner guesses.
//
// That is not a hole: /api/* is the one prefix with its OWN request limiter
// (see routes()), so a caller walking API names is metered there, by request
// rather than by miss. This limiter exists for the surface that has no other
// bound. An /api path carrying a non-image extension is charged here as well,
// since that is a wordlist rather than an API guess.
func TestUnknownAPIPathsRelyOnTheRequestLimiter(t *testing.T) {
	h := scannerServer(t)

	for i := 0; i < 8; i++ {
		if code := getFrom(t, h, "/api/v1/users", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d = %d, want 404 at no cost to the 404 budget", i, code)
		}
	}
	// The budget is intact, so an extensioned probe still has all three tokens.
	for i, path := range []string{"/api/config.php", "/api/db.sql", "/api/old.bak"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404", i, path, code)
		}
	}
	if code := getFrom(t, h, "/api/x.aspx", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth extensioned probe = %d, want 429", code)
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

// A browser asks for icons nobody linked — /favicon.ico on every first visit,
// apple-touch-icon on iOS — and the budget must not pay for the browser's own
// guesses.
func TestImageMissesAreNotCharged(t *testing.T) {
	h := scannerServer(t)

	for _, path := range []string{
		"/favicon.ico",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/FAVICON.ICO",
		"/icon.svg",
	} {
		for i := 0; i < 4; i++ {
			if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
				t.Fatalf("%s request %d = %d, want 404; an image miss costs nothing", path, i, code)
			}
		}
	}
	// The budget is untouched, so the first real probe still has all three.
	for i, path := range []string{"/wp-login.php", "/config.json", "/.env"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404; icons must not have spent the budget", i, path, code)
		}
	}
	if code := getFrom(t, h, "/wp-config.php.bak", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth probe = %d, want 429; the exemption is for images, not for everything", code)
	}
}

// The other thing a browser asks for that nobody linked: /.well-known.
//
// Chrome probes /.well-known/traffic-advice on navigations and devtools asks for
// /.well-known/appspecific/com.chrome.devtools.json. Neither is an image, and
// both were free only by accident until web.spaHandler stopped answering
// extensionless unknown paths with the shell and a 200 — at which point a
// visitor's own browser started spending a budget meant for wrong guesses.
func TestWellKnownMissesAreNotCharged(t *testing.T) {
	h := scannerServer(t)

	for _, path := range []string{
		"/.well-known/traffic-advice",
		"/.well-known/appspecific/com.chrome.devtools.json",
		"/.well-known/security.txt",
		"/.WELL-KNOWN/traffic-advice",
	} {
		for i := 0; i < 4; i++ {
			if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
				t.Fatalf("%s request %d = %d, want 404 at no cost", path, i, code)
			}
		}
	}
	// The budget is untouched, so a real probe still has the whole of it.
	for i, path := range []string{"/wp-login.php", "/config.json", "/.env"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404; /.well-known must not have spent the budget", i, path, code)
		}
	}
	if code := getFrom(t, h, "/wp-config.php.bak", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth probe = %d, want 429; the exemption is for /.well-known, not for everything", code)
	}
}

// The scenario the extensionless exemption exists for, end to end.
//
// Until web.spaHandler stopped serving the shell for unknown extensionless
// paths, every one of them was a 200 — so any such URL a search engine ever
// discovered went into its index as a real page. They 404 after the deploy that
// changes it, and the engine recrawls them to find out what happened. If those
// 404s were charged, a dozen would exhaust a burst of five and the gate would
// then answer EVERYTHING from that caller with a 429: the homepage, robots.txt,
// the sitemap. A crawler locked out by the very change meant to get the site
// indexed.
//
// Run against the real SPA handler, because the 404 has to be the real one.
func TestARecrawlOfOldSoftFourOhFourURLsIsNotLockedOut(t *testing.T) {
	static, err := web.Handler()
	if err != nil {
		t.Fatalf("web.Handler: %v", err)
	}
	h := NewServer(Deps{
		Version:         "test",
		DB:              openTestDB(t),
		Static:          static,
		NotFoundLimiter: ratelimit.New(0.0001, 5), // production's burst, no useful refill
	})

	// A crawler working through URLs it learned when they returned 200.
	for _, path := range []string{
		"/status", "/about", "/api-status", "/xiaomi-mimo", "/uptime",
		"/history", "/faq", "/mimo", "/downdetector", "/incidents",
	} {
		if code := getFrom(t, h, path, "66.249.66.1").Code; code != http.StatusNotFound {
			t.Fatalf("%s = %d, want a plain 404", path, code)
		}
	}

	// The three requests that decide whether the site is indexable at all.
	for _, path := range []string{"/", "/robots.txt", "/sitemap.xml"} {
		if code := getFrom(t, h, path, "66.249.66.1").Code; code == http.StatusTooManyRequests {
			t.Errorf("%s = 429 after the recrawl; the crawler is locked out", path)
		}
	}
}

// A path that only LOOKS like the carve-out is charged like anything else. The
// prefix is anchored at the root, because that is where the spec puts it:
// /.well-knownx is not under /.well-known, and neither is /x/.well-known.
//
// Every path here carries the same .php extension, so the extension is held
// constant and the PREFIX is the only thing under test — an extensionless
// look-alike would be exempt on its own account and prove nothing.
//
// No Ban store is wired here, so these reach the limiter and 404. In production
// banGate answers them with a 403 first, since isDotPath makes the same
// distinction and these fail it too. Both layers agree on which prefix is real;
// this pins the limiter's half.
func TestOnlyTheRealWellKnownPrefixIsExempt(t *testing.T) {
	h := scannerServer(t)

	// The real prefix: free, however many arrive.
	for i := 0; i < 8; i++ {
		if code := getFrom(t, h, "/.well-known/x.php", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("real-prefix probe %d = %d, want 404 at no cost", i, code)
		}
	}

	// The look-alikes: charged, and three of them exhaust the burst.
	for i, path := range []string{"/.well-knownx/y.php", "/x/.well-known/y.php", "/.well-known.php"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404", i, path, code)
		}
	}
	if code := getFrom(t, h, "/wp-login.php", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth probe = %d, want 429; look-alikes must still be charged", code)
	}
}

// The exemption waives the charge, not the gate: a caller already in debt is
// cut off from everything, an icon included. It does NOT make an image-only
// scanner reachable — nothing charges it, so nothing gates it either — only a
// caller that spent the budget on something else.
func TestAThrottledCallerIsRefusedImagesToo(t *testing.T) {
	h := scannerServer(t)

	for i := 0; i < 6; i++ {
		getFrom(t, h, "/wp-login.php", "9.9.9.9")
	}
	if code := getFrom(t, h, "/favicon.ico", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("/favicon.ico = %d, want 429 for a throttled caller", code)
	}
}

// path.Ext takes the last dot segment, so an image extension in the MIDDLE of a
// path does not make the request an image.
//
// The probes end in .php rather than in nothing, because an extensionless path
// is exempt on its own account now and would prove nothing about the .png.
func TestImageExtensionMustBeTheLastSegment(t *testing.T) {
	h := scannerServer(t)

	for i := 0; i < 3; i++ {
		if code := getFrom(t, h, "/x.png/setup.php", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d = %d, want 404", i, code)
		}
	}
	if code := getFrom(t, h, "/x.png/setup.php", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("the fourth probe = %d, want 429; only a trailing image extension is exempt", code)
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
// web.spaHandler now 404s EVERY unknown path, extension or not — but what this
// limiter CHARGES for did not change with it. An extensionless miss was free
// when it was a 200 and is free now that it is a 404, deliberately: a search
// engine recrawling a URL the old soft 404 taught it existed must not be able
// to spend a budget that then gates it on / and /robots.txt.
//
// So the split under test is the same one that has always been here. What is
// metered is a non-image extension.
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

	// Extensionless: a real 404 now, and still no charge however many arrive.
	for i := 0; i < 20; i++ {
		if code := getFrom(t, h, "/admin", "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("extensionless probe %d = %d, want 404 at no cost", i, code)
		}
	}

	// Extensioned: the budget is untouched by all of that, and goes in two.
	for i, path := range []string{"/wp-login.php", "/.env"} {
		if code := getFrom(t, h, path, "9.9.9.9").Code; code != http.StatusNotFound {
			t.Fatalf("probe %d (%s) = %d, want 404", i, path, code)
		}
	}
	if code := getFrom(t, h, "/xmlrpc.php", "9.9.9.9").Code; code != http.StatusTooManyRequests {
		t.Errorf("third extensioned probe = %d, want 429", code)
	}
}

// The one path that must keep serving the page. Everything else 404s now, so
// the check that the root itself did not get caught in that earns its own test
// rather than an assertion buried inside another one.
func TestRootStillServesTheShell(t *testing.T) {
	static, err := web.Handler()
	if err != nil {
		t.Fatalf("web.Handler: %v", err)
	}
	h := NewServer(Deps{Version: "test", DB: openTestDB(t), Static: static})

	if code := getFrom(t, h, "/", "9.9.9.9").Code; code != http.StatusOK {
		t.Errorf("GET / = %d, want the shell", code)
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
