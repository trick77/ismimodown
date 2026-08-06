package httpapi

import (
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/trick77/ismimodown/internal/ratelimit"
)

// recovery converts panics in downstream handlers into 500 responses. Without
// it, a handler panic bypasses slog entirely and lands on stderr in Go's
// default log format.
func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "err", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy is the page's resource allow-list.
//
// The site loads almost nothing from anywhere else: the SPA, its fonts and its
// icons are all embedded in the binary and served same-origin, and every fetch
// goes to this origin's /api. So the policy is 'self' throughout with exactly
// one exception — anything else that trips it is a regression.
//
// The exception is Microsoft Clarity (heatmaps and session replay), which loads
// its tag from www.clarity.ms and beacons to the *.clarity.ms shard it is
// load-balanced onto plus c.bing.com. Those origins are named on script-src and
// connect-src only, not on default-src: every other directive here is set
// explicitly, so widening the fallback would be a wildcard nobody reads. Clarity
// hands out an inline <script> to paste into <head>; ui/src/clarity.ts injects
// the tag from the bundle instead, precisely so that 'unsafe-inline' stays off.
//
// 'unsafe-inline' for styles is the one concession, and it is unavoidable:
// ECharts positions its tooltip by writing inline style on a div it creates,
// and Tailwind's runtime does the same for CSS custom properties. Scripts get
// no such exception, which is the half that matters — there is no HTML sink in
// this UI (React escapes everything, no dangerouslySetInnerHTML anywhere), so
// script-src 'self' means an injected string has nowhere to execute.
//
// frame-ancestors 'none' rather than X-Frame-Options alone: modern browsers
// prefer it, and the header is kept alongside for the ones that do not.
// form-action and base-uri are 'none' because this page has no form and no
// <base> — declaring that costs nothing and closes two redirection tricks.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://www.clarity.ms; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self' https://*.clarity.ms https://c.bing.com; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// securityHeaders sets the response headers that bound what a browser will do
// with this site.
//
// It lives in the handler rather than in the Traefik labels so it travels with
// the binary: `make dev` and any future deployment get the same policy as
// production, and a reverse-proxy config change cannot silently drop it.
//
// HSTS is deliberately NOT set here. The process serves plain HTTP and does not
// know whether it is behind TLS; a Strict-Transport-Security header emitted on
// an http:// origin is ignored at best, and would be wrong on a local dev
// server. It belongs on the TLS terminator — see DEPLOY.md.
//
// Set before the handler runs, not after: headers must be in the map before
// anything writes a status, and the SSE stream writes its own immediately.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		// Without nosniff a browser is free to ignore the declared type and
		// guess from the bytes — which is how a JSON response full of
		// attacker-chosen strings gets executed as HTML on some clients.
		h.Set("X-Content-Type-Options", "nosniff")
		// Belt and braces with frame-ancestors above, for clients that honour
		// only the older header.
		h.Set("X-Frame-Options", "DENY")
		// The path can carry a window or model name; no reason to hand even
		// that to whatever a visitor clicks through to.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Nothing here needs a camera, a microphone or a location, and saying
		// so stops any future embedded content from asking on the site's behalf.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		// No cross-origin caller has any business reading these responses, and
		// none needs to: the SPA is same-origin. Deliberately no
		// Access-Control-Allow-Origin anywhere — the API is public to read in a
		// browser tab, not to embed in someone else's page.
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the response status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.ResponseWriter.Write(b)
}

// Flush forwards flushes so the /api/events SSE handler keeps working through
// the wrapper — without it every pushed cycle would sit in the buffer and the
// dashboard would only update on reload.
func (rec *statusRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer for http.ResponseController.
func (rec *statusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

// notFoundPenalty throttles a caller by the 404s it causes rather than by the
// requests it makes.
//
// The request limiter guards /api/* only, and rightly so: the SPA and its
// hashed assets are served from memory, and one page load is a dozen of them at
// once. But that leaves the entire non-API surface unmetered, and that is
// exactly where a scanner works — /wp-login.php, /.env, /.git/config, a
// wordlist of them, none of which the request limiter ever sees. Every one is a
// 404, and a caller producing nothing but 404s is not reading the page.
//
// Gate and charge are deliberately separate. EVERY request is checked against
// the caller's budget, including a static one — a scanner cut off from
// /wp-login.php must not still be free to pull the shell a thousand times — but
// only a 404 SPENDS from it. A visitor's page load therefore costs nothing and
// can never be throttled here, while a wordlist runs the bucket into debt
// within a handful of paths and stays there while it keeps spraying.
//
// Only 404 charges, not 4xx at large. A 429 from the request limiter and a 400
// from a bad window parameter are both things a real client produces; charging
// for those would compound the two limiters into something far harsher than
// either was sized for.
//
// The budget still has to absorb the 404s an honest browser makes without being
// asked to: /favicon.ico above all — the page ships /icon.svg and no .ico, so
// every first visit misses once — plus apple-touch-icon variants, robots.txt, a
// source map, and whatever a stale shell requests across a deploy.
//
// A nil limiter disables it, so tests and callers that want no such limit need
// wire nothing.
func notFoundPenalty(l *ratelimit.Limiter, next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The container healthcheck is never throttled. It cannot 404, so it
		// could only ever be collateral from another caller sharing its key —
		// and a health probe that fails because someone else scanned the box
		// restarts a healthy container.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		key := ratelimit.ClientIP(r)
		if !l.Permitted(key) {
			// Knocking while blocked costs a token too, and this is the only
			// path that can put a caller into debt at all: the gate needs a
			// whole token to let a request through, so a charge levied after
			// the response can never take the bucket below zero. Without this
			// the block would be one refill long however long the spraying went
			// on, and the floor, the debt and this header would all be
			// describing a state nothing could reach.
			//
			// It cannot lock anyone out: the debt stops at -burst, so the worst
			// case is one bounded block, and a client that backs off at all
			// gains more from the refill than it loses by asking.
			l.Charge(key, 1)
			writeTooManyRequests(w, r, l.RetryAfter(key))
			return
		}

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusNotFound {
			l.Charge(key, 1)
		}
	})
}

// writeTooManyRequests answers a throttled caller in the content type its path
// implies: a client that parses every /api/* response as JSON must not be
// handed text/plain, and a browser asking for a page gains nothing from JSON.
//
// Retry-After is this caller's own wait, taken from the limiter rather than
// written here as a constant: someone who has just run out is told one refill,
// someone who kept knocking is told the length of the debt it dug. A constant
// would be obeyed and answered with another 429.
func writeTooManyRequests(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	// Rounded UP, and never below one second: truncating a 179.5s block to 179
	// sends an obedient client back half a second early into a second 429, and
	// a limiter that cannot answer (RetryAfter is 0 when it never refills) must
	// not emit "Retry-After: 0", which reads as "retry immediately".
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}` + "\n"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte("too many requests\n"))
}

// logging logs each request with method, path, status, duration and caller.
//
// /healthz is skipped: the container healthcheck hits it every 60s and would
// otherwise be most of the log volume on an idle server.
//
// Deliberately logs r.URL.Path and never the query string. Nothing secret
// travels in a query string on this API today, but the endpoints are public and
// unauthenticated, so the log must not become a mirror of arbitrary
// caller-supplied text.
//
// The caller is ratelimit.ClientIP — the limiter's OWN bucket key, not
// r.RemoteAddr. Three things follow from that, and they are the reason to reuse
// it rather than read the address again here:
//
//   - Behind Traefik, RemoteAddr is the proxy. The key reads the LAST
//     X-Forwarded-For entry, the one the nearest trusted proxy appended, rather
//     than the caller-supplied first entry anyone can invent.
//   - IPv6 is already collapsed to its /64, so the log records the block rather
//     than the address. Rotating the low 64 bits is free, so the /64 is the
//     identity that means anything anyway.
//   - A 429 line and the bucket that produced it now name the same thing. A log
//     that said "some other notion of the caller" would be unable to answer the
//     only question a burst of 429s raises: one scraper, or many readers.
//
// This is every request, not only the failures — so the log is a visitor record
// for a public page, and how long it is kept is a deployment decision rather
// than this file's.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		// Level reflects the outcome so failures stand out instead of drowning
		// in the INFO request stream: 5xx -> ERROR, 4xx -> WARN, otherwise INFO.
		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		slog.LogAttrs(r.Context(), level, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.String("dur", time.Since(start).String()),
			slog.String("client", ratelimit.ClientIP(r)),
		)
	})
}
