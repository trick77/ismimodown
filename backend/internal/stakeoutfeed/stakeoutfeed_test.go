package stakeoutfeed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector is a fake Stakeout /api/feed: it records each batch and answers
// with the next status in line (204 once the line is empty).
type collector struct {
	mu       sync.Mutex
	batches  []batch
	auth     []string
	statuses []int
	got      chan struct{}
}

func newCollector(statuses ...int) (*collector, *httptest.Server) {
	c := &collector{statuses: statuses, got: make(chan struct{}, 100)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b batch
		_ = json.Unmarshal(body, &b)
		c.mu.Lock()
		c.batches = append(c.batches, b)
		c.auth = append(c.auth, r.Header.Get("Authorization"))
		status := http.StatusNoContent
		if len(c.statuses) > 0 {
			status, c.statuses = c.statuses[0], c.statuses[1:]
		}
		c.mu.Unlock()
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "1")
		}
		w.WriteHeader(status)
		c.got <- struct{}{}
	}))
	return c, srv
}

func (c *collector) wait(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-c.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("collector got fewer than %d batches", n)
		}
	}
}

func (c *collector) events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Event
	for _, b := range c.batches {
		for _, raw := range b.Events {
			var e Event
			_ = json.Unmarshal(raw, &e)
			out = append(out, e)
		}
	}
	return out
}

func newFeeder(endpoint string, o Options) *Feeder {
	o.Endpoint = endpoint
	o.Key = "feed-key"
	o.Site = "example.com"
	if o.ClientIP == nil {
		o.ClientIP = func(*http.Request) string { return "203.0.113.9" }
	}
	if o.FlushEvery == 0 {
		o.FlushEvery = 20 * time.Millisecond
	}
	return New(o)
}

// One request through the middleware arrives at the collector as one event in
// the collector's field names, with the bearer key and the site on the batch.
func TestMiddlewareFeedsTheRequest(t *testing.T) {
	c, srv := newCollector()
	defer srv.Close()
	f := newFeeder(srv.URL+"/api/feed", Options{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("12345"))
	})
	h := f.Middleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?window=24h", nil)
	req.Header.Set("User-Agent", "curl/8")
	req.Header.Set("Referer", "https://search.example/?q=secret#frag")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Accept-Language", "de-CH")
	req.Header.Set("Authorization", "Bearer visitor-secret")
	h.ServeHTTP(httptest.NewRecorder(), req)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	c.wait(t, 1)
	cancel()
	<-done

	evs := c.events()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	e := evs[0]
	if e.IP != "203.0.113.9" || e.Method != "GET" || e.Path != "/api/dashboard" || e.Status != http.StatusTeapot ||
		e.Bytes != 5 || e.Route != "GET /api/dashboard" || e.UA != "curl/8" || e.SFSite != "cross-site" ||
		e.Lang != "de-CH" {
		t.Errorf("event = %+v", e)
	}
	if e.Ref != "https://search.example/" {
		t.Errorf("ref = %q, want the query and fragment dropped", e.Ref)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auth[0] != "Bearer feed-key" {
		t.Errorf("Authorization = %q", c.auth[0])
	}
	if b := c.batches[0]; b.V != 1 || b.Site != "example.com" {
		t.Errorf("envelope = v %d site %q", b.V, b.Site)
	}
}

// Raw headers never leave: a visitor's Authorization or Cookie is no field of
// the event, whatever it carries.
func TestNoRawHeaderIsSent(t *testing.T) {
	c, srv := newCollector()
	defer srv.Close()
	f := newFeeder(srv.URL, Options{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer visitor-secret")
	req.Header.Set("Cookie", "session=visitor-secret")
	f.Middleware(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), req)

	ctx, cancel := context.WithCancel(context.Background())
	go f.Run(ctx)
	c.wait(t, 1)
	cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	raw, _ := json.Marshal(c.batches)
	if strings.Contains(string(raw), "visitor-secret") {
		t.Errorf("batch carries a raw header: %s", raw)
	}
}

// Skipped requests (the healthcheck) are not fed.
func TestSkipLeavesTheRequestOut(t *testing.T) {
	f := newFeeder("http://127.0.0.1:1", Options{Skip: func(r *http.Request) bool { return r.URL.Path == "/healthz" }})
	f.Middleware(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if n := len(f.queue); n != 0 {
		t.Errorf("queued %d events for a skipped request", n)
	}
}

// A full queue drops, it never blocks the request.
func TestFullQueueDropsInsteadOfBlocking(t *testing.T) {
	f := newFeeder("http://127.0.0.1:1", Options{QueueSize: 2})
	h := f.Middleware(http.NotFoundHandler())
	done := make(chan struct{})
	go func() {
		for range 5 {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a full queue blocked the request")
	}
	if got := f.Dropped(); got != 3 {
		t.Errorf("dropped = %d, want 3", got)
	}
}

// A 429 keeps the batch and sends it again once Retry-After has passed.
func TestRateLimitedBatchIsSentAgain(t *testing.T) {
	c, srv := newCollector(http.StatusTooManyRequests)
	defer srv.Close()
	f := newFeeder(srv.URL, Options{})
	f.Enqueue(Event{IP: "203.0.113.1", Method: "GET", Path: "/"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)
	c.wait(t, 2)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.batches[1].Events) != 1 {
		t.Errorf("retry carried %d events, want the same 1", len(c.batches[1].Events))
	}
	if f.Dropped() != 0 {
		t.Errorf("dropped = %d, want 0", f.Dropped())
	}
}

// A batch the collector will refuse again (400, 401, 413) is dropped and
// counted, so it does not block every batch behind it.
func TestRefusedBatchIsDropped(t *testing.T) {
	c, srv := newCollector(http.StatusUnauthorized)
	defer srv.Close()
	f := newFeeder(srv.URL, Options{})
	f.Enqueue(Event{IP: "203.0.113.1", Method: "GET", Path: "/"})

	ctx, cancel := context.WithCancel(context.Background())
	go f.Run(ctx)
	c.wait(t, 1)
	f.Enqueue(Event{IP: "203.0.113.2", Method: "GET", Path: "/"})
	c.wait(t, 1)
	cancel()

	if f.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", f.Dropped())
	}
	if evs := c.events(); evs[len(evs)-1].IP != "203.0.113.2" {
		t.Errorf("the next batch did not go out after the refused one")
	}
}

// A batch never exceeds the collector's 500 events.
func TestBatchesStayWithinTheEventLimit(t *testing.T) {
	c, srv := newCollector()
	defer srv.Close()
	f := newFeeder(srv.URL, Options{QueueSize: 2000, FlushEvery: time.Hour})
	for range 1200 {
		f.Enqueue(Event{IP: "203.0.113.1", Method: "GET", Path: "/"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	c.wait(t, 2)
	cancel()
	<-done

	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, b := range c.batches {
		if len(b.Events) > maxBatchEvents {
			t.Errorf("batch of %d events, limit %d", len(b.Events), maxBatchEvents)
		}
		total += len(b.Events)
	}
	if total != 1200 {
		t.Errorf("sent %d events, want all 1200 (the rest on shutdown)", total)
	}
}

// A batch stays under the collector's byte bound even when every event is at
// its field limits.
func TestBatchCutHonoursTheByteBound(t *testing.T) {
	big := Event{IP: "203.0.113.1", Method: "GET", Path: "/" + strings.Repeat("a", limitURL-1),
		Ref: strings.Repeat("r", limitURL), UA: strings.Repeat("u", limitString)}
	raw, _ := json.Marshal(big)
	evs := make([]json.RawMessage, maxBatchEvents)
	for i := range evs {
		evs[i] = raw
	}
	n, size := batchCut(evs)
	if n == 0 || n >= maxBatchEvents || size > maxBatchBytes {
		t.Errorf("batchCut = %d events, %d bytes; want fewer than %d events within %d bytes", n, size,
			maxBatchEvents, maxBatchBytes)
	}
}

// /api/events streams: the wrapper must keep flushing through.
func TestMiddlewareKeepsTheStreamFlushing(t *testing.T) {
	f := newFeeder("http://127.0.0.1:1", Options{})
	flushed := false
	h := f.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: x\n\n"))
		flushed = http.NewResponseController(w).Flush() == nil
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if !flushed {
		t.Error("Flush through the feed's writer failed")
	}
}

// A panicking handler is fed as the 500 Recover writes, and the panic goes on.
func TestPanicIsFedAs500(t *testing.T) {
	f := newFeeder("http://127.0.0.1:1", Options{})
	h := f.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not go on")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if e := <-f.queue; e.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", e.Status)
	}
}

// Only a method-qualified pattern is a route; a prefix or catch-all goes empty
// so Stakeout names the path by the site's rules.
func TestRouteNeedsAMethod(t *testing.T) {
	for in, want := range map[string]string{"GET /api/dashboard": "GET /api/dashboard", "/api/": "", "/": "", "": ""} {
		if got := route(in); got != want {
			t.Errorf("route(%q) = %q, want %q", in, got, want)
		}
	}
}

// A malformed forwarded address falls back to the peer; neither parsing drops
// the event rather than sending one Stakeout would drop.
func TestClientAddrFallsBackToThePeer(t *testing.T) {
	f := newFeeder("http://127.0.0.1:1", Options{ClientIP: func(*http.Request) string { return strings.Repeat("x", 1000) }})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[2001:db8::1]:443"
	if got, ok := f.clientAddr(r); !ok || got != "2001:db8::1" {
		t.Errorf("clientAddr = %q, %v; want the peer", got, ok)
	}
	r.RemoteAddr = "garbage"
	f.Middleware(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), r)
	if len(f.queue) != 0 || f.Dropped() != 1 {
		t.Errorf("queued %d, dropped %d; want the addressless event dropped", len(f.queue), f.Dropped())
	}
}

func TestBackoffDoublesToTheCap(t *testing.T) {
	if backoff(0) != backoffStart || backoff(1) != 2*backoffStart || backoff(100) != backoffMax {
		t.Errorf("backoff = %v %v %v", backoff(0), backoff(1), backoff(100))
	}
}

// A collector that never answers cannot hold up a shutdown past the grace.
func TestShutdownIsBoundedByTheGrace(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	f := newFeeder(srv.URL, Options{FlushEvery: time.Millisecond})
	f.Enqueue(Event{IP: "203.0.113.1", Method: "GET", Path: "/"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // the send is in flight
	cancel()
	select {
	case <-done:
	case <-time.After(shutdownGrace + 3*time.Second):
		t.Fatal("Run outlived the shutdown grace")
	}
	if f.Dropped() != 1 {
		t.Errorf("dropped = %d, want the unsent event counted", f.Dropped())
	}
}

// While the collector is away the backlog is bounded in bytes, oldest out:
// escape-heavy paths marshal to several times their clipped length.
func TestBacklogIsBoundedInBytes(t *testing.T) {
	f := newFeeder("http://127.0.0.1:1", Options{})
	e := Event{IP: "203.0.113.1", Method: "GET", Path: clip("/"+strings.Repeat("<", limitURL), limitURL)}
	var pending []json.RawMessage
	size := 0
	for range 2000 {
		pending = f.add(pending, e, &size)
	}
	if size > pendingMaxBytes || size != sizeOf(pending) {
		t.Errorf("backlog = %d bytes (counted %d), bound %d", sizeOf(pending), size, pendingMaxBytes)
	}
	if f.Dropped() == 0 || int(f.Dropped())+len(pending) != 2000 {
		t.Errorf("dropped %d, kept %d of 2000", f.Dropped(), len(pending))
	}
}

func TestClipKeepsUTF8Whole(t *testing.T) {
	if got := clip("aé", 2); got != "a" {
		t.Errorf("clip = %q, want the cut before é", got)
	}
}
