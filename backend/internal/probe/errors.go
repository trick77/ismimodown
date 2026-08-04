package probe

import "errors"

// Error classes. These are the ONLY failure vocabulary served publicly —
// error_detail carries the underlying text and stays operator-only, because a
// provider error body can echo request fragments.
//
// The classes are deliberately aligned with the timeout ladder rather than
// collapsed into one "timeout": a single outer deadline records "slow" and
// "dead" identically, and "no first token for 150 s" (queueing) is a different
// finding from "first token at 800 ms, then silence" (throughput collapse).
const (
	// ErrClassConnectTimeout — the TCP handshake, or dial+TLS, never completed.
	ErrClassConnectTimeout = "connect_timeout"
	// ErrClassDNS — the hostname did not resolve. Distinct from a connect
	// failure: a DNS outage is ours (or the resolver's), not the endpoint's.
	ErrClassDNS = "dns_error"
	// ErrClassHeaderTimeout — connected, but no response headers.
	ErrClassHeaderTimeout = "header_timeout"
	// ErrClassTTFTTimeout — headers arrived, no first token. Queueing.
	ErrClassTTFTTimeout = "ttft_timeout"
	// ErrClassStalled — the stream started, then went silent mid-flight.
	ErrClassStalled = "stalled"
	// ErrClassTimeout — the overall deadline. The backstop, not the first line.
	ErrClassTimeout = "timeout"
	// ErrClassHTTP — a non-2xx response.
	ErrClassHTTP = "http_error"
	// ErrClassRateLimited — HTTP 429, separated from http_error because it says
	// something specific about our own probe volume rather than MiMo's health.
	ErrClassRateLimited = "rate_limited"
	// ErrClassAuth — HTTP 401/403. Almost always our key, not their outage, and
	// must not be allowed to read as a MiMo failure on the dashboard.
	ErrClassAuth = "auth_error"
	// ErrClassRefused — the port actively refused the connection.
	ErrClassRefused = "connection_refused"
	// ErrClassProtocol — the response was not the SSE stream we can parse.
	ErrClassProtocol = "protocol_error"
	// ErrClassCanceled — shutdown, not a fault. Never counted against
	// availability.
	ErrClassCanceled = "canceled"
)

// ErrStalled marks a stream aborted by the idle watchdog. Deliberately distinct
// from context.Canceled so a stalled upstream is not misreported as a shutdown.
var ErrStalled = errors.New("stream stalled: no chunk within the idle window")

// ErrTTFTTimeout marks a stream that produced headers but never a first token.
var ErrTTFTTimeout = errors.New("no first token within the TTFT window")
