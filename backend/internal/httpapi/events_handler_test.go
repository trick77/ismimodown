package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ship/kit/ratelimit"
	"github.com/trick77/ship/kit/sse"
)

// The stream is the reason the dashboard updates without polling, so it has to
// work end to end through the middleware chain — a wrapper that swallows Flush
// would leave every event in the buffer.
func TestEventsStreamsAPublishedCycle(t *testing.T) {
	db := openTestDB(t)
	broker := newTestHub()
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db), Broker: broker,
		Models: []string{"mimo-v2.6-flash"},
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	// Must not be cached; a cached event stream is a stuck dashboard.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	// Wait until the handler has actually subscribed, then publish.
	deadline := time.Now().Add(3 * time.Second)
	for broker.Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed")
		}
		time.Sleep(2 * time.Millisecond)
	}
	broker.Publish([]byte(`{"cycle_id":42}`))

	scanner := bufio.NewScanner(resp.Body)
	var sawEvent, sawData bool
	for scanner.Scan() {
		line := scanner.Text()
		if line == "event: cycle" {
			sawEvent = true
		}
		if strings.HasPrefix(line, "data: ") {
			sawData = strings.Contains(line, `"cycle_id":42`)
			break
		}
	}
	if !sawEvent || !sawData {
		t.Errorf("did not receive the published cycle (event=%v data=%v)", sawEvent, sawData)
	}
}

// Past the cap the handler must refuse rather than hold a connection it will
// never feed — a silent, permanently-idle stream is worse than a retryable 503.
func TestEventsRefusesPastTheSubscriberCap(t *testing.T) {
	db := openTestDB(t)
	broker := newTestHub()
	h := NewServer(Deps{DB: db, Samples: samples.New(db), Broker: broker})

	// Fill every slot directly.
	var cancels []func()
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	// A distinct key per slot, so this fills the global cap rather than
	// tripping the per-client one after four.
	for i := 0; i < testHubMax; i++ {
		_, cancel, ok := broker.Subscribe(fmt.Sprintf("10.0.0.%d", i))
		if !ok {
			t.Fatalf("failed to fill slot %d", i)
		}
		cancels = append(cancels, cancel)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 once the cap is reached", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 503 must tell the client when to come back")
	}
}

// With no broker wired the route must degrade, not panic.
func TestEventsWithoutABrokerIsUnavailable(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{DB: db, Samples: samples.New(db)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// The stream is deliberately outside the request rate limiter: it is one
// request that lasts hours, so a per-request bucket says nothing about it, and
// a limiter would reject reconnects during exactly the incident people are
// watching.
func TestEventsIsNotSubjectToTheRequestRateLimiter(t *testing.T) {
	db := openTestDB(t)
	broker := newTestHub()
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db), Broker: broker,
		Limiter: newExhaustedLimiter(),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := contextWithImmediateCancel()
	h.ServeHTTP(rec, req.WithContext(ctx))
	cancel()

	if rec.Code == http.StatusTooManyRequests {
		t.Error("the event stream must not be rejected by the per-request limiter")
	}
}

func newExhaustedLimiter() *ratelimit.Limiter {
	l := ratelimit.New(ratelimit.Options{Rate: 0.00001, Burst: 1})
	l.Allow("192.0.2.1") // burn the only token for the test's client
	return l
}

func contextWithImmediateCancel() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// On shutdown the stream must return promptly. http.Server.Shutdown waits for
// active connections but never cancels their request contexts, so a handler
// blocked on r.Context() alone would hold the connection until the shutdown
// timeout expired — one open dashboard turning every restart into a hang and a
// non-zero exit. The daemon mounts the hub's Close with RegisterOnShutdown, as
// here.
func TestEventsReturnsWhenShutdownIsSignalled(t *testing.T) {
	db := openTestDB(t)
	broker := newTestHub()
	h := NewServer(Deps{DB: db, Samples: samples.New(db), Broker: broker})

	srv := httptest.NewServer(h)
	defer srv.Close()
	srv.Config.RegisterOnShutdown(broker.Close)

	resp, err := http.Get(srv.URL + "/api/events") //nolint:bodyclose // closed by the defer below
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Wait until the handler is actually subscribed and streaming.
	deadline := time.Now().Add(3 * time.Second)
	for broker.Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed")
		}
		time.Sleep(2 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := make(chan error, 1)
	go func() { shutdownErr <- srv.Config.Shutdown(ctx) }()

	// The body must reach EOF quickly, without the client cancelling, and
	// Shutdown must drain rather than run out its deadline.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close on shutdown; Shutdown would hang until its timeout")
	}
	if err := <-shutdownErr; err != nil {
		t.Errorf("Shutdown = %v, want a clean drain", err)
	}
}

// The per-client stream cap is only a bound if the handler actually tells the
// broker WHO is asking. Keyed on the same clientKey the request limiter uses,
// so the two agree on identity behind Traefik.
func TestEventsSubscribesWithTheClientKey(t *testing.T) {
	db := openTestDB(t)
	fake := &keyRecordingBroker{}
	h := NewServer(Deps{DB: db, Samples: samples.New(db), Broker: fake})

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	// Traefik appends the observed peer last; the first entry is caller-supplied
	// and must not become the identity, or one scraper mints unlimited ones.
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")
	ctx, cancel := contextWithImmediateCancel()
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	cancel()

	if fake.key != "198.51.100.7" {
		t.Errorf("subscribed with key %q, want the last X-Forwarded-For entry", fake.key)
	}
}

type keyRecordingBroker struct{ key string }

func (b *keyRecordingBroker) Subscribe(key string) (<-chan []byte, func(), bool) {
	b.key = key
	ch := make(chan []byte)
	return ch, func() {}, true
}

// testHubMax is the global cap of the test hub: small, so filling it is cheap.
const testHubMax = 8

// newTestHub is the daemon's hub shape with a smaller global cap.
func newTestHub() *sse.Hub[[]byte] {
	return sse.NewHub[[]byte](sse.HubOptions{Buffer: 1, Max: testHubMax, MaxPerKey: 4})
}

// A writer that cannot flush would leave every event in the buffer: refuse
// with a 500 before the stream's 200 rather than open a stream that delivers
// nothing. Called on the handler itself: the middleware's writers all flush,
// passing the call down, so through the chain this writer would get its 200.
func TestEventsRefusesAWriterThatCannotFlush(t *testing.T) {
	db := openTestDB(t)
	broker := newTestHub()
	h := NewServer(Deps{DB: db, Samples: samples.New(db), Broker: broker})

	w := &noFlushWriter{header: http.Header{}}
	h.inner.handleEvents(w, httptest.NewRequest(http.MethodGet, "/api/events", nil))

	if w.code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.code)
	}
	if broker.Len() != 0 {
		t.Errorf("subscriptions = %d after the handler returned, want 0", broker.Len())
	}
}

type noFlushWriter struct {
	header http.Header
	code   int
}

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(code int)        { w.code = code }
