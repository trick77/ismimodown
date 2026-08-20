package samples

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// The "is it slower than it was" reading.
//
// The dashboard's other latency scoring compares a WINDOW against a 7-day
// baseline, which answers "is this bad" and cannot answer "is this worse than
// this morning" — a week-long baseline dilutes a slowdown that started three
// hours ago until it nearly disappears. This block answers the second question,
// and only the second.
//
// Three hours against the twenty-four before them. Three is a floor rather than
// a preference: at one cycle every five minutes it holds 36 runs against a
// MinSamplesForPercentile of 20, and anything shorter is structurally incapable
// of producing a percentile — the same arithmetic that keeps a 1h window out of
// Windows.
//
// It is NOT scoped to the selected window, exactly like Summary.Recent: the
// range pills choose what the charts draw, and a reading that changed meaning
// when the reader clicked 30d would be a different statistic under the same
// sentence. The handler serves this block unchanged on every window.
const (
	TrendRecent    = 3 * time.Hour
	TrendReference = 24 * time.Hour
)

// TrendSeriesWindow is the plot that rides along with the figures: the whole
// 27 hours the comparison spans, at half-hour buckets.
//
// Deliberately not one of the Windows: those are the reader's choices and this
// is the page's own. The bucket is wide enough that 54 points cover the span —
// two models times two metrics is 216 points, which is a plot, not a payload
// problem — and narrow enough that the shape of a slowdown is still visible.
//
// The page draws this rather than the selected window's series because the
// series a reader has selected may not contain three hours at all: the 30d
// bucket is six hours wide.
var TrendSeriesWindow = Window{
	Key:      "trend",
	Duration: TrendRecent + TrendReference,
	Bucket:   30 * time.Minute,
}

// TrendMetric is one metric's before-and-after, plus the shape between them.
//
// The RATIO is deliberately absent. Whether 1.2 is worth a word on the page is
// a question about this endpoint's ordinary noise, and that judgement lives
// with the rest of the scoring in ui/src/verdict.ts — the daemon publishes
// measurements, the client decides what they mean. Publishing a ratio here
// would put half of one decision on each side of the wire.
type TrendMetric struct {
	// Recent is the last TrendRecent. Before is the TrendReference ending where
	// Recent begins — the two never overlap, so a slowdown cannot dilute its own
	// reference.
	Recent Stats `json:"recent"`
	Before Stats `json:"before"`
	// Points is the whole span, oldest first. Each carries its own censored
	// count, which is what lets the client say the delta is understated: both
	// medians are computed over runs that FINISHED, so a period whose slowest
	// runs were cut off publishes a flattering figure.
	Points []Point `json:"points"`
}

// ModelTrend is one model's reading. Two metrics, and only two.
//
// Availability and correctness are absent on purpose: those are scored against
// a stated target rather than against yesterday, and giving them a trend would
// let a run of bad days quietly become the new normal. A failure is not a trend
// — it takes the banner outright.
//
// The network handshake is absent for a different reason: it is a couple of
// dozen milliseconds inside a wait of seconds, the network panel already plots
// it, and a headline nobody can feel is worse than no headline.
type ModelTrend struct {
	ModelID string      `json:"model_id"`
	TTFT    TrendMetric `json:"ttft"`
	TPS     TrendMetric `json:"tps"`
}

// Trend is the whole reading, one entry per model.
type Trend struct {
	// The two spans, in seconds, so the client can label the comparison without
	// hard-coding a duration the daemon owns.
	RecentSeconds int64 `json:"recent_s"`
	BeforeSeconds int64 `json:"before_s"`
	BucketSeconds int64 `json:"bucket_s"`

	Models      []ModelTrend `json:"models"`
	GeneratedAt time.Time    `json:"generated_at"`
}

// percentileRangeSQL is percentileSQL with an upper bound.
//
// The reference period ENDS where the recent one begins, and a half-open bound
// is what keeps the two from sharing a cycle: `>= from AND < to`. Sharing one
// would be invisible at three hours and wrong in principle — the comparison
// asks how the last three hours differ from the day before them, and a run
// counted on both sides makes them look more alike than they are.
const percentileRangeSQL = `
WITH vals AS (
	SELECT %s AS v FROM infer_probes i
	JOIN cycles c ON c.id = i.cycle_id
	WHERE i.model_id = ? AND i.ok = 1 AND %s IS NOT NULL
	  AND c.started_at >= ? AND c.started_at < ?
),
ranked AS (
	SELECT v,
		ROW_NUMBER() OVER (ORDER BY v) AS rn,
		COUNT(*) OVER () AS n
	FROM vals
)
SELECT
	COALESCE(MAX(n), 0),
	MAX(CASE WHEN rn = MAX(1, (n * 50 + 99) / 100) THEN v END),
	MAX(CASE WHEN rn = MAX(1, (n * 95 + 99) / 100) THEN v END)
FROM ranked`

// statsBetween is stats() over a half-open range.
//
// It keeps MinSamplesForPercentile: a period that cannot produce a median
// publishes Sufficient false and nil values, and the client says so in words
// rather than drawing a delta against a number that was never measured.
//
// The column goes through checkSeriesColumn for the same reason Series does it
// — it reaches the query as interpolated text, since a placeholder cannot name
// a column — even though every caller here passes a constant.
func (s *Store) statsBetween(ctx context.Context, column, modelID string, from, to time.Time) (Stats, error) {
	if err := checkSeriesColumn(column); err != nil {
		return Stats{}, err
	}
	q := fmt.Sprintf(percentileRangeSQL, column, column)
	var n int
	var p50, p95 sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, q, modelID, rfc(from), rfc(to)).Scan(&n, &p50, &p95); err != nil {
		return Stats{}, err
	}
	st := Stats{N: n, Sufficient: n >= MinSamplesForPercentile}
	if st.Sufficient {
		if p50.Valid {
			v := p50.Float64
			st.P50 = &v
		}
		if p95.Valid {
			v := p95.Float64
			st.P95 = &v
		}
	}
	return st, nil
}

// Trend builds the reading for every model.
func (s *Store) Trend(ctx context.Context, models []string, now time.Time) (Trend, error) {
	recentFrom := now.Add(-TrendRecent)
	beforeFrom := now.Add(-TrendRecent - TrendReference)

	out := Trend{
		RecentSeconds: int64(TrendRecent / time.Second),
		BeforeSeconds: int64(TrendReference / time.Second),
		BucketSeconds: int64(TrendSeriesWindow.Bucket / time.Second),
		// Allocated rather than declared: an empty model list must marshal as []
		// and not null, or a client mapping over it throws.
		Models:      make([]ModelTrend, 0, len(models)),
		GeneratedAt: now.UTC(),
	}

	for _, model := range models {
		mt := ModelTrend{ModelID: model}
		for _, spec := range []struct {
			dst    *TrendMetric
			column string
		}{
			{&mt.TTFT, "ttft_ms"},
			{&mt.TPS, "output_tps"},
		} {
			var err error
			if spec.dst.Recent, err = s.statsBetween(ctx, spec.column, model, recentFrom, now); err != nil {
				return Trend{}, err
			}
			if spec.dst.Before, err = s.statsBetween(ctx, spec.column, model, beforeFrom, recentFrom); err != nil {
				return Trend{}, err
			}
			points, err := s.Series(ctx, spec.column, model, TrendSeriesWindow, now)
			if err != nil {
				return Trend{}, err
			}
			if points == nil {
				points = []Point{}
			}
			spec.dst.Points = points
		}
		out.Models = append(out.Models, mt)
	}
	return out, nil
}
