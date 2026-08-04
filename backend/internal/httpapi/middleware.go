package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
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
// The site loads nothing from anywhere else: the SPA, its fonts and its icons
// are all embedded in the binary and served same-origin, and every fetch goes
// to this origin's /api. So the policy can be as tight as 'self' throughout
// without a single exception — anything that trips it is a regression.
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
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
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

// logging logs each request with method, path, status and duration.
//
// /healthz is skipped: the container healthcheck hits it every 60s and would
// otherwise be most of the log volume on an idle server.
//
// Deliberately logs r.URL.Path and never the query string. Nothing secret
// travels in a query string on this API today, but the endpoints are public and
// unauthenticated, so the log must not become a mirror of arbitrary
// caller-supplied text.
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
		)
	})
}
