package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/ban"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ship/kit/ratelimit"
	"github.com/trick77/toolkit/analytics"
)

// feedRecorder is a Feed that keeps what it is handed.
type feedRecorder struct {
	mu  sync.Mutex
	evs []analytics.Event
}

func (f *feedRecorder) Enqueue(ev analytics.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evs = append(f.evs, ev)
}

func (f *feedRecorder) events() []analytics.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]analytics.Event(nil), f.evs...)
}

// The feed sees what the gates refuse and the route the inner mux matched; a
// static file, a prefix or a refusal before the mux carries none.
func TestFeedSeesRoutesAndRefusals(t *testing.T) {
	db := openTestDB(t)
	feed := &feedRecorder{}
	h := NewServer(Deps{
		DB:      db,
		Samples: samples.New(db),
		Models:  []string{"mimo-v2.6-flash"},
		Limiter: ratelimit.New(ratelimit.Options{Rate: 0.0001, Burst: 1}),
		Ban:     ban.New(48*time.Hour, 100),
		Static: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("shell"))
		}),
		Feed: feed,
	})

	getFrom(t, h, "/api/dashboard", "9.9.9.9")
	getFrom(t, h, "/api/dashboard", "9.9.9.9") // over the limit
	getFrom(t, h, "/", "9.9.9.9")
	getFrom(t, h, "/wp-admin/install.php", "9.9.9.9")

	want := []struct {
		path, route string
		status      int
		bytes       int64 // -1: whatever the body was
	}{
		{"/api/dashboard", "GET /api/dashboard", http.StatusOK, -1},
		{"/api/dashboard", "", http.StatusTooManyRequests, -1},
		{"/", "", http.StatusOK, 5},
		{"/wp-admin/install.php", "", http.StatusForbidden, 0},
	}
	got := feed.events()
	if len(got) != len(want) {
		t.Fatalf("fed %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Path != w.path || g.Route != w.route || g.Status != w.status || (w.bytes >= 0 && g.Bytes != w.bytes) {
			t.Errorf("event %d = %s route %q status %d bytes %d, want %+v", i, g.Path, g.Route, g.Status, g.Bytes, w)
		}
	}
}

// The container's healthcheck is not traffic; a caller Health refused is.
func TestFeedLeavesTheProbesOut(t *testing.T) {
	feed := &feedRecorder{}
	h := NewServer(Deps{DB: openTestDB(t), Feed: feed})
	h.ServeHTTP(httptest.NewRecorder(), localRequest(http.MethodGet, "/readyz"))
	if got := feed.events(); len(got) != 0 {
		t.Errorf("fed the healthcheck: %+v", got)
	}
	getFrom(t, h, "/readyz", "9.9.9.9")
	if got := feed.events(); len(got) != 1 || got[0].Status != http.StatusNotFound {
		t.Errorf("fed %+v, want the outside caller's 404", got)
	}
}

// A panicking handler is fed as the 500 Recover writes; one that had begun its
// answer keeps its 200, and is fed as failed all the same.
func TestFeedSeesPanics(t *testing.T) {
	for begun, want := range map[bool]int{false: http.StatusInternalServerError, true: http.StatusOK} {
		feed := &feedRecorder{}
		h := NewServer(Deps{
			DB: openTestDB(t),
			Static: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if begun {
					_, _ = w.Write([]byte("half"))
				}
				panic("boom")
			}),
			Feed: feed,
		})
		func() {
			defer func() { _ = recover() }() // Recover aborts a begun answer
			getFrom(t, h, "/", "9.9.9.9")
		}()
		if got := feed.events(); len(got) != 1 || got[0].Status != want || got[0].Err != "panic" {
			t.Errorf("begun %v: fed %+v, want one %d with err panic", begun, got, want)
		}
	}
}

// The feed gets the real address, not clientKey's /64, which the collector
// would drop; a malformed forwarded entry falls back to the peer.
func TestFeedGetsTheRealAddress(t *testing.T) {
	for _, tc := range []struct{ forwarded, peer, want string }{
		{"203.0.113.9, 2001:db8::1", "10.0.0.2:443", "2001:db8::1"},
		{"garbage", "[2001:db8::2]:443", "2001:db8::2"},
		{"", "198.51.100.7:443", "198.51.100.7"},
	} {
		feed := &feedRecorder{}
		h := NewServer(Deps{DB: openTestDB(t), Feed: feed})
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = tc.peer
		if tc.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got := feed.events(); len(got) != 1 || got[0].IP != tc.want {
			t.Errorf("X-Forwarded-For %q from %s: fed %+v, want ip %s", tc.forwarded, tc.peer, got, tc.want)
		}
	}
}

// The event stream still flushes through the feed's writer.
func TestFeedKeepsTheStreamFlushing(t *testing.T) {
	var err error
	h := NewServer(Deps{
		DB: openTestDB(t),
		Static: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("data: x\n\n"))
			err = http.NewResponseController(w).Flush()
		}),
		Feed: &feedRecorder{},
	})
	rec := getFrom(t, h, "/", "9.9.9.9")
	if err != nil || !rec.Flushed {
		t.Errorf("flush = %v, flushed %v", err, rec.Flushed)
	}
}
