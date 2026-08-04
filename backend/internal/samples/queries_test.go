package samples

import (
	"context"
	"testing"
	"time"

	"github.com/trick77/mimostats/internal/probe"
)

// seedCycles writes n cycles ending at `end`, one per minute, each carrying one
// successful infer sample for model with the given ttft.
func seedCycles(t *testing.T, s *Store, model string, end time.Time, n int, ttft float64) {
	t.Helper()
	for i := 0; i < n; i++ {
		at := end.Add(-time.Duration(n-i) * time.Minute)
		if _, err := s.Save(context.Background(), Cycle{
			StartedAt: at, Net: okNet(),
			Infer: []probe.InferResult{okInfer(model, ttft)},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
}

// The suppression rule the plan names: 19 samples returns insufficient_data,
// 20 returns a number. A P50 from a handful of samples is exactly the figure
// that gets screenshotted out of context.
func TestPercentileSuppressionBoundary(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	w, _ := LookupWindow("24h")

	t.Run("19 samples is insufficient", func(t *testing.T) {
		s := New(openTestDB(t))
		seedCycles(t, s, "mimo-v2.5", now, 19, 900)

		st, err := s.stats(context.Background(), "ttft_ms", "mimo-v2.5", probe.ProbeInfer, now.Add(-w.Duration))
		if err != nil {
			t.Fatalf("stats: %v", err)
		}
		if st.N != 19 {
			t.Fatalf("n = %d, want 19", st.N)
		}
		if st.Sufficient {
			t.Error("19 samples must not be sufficient")
		}
		if st.P50 != nil {
			t.Errorf("p50 = %v, must be nil so it renders as insufficient_data not 0ms", *st.P50)
		}
	})

	t.Run("20 samples is sufficient", func(t *testing.T) {
		s := New(openTestDB(t))
		seedCycles(t, s, "mimo-v2.5", now, 20, 900)

		st, err := s.stats(context.Background(), "ttft_ms", "mimo-v2.5", probe.ProbeInfer, now.Add(-w.Duration))
		if err != nil {
			t.Fatalf("stats: %v", err)
		}
		if !st.Sufficient {
			t.Fatalf("20 samples must be sufficient, n = %d", st.N)
		}
		if st.P50 == nil || *st.P50 != 900 {
			t.Errorf("p50 = %v, want 900", st.P50)
		}
	})
}

// The percentile-exclusion rule, end to end through the query layer: a window
// with one 240s timeout and nineteen 900ms successes must report P50 900ms and
// availability 95% — never a P50 dragged toward the timeout.
func TestFailedRunsAreExcludedFromPercentilesButCountedInAvailability(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedCycles(t, s, "mimo-v2.5", now, 19, 900)
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-30 * time.Second), Net: okNet(),
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeInfer,
			TotalMs: 240000, OK: false, ErrorClass: probe.ErrClassTimeout,
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	ms := sum.Models[0]

	if ms.TTFT.N != 19 {
		t.Errorf("percentile n = %d, want 19 (the timeout must not be in it)", ms.TTFT.N)
	}
	if ms.TTFT.P50 != nil && *ms.TTFT.P50 != 900 {
		t.Errorf("p50 = %v, want 900 — the timeout must not drag it", *ms.TTFT.P50)
	}
	if ms.Attempts != 20 || ms.Succeeded != 19 {
		t.Fatalf("attempts = %d, succeeded = %d, want 20 and 19", ms.Attempts, ms.Succeeded)
	}
	if ms.Available != 95 {
		t.Errorf("availability = %v%%, want 95%%", ms.Available)
	}
}

func TestPercentilesAreNearestRank(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// 18 samples at 100ms plus two outliers. Sorted, that is positions 1-18 at
	// 100, position 19 at 4000, position 20 at 5000.
	//
	// Nearest-rank puts p50 at ceil(0.50*20) = 10 -> 100, and p95 at
	// ceil(0.95*20) = 19 -> 4000. Note p95 is NOT the maximum: with 20 samples
	// the top 5% is one sample, and nearest-rank names the 19th rather than the
	// 20th. That is the correct definition, and asserting it here pins the
	// integer-ceiling arithmetic in the SQL, which truncates by default.
	seedCycles(t, s, "mimo-v2.5", now, 18, 100)
	for i, ttft := range []float64{4000, 5000} {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(20-i) * time.Second), Net: okNet(),
			Infer: []probe.InferResult{okInfer("mimo-v2.5", ttft)},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	st, err := s.stats(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, now.Add(-w.Duration))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.N != 20 {
		t.Fatalf("n = %d, want 20", st.N)
	}
	if st.P50 == nil || *st.P50 != 100 {
		t.Errorf("p50 = %v, want 100", deref(st.P50))
	}
	if st.P95 == nil || *st.P95 != 4000 {
		t.Errorf("p95 = %v, want 4000 — p95 must surface the tail", deref(st.P95))
	}
}

func deref(f *float64) any {
	if f == nil {
		return "nil"
	}
	return *f
}

// Windows are an allow-list because nothing caller-supplied may reach SQL, and
// because retention deletes at 3 months: offering a longer window would return
// an empty chart that looks like an outage.
func TestWindowAllowList(t *testing.T) {
	for _, key := range []string{"1h", "24h", "48h", "7d", "30d", "3mo"} {
		if _, ok := LookupWindow(key); !ok {
			t.Errorf("window %q must be allowed", key)
		}
	}
	for _, key := range []string{"6mo", "1y", "", "24H", "'; DROP TABLE cycles--"} {
		if _, ok := LookupWindow(key); ok {
			t.Errorf("window %q must be rejected", key)
		}
	}

	// No window may exceed retention.
	for _, w := range Windows {
		if w.Duration > 2160*time.Hour {
			t.Errorf("window %s (%v) is longer than the 3-month retention", w.Key, w.Duration)
		}
		if w.Bucket <= 0 {
			t.Errorf("window %s has no bucket size", w.Key)
		}
		// A caller must never be able to provoke an enormous point count.
		if points := w.Duration / w.Bucket; points > 1000 {
			t.Errorf("window %s yields %d points; the bucket is too small", w.Key, points)
		}
	}
}

// The series column is interpolated into SQL — it cannot be a bound parameter —
// so the allow-list is the boundary that keeps it from becoming an injection.
func TestSeriesColumnAllowList(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")

	for _, bad := range []string{
		"ttft_ms; DROP TABLE cycles--",
		"(SELECT error_detail FROM infer_probes)",
		"error_detail",
		"",
	} {
		if _, err := s.Series(context.Background(), bad, "mimo-v2.5", probe.ProbeInfer, w, time.Now()); err == nil {
			t.Errorf("column %q must be rejected", bad)
		}
	}
	if _, err := s.Series(context.Background(), "ttft_ms", "mimo-v2.5", probe.ProbeInfer, w, time.Now()); err != nil {
		t.Errorf("a valid column must be accepted: %v", err)
	}
}

func TestSeriesBucketsByWindow(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	seedCycles(t, s, "mimo-v2.5", now, 60, 900)

	w, _ := LookupWindow("1h") // 5-minute buckets
	pts, err := s.Series(context.Background(), "ttft_ms", "mimo-v2.5", probe.ProbeInfer, w, now)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(pts) == 0 {
		t.Fatal("no points")
	}
	// 60 one-minute cycles into 5-minute buckets: at most 13 buckets.
	if len(pts) > 13 {
		t.Errorf("%d buckets for a 1h window; the server-side bucket size is not being applied", len(pts))
	}
	// Buckets must be ordered and aligned to the bucket size.
	bucketSecs := int64(w.Bucket / time.Second)
	for i, p := range pts {
		if p.T%bucketSecs != 0 {
			t.Errorf("bucket %d at %d is not aligned to %ds", i, p.T, bucketSecs)
		}
		if i > 0 && p.T <= pts[i-1].T {
			t.Errorf("buckets are not ascending: %d then %d", pts[i-1].T, p.T)
		}
	}
}

// The two probes are never aggregated: the gap between their TTFTs IS the
// prefill signal, so a query for one must not see the other.
func TestProbeIsAlwaysAFilterNeverAnAggregation(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// One cycle carrying both a fast infer and a slow wide.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-time.Minute), Net: okNet(),
		Infer: []probe.InferResult{
			okInfer("mimo-v2.5", 900),
			{ModelID: "mimo-v2.5", Probe: probe.ProbeWide, TTFTMs: 3000, TotalMs: 6000, OK: true},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	inferStats, err := s.stats(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, now.Add(-w.Duration))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	wideStats, err := s.stats(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeWide, now.Add(-w.Duration))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if inferStats.N != 1 || wideStats.N != 1 {
		t.Errorf("infer n = %d, wide n = %d; each must see only its own probe", inferStats.N, wideStats.N)
	}
}

func TestRecentSamplesClampsTheLimit(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	seedCycles(t, s, "mimo-v2.5", now, 30, 900)

	rows, err := s.RecentSamples(context.Background(), "mimo-v2.5", probe.ProbeInfer, 999999)
	if err != nil {
		t.Fatalf("RecentSamples: %v", err)
	}
	if len(rows) > MaxSampleLimit {
		t.Errorf("returned %d rows, above the %d clamp", len(rows), MaxSampleLimit)
	}
	// Newest first, so a dashboard tail is actually the tail.
	for i := 1; i < len(rows); i++ {
		if rows[i].At.After(rows[i-1].At) {
			t.Errorf("samples are not newest-first at index %d", i)
		}
	}
}

// error_detail is operator-only: a provider error body can echo request
// fragments. The public Sample type must have no way to carry it.
func TestSampleTypeCannotCarryErrorDetail(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	if _, err := s.Save(context.Background(), Cycle{
		StartedAt: now.Add(-time.Minute), Net: okNet(),
		Infer: []probe.InferResult{{
			ModelID: "mimo-v2.5", Probe: probe.ProbeInfer, TotalMs: 100,
			OK: false, ErrorClass: probe.ErrClassHTTP,
			ErrorDetail: "SECRET-PROVIDER-BODY-tp-key-fragment",
		}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rows, err := s.RecentSamples(context.Background(), "mimo-v2.5", probe.ProbeInfer, 10)
	if err != nil {
		t.Fatalf("RecentSamples: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].ErrorClass == nil || *rows[0].ErrorClass != probe.ErrClassHTTP {
		t.Errorf("error_class must be served, got %v", rows[0].ErrorClass)
	}
	// The detail is in the database (operators need it) and nowhere in the
	// public struct.
	var stored string
	if err := s.db.QueryRow(`SELECT error_detail FROM infer_probes`).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored == "" {
		t.Error("error_detail must be recorded for operators")
	}
}

func TestSummarizeReportsFaultsAndSkips(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// One healthy cycle, one where MiMo's edge was unreachable.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-2 * time.Minute), Net: okNet(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: false},
			{Target: probe.TargetRefSGP, OK: true},
			{Target: probe.TargetRefEU, OK: true},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.RecordSkip(ctx, now.Add(-90*time.Second), "mimo-v2.5-pro", probe.ProbeInfer); err != nil {
		t.Fatalf("RecordSkip: %v", err)
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	if sum.Cycles != 2 {
		t.Errorf("cycles = %d, want 2", sum.Cycles)
	}
	if sum.Faults[probe.FaultOK] != 1 || sum.Faults[probe.FaultEdge] != 1 {
		t.Errorf("faults = %v, want one ok and one edge", sum.Faults)
	}
	// Surfaced rather than swallowed: silent skipping makes availability lie by
	// omission.
	if sum.Skipped != 1 {
		t.Errorf("skipped_runs = %d, want 1", sum.Skipped)
	}
	// The network layer is summarised per target, so the attribution is
	// readable rather than inferred.
	if len(sum.Net) != 3 {
		t.Fatalf("net summaries = %d, want 3", len(sum.Net))
	}
}

// Correctness is suppressed on thin data for the same reason percentiles are:
// one wrong answer out of three is not a 67% correctness rate.
func TestCorrectnessIsSuppressedOnThinData(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	seedCycles(t, s, "mimo-v2.5", now, 5, 900)

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(context.Background(), w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Models[0].CorrectPct != nil {
		t.Errorf("correct_pct = %v with only 5 answers; must be suppressed", *sum.Models[0].CorrectPct)
	}
}

// Samples outside the window must not leak in — that is the whole meaning of a
// window, and a boundary bug would silently mix a quiet night into a busy hour.
func TestWindowBoundsAreRespected(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// One sample inside a 1h window, one well outside it.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-30 * time.Minute), Net: okNet(),
		Infer: []probe.InferResult{okInfer("mimo-v2.5", 900)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-5 * time.Hour), Net: okNet(),
		Infer: []probe.InferResult{okInfer("mimo-v2.5", 5000)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("1h")
	st, err := s.stats(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, now.Add(-w.Duration))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.N != 1 {
		t.Errorf("n = %d, want 1; the older sample must be outside the 1h window", st.N)
	}
}
