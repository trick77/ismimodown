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

	"github.com/trick77/ismimodown/internal/ratelimit"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ismimodown/internal/sse"
)

// The stream is the reason the dashboard updates without polling, so it has to
// work end to end through the middleware chain — a wrapper that swallows Flush
// would leave every event in the buffer.
func TestEventsStreamsAPublishedCycle(t *testing.T) {
	db := openTestDB(t)
	broker := sse.New()
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db), Broker: broker,
		Models: []string{"mimo-v2.5"},
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
	for broker.Count() == 0 {
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
	broker := sse.New()
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
	for i := 0; i < sse.MaxSubscribers; i++ {
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
	broker := sse.New()
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
	l := ratelimit.New(0.00001, 1)
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
// non-zero exit.
func TestEventsReturnsWhenShutdownIsSignalled(t *testing.T) {
	db := openTestDB(t)
	broker := sse.New()
	shutdown := make(chan struct{})
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db), Broker: broker, Shutdown: shutdown,
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	// Wait until the handler is actually subscribed and streaming.
	deadline := time.Now().Add(3 * time.Second)
	for broker.Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed")
		}
		time.Sleep(2 * time.Millisecond)
	}

	close(shutdown)

	// The body must reach EOF quickly, without the client cancelling.
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
}

// With no shutdown channel wired, the handler must still work — a nil channel
// blocks forever in a select, which is the correct behaviour here.
func TestEventsWorksWithoutAShutdownChannel(t *testing.T) {
	db := openTestDB(t)
	broker := sse.New()
	h := NewServer(Deps{DB: db, Samples: samples.New(db), Broker: broker})

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// The per-client stream cap is only a bound if the handler actually tells the
// broker WHO is asking. Keyed on the same ClientIP the request limiter uses, so
// the two agree on identity behind Traefik.
func TestEventsSubscribesWithTheClientIP(t *testing.T) {
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
