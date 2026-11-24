// Package analyticsfeed reports every request this server answers to a Trakk
// collector's server feed (POST /api/feed): the half of the traffic the
// browser script never sees — crawlers, scanners, the 403s banGate hands out,
// the 429s the limiters write. The contract is Trakk's INTEGRATION.md.
//
// Nothing here may slow down or fail a request. The middleware only enqueues;
// a full queue drops the event and counts it. One goroutine (Run) batches and
// sends. A collector that is down costs a log line per failed send, never a
// visitor's wait.
//
// No ismimodown import on purpose (kit is the one dependency). The other apps
// use trick77/toolkit's analytics package; this one predates it.
package analyticsfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/trick77/ship/kit/httpx"
)

// The collector's limits (Trakk README, "Server feed"): a batch over either
// is a 413 and lost whole.
const (
	maxBatchEvents = 500
	// 256 KiB is the collector's bound; the envelope and slack stay under it.
	maxBatchBytes = 240 << 10
)

// Per-field limits in bytes. The collector clips longer values itself; doing
// it here keeps one hostile user agent from inflating a batch past its bound.
const (
	limitMethod = 16
	limitSF     = 32
	limitRoute  = 200
	limitString = 512
	limitURL    = 2048
)

// Retry timing. A send gets the 30 s Trakk's contract asks for: the
// collector takes that long to receive a body, and a shorter client gives up on
// a batch Trakk then stores, so the retry stores it twice. Without
// Retry-After the wait doubles per failure up to the cap and resets on success.
const (
	sendTimeout  = 30 * time.Second
	backoffStart = 30 * time.Second
	backoffMax   = 30 * time.Minute
	// After Run's ctx ends, sends get this long in all, the one in flight
	// included: a dead collector must not hold up a shutdown.
	shutdownGrace = 5 * time.Second
)

// Event is one request, in the collector's field names. Only these fields
// leave the process, never raw headers: a header can carry a credential.
type Event struct {
	T      int64  `json:"t"`
	IP     string `json:"ip"`
	UA     string `json:"ua,omitempty"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Route  string `json:"route,omitempty"`
	Status int    `json:"status"`
	DurMS  int64  `json:"dur_ms"`
	Bytes  int64  `json:"bytes"`
	Ref    string `json:"ref,omitempty"`
	Lang   string `json:"lang,omitempty"`
	SFSite string `json:"sf_site,omitempty"`
	SFMode string `json:"sf_mode,omitempty"`
	SFDest string `json:"sf_dest,omitempty"`
	ChUA   string `json:"ch_ua,omitempty"`
}

// Options configures a Feeder.
type Options struct {
	// Endpoint is the collector's feed URL, https://<Trakk host>/api/feed.
	Endpoint string
	// Key is the site's feed key, sent as a bearer token. Never logged.
	Key string
	// Site is the domain the key belongs to in the collector's sites.json.
	Site string
	// ClientIP names the caller's address. It must be the real address, not
	// a rate-limit bucket key: the collector drops an event whose ip does not
	// parse, and a /64 does not. A value that does not parse falls back to
	// the connection's peer.
	ClientIP func(*http.Request) string
	// Skip leaves a request out (the container healthcheck).
	Skip func(*http.Request) bool

	// QueueSize bounds the events waiting to be sent. Default 5000.
	QueueSize int
	// FlushEvery is the longest an event waits for a batch. Default 10 s.
	FlushEvery time.Duration
	// Client sends the batches. Default: a client with a 30 s timeout.
	Client *http.Client
	// Now is a seam for tests.
	Now func() time.Time
}

// Feeder queues request events and sends them in batches.
type Feeder struct {
	o       Options
	queue   chan Event
	dropped atomic.Int64
}

// New builds a Feeder. Call Run to start sending.
func New(o Options) *Feeder {
	if o.QueueSize <= 0 {
		o.QueueSize = 5000
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = 10 * time.Second
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: sendTimeout}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Feeder{o: o, queue: make(chan Event, o.QueueSize)}
}

// Dropped is how many events a full queue or a refused batch has lost.
func (f *Feeder) Dropped() int64 { return f.dropped.Load() }

// Middleware records every request that passes through it. It must wrap the
// ban gate and the limiters, so their 403s and 429s are fed too, and sit
// inside any middleware that clones the request (kit's AccessLog and Recover
// do, r.WithContext): the mux writes the route onto the request it gets, and a
// clone's route never reaches this one.
//
// A handler that panics is fed as the 500 Recover will write, then the panic
// goes on.
func (f *Feeder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.o.Skip != nil && f.o.Skip(r) {
			next.ServeHTTP(w, r)
			return
		}
		start := f.o.Now()
		cw := &countingWriter{StatusWriter: httpx.NewStatusWriter(w)}
		defer func() {
			status := cw.Status()
			p := recover()
			if p != nil {
				status = http.StatusInternalServerError
			}
			f.record(r, start, status, cw.bytes)
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(cw, r)
	})
}

func (f *Feeder) record(r *http.Request, start time.Time, status int, n int64) {
	if status == 0 {
		status = http.StatusOK
	}
	ip, ok := f.clientAddr(r)
	if !ok {
		// Trakk drops an event without a parseable address; sending it would
		// only cost batch room.
		f.dropped.Add(1)
		return
	}
	h := r.Header
	f.Enqueue(Event{
		T:      start.UnixMilli(),
		IP:     ip,
		UA:     clip(h.Get("User-Agent"), limitString),
		Method: clip(r.Method, limitMethod),
		Path:   clip(r.URL.Path, limitURL),
		Route:  clip(route(r.Pattern), limitRoute),
		Status: status,
		DurMS:  f.o.Now().Sub(start).Milliseconds(),
		Bytes:  n,
		Ref:    clip(stripQuery(h.Get("Referer")), limitURL),
		Lang:   clip(h.Get("Accept-Language"), limitString),
		SFSite: clip(h.Get("Sec-Fetch-Site"), limitSF),
		SFMode: clip(h.Get("Sec-Fetch-Mode"), limitSF),
		SFDest: clip(h.Get("Sec-Fetch-Dest"), limitSF),
		ChUA:   clip(h.Get("Sec-CH-UA"), limitString),
	})
}

// route is the mux pattern when it names a method ("GET /api/dashboard"), else
// empty. A methodless one is a prefix or catch-all ("/api/" where a limiter
// refused before the inner mux ran, "/" for static files); Trakk wants those
// empty, and names the path by the site's rules instead.
func route(pattern string) string {
	if !strings.Contains(pattern, " ") {
		return ""
	}
	return pattern
}

// clientAddr is ClientIP's address when it parses, else the connection's
// peer: a malformed X-Forwarded-For entry must not cost the event.
func (f *Feeder) clientAddr(r *http.Request) (string, bool) {
	if f.o.ClientIP != nil {
		if a, err := netip.ParseAddr(f.o.ClientIP(r)); err == nil {
			return a.String(), true
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.String(), true
	}
	return "", false
}

// Enqueue adds one event, or drops it when the queue is full: a request never
// waits for the collector.
func (f *Feeder) Enqueue(e Event) {
	select {
	case f.queue <- e:
	default:
		f.dropped.Add(1)
	}
}

// Run sends batches until ctx ends, then drains the queue and sends once more.
// Cancel ctx only once the HTTP server has stopped: requests still draining
// (every open event stream ends there) are fed into the queue.
//
// Sends do not end with ctx: a batch Trakk took but whose answer a cut
// connection lost would be kept and sent twice. They get shutdownGrace after
// ctx ends, in all, then are cut.
func (f *Feeder) Run(ctx context.Context) {
	sendCtx, cancelSend := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelSend()
	stopGrace := context.AfterFunc(ctx, func() { time.AfterFunc(shutdownGrace, cancelSend) })
	defer stopGrace()

	tick := time.NewTicker(f.o.FlushEvery)
	defer tick.Stop()
	var pending []json.RawMessage
	size := 0
	// After a 429 or a failure the collector is left alone until then.
	var notBefore time.Time
	failures := 0

	flush := func() {
		for len(pending) > 0 {
			n, _ := batchCut(pending)
			wait, err := f.send(sendCtx, pending[:n])
			switch {
			case err == nil:
				pending = pending[n:]
				failures = 0
			case errors.Is(err, errRetry):
				if wait <= 0 {
					wait = backoff(failures)
				}
				failures++
				notBefore = f.o.Now().Add(wait)
				slog.Warn("analytics feed deferred", "events", len(pending), "retry_in", wait.Round(time.Second), "reason", err.Error())
				size = sizeOf(pending)
				return
			default:
				// The collector will refuse this batch again (400, 401, 413):
				// keeping it would block every batch behind it.
				f.dropped.Add(int64(n))
				slog.Error("analytics feed batch refused", "events", n, "reason", err.Error())
				pending = pending[n:]
			}
			// A shutdown takes over between batches: it sends what is left
			// within its own grace.
			if ctx.Err() != nil {
				size = sizeOf(pending)
				return
			}
		}
		size = 0
	}
	ready := func() bool { return !f.o.Now().Before(notBefore) }

	for {
		select {
		case <-ctx.Done():
		drain:
			for {
				select {
				case e := <-f.queue:
					pending = f.add(pending, e, &size)
				default:
					break drain
				}
			}
			for len(pending) > 0 && sendCtx.Err() == nil {
				before := len(pending)
				flush()
				if len(pending) == before {
					break
				}
			}
			if len(pending) > 0 {
				f.dropped.Add(int64(len(pending)))
				slog.Warn("analytics feed: events lost at shutdown", "events", len(pending))
			}
			return
		case e := <-f.queue:
			pending = f.add(pending, e, &size)
			if (len(pending) >= maxBatchEvents || size >= maxBatchBytes) && ready() {
				flush()
			}
		case <-tick.C:
			if len(pending) > 0 && ready() {
				flush()
			}
		}
	}
}

// backoff is the wait after the n-th consecutive failure without a
// Retry-After: doubling from backoffStart, capped at backoffMax.
func backoff(n int) time.Duration {
	d := backoffStart
	for range n {
		d *= 2
		if d >= backoffMax {
			return backoffMax
		}
	}
	return d
}

// pendingMaxBytes bounds what waits while the collector is away: past it the
// oldest events go, so a long outage costs history, never memory. Bytes, not
// events: JSON escapes '<', '>', '&' and broken UTF-8 to six bytes each, so a
// scanner's clipped path can still marshal to several times its length.
const pendingMaxBytes = 4 << 20

func (f *Feeder) add(pending []json.RawMessage, e Event, size *int) []json.RawMessage {
	b, err := json.Marshal(e)
	if err != nil {
		f.dropped.Add(1)
		return pending
	}
	for len(pending) > 0 && *size+len(b) > pendingMaxBytes {
		*size -= len(pending[0])
		pending = pending[1:]
		f.dropped.Add(1)
	}
	*size += len(b)
	return append(pending, b)
}

// batchCut is how many leading events fit one batch.
func batchCut(evs []json.RawMessage) (n, size int) {
	for n < len(evs) && n < maxBatchEvents {
		if n > 0 && size+len(evs[n])+1 > maxBatchBytes {
			break
		}
		size += len(evs[n]) + 1
		n++
	}
	return n, size
}

func sizeOf(evs []json.RawMessage) int {
	s := 0
	for _, e := range evs {
		s += len(e)
	}
	return s
}

// errRetry marks a batch the collector may take later: 408, 429, 5xx or no
// answer at all.
var errRetry = errors.New("retry")

type batch struct {
	V      int               `json:"v"`
	Site   string            `json:"site"`
	Events []json.RawMessage `json:"events"`
}

// send posts one batch. A retryable failure wraps errRetry and returns the
// collector's Retry-After, or 0 for the caller's back-off.
func (f *Feeder) send(ctx context.Context, evs []json.RawMessage) (time.Duration, error) {
	body, err := json.Marshal(batch{V: 1, Site: f.o.Site, Events: evs})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.o.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.o.Key)
	resp, err := f.o.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", errRetry, transportReason(err))
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode/100 == 2:
		return 0, nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode >= 500:
		return retryAfter(resp.Header.Get("Retry-After")), fmt.Errorf("%w: status %d", errRetry, resp.StatusCode)
	default:
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
}

// transportReason names a transport failure in a word: the log line stays
// short and carries nothing of the request.
func transportReason(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "unreachable"
}

// retryAfter reads Retry-After in seconds, or 0 when it is missing or odd.
// Capped at an hour: a typo on the collector must not silence the feed for a
// day.
func retryAfter(v string) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || s <= 0 {
		return 0
	}
	return min(time.Duration(s)*time.Second, time.Hour)
}

// countingWriter is kit's StatusWriter plus the body bytes sent. Flush,
// Hijack and Unwrap are kit's: /api/events streams through it.
type countingWriter struct {
	*httpx.StatusWriter
	bytes int64
}

func (w *countingWriter) Write(b []byte) (int, error) {
	n, err := w.StatusWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// stripQuery drops a URL's query and fragment: they carry tokens and search
// terms nobody asked to keep.
func stripQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
