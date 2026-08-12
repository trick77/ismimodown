package samples

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
)

// Prefill measures what a ~3800-token prompt adds to time-to-first-token over a
// ~34-token one, per model.
//
// The pairing is a JOIN ON cycle_id, and that is the whole point of this file.
//
// It was measured for a long time by plotting the two probes' bucketed
// percentiles against each other and reading the gap. Fourteen days of
// production data says that does not work: paired by time bucket the median
// difference came out at -147 ms for one model and +97 ms for the other, with
// 55% and 42% of hours NEGATIVE. Paired within the cycle the same fortnight
// gives +245 ms and +305 ms with 42% and 30% negative — a real, positive
// prefill cost that the bucket pairing had buried.
//
// The reason is that a wide run and a short run from different cycles are
// minutes apart and see different queue states, and queueing on this endpoint
// swings by ~±1600 ms against a prefill cost of ~250. Subtracting across
// cycles measures the queue; subtracting within one cancels it. The schema was
// built for this — see the header of 0001_init.sql, "that subtraction is a JOIN
// on cycle_id, never a nearest-timestamp guess" — and a wide cycle deliberately
// carries short AND wide for the same model, back to back, for exactly this
// reason (scheduler.WideSlot).
//
// What the pairing does NOT fix is the per-run spread. σ is still ~1600 ms
// against a ~250 ms effect, so a SINGLE pair says nothing and no chart at one
// point per wide run can say anything either. Only the aggregate over a window
// resolves it, which is why this returns a figure and an interval rather than a
// series.

// MinPrefillPairs is the suppression threshold, and it is a statement about
// resolution rather than a taste for round numbers.
//
// The standard error on the median is roughly σ/√n, so with σ ≈ 1600 ms it
// takes ~170 pairs to bring the interval inside the ~250 ms effect. The wide
// probe runs hourly per model, so:
//
//	24h →  24 pairs → ±334 ms — wider than the thing being measured
//	48h →  48 pairs → ±237 ms — still ~1σ
//	7d  → 168 pairs → ±126 ms — the first window that resolves it
//
// 24h and 48h therefore cannot support this figure at the current cadence, and
// no amount of uptime changes that: the window holds what the schedule puts in
// it. Raising the cadence would, at ~7x the wide token spend.
//
// The gate is on PAIRS rather than on the window key on purpose. It stays
// correct if the cadence ever changes, and it also catches the cases a
// window-name rule gets wrong anyway — a fresh deploy, or a stretch where the
// wide probe was failing, both of which leave a 7d window far short of 168.
const MinPrefillPairs = 150

// PrefillCost is one model's prefill measurement over one window.
type PrefillCost struct {
	ModelID string `json:"model_id"`

	// Pairs is cycles where BOTH probes produced a TTFT. It is the honest
	// denominator: a cycle where either side failed cannot be subtracted.
	Pairs int `json:"pairs"`

	// Sufficient reports whether Pairs cleared MinPrefillPairs. When false
	// every figure below is nil — an explicit JSON null, so a client can tell
	// "suppressed" from "not sent" — and must render as insufficient data,
	// never as 0 ms.
	Sufficient bool `json:"sufficient"`

	// P50 is the median difference, in ms. Positive is a cost.
	P50 *float64 `json:"p50_ms"`

	// Lo and Hi bound P50 at roughly 95%. Order statistics, not σ/√n: the
	// differences are not normal — they inherit the queue's long right tail —
	// and a rank-based interval needs no distribution at all. See medianCI.
	//
	// Published rather than optional, because the entire finding of this file
	// is that the point estimate alone is not readable. A client comparing two
	// periods must be able to ask whether the intervals overlap before it
	// claims anything moved.
	Lo *float64 `json:"lo_ms"`
	Hi *float64 `json:"hi_ms"`

	// WideP50 is the median absolute TTFT of the wide runs in these pairs.
	//
	// Here so the panel can state the SHARE: ~250 ms of prefill inside a
	// ~2600 ms wait is the actually useful reading, and it says prompt size is
	// not what makes this endpoint slow. A cost with no total to sit inside is
	// a number nobody can act on.
	WideP50 *float64 `json:"wide_p50_ms"`

	// Censored is how many cycles lost their pair to our own timeout ladder.
	//
	// The same doctrine as everywhere else here: the percentiles are computed
	// over pairs that FINISHED, so the ones this counts were removed from the
	// slow end and the figure improves as truncation worsens. Never folded
	// back in; always published beside.
	Censored int `json:"censored"`
}

// PrefillCosts measures every model over one window.
//
// The window is half-open — [now-Duration, now) — unlike Series, which has no
// upper bound because it only ever plots up to the present. The bound is what
// lets a caller ask for the PREVIOUS period by passing now-Duration, which is
// how the panel gets something to compare against.
func (s *Store) PrefillCosts(ctx context.Context, models []string, w Window, now time.Time) ([]PrefillCost, error) {
	out := make([]PrefillCost, 0, len(models))
	for _, m := range models {
		c, err := s.prefillCost(ctx, m, w, now)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) prefillCost(ctx context.Context, modelID string, w Window, now time.Time) (PrefillCost, error) {
	out := PrefillCost{ModelID: modelID}
	since := now.Add(-w.Duration)

	// Both sides of the pair, ordered, and small enough to sort in Go.
	//
	// Deliberately unlike Series, which computes its percentiles in SQL because
	// a 3-month window there is ~110 000 rows. A pair needs a WIDE run, and the
	// wide probe runs hourly, so the largest window this can return is about
	// 2 200 rows per model. Two medians and a rank-based interval over that are
	// clearer in Go than as four more CASE WHEN rn = ... arms, and the interval
	// needs sqrt(), which SQLite only has when it was compiled with the math
	// functions.
	const q = `
		SELECT w.ttft_ms - s.ttft_ms AS delta, w.ttft_ms AS wide
		FROM infer_probes s
		JOIN infer_probes w
		  ON w.cycle_id = s.cycle_id AND w.model_id = s.model_id AND w.probe = ?
		JOIN cycles c ON c.id = s.cycle_id
		WHERE s.model_id = ? AND s.probe = ? AND s.ok = 1 AND w.ok = 1
		  AND s.ttft_ms IS NOT NULL AND w.ttft_ms IS NOT NULL
		  AND c.started_at >= ? AND c.started_at < ?`

	rows, err := s.db.QueryContext(ctx, q,
		probe.ProbeWide, modelID, probe.ProbeShort, rfc(since), rfc(now))
	if err != nil {
		return out, err
	}
	defer rows.Close()

	var deltas, wides []float64
	for rows.Next() {
		var d, wide float64
		if err := rows.Scan(&d, &wide); err != nil {
			return out, err
		}
		deltas = append(deltas, d)
		wides = append(wides, wide)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	cens, err := s.prefillCensored(ctx, modelID, since, now)
	if err != nil {
		return out, err
	}
	out.Censored = cens
	out.Pairs = len(deltas)
	if out.Pairs < MinPrefillPairs {
		return out, nil
	}

	sort.Float64s(deltas)
	sort.Float64s(wides)
	out.Sufficient = true
	p50 := median(deltas)
	out.P50 = &p50
	widep50 := median(wides)
	out.WideP50 = &widep50
	lo, hi := medianCI(deltas)
	out.Lo, out.Hi = &lo, &hi
	return out, nil
}

// prefillCensored counts cycles that ran a wide probe and lost the pair to the
// timeout ladder — either side cut off is enough, since neither can be
// subtracted from the other.
//
// It carries the same unattributable-cycle exclusion the rest of this package
// applies: a run cut off during OUR uplink outage is not MiMo's slow tail, and
// counting it here would disagree with the model card, which already refuses to.
func (s *Store) prefillCensored(ctx context.Context, modelID string, since, now time.Time) (int, error) {
	censExpr, censArgs := censoredSQL("i")
	q := fmt.Sprintf(`
		SELECT count(DISTINCT c.id)
		FROM cycles c
		JOIN infer_probes i ON i.cycle_id = c.id
		LEFT JOIN cycle_fault f ON f.cycle_id = c.id
		WHERE i.model_id = ? AND %s
		  AND c.started_at >= ? AND c.started_at < ?
		  AND COALESCE(f.fault, '') NOT IN (?, ?)
		  AND EXISTS (
		      SELECT 1 FROM infer_probes x
		      WHERE x.cycle_id = c.id AND x.model_id = ? AND x.probe = ?
		  )`, censExpr)

	args := []any{modelID}
	args = append(args, censArgs...)
	args = append(args, rfc(since), rfc(now),
		probe.FaultUplink, probe.FaultRoute, modelID, probe.ProbeWide)

	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// median of a sorted slice. Lower of the two middles on an even count, matching
// the nearest-rank convention percentileSQL uses, so the two never disagree
// about what "the median" means on the same data.
func median(sorted []float64) float64 {
	return sorted[(len(sorted)-1)/2]
}

// medianCI is the distribution-free ~95% confidence interval for a median: the
// k-th and (n+1-k)-th order statistics, with k = floor(n/2 - 0.98·√n).
//
// Order statistics rather than ±1.96·σ/√n because these differences are not
// normal — they inherit the queue's long right tail, which is exactly what the
// pairing could not cancel — and a rank-based interval assumes no shape at all.
// It is also what a reader can check by hand against the raw pairs.
//
// The caller guarantees n ≥ MinPrefillPairs, so k cannot fall below 1 here; the
// clamp is kept anyway because this function is the kind that gets reused.
func medianCI(sorted []float64) (lo, hi float64) {
	n := len(sorted)
	k := int(math.Floor(float64(n)/2 - 0.98*math.Sqrt(float64(n))))
	if k < 1 {
		k = 1
	}
	return sorted[k-1], sorted[n-k]
}
