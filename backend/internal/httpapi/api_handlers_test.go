package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trick77/mimostats/internal/probe"
	"github.com/trick77/mimostats/internal/ratelimit"
	"github.com/trick77/mimostats/internal/samples"
)

var testNow = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func newAPIServer(t *testing.T) (http.Handler, *samples.Store) {
	t.Helper()
	db := openTestDB(t)
	store := samples.New(db)
	h := NewServer(Deps{
		Version: "test", DB: db, Samples: store,
		Models:         []string{"mimo-v2.5", "mimo-v2.5-pro"},
		BaseURL:        "https://token-plan-sgp.example/v1",
		RefSGPHost:     "sgp1.example.com",
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
				ModelID: "mimo-v2.5", Probe: probe.ProbeInfer,
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

	rec := get(t, h, "/api/summary?window=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var sum samples.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode: %v", err)
	}
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

	rec := get(t, h, "/api/summary?window=24h")
	var sum samples.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode: %v", err)
	}
	m := sum.Models[0]
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

// Unknown parameters are rejected rather than defaulted: silently charting a
// different range than the caller asked for is worse than an error.
func TestUnknownParametersAreRejected(t *testing.T) {
	h, _ := newAPIServer(t)

	cases := []struct{ name, path string }{
		{"window", "/api/summary?window=6mo"},
		// URL-encoded, because the point is what the HANDLER does with a hostile
		// value, not what net/http does with a malformed request line.
		{"window injection", "/api/summary?window=%27%20OR%201%3D1--"},
		{"metric injection", "/api/series?metric=ttft_ms%3B%20DROP%20TABLE%20cycles--"},
		{"probe", "/api/summary?probe=narrow"},
		{"metric", "/api/series?metric=error_detail"},
		{"model", "/api/samples?model=gpt-4"},
		{"limit", "/api/samples?limit=abc"},
		{"negative limit", "/api/samples?limit=-5"},
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

	rec := get(t, h, "/api/series?metric=ttft&window=24h&probe=infer")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Window  string                     `json:"window"`
		BucketS int                        `json:"bucket_s"`
		Metric  string                     `json:"metric"`
		Probe   string                     `json:"probe"`
		Models  map[string][]samples.Point `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
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
	if out.Probe != "infer" {
		t.Errorf("probe = %q; it must be echoed so a chart cannot mix the two", out.Probe)
	}
}

// The network is not a model. It gets its own series shape so it can never be
// drawn in a model's colour or averaged into one.
func TestNetworkSeriesIsSeparateFromModels(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 25, 900)

	rec := get(t, h, "/api/series?metric=network&window=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Metric  string                     `json:"metric"`
		Targets map[string][]samples.Point `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, target := range []string{probe.TargetMimoSGP, probe.TargetRefSGP} {
		if _, ok := out.Targets[target]; !ok {
			t.Errorf("network series is missing %s", target)
		}
	}
}

// THE assertion: no public endpoint may ever emit error_detail. A provider
// error body can echo request fragments, including credentials.
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
			ModelID: "mimo-v2.5", Probe: probe.ProbeInfer, TotalMs: 500,
			OK: false, ErrorClass: probe.ErrClassHTTP, ErrorDetail: secret,
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// dataPaths carry measurements. /api/methodology is listed separately below
	// because it legitimately NAMES the field in order to document that the
	// field is withheld — the disclosure is the point, and forbidding the word
	// there would push the project toward hiding the caveat rather than
	// publishing it.
	dataPaths := []string{
		"/api/models",
		"/api/summary?window=24h",
		"/api/summary?window=48h&probe=wide",
		"/api/series?metric=ttft&window=24h",
		"/api/series?metric=network&window=24h",
		"/api/samples?model=mimo-v2.5&limit=100",
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

	// The value must never appear anywhere, methodology included.
	t.Run("/api/methodology", func(t *testing.T) {
		rec := get(t, h, "/api/methodology")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, secret) {
			t.Errorf("methodology leaked an error_detail VALUE: %s", body)
		}
		if !strings.Contains(body, "never served publicly") {
			t.Error("methodology must disclose that provider error bodies are withheld")
		}
	})
}

const testUserAgent = "someagent/9.9.9 probe-fixture/1.0"

// The request shape is operator-only. Anything that lets the endpoint recognise
// this probe by its payload lets it serve the probe differently, and every
// figure on the site would then measure the special case instead of production.
//
// Two things in particular: the client string the probe presents, and the id of
// the rotating question. Both are recorded and both stay unserved.
func TestRequestShapeIsNotServed(t *testing.T) {
	h, store := newAPIServer(t)

	if _, err := store.Save(context.Background(), samples.Cycle{
		StartedAt: testNow.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true, ConnectMs: 170},
			{Target: probe.TargetRefSGP, OK: true, ConnectMs: 265},
		},
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeInfer, TTFTMs: 900,
			OK: true, QuestionID: "capital-france",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, p := range []string{
		"/api/models",
		"/api/methodology",
		"/api/summary?window=24h",
		"/api/samples?model=mimo-v2.5&limit=100",
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
			ModelID: "mimo-v2.5", Probe: probe.ProbeInfer, TotalMs: 240000,
			OK: false, ErrorClass: probe.ErrClassTimeout, ErrorDetail: "internal detail",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rec := get(t, h, "/api/samples?model=mimo-v2.5")
	if !strings.Contains(rec.Body.String(), probe.ErrClassTimeout) {
		t.Errorf("error_class must be served: %s", rec.Body.String())
	}
}

// The methodology endpoint is the reason the site can be trusted; the caveats
// that make the numbers honest must be present, not quietly dropped.
func TestMethodologyPublishesTheUnflatteringParts(t *testing.T) {
	h, _ := newAPIServer(t)

	rec := get(t, h, "/api/methodology")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()

	for _, must := range []string{
		"server-side time",     // never "model time"
		"no European GPUs",     // why the residual is not model time
		"client_identity",      // that the probe does not present as neutral
		"reasoning_tokens",     // the check that keeps thinking out of the timings
		"Not the same carrier", // the SGP reference limitation
		"insufficient_data",    // percentile suppression
		"never served publicly",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("methodology must disclose %q; it is missing from: %s", must, body)
		}
	}
	// The residual must never be called model time.
	if strings.Contains(strings.ToLower(body), "model time") &&
		!strings.Contains(body, "would be a claim the measurement cannot support") {
		t.Error("methodology uses 'model time' without the disclaimer")
	}
}

// The scope line names the models, and BACKEND_MODELS can change them. Spelling
// them out as a literal makes the one endpoint the site's credibility rests on
// state what the code once intended to probe rather than what it probes.
func TestMethodologyScopeNamesTheConfiguredModels(t *testing.T) {
	db := openTestDB(t)
	h := NewServer(Deps{
		DB: db, Samples: samples.New(db),
		Models: []string{"mimo-v9-tiny", "mimo-v9-huge"},
		Now:    func() time.Time { return testNow },
	})

	body := get(t, h, "/api/methodology").Body.String()
	for _, id := range []string{"mimo-v9-tiny", "mimo-v9-huge"} {
		if !strings.Contains(body, id) {
			t.Errorf("scope omits the configured model %q: %s", id, body)
		}
	}
	if strings.Contains(body, "mimo-v2.5") {
		t.Errorf("scope names a model that is not configured: %s", body)
	}
}

// Without a rate limit one scraper pins a public, unauthenticated API.
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
		rec := get(t, h, "/api/models")
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

// The cache exists so the database is not the rate limit.
func TestResponsesAreCachedAndInvalidatedOnACycle(t *testing.T) {
	db := openTestDB(t)
	store := samples.New(db)
	srv := NewServer(Deps{
		DB: db, Samples: store,
		Models: []string{"mimo-v2.5"},
		Now:    func() time.Time { return testNow },
	})
	seed(t, store, 25, 900)

	first := get(t, srv, "/api/summary?window=24h").Body.String()

	// New data landing without an invalidation must NOT change the response —
	// that is what proves the cache is live.
	seed(t, store, 5, 5000)
	second := get(t, srv, "/api/summary?window=24h").Body.String()
	if second != first {
		t.Error("response changed without invalidation; the cache is not being used")
	}

	// After a cycle lands, the page must reflect it immediately rather than up
	// to a TTL later — which matters most during an incident.
	srv.OnCycle()
	third := get(t, srv, "/api/summary?window=24h").Body.String()
	if third == first {
		t.Error("OnCycle did not invalidate the cache; the dashboard would lag a live incident")
	}
}

func TestSamplesEndpointClampsLimit(t *testing.T) {
	h, store := newAPIServer(t)
	seed(t, store, 30, 900)

	rec := get(t, h, "/api/samples?model=mimo-v2.5&limit=100000")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Samples []samples.Sample `json:"samples"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Samples) > samples.MaxSampleLimit {
		t.Errorf("returned %d samples, above the clamp", len(out.Samples))
	}
}

// An empty database must serve a usable page, not an error: the site is public
// from minute one and the first samples take five minutes to arrive.
func TestEmptyDatabaseStillServesEveryEndpoint(t *testing.T) {
	h, _ := newAPIServer(t)

	for _, p := range []string{
		"/api/models", "/api/summary?window=24h",
		"/api/series?metric=ttft&window=24h", "/api/series?metric=network&window=24h",
		"/api/samples?model=mimo-v2.5", "/api/methodology",
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
