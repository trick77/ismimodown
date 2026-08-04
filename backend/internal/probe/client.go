package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"syscall"
	"time"
)

// maxErrorBodyBytes bounds how much of a provider error body is read. It goes
// into error_detail, which is operator-only — but an unbounded read of a hostile
// or broken upstream is still a way to lose the process.
const maxErrorBodyBytes = 4096

// maxSSELineBytes bounds one SSE line. Generous for a chunk carrying a few
// tokens; a line beyond it means the response is not the stream we think it is.
const maxSSELineBytes = 1 << 20

// Config configures the inference probe client.
type Config struct {
	BaseURL      string
	APIKey       string
	UserAgent    string
	SystemPrompt string

	DialTimeout   time.Duration
	HeaderTimeout time.Duration
	TTFTTimeout   time.Duration
	IdleTimeout   time.Duration
	Timeout       time.Duration
}

// Client runs one inference probe against an OpenAI-compatible endpoint.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient builds a Client.
//
// DisableKeepAlives is deliberate and load-bearing: every run must open a fresh
// connection, otherwise httptrace reports zeros for DNS/connect/TLS and the
// cross-check against the standalone ping silently becomes meaningless. At a
// five-minute cadence Go's 90 s IdleConnTimeout means no keep-alive would
// survive anyway — this makes the guarantee explicit rather than incidental.
func NewClient(cfg Config) *Client {
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{
			Timeout: cfg.DialTimeout,
		}).DialContext,
		TLSHandshakeTimeout: cfg.DialTimeout,
		// Bounds "connected but no headers" separately from the outer deadline,
		// so header_timeout and timeout stay distinguishable.
		ResponseHeaderTimeout: cfg.HeaderTimeout,
	}
	return &Client{
		cfg: cfg,
		// No Timeout on the http.Client itself: it would cover the whole body
		// read, colliding with the TTFT and idle watchdogs and collapsing three
		// distinct findings back into one. The overall bound is a context
		// deadline in Run.
		http: &http.Client{Transport: transport},
	}
}

// Request describes one probe run.
type Request struct {
	ModelID string
	Probe   string
	// Prompt is the user message. For wide it already carries the cache-defeat
	// nonce ahead of the document.
	Prompt string
	// MaxTokens caps the output. Verified honoured under this exact field name.
	MaxTokens int
	// QuestionID labels which bank entry produced Prompt, for the correctness canary.
	QuestionID string
	// Assert reports whether the answer is correct. Nil skips the check (wide
	// has no single assertable answer).
	Assert func(content string) bool
}

// Run executes one probe and returns a result.
//
// It returns an error only for a caller mistake (an unbuildable request). Every
// transport, protocol and timeout failure comes back as a RESULT with OK=false
// and an ErrorClass — a timeout is a recorded sample, never a dropped one, and
// dropping it would make the availability strip lie by omission.
func (c *Client) Run(ctx context.Context, req Request) (InferResult, error) {
	res := InferResult{ModelID: req.ModelID, Probe: req.Probe, QuestionID: req.QuestionID}

	// An unset cap is a caller bug, and a silent one: MaxCompletionTokens is
	// `omitempty`, so zero drops the field entirely and the model runs to its
	// own default. That is the exact failure the pre-implementation curls
	// existed to rule out — it breaks the latency numbers (a 2 000-token
	// response is not measuring what a 70-token one measures) and the cost
	// model at the same time. Fail loudly rather than bill for it.
	if req.MaxTokens <= 0 {
		return res, fmt.Errorf("probe request for %s/%s has no output cap", req.ModelID, req.Probe)
	}

	body, err := json.Marshal(chatCompletionRequest{
		Model: req.ModelID,
		Messages: []Message{
			// The system message is NOT cosmetic. Without one MiMo injects its
			// own — 250 prompt tokens, ~192 of them served from cache — which
			// inflates the token budget ~6.5x and turns the prefill this probe
			// exists to time into a cache lookup.
			{Role: "system", Content: c.cfg.SystemPrompt},
			{Role: "user", Content: req.Prompt},
		},
		Stream:              true,
		Thinking:            &thinkingOption{Type: "disabled"},
		EnableThinking:      boolPtr(false),
		MaxCompletionTokens: req.MaxTokens,
		StreamOptions:       &streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return res, fmt.Errorf("marshal probe request: %w", err)
	}

	// The overall deadline is the backstop, below the TTFT and idle watchdogs
	// rather than instead of them.
	runCtx, cancelRun := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancelRun()
	// A separate cancel-with-cause layer carries WHICH watchdog fired, so the
	// error class survives the trip back out through the transport.
	streamCtx, cancelStream := context.WithCancelCause(runCtx)
	defer cancelStream(nil)

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost,
		strings.TrimSuffix(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return res, fmt.Errorf("build probe request: %w", err)
	}

	// A fresh session id per run. loom caches one per conversation so a thread
	// pins to a single upstream node; here that would pin every probe to
	// whichever node answered first and report that one node as MiMo.
	sessionID := NewSessionID()
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("User-Agent", c.cfg.UserAgent)
	httpReq.Header.Set("X-Session-Id", sessionID)
	httpReq.Header.Set("X-Session-Affinity", sessionID)
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	// Accept-Encoding deliberately unset — net/http negotiates and decompresses
	// gzip transparently. Setting it by hand disables that and would leave the
	// stream reader parsing compressed bytes.

	start := time.Now()
	trace, traceTimes := newConnTrace(start)
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(streamCtx, trace))

	resp, err := c.http.Do(httpReq)
	if err != nil {
		res.ErrorClass = classifyRequestErr(ctx, streamCtx, err)
		res.ErrorDetail = err.Error()
		res.TotalMs = msSince(start)
		applyTrace(&res, traceTimes)
		return res, nil
	}
	defer resp.Body.Close()

	res.HTTPStatus = resp.StatusCode
	applyTrace(&res, traceTimes)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		res.ErrorClass = classifyStatus(resp.StatusCode)
		res.ErrorDetail = string(detail)
		res.TotalMs = msSince(start)
		return res, nil
	}

	c.readStream(streamCtx, cancelStream, resp.Body, start, &res)
	res.TotalMs = msSince(start)

	if res.OK && req.Assert != nil {
		ok := req.Assert(res.Content)
		res.AnswerOK = &ok
	}
	return res, nil
}

// readStream consumes the SSE body, timing every chunk arrival.
//
// Streaming is not optional. TTFT is unmeasurable without it; it separates "no
// first token for 150 s" (queueing) from "first token at 800 ms then 2 tok/s"
// (throughput collapse); and the idle watchdog only exists because chunks
// arrive one at a time.
func (c *Client) readStream(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	body io.Reader,
	start time.Time,
	res *InferResult,
) {
	// Two watchdogs, not one. "Headers arrived but no first token" and "died
	// mid-stream" are different findings, and loom's single idle timer seeded at
	// request start cannot tell them apart.
	ttftTimer := time.AfterFunc(c.cfg.TTFTTimeout, func() { cancel(ErrTTFTTimeout) })
	defer ttftTimer.Stop()
	var idleTimer *time.Timer
	defer func() {
		if idleTimer != nil {
			idleTimer.Stop()
		}
	}()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineBytes)

	var (
		firstDelta   time.Time
		firstContent time.Time
		lastChunk    time.Time
		gaps         []float64
		content      strings.Builder
		sawDone      bool
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			sawDone = true
			break
		}

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// A single malformed chunk is a protocol failure, not a slow model.
			res.ErrorClass = ErrClassProtocol
			res.ErrorDetail = "unparseable SSE chunk: " + err.Error()
			return
		}

		// The usage-only chunk arrives last and carries no choices.
		if chunk.Usage != nil {
			res.Usage = *chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			res.FinishReason = choice.FinishReason
		}

		// "Any delta" includes the opening role chunk, whose content is empty.
		// That chunk is the first thing the model emitted, so it is what ttft_ms
		// measures — and treating only non-empty content as a delta would
		// collapse ttft_ms onto ttfat_ms.
		hasDelta := choice.Delta.Role != "" ||
			choice.Delta.Content != "" ||
			choice.Delta.ReasoningContent != ""
		now := time.Now()

		// ttft_ms: the first chunk carrying ANY delta.
		if hasDelta && firstDelta.IsZero() {
			firstDelta = now
			ttftTimer.Stop()
			// The idle watchdog starts only once the stream is genuinely
			// flowing, so a slow first token is charged to TTFT and never
			// misreported as a stall.
			idleTimer = time.AfterFunc(c.cfg.IdleTimeout, func() { cancel(ErrStalled) })
		} else if hasDelta && idleTimer != nil {
			idleTimer.Reset(c.cfg.IdleTimeout)
		}

		// ttfat_ms: the first chunk carrying actual CONTENT. MiMo opens with an
		// empty-content role chunk, so these differ by one chunk when healthy.
		if choice.Delta.Content != "" {
			if firstContent.IsZero() {
				firstContent = now
			}
			// Inter-token gaps are measured between content chunks only. A
			// reasoning chunk arriving in a thinking-disabled run is an anomaly
			// to surface, not a sample to fold into the throughput median.
			if !lastChunk.IsZero() {
				gaps = append(gaps, float64(now.Sub(lastChunk).Nanoseconds())/1e6)
			}
			lastChunk = now
			content.WriteString(choice.Delta.Content)
		}
	}

	if err := scanner.Err(); err != nil {
		res.ErrorClass = classifyStreamErr(ctx, err)
		res.ErrorDetail = err.Error()
		return
	}
	// The scanner can also stop because the watchdog cancelled the context,
	// which surfaces as a clean EOF on some transports rather than an error.
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		res.ErrorClass = classifyCause(cause)
		res.ErrorDetail = cause.Error()
		return
	}
	if firstDelta.IsZero() {
		// Headers and a clean body, but the model never emitted anything.
		res.ErrorClass = ErrClassProtocol
		res.ErrorDetail = "stream produced no deltas"
		return
	}
	if !sawDone {
		// Truncated mid-stream without [DONE]: the upstream went away.
		res.ErrorClass = ErrClassStalled
		res.ErrorDetail = "stream ended without [DONE]"
		return
	}

	res.TTFTMs = float64(firstDelta.Sub(start).Nanoseconds()) / 1e6
	if !firstContent.IsZero() {
		res.TTFATMs = float64(firstContent.Sub(start).Nanoseconds()) / 1e6
	}
	res.ITLP50Ms = percentile(gaps, 50)
	res.ITLP95Ms = percentile(gaps, 95)
	res.Content = content.String()

	// Gross throughput over the DECODE window (first content token to last),
	// not over the whole request: including TTFT would fold queueing into a
	// number that is supposed to describe generation speed.
	if n := res.Usage.CompletionTokens; n > 0 && !firstContent.IsZero() && lastChunk.After(firstContent) {
		res.OutputTPS = float64(n) / lastChunk.Sub(firstContent).Seconds()
	}
	res.OK = true
}

// percentile returns the p-th percentile of vals using nearest-rank. Returns 0
// for an empty set, which the caller stores as "no reading" rather than "zero
// latency" — a one-chunk response genuinely has no inter-token interval.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	rank := int(float64(len(sorted))*p/100 + 0.5)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// connTrace collects the connection-level split for one run.
type connTrace struct {
	start               time.Time
	dnsStart, dnsDone   time.Time
	connStart, connDone time.Time
	tlsStart, tlsDone   time.Time
}

func newConnTrace(start time.Time) (*httptrace.ClientTrace, *connTrace) {
	t := &connTrace{start: start}
	return &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { t.dnsStart = time.Now() },
		DNSDone:           func(httptrace.DNSDoneInfo) { t.dnsDone = time.Now() },
		ConnectStart:      func(string, string) { t.connStart = time.Now() },
		ConnectDone:       func(string, string, error) { t.connDone = time.Now() },
		TLSHandshakeStart: func() { t.tlsStart = time.Now() },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { t.tlsDone = time.Now() },
	}, t
}

func applyTrace(res *InferResult, t *connTrace) {
	if !t.dnsStart.IsZero() && !t.dnsDone.IsZero() {
		res.DNSMs = float64(t.dnsDone.Sub(t.dnsStart).Nanoseconds()) / 1e6
	}
	if !t.connStart.IsZero() && !t.connDone.IsZero() {
		res.ConnectMs = float64(t.connDone.Sub(t.connStart).Nanoseconds()) / 1e6
	}
	if !t.tlsStart.IsZero() && !t.tlsDone.IsZero() {
		res.TLSMs = float64(t.tlsDone.Sub(t.tlsStart).Nanoseconds()) / 1e6
	}
}

func classifyStatus(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return ErrClassRateLimited
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ErrClassAuth
	default:
		return ErrClassHTTP
	}
}

// classifyRequestErr maps a failure that happened before any body was read.
// outer is the caller's context: a cancellation there is shutdown, not a fault.
func classifyRequestErr(outer, stream context.Context, err error) string {
	if cause := context.Cause(stream); cause != nil {
		if class := classifyCause(cause); class != "" {
			return class
		}
	}
	if outer.Err() == context.Canceled {
		return ErrClassCanceled
	}
	// Checked BEFORE the DeadlineExceeded test, not after: http.Transport
	// surfaces ResponseHeaderTimeout as an error that also satisfies
	// errors.Is(err, context.DeadlineExceeded), so testing the deadline first
	// swallows every header timeout into the generic "timeout" class — which is
	// exactly the collapse the ladder exists to prevent.
	if strings.Contains(err.Error(), "awaiting response headers") ||
		strings.Contains(err.Error(), "awaiting headers") {
		return ErrClassHeaderTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// The outer deadline tripped before headers.
		return ErrClassTimeout
	}
	// Checked BEFORE the net.Error timeout test: a resolver that times out on
	// its own returns a *net.DNSError whose Timeout() is true, so testing
	// net.Error first reports a DNS outage as a connect timeout — blaming the
	// endpoint for a failure that never reached it. (A lookup killed by the
	// OUTER deadline still lands on `timeout` above, which is correct: what
	// expired was the run, not the resolver.)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrClassDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrClassConnectTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ErrClassRefused
	}
	if strings.Contains(err.Error(), "connection refused") {
		return ErrClassRefused
	}
	return ErrClassConnectTimeout
}

func classifyStreamErr(ctx context.Context, err error) string {
	if cause := context.Cause(ctx); cause != nil {
		if class := classifyCause(cause); class != "" {
			return class
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrClassTimeout
	}
	// An oversized SSE line is the response not being the stream we think it is,
	// which is what maxSSELineBytes exists to detect. Without this it falls
	// through to `stalled` and reads on the dashboard as a MiMo network stall
	// rather than the protocol failure it is.
	if errors.Is(err, bufio.ErrTooLong) {
		return ErrClassProtocol
	}
	return ErrClassStalled
}

// classifyCause maps a watchdog cancellation cause onto an error class.
// Returns "" when the cause is not one of ours.
func classifyCause(cause error) string {
	switch {
	case errors.Is(cause, ErrStalled):
		return ErrClassStalled
	case errors.Is(cause, ErrTTFTTimeout):
		return ErrClassTTFTTimeout
	case errors.Is(cause, context.DeadlineExceeded):
		return ErrClassTimeout
	default:
		return ""
	}
}

func boolPtr(b bool) *bool { return &b }
