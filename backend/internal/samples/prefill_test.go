package samples

import (
	"context"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
)

// seedPairs writes n cycles, each carrying a short AND a wide run for model —
// the shape a wide cycle actually has, since one cycle carries both for the
// model whose turn it is (scheduler.WideSlot).
//
// delta(i) supplies the wide run's excess over the short one, so a test can
// hand this a constant, a spread, or a sign flip.
func seedPairs(t *testing.T, s *Store, model string, end time.Time, n int, short float64, delta func(i int) float64) {
	t.Helper()
	for i := 0; i < n; i++ {
		at := end.Add(-time.Duration(n-i) * time.Minute)
		wide := okInfer(model, short+delta(i))
		wide.Probe = probe.ProbeWide
		wide.Usage.PromptTokens = 3844
		if _, err := s.Save(context.Background(), Cycle{
			StartedAt: at, Net: okNet(),
			Infer: []probe.InferResult{okInfer(model, short), wide},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

func flat(v float64) func(int) float64 { return func(int) float64 { return v } }

func TestPrefillPairsWithinTheCycle(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")
	s := New(openTestDB(t))
	seedPairs(t, s, "mimo-v2.5", now, 200, 900, flat(250))

	got, err := s.PrefillCosts(context.Background(), []string{"mimo-v2.5"}, w, now)
	if err != nil {
		t.Fatalf("PrefillCosts: %v", err)
	}
	c := got[0]
	if c.Pairs != 200 {
		t.Fatalf("pairs = %d, want 200", c.Pairs)
	}
	if !c.Sufficient || c.P50 == nil {
		t.Fatal("200 pairs must be sufficient")
	}
	if *c.P50 != 250 {
		t.Errorf("p50 = %v, want 250", *c.P50)
	}
	// The cost has to arrive beside the total it sits inside, or a reader
	// cannot tell 250 ms of a 1150 ms wait from 250 ms of a 400 ms one.
	if c.WideP50 == nil || *c.WideP50 != 1150 {
		t.Errorf("wide p50 = %v, want 1150", c.WideP50)
	}
}

// A short run with no wide partner in its cycle is not a pair. This is the
// whole correction: pairing by time bucket instead swept in short runs from
// neighbouring cycles, which differ from the wide run by queueing rather than
// by prefill.
func TestPrefillIgnoresUnpairedRuns(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")
	s := New(openTestDB(t))
	seedPairs(t, s, "mimo-v2.5", now, 160, 900, flat(300))
	// The other 11 cycles in every hour: short only, and wildly slower. If
	// these reached the subtraction they would drag the median off 300.
	seedCycles(t, s, "mimo-v2.5", now.Add(-2*time.Hour), 500, 4000)

	got, err := s.PrefillCosts(context.Background(), []string{"mimo-v2.5"}, w, now)
	if err != nil {
		t.Fatalf("PrefillCosts: %v", err)
	}
	if got[0].Pairs != 160 {
		t.Errorf("pairs = %d, want 160 — only cycles carrying BOTH probes", got[0].Pairs)
	}
	if got[0].P50 == nil || *got[0].P50 != 300 {
		t.Errorf("p50 = %v, want 300", got[0].P50)
	}
}

// The threshold is a statement about resolution: below it the interval is
// wider than the effect, so a figure would be a number with no reading in it.
func TestPrefillSuppressionBoundary(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")

	t.Run("one pair short is suppressed", func(t *testing.T) {
		s := New(openTestDB(t))
		seedPairs(t, s, "mimo-v2.5", now, MinPrefillPairs-1, 900, flat(250))
		got, _ := s.PrefillCosts(context.Background(), []string{"mimo-v2.5"}, w, now)
		if got[0].Sufficient {
			t.Error("must not be sufficient one pair below the threshold")
		}
		// Nil, never zero: a zero would render as "prefill is free".
		if got[0].P50 != nil || got[0].Lo != nil || got[0].Hi != nil || got[0].WideP50 != nil {
			t.Error("every figure must be nil when suppressed")
		}
		// The count still travels, so the client can say WHY.
		if got[0].Pairs != MinPrefillPairs-1 {
			t.Errorf("pairs = %d, want %d", got[0].Pairs, MinPrefillPairs-1)
		}
	})

	t.Run("exactly the threshold is sufficient", func(t *testing.T) {
		s := New(openTestDB(t))
		seedPairs(t, s, "mimo-v2.5", now, MinPrefillPairs, 900, flat(250))
		got, _ := s.PrefillCosts(context.Background(), []string{"mimo-v2.5"}, w, now)
		if !got[0].Sufficient || got[0].P50 == nil {
			t.Error("the threshold itself must be sufficient")
		}
	})
}

// A negative difference is a reading, not an error: it says the short run in
// that cycle was the slower of the two, which is what queue noise looks like.
func TestPrefillKeepsNegativeDifferences(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")
	s := New(openTestDB(t))
	// Alternating ±400 around zero: the median must land on a negative rather
	// than being clamped up to 0.
	seedPairs(t, s, "mimo-v2.5", now, 200, 2000, func(i int) float64 {
		if i%2 == 0 {
			return -400
		}
		return 400
	})

	got, _ := s.PrefillCosts(context.Background(), []string{"mimo-v2.5"}, w, now)
	if got[0].P50 == nil || *got[0].P50 != -400 {
		t.Errorf("p50 = %v, want -400 (lower middle of an even count)", got[0].P50)
	}
}

// The interval is what stops a point estimate being read as exact. On a spread
// this wide it must be wide too — that IS the finding.
func TestPrefillIntervalWidensWithTheSpread(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")

	tight := New(openTestDB(t))
	seedPairs(t, tight, "m", now, 200, 900, func(i int) float64 { return 250 + float64(i%3) })
	noisy := New(openTestDB(t))
	seedPairs(t, noisy, "m", now, 200, 900, func(i int) float64 { return 250 + float64((i%40)-20)*80 })

	a, _ := tight.PrefillCosts(context.Background(), []string{"m"}, w, now)
	b, _ := noisy.PrefillCosts(context.Background(), []string{"m"}, w, now)

	tightWidth := *a[0].Hi - *a[0].Lo
	noisyWidth := *b[0].Hi - *b[0].Lo
	if !(noisyWidth > tightWidth) {
		t.Errorf("interval must widen with the spread: tight %v, noisy %v", tightWidth, noisyWidth)
	}
	// And it must bracket the estimate, or it is not an interval.
	if !(*b[0].Lo <= *b[0].P50 && *b[0].P50 <= *b[0].Hi) {
		t.Errorf("p50 %v outside [%v, %v]", *b[0].P50, *b[0].Lo, *b[0].Hi)
	}
}

// Half-open on the right, which is what lets the handler ask for the previous
// period by passing now-Duration. Without it both periods would end at the same
// instant and the comparison would be a window against itself.
func TestPrefillWindowIsHalfOpen(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")
	s := New(openTestDB(t))
	// Recent pairs only. Asking for the PREVIOUS 7d must not see them.
	seedPairs(t, s, "m", now, 200, 900, flat(250))

	prev, err := s.PrefillCosts(context.Background(), []string{"m"}, w, now.Add(-w.Duration))
	if err != nil {
		t.Fatalf("PrefillCosts: %v", err)
	}
	if prev[0].Pairs != 0 {
		t.Errorf("previous period saw %d pairs, want 0", prev[0].Pairs)
	}
}

// Truncated pairs are excluded from the figure, so the count has to be
// published beside it — the same rule the model card follows. Without it the
// figure improves as truncation worsens and nothing says so.
func TestPrefillPublishesCensoredPairs(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("7d")
	s := New(openTestDB(t))
	seedPairs(t, s, "m", now, 160, 900, flat(250))

	// One more cycle carrying a wide probe whose short partner was cut off.
	cut := okInfer("m", 0)
	cut.OK = false
	cut.TTFTMs = 0
	cut.ErrorClass = probe.CensoringErrorClasses[0]
	wide := okInfer("m", 3000)
	wide.Probe = probe.ProbeWide
	if _, err := s.Save(context.Background(), Cycle{
		StartedAt: now.Add(-time.Hour), Net: okNet(),
		Infer: []probe.InferResult{cut, wide},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, _ := s.PrefillCosts(context.Background(), []string{"m"}, w, now)
	if got[0].Pairs != 160 {
		t.Errorf("pairs = %d, want 160 — a cut-off run cannot be subtracted", got[0].Pairs)
	}
	if got[0].Censored != 1 {
		t.Errorf("censored = %d, want 1", got[0].Censored)
	}
}
