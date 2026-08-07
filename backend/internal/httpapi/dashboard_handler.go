package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/samples"
)

// The composition the SPA renders, in one response.
//
// It exists because one page load was fifteen requests. The page needed three
// summaries, five series, the cost breakdown, a pulse per model and the raw
// rows for every model-and-probe pair, and it asked for each of them
// separately — fifteen round trips and fifteen per-IP limiter tokens for one
// render, against a burst of twenty. Clicking two range pills in a row spent
// the bucket and the dashboard answered itself with "rate limited".
//
// Worse than the count: the second wave could not start until the first had
// answered, because the models to fan out over came from the summary. The
// server has known that list since it booted. Composing here removes the wave
// as well as the requests, and keeps one load at one request no matter how
// many models are configured — the old shape grew with the fleet.
//
// The API serves the page. A page that has to make fifteen calls to draw
// itself is one the API declined to serve.
const (
	// The two comparison windows the verdict is built from, alongside whichever
	// window the reader selected. Fixed here rather than accepted as
	// parameters: they are the PAGE's question — "how does now compare to
	// normal" — not a caller's choice.
	dashboardNowWindow      = "24h"
	dashboardBaselineWindow = "7d"

	// A day of cycles at the five-minute cadence, which is what the pulse strip
	// draws.
	dashboardPulseLimit = 288

	// Rows per model AND probe — the supply, not the cap. SamplesTable slices
	// to its own ROWS; asking for fewer than that here would quietly shorten
	// how far back the table reaches without saving a request.
	dashboardSampleLimit = 20

	// The errors block: how many, and how far back.
	//
	// Ten, and it was five while the block held one kind of row. The query now
	// carries both ways a run goes wrong — the calls that failed AND the calls
	// that came back graded wrong — and those two compete for the same rows.
	// Wrong answers are the noisier of the pair by this page's own reckoning
	// (verdict.ts sets DEGRADED_WRONG_RECENT at 3 against ELEVATED_RECENT at
	// 1), so at five a cluster of them inside one hour would evict last
	// evening's http_error and the card would quietly stop showing the thing it
	// was built to show. Ten is still a summary and not a log — the raw table
	// below carries the full record, and the availability strip carries the
	// counting — and it is still well under samples.MaxFailureLimit.
	//
	// A day, fixed, whatever window the reader selected. Same independence
	// RecentCycles has and for the same reason: the last few bad runs are a
	// fact about the endpoint, not about the chart selector, and tying them to
	// the pills would make a 30d selection quote a fortnight-old error as the
	// current one. A handler test asserts the block is identical on 24h and
	// 3mo.
	dashboardFailureLimit  = 10
	dashboardFailureWindow = 24 * time.Hour
)

// handleDashboard serves the whole page for one window.
//
// Only ?window= is caller-controlled, and it is validated against the same
// allow-list every other window is. The probe kinds, the two comparison
// windows and the two row limits are deliberately NOT parameters: they are
// what the page asks, and a knob for each would mint cache entries per
// combination while meaning nothing — the payload needs both probe kinds and
// all three windows regardless of what any caller would prefer.
func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	window, ok := s.window(w, r)
	if !ok {
		return
	}

	// Keyed on the window alone, so the cache holds one entry per window in the
	// allow-list — five at most, none of them caller-shaped. A hit costs the
	// same map lookup a single granular response used to.
	s.writeJSON(w, r, "dashboard|"+window.Key, func() (any, error) {
		return s.buildDashboard(r.Context(), window, s.now())
	})
}

type dashboardPayload struct {
	Window      string                `json:"window"`
	GeneratedAt time.Time             `json:"generated_at"`
	Summary     samples.Summary       `json:"summary"`
	Now         samples.Summary       `json:"now"`
	Baseline    samples.Summary       `json:"baseline"`
	Series      dashboardSeries       `json:"series"`
	Cost        samples.CostBreakdown `json:"cost"`
	Pulse       []any                 `json:"pulse"`
	Samples     []any                 `json:"samples"`
	// The last few failed inference calls, newest first, over a fixed day — see
	// dashboardFailureWindow for why it ignores the selected window. Class and
	// status only: samples.Failure explains why the upstream text stays behind.
	Failures []samples.Failure `json:"failures"`
}

// dashboardSeries names the five lines the page draws.
//
// Named fields rather than a metric-keyed map because two of them are the same
// metric — ttft on the short probe and on the wide one — and the gap between
// those two IS the prefill signal. A map keyed by metric could not hold both.
type dashboardSeries struct {
	TTFT     any `json:"ttft"`
	TTFTWide any `json:"ttft_wide"`
	TPS      any `json:"tps"`
	Total    any `json:"total"`
	Network  any `json:"network"`
}

// buildDashboard runs every query one page load needs.
//
// Sequential on purpose. It runs at most once per window per cache TTL, and
// once more when a cycle invalidates, so there is nothing here worth a
// dependency on an errgroup and a fan of concurrent SQLite connections. Being
// sequential also buys the two properties the payload needs: every part shares
// ONE now, so no two figures in the response describe different instants, and
// the first error abandons the remaining queries instead of running them to
// throw the results away. The context still cancels the whole thing when the
// reader closes the tab.
//
// All-or-nothing on error, for the same reason the cost breakdown is one
// endpoint: a half-built page would be pinned in the cache for the TTL, and
// the client has no partial-render path. It keeps its last good state and
// shows the banner over it.
func (s *server) buildDashboard(ctx context.Context, window samples.Window, now time.Time) (any, error) {
	out := dashboardPayload{
		Window:      window.Key,
		GeneratedAt: now,
		// Allocated, not declared: an empty model list must still marshal as []
		// rather than null, or a client mapping over it throws.
		Pulse:   make([]any, 0, len(s.deps.Models)),
		Samples: make([]any, 0, 2*len(s.deps.Models)),
	}

	// The selected window first, then the two fixed ones — skipping any that
	// the reader has already selected. Three summaries are what the page shows;
	// two is what it costs when "now" or "normal" is also the selection.
	wanted := []string{window.Key}
	for _, key := range []string{dashboardNowWindow, dashboardBaselineWindow} {
		if !slices.Contains(wanted, key) {
			wanted = append(wanted, key)
		}
	}
	byWindow := map[string]samples.Summary{}
	for _, key := range wanted {
		// Allow-listed constants, so the lookup cannot fail today; the selected
		// one was validated by the caller. Loud rather than skipped anyway: a
		// miss would leave `now` or `baseline` a zero-value Summary served with
		// a 200, and the verdict banner would compare against a summary with no
		// models in it — permanently wrong with nothing anywhere saying so.
		// Renaming a key in samples.Windows is what would do it.
		win, ok := samples.LookupWindow(key)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errUnknownWindow, key)
		}
		sum, err := s.deps.Samples.Summarize(ctx, win, s.deps.Models, probe.ProbeShort, now)
		if err != nil {
			return nil, err
		}
		byWindow[key] = sum
	}
	out.Summary = byWindow[window.Key]
	out.Now = byWindow[dashboardNowWindow]
	out.Baseline = byWindow[dashboardBaselineWindow]

	// probe is a filter here, never an aggregation: mixing the short probe's
	// TTFT with the wide one's would destroy the only thing wide exists to
	// measure.
	for _, spec := range []struct {
		dst    *any
		metric string
		probe  string
	}{
		{&out.Series.TTFT, "ttft", probe.ProbeShort},
		{&out.Series.TTFTWide, "ttft", probe.ProbeWide},
		{&out.Series.TPS, "tps", probe.ProbeShort},
		{&out.Series.Total, "total", probe.ProbeShort},
	} {
		v, err := s.buildModelSeries(ctx, spec.metric, spec.probe, window, now)
		if err != nil {
			return nil, err
		}
		*spec.dst = v
	}

	net, err := s.buildNetSeries(ctx, window, now)
	if err != nil {
		return nil, err
	}
	out.Series.Network = net

	cost, err := s.deps.Samples.Cost(ctx, window, s.deps.Prices, now)
	if err != nil {
		return nil, err
	}
	out.Cost = cost

	for _, model := range s.deps.Models {
		rows, err := s.deps.Samples.RecentPulse(ctx, model, probe.ProbeShort, dashboardPulseLimit)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []samples.Pulse{}
		}
		out.Pulse = append(out.Pulse, map[string]any{
			"model_id": model, "probe": probe.ProbeShort, "cycles": rows,
		})
	}

	// The whole model-and-probe cross product, model-major with short before
	// wide. The raw table calls itself the unaggregated record, so anything
	// missing here is missing from the one surface that promises nothing is.
	//
	// Order is part of the contract: the client concatenates these groups and
	// sorts on the instant, and a group arriving where another was expected
	// would relabel rows rather than reorder them.
	for _, model := range s.deps.Models {
		for _, kind := range []string{probe.ProbeShort, probe.ProbeWide} {
			rows, err := s.deps.Samples.RecentSamples(ctx, model, kind, dashboardSampleLimit)
			if err != nil {
				return nil, err
			}
			if rows == nil {
				rows = []samples.Sample{}
			}
			out.Samples = append(out.Samples, map[string]any{
				"model_id": model, "probe": kind, "samples": rows,
			})
		}
	}

	// The bad runs last — failures and graded-wrong answers alike — and NOT
	// scoped to `window`: the block reaches back a fixed day so the errors card
	// says the same thing whichever pill is selected.
	failures, err := s.deps.Samples.RecentFailures(
		ctx, s.deps.Models, now.Add(-dashboardFailureWindow), dashboardFailureLimit)
	if err != nil {
		return nil, err
	}
	// [] not null: a day with nothing wrong in it is the common case, and the
	// card renders its own empty state rather than the client guarding a null.
	if failures == nil {
		failures = []samples.Failure{}
	}
	out.Failures = failures

	return out, nil
}

// errUnknownMetric can only fire if a metric named above leaves metricColumns.
// Every metric here is a literal in this file, so this is a build-time mistake
// surfacing at runtime, not a caller's bad input — hence a 500 rather than the
// 400 an unknown ?metric= used to earn.
var errUnknownMetric = errors.New("unknown metric")

// errUnknownWindow is the same kind of mistake for the two comparison windows:
// both are literals in this file, so a miss is a rename in samples.Windows that
// nothing else caught, not a caller's bad input.
var errUnknownWindow = errors.New("unknown window")

// buildModelSeries is one metric across every model, bucketed for the window.
func (s *server) buildModelSeries(ctx context.Context, metric, probeKind string, window samples.Window, now time.Time) (any, error) {
	// Through metricColumns rather than straight to a column name: the wire
	// name is a stable contract, the column is an implementation detail a
	// migration may rename, and this is the second gate in front of the SQL
	// allow-list in the samples package.
	column, ok := metricColumns[metric]
	if !ok {
		return nil, errUnknownMetric
	}
	out := map[string][]samples.Point{}
	for _, model := range s.deps.Models {
		pts, err := s.deps.Samples.Series(ctx, column, model, probeKind, window, now)
		if err != nil {
			return nil, err
		}
		// [] not null, so the client can map over a model with no data yet.
		if pts == nil {
			pts = []samples.Point{}
		}
		out[model] = pts
	}
	return map[string]any{
		"window": window.Key, "bucket_s": int(window.Bucket / time.Second),
		"metric": metric, "probe": probeKind, "models": out,
	}, nil
}

// buildNetSeries is the connect time to each ping target.
//
// Per-target, not per-model, and so its own shape rather than a metric on a
// model — the network is not a model, and pretending otherwise is how it ends
// up drawn in a model's colour.
//
// All FOUR targets, unlike Summarize's net loop, which stays at the Singapore
// pair. The difference is deliberate: this feeds a chart the reader interprets,
// that feeds published availability figures. A second provider edge in the
// summary would sit beside mimo_sgp computed under a different exclusion rule.
func (s *server) buildNetSeries(ctx context.Context, window samples.Window, now time.Time) (any, error) {
	out := map[string][]samples.Point{}
	for _, target := range []string{
		probe.TargetMimoSGP, probe.TargetRefSGP,
		probe.TargetMimoAMS, probe.TargetRefAMS,
	} {
		pts, err := s.deps.Samples.NetSeries(ctx, target, window, now)
		if err != nil {
			return nil, err
		}
		if pts == nil {
			pts = []samples.Point{}
		}
		out[target] = pts
	}
	return map[string]any{
		"window": window.Key, "bucket_s": int(window.Bucket / time.Second),
		"metric": "network", "targets": out,
	}, nil
}
