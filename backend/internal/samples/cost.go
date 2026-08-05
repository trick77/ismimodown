package samples

import (
	"context"
	"fmt"
	"time"

	"github.com/trick77/ismimodown/internal/config"
	"github.com/trick77/ismimodown/internal/probe"
)

// What this dashboard's own probing costs, priced from the usage MiMo reported
// on every run.
//
// Nothing here is measured a second time. The four token columns are already
// written for every successful run by insertInfer, straight out of the usage
// chunk the stream ends with; this file groups them and multiplies.

// secondsPerDay and offPeakStartSecond express the billing window in the units
// the query works in — seconds since the UTC epoch, modulo a day.
const (
	secondsPerDay      = 86400
	offPeakStartSecond = config.OffPeakStartUTCHour * 3600
)

// CycleSeconds mirrors scheduler.CycleInterval, and halfCycleSeconds is the
// offset that turns a floor into a round.
//
// Restated rather than imported: `samples` is the layer the scheduler writes
// into, and a dependency the other way would make the store aware of the loop
// that fills it. The value is also a property of the rows already in the table
// — the cadence they were written at — rather than of the process running now.
//
// Exported only so the restatement can be CHECKED. A restated constant that
// silently disagrees with the one it mirrors is the whole hazard, so the
// scheduler asserts the two are equal at compile time; nothing reads this to
// decide anything.
//
// They exist because the cost series SUMS per bucket, and every bucket width is
// a multiple of the cadence, so bucket boundaries land exactly on cycle ticks.
// The scheduler jitters each cycle by ±30 s around its aligned tick, which puts
// the boundary cycle on either side of the line at random: buckets end up
// holding two, three or four cycles instead of three, and the line saws by ±50%
// for reasons that have nothing to do with what was spent. Rounding each run to
// the tick it belongs to before flooring it into a bucket removes the straddle
// and leaves the money untouched.
const (
	CycleSeconds     = 300
	halfCycleSeconds = CycleSeconds / 2
)

// PhaseFull and PhaseOffPeak name the two billing phases.
//
// Served as strings rather than as a boolean, so the client renders a label it
// was given instead of inventing one from a flag — and so a third phase, if MiMo
// ever adds one, does not have to break the shape.
const (
	PhaseFull    = "full"
	PhaseOffPeak = "offpeak"
)

// Tokens is a token count broken into the parts that are billed differently.
//
// Prompt is the WHOLE input count and Cached is a subset of it, exactly as MiMo
// reports them: cached_tokens arrives nested under prompt_tokens_details. The
// uncached remainder is Prompt-Cached, which is what gets the input rate.
//
// There is deliberately no reasoning field. reasoning_tokens is nested inside
// completion_tokens the same way, so it is already inside Output; carrying it
// here would invite someone to add it, and adding it bills those tokens twice.
type Tokens struct {
	Prompt int64 `json:"prompt"`
	Cached int64 `json:"cached"`
	Output int64 `json:"output"`
}

func (t Tokens) add(o Tokens) Tokens {
	return Tokens{t.Prompt + o.Prompt, t.Cached + o.Cached, t.Output + o.Output}
}

// Total is every token the run reported. Prompt already contains Cached, so
// they are not added twice.
func (t Tokens) Total() int64 { return t.Prompt + t.Output }

// CostGroup is one priced slice of the window: a phase, a probe kind, or the
// whole thing.
type CostGroup struct {
	Runs   int    `json:"runs"`
	Tokens Tokens `json:"tokens"`
	// USD is what we were billed — list price with the off-peak coefficient
	// already applied to whatever share of this group fell inside the window.
	//
	// A pointer, and never nil in practice: prices are a constant table now, so
	// every group gets a figure. The nullability survives in the JSON shape
	// rather than being flattened out, because doing that would drag the
	// client's formatter and its "not priced is not the same as free" tests into
	// a change that is about configuration and not about money.
	USD *float64 `json:"usd"`
	// ListUSD is the same tokens at full rate. The difference between the two is
	// the rebate, and quoting it is what keeps "saved $x" from being a figure
	// only this code can reproduce.
	ListUSD *float64 `json:"list_usd"`
}

// CostPoint is one bucket of the cost series.
type CostPoint struct {
	T   int64    `json:"t"`
	USD *float64 `json:"usd"`
	// Runs is what the bucket is made of. A bucket absent entirely means no run
	// landed in it, which the chart must draw as a gap rather than as a zero.
	Runs int `json:"runs"`
}

// PhaseCost is one billing phase's slice, named.
type PhaseCost struct {
	Phase string `json:"phase"`
	CostGroup
}

// ProbeCost is one probe kind's slice.
//
// Split out because the mean over both is a number that describes no run that
// exists: a wide run carries a ~3.6k-token prompt and a short one carries 70, so
// they differ by more than an order of magnitude. Per-probe is the coarsest
// split that still yields a per-inference figure anyone can act on.
type ProbeCost struct {
	Probe string `json:"probe"`
	CostGroup
}

// CostBreakdown is the cost panel of the dashboard payload.
type CostBreakdown struct {
	Window   string `json:"window"`
	Currency string `json:"currency"`
	// Prices is the table the figures were computed with, published so a total
	// can be checked rather than trusted. Always config.DefaultPrices.
	Prices map[string]config.ModelPrice `json:"prices"`
	// Coefficient is the off-peak multiplier the phases were billed at.
	Coefficient float64 `json:"offpeak_coefficient"`

	Total  CostGroup   `json:"total"`
	Phases []PhaseCost `json:"phases"`
	Probes []ProbeCost `json:"probes"`
	Series []CostPoint `json:"series"`
	// BucketSeconds is the width of a Series bucket, so the client can size a
	// mark without guessing from the gaps.
	BucketSeconds int64 `json:"bucket_s"`

	// Unpriced is how many runs in the window carry no usage at all.
	//
	// The usage chunk arrives LAST, so a run cut off by the timeout ladder was
	// billed for whatever prefill and decode happened before the cut and reports
	// none of it. Those runs are not in any figure above, and the count exists so
	// the total can say so instead of quietly being too low exactly when the
	// endpoint is at its worst. Never estimate a cost for them.
	Unpriced int `json:"unpriced_runs"`

	// OffPeakSpans are the [from,to) instants, in unix seconds, where the
	// reduced rate applied inside this window — clipped to it, so a span that
	// opened before the left edge starts at the edge.
	//
	// Served rather than derived client-side: the client used to keep its own
	// copy of the window and the DST rules that turn it into local hours, and two
	// implementations of one clock is one too many.
	OffPeakSpans [][2]int64 `json:"offpeak_spans"`
	// OffPeakUntil is when the CURRENT state ends: the close of the window if it
	// is open now, otherwise the next open. An instant, never a countdown — the
	// page re-renders on the 5-minute cycle, so "in 3h" would be wrong for most
	// of the time it was on screen.
	OffPeakUntil  int64 `json:"offpeak_until"`
	OffPeakActive bool  `json:"offpeak_active"`

	GeneratedAt time.Time `json:"generated_at"`
}

// isOffPeakUnix answers whether an instant billed at the reduced rate.
func isOffPeakUnix(sec int64) bool {
	// Go's % keeps the sign of the dividend, and unix seconds before 1970 would
	// come out negative. Not reachable from stored data, but the correction is
	// one line and its absence would be a silent mis-banding rather than an
	// error.
	day := sec % secondsPerDay
	if day < 0 {
		day += secondsPerDay
	}
	return day >= offPeakStartSecond
}

// offPeakSpans returns the reduced-rate spans overlapping [from,to), clipped.
func offPeakSpans(from, to int64) [][2]int64 {
	if to <= from {
		return [][2]int64{}
	}
	spans := make([][2]int64, 0, 4)
	// Start a day early: the window closes exactly at midnight UTC, so today's
	// span cannot reach back over the left edge as the constants stand. The
	// extra iteration contributes nothing today and is what keeps a window moved
	// past 24:00 from being half-drawn.
	day := (from/secondsPerDay)*secondsPerDay - secondsPerDay
	for ; day < to; day += secondsPerDay {
		start, end := day+offPeakStartSecond, day+secondsPerDay
		if start < from {
			start = from
		}
		if end > to {
			end = to
		}
		if end > start {
			spans = append(spans, [2]int64{start, end})
		}
	}
	return spans
}

// costRow is one aggregate group as the database returns it.
type costRow struct {
	bucket  int64
	modelID string
	probe   string
	offPeak bool
	runs    int
	tokens  Tokens
}

// Cost aggregates the window's token usage and prices it.
//
// The database groups; Go multiplies. Prices are configuration and change, so
// interpolating them into SQL would mean the query had to be rebuilt whenever
// they did — and, worse, that a stored total could not be recomputed at a
// corrected price later. Raw tokens stay in the table; money is derived on read.
func (s *Store) Cost(ctx context.Context, w Window, prices map[string]config.ModelPrice, now time.Time) (CostBreakdown, error) {
	since := now.Add(-w.Duration)
	bucketSecs := int64(w.Bucket / time.Second)

	out := CostBreakdown{
		Window:        w.Key,
		Currency:      "USD",
		Coefficient:   config.OffPeakCoefficient,
		BucketSeconds: bucketSecs,
		GeneratedAt:   now.UTC(),
	}
	out.Prices = prices

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT -- Rounded to the cycle tick the run belongs to, THEN floored into
		       -- the bucket. Flooring the raw instant lets the ±30 s scheduling
		       -- jitter carry a boundary cycle across the line, which shows up
		       -- as a sawtooth in a series that sums. See cycleSeconds.
		       ((unixepoch(c.started_at) + %[4]d) / %[5]d * %[5]d) / %[1]d * %[1]d AS bucket,
		       i.model_id,
		       i.probe,
		       -- The billing phase, decided per run from its own timestamp
		       -- against the UTC clock. Not from the bucket: a bucket can
		       -- straddle the boundary, and rounding a run into the wrong phase
		       -- would misprice it.
		       CASE WHEN unixepoch(c.started_at) %% %[2]d >= %[3]d THEN 1 ELSE 0 END AS offpeak,
		       count(*)                        AS runs,
		       COALESCE(sum(i.prompt_tokens), 0) AS prompt_tokens,
		       COALESCE(sum(i.cached_tokens), 0) AS cached_tokens,
		       COALESCE(sum(i.output_tokens), 0) AS output_tokens
		FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE c.started_at >= ?
		  -- Runs that reported usage. A failed run stores NULL in all four
		  -- columns, so it cannot be priced; it is counted separately below
		  -- rather than folded in as a free run.
		  --
		  -- Zero is the same case wearing a different hat. Every probe sends a
		  -- system message, which puts a floor of ~20 on prompt_tokens (see
		  -- config.DefaultSystemPrompt), so a SUCCEEDED run reporting zero did
		  -- not cost nothing — its usage chunk never arrived, and the column
		  -- cannot tell "none" from "not reported". Pricing it would publish a
		  -- free inference.
		  AND COALESCE(i.prompt_tokens, 0) > 0
		GROUP BY bucket, i.model_id, i.probe, offpeak
		ORDER BY bucket`,
		bucketSecs, secondsPerDay, offPeakStartSecond, halfCycleSeconds, CycleSeconds),
		since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return CostBreakdown{}, fmt.Errorf("cost query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		byPhase  = map[string]*CostGroup{}
		byProbe  = map[string]*CostGroup{}
		byBucket = map[int64]*CostPoint{}
		order    []int64
		total    CostGroup
	)
	// A window with no rows in it still has a total, and that total is zero
	// rather than unknown. Left nil, the response would refuse to name a figure,
	// which is a state the client has no way to render.
	zeroBilled, zeroList := 0.0, 0.0
	total.USD, total.ListUSD = &zeroBilled, &zeroList

	for rows.Next() {
		var r costRow
		var off int
		if err := rows.Scan(&r.bucket, &r.modelID, &r.probe, &off,
			&r.runs, &r.tokens.Prompt, &r.tokens.Cached, &r.tokens.Output); err != nil {
			return CostBreakdown{}, fmt.Errorf("cost scan: %w", err)
		}
		r.offPeak = off == 1

		// Every model in config.DefaultModels has an entry in
		// config.DefaultPrices, and both are constants now, so a miss here is not
		// a reachable configuration — it needs a source edit that changes one
		// list without the other, which config_test.go refuses.
		//
		// It IS reachable from history: retention keeps samples for 3 months, so
		// renaming a probed model leaves that long a tail of rows whose model_id
		// the new table has never heard of. Those rows price at the zero value
		// and contribute nothing to a total that still presents itself as
		// complete. Accepted deliberately — the alternative was a whole "this
		// window could not be priced" path through the API and the panel, and it
		// went. If a model is ever renamed, delete or remap its rows.
		price := prices[r.modelID]
		list := priceOf(r.tokens, price)
		billed := list
		if r.offPeak {
			billed = list * config.OffPeakCoefficient
		}

		phase := PhaseFull
		if r.offPeak {
			phase = PhaseOffPeak
		}
		accumulate(&total, r, billed, list)
		accumulate(groupFor(byPhase, phase), r, billed, list)
		accumulate(groupFor(byProbe, r.probe), r, billed, list)

		pt, ok := byBucket[r.bucket]
		if !ok {
			pt = &CostPoint{T: r.bucket}
			byBucket[r.bucket] = pt
			order = append(order, r.bucket)
		}
		pt.Runs += r.runs
		addUSD(&pt.USD, billed)
	}
	if err := rows.Err(); err != nil {
		return CostBreakdown{}, fmt.Errorf("cost rows: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM infer_probes i
		JOIN cycles c ON c.id = i.cycle_id
		WHERE c.started_at >= ? AND COALESCE(i.prompt_tokens, 0) = 0`,
		since.UTC().Format(time.RFC3339Nano)).Scan(&out.Unpriced); err != nil {
		return CostBreakdown{}, fmt.Errorf("cost unpriced: %w", err)
	}

	out.Total = total
	// Allocated, so they marshal as [] and not null. The client maps over both,
	// and a null there is one missing guard away from a blank page.
	out.Phases = make([]PhaseCost, 0, 2)
	out.Probes = make([]ProbeCost, 0, 2)
	for _, phase := range []string{PhaseFull, PhaseOffPeak} {
		if g, ok := byPhase[phase]; ok {
			out.Phases = append(out.Phases, PhaseCost{Phase: phase, CostGroup: *g})
		}
	}
	// The constants, not literals: this list is what fixes the order the panel
	// names the probes in, and a string here that no row carries drops a whole
	// probe from the cost card silently.
	for _, kind := range []string{probe.ProbeShort, probe.ProbeWide} {
		if g, ok := byProbe[kind]; ok {
			out.Probes = append(out.Probes, ProbeCost{Probe: kind, CostGroup: *g})
		}
	}
	out.Series = make([]CostPoint, 0, len(order))
	for _, b := range order {
		out.Series = append(out.Series, *byBucket[b])
	}

	out.OffPeakSpans = offPeakSpans(since.UTC().Unix(), now.UTC().Unix())
	out.OffPeakActive = isOffPeakUnix(now.UTC().Unix())
	nowSec := now.UTC().Unix()
	day := (nowSec / secondsPerDay) * secondsPerDay
	if out.OffPeakActive {
		out.OffPeakUntil = day + secondsPerDay
	} else {
		out.OffPeakUntil = day + offPeakStartSecond
	}
	return out, nil
}

// priceOf returns the list-price cost of a token count.
//
// The input side is split: cached_tokens is a SUBSET of prompt_tokens, so the
// uncached remainder gets the input rate and the cached part gets its own. A
// cached count larger than the prompt count would make the remainder negative,
// which is not possible from MiMo's own accounting but would silently produce a
// credit if it ever happened, so it is clamped.
func priceOf(t Tokens, p config.ModelPrice) float64 {
	uncached := t.Prompt - t.Cached
	if uncached < 0 {
		uncached = 0
	}
	const perMillion = 1_000_000
	return float64(uncached)*p.In/perMillion +
		float64(t.Cached)*p.Cached/perMillion +
		float64(t.Output)*p.Out/perMillion
}

func groupFor(m map[string]*CostGroup, key string) *CostGroup {
	g, ok := m[key]
	if !ok {
		g = &CostGroup{}
		m[key] = g
	}
	return g
}

func accumulate(g *CostGroup, r costRow, billed, list float64) {
	g.Runs += r.runs
	g.Tokens = g.Tokens.add(r.tokens)
	addUSD(&g.USD, billed)
	addUSD(&g.ListUSD, list)
}

func addUSD(dst **float64, v float64) {
	if *dst == nil {
		total := v
		*dst = &total
		return
	}
	**dst += v
}
