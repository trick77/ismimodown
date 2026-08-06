package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/config"
	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/ratelimit"
	"github.com/trick77/ismimodown/internal/samples"
)

var testNow = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func newAPIServer(t *testing.T) (http.Handler, *samples.Store) {
	t.Helper()
	db := openTestDB(t)
	store := samples.New(db)
	h := NewServer(Deps{
		Version: "test", DB: db, Samples: store,
		Models:         []string{"mimo-v2.5", "mimo-v2.5-pro"},
		ProbeUserAgent: testUserAgent,
		Now:            func() time.Time { return testNow },
	})
	return h, store
}

func seed(t *testing.T, store *samples.Store, n int, ttft float64) {
	t.Helper()
	for i := 0; i < n; i++ {
		yes := true
		if _, err := store.Save(context.Background(), samples.Cycle{
			StartedAt: testNow.Add(-time.Duration(n-i) * time.Minute),
			Net: []probe.NetResult{
				{Target: probe.TargetMimoSGP, ConnectMs: 170, OK: true},
				{Target: probe.TargetRefSGP, ConnectMs: 265, OK: true},
			},
			Infer: []probe.InferResult{{
				ModelID: "mimo-v2.5", Probe: probe.ProbeShort,
				TTFTMs: ttft, TotalMs: ttft + 800, ITLP50Ms: 24, OutputTPS: 41,
				OK: true, AnswerOK: &yes, QuestionID: "capital-france",
			}},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSummaryServesTheDashboardState(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 25, 900)

	rec := get(t, h, "/api/dashboard?window=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	sum := getDashboard(t, h, "24h").Summary
	if sum.Window != "24h" || sum.Cycles != 25 {
		t.Errorf("window = %q, cycles = %d", sum.Window, sum.Cycles)
	}
	if len(sum.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(sum.Models))
	}
	if !sum.Models[0].TTFT.Sufficient || sum.Models[0].TTFT.P50 == nil {
		t.Error("25 samples should clear the suppression threshold")
	}
	// The network layer must be summarised alongside, or the subtraction the
	// whole site exists for cannot be shown.
	if len(sum.Net) != 2 {
		t.Errorf("net summaries = %d, want 2", len(sum.Net))
	}
	if ct := rec.Header().Get("Cache-Control"); !strings.Contains(ct, "max-age") {
		t.Errorf("Cache-Control = %q, want a max-age", ct)
	}
}

// Below the threshold the API must say "we don't know", not "0 ms".
func TestSummarySuppressesThinPercentiles(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 5, 900)

	rec := get(t, h, "/api/dashboard?window=24h")
	m := getDashboard(t, h, "24h").Summary.Models[0]
	if m.TTFT.Sufficient {
		t.Error("5 samples must not be sufficient")
	}
	if m.TTFT.P50 != nil {
		t.Errorf("p50 = %v; a thin window must serve null, never a number", *m.TTFT.P50)
	}
	// And the JSON must carry an explicit null rather than omitting the field,
	// so the client can tell "suppressed" from "not sent".
	if !strings.Contains(rec.Body.String(), `"p50_ms":null`) {
		t.Errorf("expected an explicit null p50 in the payload: %s", rec.Body.String())
	}
}

// The window is the only caller-controlled input left on the API, and it is
// rejected rather than defaulted: silently charting a different range than the
// caller asked for is worse than an error.
//
// The metric, model, probe and limit cases that used to sit here went with the
// parameters they guarded. Those were never a caller's choice — they were the
// page's, arriving the long way round — and the strongest form of validating
// an input is not accepting it.
func TestUnknownParametersAreRejected(t *testing.T) {
	h, _ := newAPIServer(t)

	cases := []struct{ name, path string }{
		{"window", "/api/dashboard?window=6mo"},
		// URL-encoded, because the point is what the HANDLER does with a hostile
		// value, not what net/http does with a malformed request line.
		{"window injection", "/api/dashboard?window=%27%20OR%201%3D1--"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := get(t, h, tc.path); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for %s", rec.Code, tc.path)
			}
		})
	}
}

func TestSeriesIsBucketedAndPerModel(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 40, 900)

	var out struct {
		Window  string                     `json:"window"`
		BucketS int                        `json:"bucket_s"`
		Metric  string                     `json:"metric"`
		Probe   string                     `json:"probe"`
		Models  map[string][]samples.Point `json:"models"`
	}
	if err := json.Unmarshal(getDashboard(t, h, "24h").Series.TTFT, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The bucket is derived server-side so a caller cannot ask for 100 000
	// points.
	if out.BucketS != 900 {
		t.Errorf("bucket_s = %d, want 900 for a 24h window", out.BucketS)
	}
	if _, ok := out.Models["mimo-v2.5"]; !ok {
		t.Errorf("series is missing mimo-v2.5: %v", out.Models)
	}
	if out.Probe != "short" {
		t.Errorf("probe = %q; it must be echoed so a chart cannot mix the two", out.Probe)
	}
}

// The network is not a model. It gets its own series shape so it can never be
// drawn in a model's colour or averaged into one.
func TestNetworkSeriesIsSeparateFromModels(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 25, 900)

	got := getDashboard(t, h, "24h")
	for _, target := range []string{probe.TargetMimoSGP, probe.TargetRefSGP} {
		if _, ok := got.Series.Network.Targets[target]; !ok {
			t.Errorf("network series is missing %s", target)
		}
	}
	// It sits beside the model series rather than among them: a metric-keyed
	// map could not hold it without making the network a model.
	if len(got.Series.TTFT) == 0 {
		t.Error("the model series went missing alongside it")
	}
}

// THE assertion: no public endpoint may ever emit error_detail. A provider
// error body can echo request fragments, including credentials.
//
// The failures block was the one shape that nearly crossed this line, and it
// does not: it serves the error class and the HTTP status, both of which are
// the daemon's own vocabulary, and leaves the upstream text where it was.
func TestNoPublicEndpointEmitsErrorDetail(t *testing.T) {
	h, store := newAPIServer(t)

	const secret = "SECRET-PROVIDER-BODY-tp-livekey-fragment"
	if _, err := store.Save(context.Background(), samples.Cycle{
		StartedAt: testNow.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout,
				ErrorDetail: secret},
			{Target: probe.TargetRefSGP, OK: true, ConnectMs: 265},
		},
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeShort, TotalMs: 500,
			OK: false, ErrorClass: probe.ErrClassHTTP, ErrorDetail: secret,
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// dataPaths carry measurements, and none of them may name the field at
	// all: a future struct change that starts serializing error_detail fails
	// here rather than on a live deployment.
	// One path now, and it carries every measurement the API serves — which
	// makes this assertion stronger than the seven it replaced, not weaker:
	// there is no longer a route it could forget to name.
	dataPaths := []string{
		"/api/dashboard?window=24h",
		"/api/dashboard?window=48h",
	}
	for _, p := range dataPaths {
		t.Run(p, func(t *testing.T) {
			rec := get(t, h, p)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, secret) {
				t.Errorf("endpoint leaked an error_detail VALUE: %s", body)
			}
			// No data endpoint should carry the field at all, so a future
			// struct change that starts serializing it fails here.
			if strings.Contains(body, "error_detail") {
				t.Errorf("data endpoint exposes the error_detail field: %s", body)
			}
		})
	}
}

const testUserAgent = "someagent/9.9.9 probe-fixture/1.0"

// The request shape is operator-only. Anything that lets the endpoint recognise
// this probe by its payload lets it serve the probe differently, and every
// figure on the site would then measure the special case instead of production.
//
// Two things in particular: the client string the probe presents, and the id of
// the rotating question. Both are recorded and both stay unserved.
//
// The failures block is why the second cycle below is a FAILED run: that block
// is the newest surface to project a row publicly, and a payload it never
// appeared in would pass this for the wrong reason.
func TestRequestShapeIsNotServed(t *testing.T) {
	h, store := newAPIServer(t)

	if _, err := store.Save(context.Background(), samples.Cycle{
		StartedAt: testNow.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true, ConnectMs: 170},
			{Target: probe.TargetRefSGP, OK: true, ConnectMs: 265},
		},
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeShort, TTFTMs: 900,
			OK: true, QuestionID: "capital-france",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A FAILED run too, so the failures block is populated rather than empty —
	// the assertions below used to run against a payload the new block never
	// appeared in, which would have passed for the wrong reason.
	if _, err := store.Save(context.Background(), samples.Cycle{
		StartedAt: testNow.Add(-2 * time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true, ConnectMs: 170},
			{Target: probe.TargetRefSGP, OK: true, ConnectMs: 265},
		},
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeShort,
			OK: false, ErrorClass: probe.ErrClassHTTP, HTTPStatus: 500,
			ErrorDetail: "upstream unavailable", QuestionID: "capital-france",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, p := range []string{
		"/api/dashboard?window=24h",
	} {
		t.Run(p, func(t *testing.T) {
			body := get(t, h, p).Body.String()
			// The configured agent string, and the field that would carry it.
			if strings.Contains(body, testUserAgent) {
				t.Errorf("endpoint served the probe's client string: %s", body)
			}
			if strings.Contains(body, "user_agent") {
				t.Errorf("endpoint exposes a user_agent field: %s", body)
			}
			// The question rotation names what is actually being asked.
			if strings.Contains(body, "question_id") || strings.Contains(body, "capital-france") {
				t.Errorf("endpoint exposes the question rotation: %s", body)
			}
		})
	}
}

// The error CLASS is public — it is the whole failure vocabulary the dashboard
// renders — so suppressing detail must not suppress that too.
func TestErrorClassIsServedEvenThoughDetailIsNot(t *testing.T) {
	h, store := newAPIServer(t)

	if _, err := store.Save(context.Background(), samples.Cycle{
		StartedAt: testNow.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true, ConnectMs: 170},
			{Target: probe.TargetRefSGP, OK: true, ConnectMs: 265},
		},
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeShort, TotalMs: 240000,
			OK: false, ErrorClass: probe.ErrClassTimeout, ErrorDetail: "internal detail",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rec := get(t, h, "/api/dashboard?window=24h")
	if !strings.Contains(rec.Body.String(), probe.ErrClassTimeout) {
		t.Errorf("error_class must be served: %s", rec.Body.String())
	}
}

// Without a rate limit one scraper pins a public, unauthenticated API.
//
// It matters more now than it did across six routes: the one that remains is
// the heaviest thing the box serves. If it ever drifts onto the root mux —
// where /api/events lives, outside the limiter, for reasons that do not apply
// to a query — this is what fails.
func TestRateLimitReturns429PastTheBurst(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db),
		Models:  []string{"mimo-v2.5"},
		Limiter: ratelimit.New(0.0001, 3), // 3 burst, effectively no refill
		Now:     func() time.Time { return testNow },
	})

	var got429 bool
	for i := 0; i < 6; i++ {
		rec := get(t, h, "/api/dashboard?window=24h")
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Error("a 429 must tell the caller when to retry")
			}
			break
		}
	}
	if !got429 {
		t.Error("no request was rate limited; a scraper could pin the server")
	}
}

// The container healthcheck must never be throttled, or a burst of API traffic
// would make the orchestrator restart a perfectly healthy process.
func TestHealthzIsNotRateLimited(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db),
		Models:  []string{"mimo-v2.5"},
		Limiter: ratelimit.New(0.0001, 1),
		Now:     func() time.Time { return testNow },
	})

	for i := 0; i < 20; i++ {
		if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK {
			t.Fatalf("healthz returned %d on request %d; it must never be throttled", rec.Code, i)
		}
	}
}

// An empty database must serve a usable page, not an error: the site is public
// from minute one and the first samples take five minutes to arrive.
//
// Every window, because the window is what a reader can still change.
func TestEmptyDatabaseStillServesEveryEndpoint(t *testing.T) {
	h, _ := newAPIServer(t)

	for _, p := range []string{
		"/api/dashboard?window=24h", "/api/dashboard?window=48h",
		"/api/dashboard?window=7d", "/api/dashboard?window=30d",
		"/api/dashboard?window=3mo",
	} {
		t.Run(p, func(t *testing.T) {
			rec := get(t, h, p)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d on an empty database: %s", rec.Code, rec.Body.String())
			}
			if !json.Valid(rec.Body.Bytes()) {
				t.Errorf("invalid JSON: %s", rec.Body.String())
			}
		})
	}
}

// The pulse rows stay the narrow projection even now that they ride inside a
// bigger payload — more so, since a day of full measurements per model would
// now be carried by the response every reader gets.
func TestPulseEndpointServesTheNarrowShape(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 30, 900)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(get(t, h, "/api/dashboard?window=24h").Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var groups []struct {
		ModelID string           `json:"model_id"`
		Cycles  []map[string]any `json:"cycles"`
	}
	if err := json.Unmarshal(raw["pulse"], &groups); err != nil {
		t.Fatalf("decode pulse: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("no pulse groups")
	}
	out := groups[0]
	if out.ModelID != "mimo-v2.5" {
		t.Errorf("model_id = %q", out.ModelID)
	}
	if len(out.Cycles) != 30 {
		t.Fatalf("returned %d cycles, want 30", len(out.Cycles))
	}
	// The point of the endpoint. Serving a Sample here would hand a day of full
	// measurements to something that draws a bar.
	for _, banned := range []string{"total_ms", "itl_p50_ms", "output_tps", "model_id", "probe"} {
		if _, ok := out.Cycles[0][banned]; ok {
			t.Errorf("cycle carries %q, which the strip never draws", banned)
		}
	}
	for _, needed := range []string{"at", "ttft_ms", "ok"} {
		if _, ok := out.Cycles[0][needed]; !ok {
			t.Errorf("cycle is missing %q", needed)
		}
	}
}

// The row limits are no longer caller-controlled — they are constants in
// dashboard_handler.go — so the clamps that guarded them, and the shared
// validator that rejected a bad model or a negative limit, went with the
// parameters. What they protected is now unreachable rather than defended:
// samples.MaxSampleLimit still clamps inside the store, and the only value
// that reaches it is a literal.

// The recent block rides inside a window-keyed response, so the one thing that
// must never happen is someone "tidying" it by adding the window's `since`
// clause to its query. This is the test that would fail the day they did: the
// banner answers "how is it right now", and the answer cannot depend on which
// chart range the reader happens to be looking at.
func TestRecentBlockIsIdenticalOnEveryWindow(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 30, 900)

	recentFor := func(window string) string {
		rec := get(t, h, "/api/dashboard?window="+window)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Summary struct {
				Recent json.RawMessage `json:"recent"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := body.Summary
		if len(out.Recent) == 0 || string(out.Recent) == "null" {
			t.Fatalf("window %s served no recent block", window)
		}
		return string(out.Recent)
	}

	day, quarter := recentFor("24h"), recentFor("3mo")
	if day != quarter {
		t.Errorf("recent block differs by window:\n 24h: %s\n 3mo: %s", day, quarter)
	}
}

// The block is what the verdict is built from, so it has to carry the fault and
// the per-model outcome — the two things a "right now" reading needs and the
// window's fault COUNTS cannot provide.
func TestSummaryRecentBlockCarriesFaultAndRuns(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 3, 900)

	out := getDashboard(t, h, "24h").Summary
	if len(out.Recent) != 3 {
		t.Fatalf("recent = %d, want 3", len(out.Recent))
	}
	if out.Recent[0].Fault != probe.FaultOK {
		t.Errorf("fault = %q, want %q", out.Recent[0].Fault, probe.FaultOK)
	}
	if run, ok := out.Recent[0].Models["mimo-v2.5"]; !ok || !run.OK {
		t.Errorf("models = %v, want a successful mimo-v2.5 run", out.Recent[0].Models)
	}
}

// seedCost writes runs that carry usage. The shared seed() helper leaves the
// token columns at zero, which is a legitimate reading and a useless fixture for
// a bill.
func seedCost(t *testing.T, store *samples.Store, n int) {
	t.Helper()
	yes := true
	for i := 0; i < n; i++ {
		if _, err := store.Save(context.Background(), samples.Cycle{
			StartedAt: testNow.Add(-time.Duration(n-i) * time.Minute),
			Net: []probe.NetResult{
				{Target: probe.TargetMimoSGP, ConnectMs: 170, OK: true},
				{Target: probe.TargetRefSGP, ConnectMs: 265, OK: true},
			},
			Infer: []probe.InferResult{{
				ModelID: "mimo-v2.5", Probe: probe.ProbeShort,
				TTFTMs: 900, TotalMs: 1700, ITLP50Ms: 24, OutputTPS: 41,
				Usage: probe.TokenUsage{PromptTokens: 1000, CompletionTokens: 200},
				OK:    true, AnswerOK: &yes, QuestionID: "capital-france",
			}},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

// costOf pulls the cost panel out of the page it now rides in.
func costOf(t *testing.T, h http.Handler, window string) samples.CostBreakdown {
	t.Helper()
	rec := get(t, h, "/api/dashboard?window="+window)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Cost samples.CostBreakdown `json:"cost"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Cost
}

func TestCostEndpointServesTheWholePanel(t *testing.T) {
	db := openTestDB(t)
	store := samples.New(db)
	h := NewServer(Deps{
		Version: "test", DB: db, Samples: store,
		Models: []string{"mimo-v2.5"},
		Prices: map[string]config.ModelPrice{"mimo-v2.5": {In: 1, Out: 10}},
		Now:    func() time.Time { return testNow },
	})
	seedCost(t, store, 3)

	got := costOf(t, h, "24h")
	if got.Window != "24h" {
		t.Errorf("window = %q", got.Window)
	}
	if got.Total.USD == nil || *got.Total.USD <= 0 {
		t.Errorf("total = %+v, want a positive figure", got.Total)
	}
	// Figures and series in one response, so the number above the chart and the
	// line in it cannot describe different instants.
	if len(got.Series) == 0 || len(got.Phases) == 0 {
		t.Errorf("series = %d points, phases = %d", len(got.Series), len(got.Phases))
	}
	// The price table is published so a total can be checked rather than
	// trusted.
	if got.Prices["mimo-v2.5"].Out != 10 {
		t.Errorf("prices = %+v, want the table that produced the figures", got.Prices)
	}
}

func TestCostEndpointRejectsAnUnknownWindow(t *testing.T) {
	h, _ := newAPIServer(t)

	rec := get(t, h, "/api/dashboard?window=6mo")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
