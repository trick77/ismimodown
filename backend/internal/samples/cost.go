package samples

import (
	"context"
	"fmt"
	"time"

	"github.com/trick77/mimostats/internal/config"
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
	// Nil when no price is configured for a model that appears in the group.
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
	// Runs is what the bucket is made of. A bucket with runs and no usd means
	// prices are not configured; a bucket absent entirely means no run landed in
	// it, which the chart must draw as a gap rather than as a zero.
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

// CostBreakdown is the /api/cost payload.
type CostBreakdown struct {
	Window string `json:"window"`
	// Priced reports whether a price table was configured. When false every USD
	// field is null and the client must show tokens without money rather than
	// rendering zeros — a $0.00 bill is a claim, and a wrong one.
	Priced   bool   `json:"priced"`
	Currency string `json:"currency"`
	// Prices is the table the figures were computed with, published so a total
	// can be checked rather than trusted. Absent when none is configured.
	Prices map[string]config.ModelPrice `json:"prices,omitempty"`
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
		Priced:        len(prices) > 0,
		Currency:      "USD",
		Coefficient:   config.OffPeakCoefficient,
		BucketSeconds: bucketSecs,
		GeneratedAt:   now.UTC(),
	}
	if out.Priced {
		out.Prices = prices
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT unixepoch(c.started_at) / %[1]d * %[1]d AS bucket,
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
		  AND i.prompt_tokens IS NOT NULL
		GROUP BY bucket, i.model_id, i.probe, offpeak
		ORDER BY bucket`,
		bucketSecs, secondsPerDay, offPeakStartSecond),
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
		anyPrice = out.Priced
	)

	for rows.Next() {
		var r costRow
		var off int
		if err := rows.Scan(&r.bucket, &r.modelID, &r.probe, &off,
			&r.runs, &r.tokens.Prompt, &r.tokens.Cached, &r.tokens.Output); err != nil {
			return CostBreakdown{}, fmt.Errorf("cost scan: %w", err)
		}
		r.offPeak = off == 1

		// The map lookup, never a zero-value comparison: a model configured at
		// 0/0 is a free tier, and its true cost of nothing must not read as
		// "no price configured".
		price, priced := prices[r.modelID]
		list := priceOf(r.tokens, price)
		billed := list
		if r.offPeak {
			billed = list * config.OffPeakCoefficient
		}
		// A model with no configured price cannot be priced, and the totals it
		// belongs to must not pretend otherwise: one unpriced model makes every
		// group it appears in unpriced, rather than reporting the others' cost
		// as if it were the whole bill.
		if !priced {
			anyPrice = false
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
		WHERE c.started_at >= ? AND i.prompt_tokens IS NULL`,
		since.UTC().Format(time.RFC3339Nano)).Scan(&out.Unpriced); err != nil {
		return CostBreakdown{}, fmt.Errorf("cost unpriced: %w", err)
	}

	out.Priced = anyPrice
	if !anyPrice {
		out.Prices = nil
	}
	out.Total = finish(total, anyPrice)
	for _, phase := range []string{PhaseFull, PhaseOffPeak} {
		if g, ok := byPhase[phase]; ok {
			out.Phases = append(out.Phases, PhaseCost{Phase: phase, CostGroup: finish(*g, anyPrice)})
		}
	}
	for _, kind := range []string{"infer", "wide"} {
		if g, ok := byProbe[kind]; ok {
			out.Probes = append(out.Probes, ProbeCost{Probe: kind, CostGroup: finish(*g, anyPrice)})
		}
	}
	out.Series = make([]CostPoint, 0, len(order))
	for _, b := range order {
		pt := *byBucket[b]
		if !anyPrice {
			pt.USD = nil
		}
		out.Series = append(out.Series, pt)
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

// finish drops the money from a group that could not be fully priced.
func finish(g CostGroup, priced bool) CostGroup {
	if !priced {
		g.USD, g.ListUSD = nil, nil
	}
	return g
}
