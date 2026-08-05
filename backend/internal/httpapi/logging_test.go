package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs redirects the default logger for the duration of one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// logLines decodes every "request" line the middleware emitted.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		if entry["msg"] == "request" {
			out = append(out, entry)
		}
	}
	return out
}

// logOne makes one request through the full middleware chain and returns the
// line it produced.
func logOne(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	buf := captureLogs(t)
	h, _ := newAPIServer(t)
	h.ServeHTTP(httptest.NewRecorder(), req)

	lines := logLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("got %d request lines, want 1: %s", len(lines), buf.String())
	}
	return lines[0]
}

// A 429 says throttling happened. Without the caller it cannot say whether that
// was one scraper or a room full of readers, which is the only question a burst
// of them raises.
func TestEveryRequestLogsTheCaller(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		wantStatus float64
	}{
		{"a served request", "/api/dashboard?window=24h", 200},
		{"a rejected one", "/api/dashboard?window=6mo", 400},
		{"a route that does not exist", "/api/nope", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.RemoteAddr = "203.0.113.7:51234"

			entry := logOne(t, req)
			if entry["status"] != tc.wantStatus {
				t.Errorf("status = %v, want %v", entry["status"], tc.wantStatus)
			}
			if entry["client"] != "203.0.113.7" {
				t.Errorf("client = %v, want the caller's address", entry["client"])
			}
		})
	}
}

// Behind a proxy RemoteAddr is the proxy. The LAST X-Forwarded-For entry is the
// one the nearest trusted hop appended; the first is caller-supplied, and
// logging it would let anyone write whatever they liked into the record.
func TestLoggedCallerIsNotTheSpoofableForwardedEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?window=24h", nil)
	req.RemoteAddr = "10.0.0.1:9999" // the proxy
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.7")

	entry := logOne(t, req)
	if entry["client"] != "203.0.113.7" {
		t.Errorf("client = %v, want the entry the proxy appended", entry["client"])
	}
}

// The log records the /64 rather than the address, because rotating the low 64
// bits is free — the block is the only identity that means anything over v6.
// It is also what the limiter buckets on, so a 429 and its bucket name the same
// thing.
func TestLoggedCallerMatchesTheLimiterKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?window=24h", nil)
	req.RemoteAddr = "[2001:db8:1234:5678:abcd:ef01:2345:6789]:443"

	entry := logOne(t, req)
	if entry["client"] != "2001:db8:1234:5678::/64" {
		t.Errorf("client = %v, want the /64 block", entry["client"])
	}
}

// The query string stays out of the log. Nothing secret travels in one on this
// API today, but the endpoints are public and unauthenticated, and a log that
// mirrors arbitrary caller text is a different kind of liability.
func TestLogRecordsThePathWithoutTheQueryString(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?window=24h", nil)
	entry := logOne(t, req)

	if entry["path"] != "/api/dashboard" {
		t.Errorf("path = %v, want the path alone", entry["path"])
	}
	for _, field := range []string{"path", "client"} {
		if strings.Contains(entry[field].(string), "window=") {
			t.Errorf("%s carried the query string: %v", field, entry[field])
		}
	}
}

// The healthcheck hits /healthz every 60s. Logging it would bury the requests
// that matter — and would record the orchestrator as a visitor.
func TestHealthzLogsNothingAtAll(t *testing.T) {
	buf := captureLogs(t)
	h, _ := newAPIServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.7:51234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if lines := logLines(t, buf); len(lines) != 0 {
		t.Errorf("healthz produced %d request lines: %s", len(lines), buf.String())
	}
}
