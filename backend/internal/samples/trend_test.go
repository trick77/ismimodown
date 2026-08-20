package samples

import (
	"context"
	"testing"
	"time"

	"github.com/trick77/ismimodown/internal/probe"
)

// seedTrendCycles writes one successful run every five minutes over [from, to),
// which is the real cadence — a trend built on a denser fixture would clear the
// sample threshold in spans that could never clear it in production.
func seedTrendCycles(t *testing.T, s *Store, model string, from, to time.Time, ttft, tps float64) {
	t.Helper()
	for at := from; at.Before(to); at = at.Add(5 * time.Minute) {
		infer := okInfer(model, ttft)
		infer.OutputTPS = tps
		if _, err := s.Save(context.Background(), Cycle{
			StartedAt: at, Net: okNet(),
			Infer: []probe.InferResult{infer},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

// The whole point of the block: a slowdown confined to the last three hours has
// to show up as a difference between the two spans. A single window figure
// covering all 27 hours cannot say this — the day of fast runs outvotes the
// slow ones, which is exactly why the reading exists.
func TestTrendSeparatesTheRecentSpanFromTheDayBeforeIt(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	recentFrom := now.Add(-TrendRecent)
	seedTrendCycles(t, s, "mimo-v2.5", now.Add(-TrendRecent-TrendReference), recentFrom, 800, 70)
	seedTrendCycles(t, s, "mimo-v2.5", recentFrom, now, 1600, 45)

	tr, err := s.Trend(ctx, []string{"mimo-v2.5"}, now)
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if len(tr.Models) != 1 {
		t.Fatalf("models = %d, want 1", len(tr.Models))
	}
	m := tr.Models[0]

	if !m.TTFT.Recent.Sufficient || m.TTFT.Recent.P50 == nil || *m.TTFT.Recent.P50 != 1600 {
		t.Errorf("recent ttft p50 = %v (n=%d), want 1600", m.TTFT.Recent.P50, m.TTFT.Recent.N)
	}
	if !m.TTFT.Before.Sufficient || m.TTFT.Before.P50 == nil || *m.TTFT.Before.P50 != 800 {
		t.Errorf("before ttft p50 = %v (n=%d), want 800", m.TTFT.Before.P50, m.TTFT.Before.N)
	}
	if m.TPS.Recent.P50 == nil || *m.TPS.Recent.P50 != 45 {
		t.Errorf("recent tps p50 = %v, want 45", m.TPS.Recent.P50)
	}
	if m.TPS.Before.P50 == nil || *m.TPS.Before.P50 != 70 {
		t.Errorf("before tps p50 = %v, want 70", m.TPS.Before.P50)
	}

	// Both spans, drawn at half-hour buckets. The plot the banner shows is this
	// one and not the selected window's, so it has to cover the comparison.
	if len(m.TTFT.Points) < 40 {
		t.Errorf("points = %d, want the whole 27 h at half-hour buckets", len(m.TTFT.Points))
	}
	if tr.RecentSeconds != int64(TrendRecent/time.Second) ||
		tr.BeforeSeconds != int64(TrendReference/time.Second) {
		t.Errorf("spans = %d/%d s, want %v/%v", tr.RecentSeconds, tr.BeforeSeconds, TrendRecent, TrendReference)
	}
}

// The two spans must not share a cycle. Overlap would make them look more alike
// than they are, and at three hours against twenty-four it would be invisible
// in the numbers — so it is asserted rather than eyeballed.
func TestTrendSpansDoNotOverlap(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	recentFrom := now.Add(-TrendRecent)

	// A full reference day, and then ONE run exactly on the boundary instant.
	// That run must land on the recent side and nowhere else — `>= from AND
	// < to` on both queries is what makes it unambiguous, and an inclusive
	// upper bound would count it twice.
	seedTrendCycles(t, s, "mimo-v2.5", now.Add(-TrendRecent-TrendReference), recentFrom, 800, 70)
	if _, err := s.Save(context.Background(), Cycle{
		StartedAt: recentFrom, Net: okNet(),
		Infer: []probe.InferResult{okInfer("mimo-v2.5", 1600)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tr, err := s.Trend(context.Background(), []string{"mimo-v2.5"}, now)
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	m := tr.Models[0]
	if m.TTFT.Recent.N != 1 {
		t.Errorf("recent n = %d, want the 1 boundary run", m.TTFT.Recent.N)
	}
	if m.TTFT.Before.N != 288 {
		t.Errorf("before n = %d, want 288 — a day at the five-minute cadence, "+
			"without the boundary run counted a second time", m.TTFT.Before.N)
	}
}

// A span too thin to produce a median must say so, not publish one. Same rule
// the window figures follow, and for the same reason: a delta computed against
// a number that was never measured is worse than no delta.
func TestTrendSuppressesASpanBelowTheSampleThreshold(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	// Nineteen runs in the recent span, one short of the threshold, and a full
	// day behind it.
	seedTrendCycles(t, s, "mimo-v2.5", now.Add(-TrendRecent-TrendReference), now.Add(-TrendRecent), 800, 70)
	seedTrendCycles(t, s, "mimo-v2.5", now.Add(-95*time.Minute), now, 1600, 45)

	tr, err := s.Trend(context.Background(), []string{"mimo-v2.5"}, now)
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	m := tr.Models[0]
	if m.TTFT.Recent.N != 19 {
		t.Fatalf("recent n = %d, want 19", m.TTFT.Recent.N)
	}
	if m.TTFT.Recent.Sufficient {
		t.Error("19 runs must not be sufficient")
	}
	if m.TTFT.Recent.P50 != nil {
		t.Errorf("p50 = %v, must be nil so the client says so in words", *m.TTFT.Recent.P50)
	}
}

// Failed runs stay out of both medians and their censoring stays visible: the
// points carry the count, which is what lets the banner say a delta is
// understated rather than publishing the flattering figure.
func TestTrendExcludesFailuresAndKeepsCensoringVisible(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedTrendCycles(t, s, "mimo-v2.5", now.Add(-TrendRecent-TrendReference), now.Add(-TrendRecent), 800, 70)
	seedTrendCycles(t, s, "mimo-v2.5", now.Add(-TrendRecent), now.Add(-30*time.Minute), 1600, 45)

	// A run our own timeout ladder cut off, inside the recent span.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-20 * time.Minute), Net: okNet(),
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", OK: false, ErrorClass: probe.CensoringErrorClasses[0],
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tr, err := s.Trend(ctx, []string{"mimo-v2.5"}, now)
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	m := tr.Models[0]
	if m.TTFT.Recent.P50 == nil || *m.TTFT.Recent.P50 != 1600 {
		t.Errorf("p50 = %v, want 1600 — a cut-off run must not enter the median", m.TTFT.Recent.P50)
	}
	censored := 0
	for _, p := range m.TTFT.Points {
		censored += p.Censored
	}
	if censored != 1 {
		t.Errorf("censored across the points = %d, want 1 — the caveat has to reach the client", censored)
	}
}

// An empty model list must marshal as [] rather than null: the client maps over
// this block.
func TestTrendWithNoModelsIsAnEmptyList(t *testing.T) {
	s := New(openTestDB(t))
	tr, err := s.Trend(context.Background(), nil, time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if tr.Models == nil {
		t.Error("models must be an allocated empty slice, not nil")
	}
}
