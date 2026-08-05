package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/ratelimit"
	"github.com/trick77/ismimodown/internal/samples"
)

// dashboardBody is what the page reads. Only the fields a test asserts on are
// typed; the rest stay raw so a shape change elsewhere does not fail here for
// the wrong reason.
type dashboardBody struct {
	Window   string          `json:"window"`
	Summary  samples.Summary `json:"summary"`
	Now      samples.Summary `json:"now"`
	Baseline samples.Summary `json:"baseline"`
	Series   struct {
		TTFT     json.RawMessage `json:"ttft"`
		TTFTWide json.RawMessage `json:"ttft_wide"`
		TPS      json.RawMessage `json:"tps"`
		Total    json.RawMessage `json:"total"`
		Network  struct {
			Targets map[string]json.RawMessage `json:"targets"`
		} `json:"network"`
	} `json:"series"`
	Cost struct {
		Window string `json:"window"`
	} `json:"cost"`
	Pulse []struct {
		ModelID string          `json:"model_id"`
		Probe   string          `json:"probe"`
		Cycles  []samples.Pulse `json:"cycles"`
	} `json:"pulse"`
	Samples []struct {
		ModelID string           `json:"model_id"`
		Probe   string           `json:"probe"`
		Samples []samples.Sample `json:"samples"`
	} `json:"samples"`
}

func getDashboard(t *testing.T, h http.Handler, window string) dashboardBody {
	t.Helper()
	rec := get(t, h, "/api/dashboard?window="+window)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out dashboardBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// One request has to carry the whole page, or the page goes back to asking
// fifteen times and spending the caller's limiter budget to draw itself once.
func TestDashboardServesOneLoad(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 25, 900)

	rec := get(t, h, "/api/dashboard?window=48h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got dashboardBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The selected window, and the two the verdict compares it against. All
	// three, or the banner has nothing to say "slower than usual" against.
	if got.Window != "48h" || got.Summary.Window != "48h" {
		t.Errorf("window = %q, summary window = %q, want 48h", got.Window, got.Summary.Window)
	}
	if got.Now.Window != "24h" {
		t.Errorf("now window = %q, want 24h", got.Now.Window)
	}
	if got.Baseline.Window != "7d" {
		t.Errorf("baseline window = %q, want 7d", got.Baseline.Window)
	}
	if got.Cost.Window != "48h" {
		t.Errorf("cost window = %q, want 48h", got.Cost.Window)
	}

	// Five lines: four model metrics and the network underneath them.
	for name, raw := range map[string]json.RawMessage{
		"ttft": got.Series.TTFT, "ttft_wide": got.Series.TTFTWide,
		"tps": got.Series.TPS, "total": got.Series.Total,
	} {
		if len(raw) == 0 {
			t.Errorf("series.%s is absent; a panel would render empty", name)
		}
	}
	// Both reference hosts, or the subtraction the whole site exists for
	// cannot be drawn.
	if len(got.Series.Network.Targets) != 2 {
		t.Errorf("network targets = %d, want 2", len(got.Series.Network.Targets))
	}

	if len(got.Pulse) != 2 {
		t.Errorf("pulse groups = %d, want one per model", len(got.Pulse))
	}
	if ct := rec.Header().Get("Cache-Control"); !strings.Contains(ct, "max-age") {
		t.Errorf("Cache-Control = %q, want a max-age", ct)
	}
}

// The raw table calls itself the unaggregated record. It showed a quarter of
// one for a while: every request named the first model, and the wide runs were
// never asked for at all. Both omissions were per-caller mistakes; composing
// server-side is what stops them being possible to make again.
//
// Order is part of the contract. The client concatenates these groups and
// sorts on the instant, so a group in the wrong position relabels rows.
func TestDashboardCoversEveryModelAndProbe(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 25, 900)

	got := getDashboard(t, h, "24h")

	var pairs []string
	for _, g := range got.Samples {
		pairs = append(pairs, g.ModelID+"/"+g.Probe)
	}
	want := []string{
		"mimo-v2.5/" + probe.ProbeShort,
		"mimo-v2.5/" + probe.ProbeWide,
		"mimo-v2.5-pro/" + probe.ProbeShort,
		"mimo-v2.5-pro/" + probe.ProbeWide,
	}
	if len(pairs) != len(want) {
		t.Fatalf("samples groups = %v, want %v", pairs, want)
	}
	for i, w := range want {
		if pairs[i] != w {
			t.Errorf("samples group %d = %q, want %q", i, pairs[i], w)
		}
	}

	// The pulse strip draws the short probe for every model — it is a
	// per-cycle availability strip, and wide does not run every cycle.
	var pulsed []string
	for _, g := range got.Pulse {
		if g.Probe != probe.ProbeShort {
			t.Errorf("pulse group %q carries probe %q, want short", g.ModelID, g.Probe)
		}
		pulsed = append(pulsed, g.ModelID)
	}
	if len(pulsed) != 2 || pulsed[0] != "mimo-v2.5" || pulsed[1] != "mimo-v2.5-pro" {
		t.Errorf("pulse models = %v, want both in order", pulsed)
	}
}

// Three summaries are what the page shows; two is what it costs when the
// reader has selected one of the fixed comparison windows. Asking for the same
// window twice would be a query the database answers twice for one render.
func TestDashboardDeduplicatesTheFixedSummaryWindows(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 25, 900)

	// Selecting "now" makes the selected summary and the now summary the same
	// question.
	same := getDashboard(t, h, "24h")
	if same.Summary.Window != "24h" || same.Now.Window != "24h" {
		t.Errorf("selected = %q, now = %q, want both 24h", same.Summary.Window, same.Now.Window)
	}
	if same.Baseline.Window != "7d" {
		t.Errorf("baseline = %q, want 7d; it must not collapse with the others",
			same.Baseline.Window)
	}

	// Selecting anything else means three distinct windows.
	distinct := getDashboard(t, h, "30d")
	if distinct.Summary.Window != "30d" ||
		distinct.Now.Window != "24h" || distinct.Baseline.Window != "7d" {
		t.Errorf("windows = %q/%q/%q, want 30d/24h/7d",
			distinct.Summary.Window, distinct.Now.Window, distinct.Baseline.Window)
	}
}

// A client that maps over these throws on null. A fresh or swept database is
// exactly when someone is watching the page, so it must render empty rather
// than break.
func TestDashboardEmptyCollectionsAreNeverNull(t *testing.T) {
	h, _ := newAPIServer(t)

	rec := get(t, h, "/api/dashboard?window=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "null,") && strings.Contains(body, `"cycles":null`) {
		t.Errorf("a collection marshalled as null: %s", body)
	}
	for _, want := range []string{`"cycles":[]`, `"samples":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in an empty payload: %s", want, body)
		}
	}

	got := getDashboard(t, h, "24h")
	// The groups exist even with nothing in them: the page draws a row per
	// model whether or not that model has answered yet.
	if len(got.Pulse) != 2 || len(got.Samples) != 4 {
		t.Errorf("pulse = %d, samples = %d on an empty database; want 2 and 4",
			len(got.Pulse), len(got.Samples))
	}
}

// The cache exists so the database is not the rate limit. One entry per
// window, and a cycle drops all of them.
func TestDashboardIsCachedPerWindow(t *testing.T) {
	db := openTestDB(t)
	store := samples.New(db)
	srv := NewServer(Deps{
		DB: db, Samples: store,
		Models: []string{"mimo-v2.5"},
		Now:    func() time.Time { return testNow },
	})
	seed(t, store, 25, 900)

	first := get(t, srv, "/api/dashboard?window=24h").Body.String()

	// New data landing without an invalidation must NOT change the response —
	// that is what proves the cache is live.
	seed(t, store, 5, 5000)
	if second := get(t, srv, "/api/dashboard?window=24h").Body.String(); second != first {
		t.Error("response changed without invalidation; the cache is not being used")
	}

	// A different window is a different entry, not the same one re-served.
	if other := get(t, srv, "/api/dashboard?window=7d").Body.String(); other == first {
		t.Error("two windows served the same body; the key is not carrying the window")
	}

	// After a cycle lands the page must reflect it immediately rather than up
	// to a TTL later — which matters most during an incident.
	srv.OnCycle()
	if third := get(t, srv, "/api/dashboard?window=24h").Body.String(); third == first {
		t.Error("OnCycle did not invalidate; the dashboard would lag a live incident")
	}
}

// One load must fit inside the burst with room to spare, or the fix did not
// land: fifteen requests against a bucket of twenty is what made two clicks in
// a row answer "rate limited".
//
// Deliberately generous — this asserts the shape of the cost, not a number. It
// fails if a load ever goes back to fanning out per model.
func TestOneLoadIsOneToken(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db),
		Models:  []string{"mimo-v2.5", "mimo-v2.5-pro"},
		Limiter: ratelimit.New(0.0001, 5), // five tokens, effectively no refill
		Now:     func() time.Time { return testNow },
	})

	// Five loads of the page, on a bucket that refills at nothing.
	for i := 0; i < 5; i++ {
		if rec := get(t, h, "/api/dashboard?window=24h"); rec.Code != http.StatusOK {
			t.Fatalf("load %d returned %d; a page load must cost one token", i, rec.Code)
		}
	}
}
