package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trick77/mimostats/internal/ratelimit"
	"github.com/trick77/mimostats/internal/samples"
	"github.com/trick77/mimostats/internal/sse"
)

// The stream is the reason the dashboard updates without polling, so it has to
// work end to end through the middleware chain — a wrapper that swallows Flush
// would leave every event in the buffer.
func TestEventsStreamsAPublishedCycle(t *testing.T) {
	db := openTestDB(t)
	broker := sse.New()
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db), Broker: broker,
		Origin: "rbx", Models: []string{"mimo-v2.5"},
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
	for i := 0; i < sse.MaxSubscribers; i++ {
		_, cancel, ok := broker.Subscribe()
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
