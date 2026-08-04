package samples

import (
	"context"
	"encoding/json"
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
	for _, key := range []string{"24h", "48h", "7d", "30d", "3mo"} {
		if _, ok := LookupWindow(key); !ok {
			t.Errorf("window %q must be allowed", key)
		}
	}
	// 1h is in the rejected set on purpose: at a 5-minute cadence it cannot hold
	// MinSamplesForPercentile samples, so it could never answer. A stale link
	// carrying it must fall back rather than render a permanently empty card.
	for _, key := range []string{"1h", "6mo", "1y", "", "24H", "'; DROP TABLE cycles--"} {
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

	w, _ := LookupWindow("24h") // 15-minute buckets
	pts, err := s.Series(context.Background(), "ttft_ms", "mimo-v2.5", probe.ProbeInfer, w, now)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(pts) == 0 {
		t.Fatal("no points")
	}
	// 60 one-minute cycles into 15-minute buckets: at most 5 buckets.
	if len(pts) > 5 {
		t.Errorf("%d buckets for a 24h window; the server-side bucket size is not being applied", len(pts))
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
	if len(sum.Net) != 2 {
		t.Fatalf("net summaries = %d, want 2", len(sum.Net))
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

	// One sample inside the window, one well outside it.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-30 * time.Minute), Net: okNet(),
		Infer: []probe.InferResult{okInfer("mimo-v2.5", 900)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-30 * time.Hour), Net: okNet(),
		Infer: []probe.InferResult{okInfer("mimo-v2.5", 5000)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	st, err := s.stats(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, now.Add(-w.Duration))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.N != 1 {
		t.Errorf("n = %d, want 1; the older sample must be outside the window", st.N)
	}
}

// deadNet is a cycle where nothing in Singapore answered: MiMo and the
// reference both failed, which AttributeFault resolves to 'uplink'.
func deadNet() []probe.NetResult {
	return []probe.NetResult{
		{Target: probe.TargetMimoSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
		{Target: probe.TargetRefSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
	}
}

// The promise /api/methodology and the availability strip have both always
// made, finally enforced: a cycle nobody could attribute must not appear in
// MiMo's availability.
//
// When our own connectivity dies, the inference probe fails too — on connect,
// before it ever reaches MiMo. Counting that as a failed attempt manufactures
// provider downtime out of our own outage, which is the single failure this
// project exists not to commit.
func TestUplinkCyclesAreExcludedFromModelAvailability(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// 10 good cycles, then 10 where nothing was reachable.
	seedCycles(t, s, "mimo-v2.5", now, 10, 900)
	for i := 0; i < 10; i++ {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(i+1) * time.Hour),
			Net:       deadNet(),
			Infer: []probe.InferResult{{
				ModelID: "mimo-v2.5", Probe: probe.ProbeInfer,
				OK: false, ErrorClass: probe.ErrClassConnectTimeout,
			}},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	ms := sum.Models[0]

	if ms.Attempts != 10 || ms.Succeeded != 10 {
		t.Fatalf("attempts = %d, succeeded = %d, want 10 and 10 — the unreachable cycles must not be counted",
			ms.Attempts, ms.Succeeded)
	}
	if ms.Available != 100 {
		t.Errorf("availability = %v%%, want 100%% — MiMo answered every cycle we could actually reach it", ms.Available)
	}
	// The cycles are still VISIBLE, just not charged to MiMo: the strip renders
	// them, and hiding them would be its own dishonesty.
	if sum.Faults[probe.FaultUplink] != 10 {
		t.Errorf("uplink faults = %d, want 10 — excluded from availability, not from the record",
			sum.Faults[probe.FaultUplink])
	}
}

// A window with nothing attributable in it must report no data rather than a
// number. 0 attempts is what every client already reads as "no data"; a 0%
// would read as a total provider outage, which is the opposite of the truth.
func TestAWindowOfNothingButUplinkReportsNoAttempts(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(i+1) * time.Minute),
			Net:       deadNet(),
			Infer: []probe.InferResult{{
				ModelID: "mimo-v2.5", Probe: probe.ProbeInfer,
				OK: false, ErrorClass: probe.ErrClassConnectTimeout,
			}},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got := sum.Models[0].Attempts; got != 0 {
		t.Errorf("attempts = %d, want 0", got)
	}
	if got := sum.Models[0].Available; got != 0 {
		t.Errorf("available_pct = %v; with no attempts the field must stay at its zero value and be read via attempts", got)
	}
}

// The exclusion is asymmetric on purpose. MiMo's edge is a provider figure and
// gets the protection; the reference host is the instrument, and its raw
// reachability is a diagnostic about our own deployment that must not be
// flattered.
func TestUplinkExclusionAppliesToMimoButNotTheReference(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedCycles(t, s, "mimo-v2.5", now, 10, 900)
	for i := 0; i < 10; i++ {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(i+1) * time.Hour),
			Net:       deadNet(),
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	byTarget := map[string]NetSummary{}
	for _, n := range sum.Net {
		byTarget[n.Target] = n
	}
	if got := byTarget[probe.TargetMimoSGP]; got.Attempts != 10 || got.Available != 100 {
		t.Errorf("mimo: attempts = %d, available = %v%%; want 10 and 100%% — unattributable cycles are not MiMo's",
			got.Attempts, got.Available)
	}
	if got := byTarget[probe.TargetRefSGP]; got.Attempts != 20 || got.Available != 50 {
		t.Errorf("reference: attempts = %d, available = %v%%; want 20 and 50%% — the instrument reports its own raw reachability",
			got.Attempts, got.Available)
	}
}

// A run that SUCCEEDED on an unattributable cycle is still MiMo answering.
//
// The fault is attributed from TCP handshakes taken at the top of the cycle, so
// a handshake that timed out while the completion went through is entirely
// possible. Dropping that row would not just under-count attempts: it would
// take the answer grade and the reasoning-token gate with it, and the gate is
// the one figure that invalidates every latency number in the window.
func TestSuccessfulRunsOnUplinkCyclesStillCount(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedCycles(t, s, "mimo-v2.5", now, 10, 900)

	in := okInfer("mimo-v2.5", 950)
	in.Usage.CompletionTokenDetails.ReasoningTokens = 512
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-30 * time.Minute),
		Net:       deadNet(),
		Infer:     []probe.InferResult{in},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	ms := sum.Models[0]

	if ms.Attempts != 11 || ms.Succeeded != 11 {
		t.Errorf("attempts = %d, succeeded = %d, want 11 and 11 — a run that succeeded is evidence MiMo answered",
			ms.Attempts, ms.Succeeded)
	}
	if ms.MaxReasoningTokens != 512 {
		t.Errorf("max_reasoning_tokens = %d, want 512 — the hard gate must not be filtered away by fault attribution",
			ms.MaxReasoningTokens)
	}
	if ms.Answered != 11 {
		t.Errorf("answered = %d, want 11 — a graded answer is not unattributable", ms.Answered)
	}
	// The invariant the exclusion must never break: a sample cannot be in the
	// percentiles without being in the denominator it is drawn from.
	if ms.TTFT.N > ms.Attempts {
		t.Errorf("ttft.n = %d > attempts = %d — latency counted from a run whose attempt was not",
			ms.TTFT.N, ms.Attempts)
	}
}

// 'route' is the historical half of the same verdict and must be excluded on
// the same terms.
//
// It was produced while a European reference host still separated "our uplink
// is down" from "the route to Singapore is degraded". In BOTH, MiMo and the
// Singapore reference were unreachable, so the inference probe failed on
// connect. Stored cycles carry it, and excluding only 'uplink' would leave the
// manufactured downtime in every window reaching back that far.
func TestRouteCyclesAreExcludedLikeUplink(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedCycles(t, s, "mimo-v2.5", now, 10, 900)
	for i := 0; i < 10; i++ {
		id, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(i+1) * time.Hour),
			Net:       deadNet(),
			Infer: []probe.InferResult{{
				ModelID: "mimo-v2.5", Probe: probe.ProbeInfer,
				OK: false, ErrorClass: probe.ErrClassConnectTimeout,
			}},
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		// Nothing produces 'route' any more, so it is written directly — the
		// point is precisely that historical rows must still read correctly.
		if _, err := s.db.ExecContext(ctx,
			`UPDATE cycle_fault SET fault = ? WHERE cycle_id = ?`, probe.FaultRoute, id); err != nil {
			t.Fatalf("update fault: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if ms := sum.Models[0]; ms.Attempts != 10 || ms.Available != 100 {
		t.Errorf("attempts = %d, available = %v%%, want 10 and 100%% — 'route' is as unattributable as 'uplink'",
			ms.Attempts, ms.Available)
	}
}

// failedInfer is a run that reached MiMo and was cut off by our own ladder.
func failedInfer(model, class string, totalMs float64) probe.InferResult {
	return probe.InferResult{
		ModelID: model, Probe: probe.ProbeInfer,
		TotalMs: totalMs, OK: false, ErrorClass: class,
	}
}

// Excluding failed runs from the percentiles is right, and it truncates the
// distribution: the runs removed are the SLOWEST ones, so the published P95 is
// a percentile of the survivors and it improves as the endpoint gets worse.
// Nothing in the figures themselves can show that, so the count has to.
func TestCensoredRunsAreCountedSoTheTruncatedTailIsVisible(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedCycles(t, s, "mimo-v2.5", now, 19, 900)
	// One of each censoring class, plus a refusal that is NOT one.
	for i, class := range []string{
		probe.ErrClassHeaderTimeout,
		probe.ErrClassTTFTTimeout,
		probe.ErrClassStalled,
		probe.ErrClassTimeout,
		probe.ErrClassRefused,
	} {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(i+1) * time.Second), Net: okNet(),
			Infer: []probe.InferResult{failedInfer("mimo-v2.5", class, 60000)},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	ms := sum.Models[0]

	// Four cut off by the ladder. connection_refused measured no latency at all,
	// so there is no tail it could have truncated — it belongs to availability.
	if ms.Censored != 4 {
		t.Errorf("censored = %d, want 4 (the ladder classes only, not connection_refused)", ms.Censored)
	}
	if ms.Attempts != 24 {
		t.Errorf("attempts = %d, want 24 — censoring shares the attempt denominator", ms.Attempts)
	}
	// And it changes nothing about the percentiles themselves.
	if ms.TTFT.N != 19 {
		t.Errorf("percentile n = %d, want 19; censored runs stay out of the percentiles", ms.TTFT.N)
	}
}

// A run cut off during OUR OWN uplink outage is not MiMo's slow tail. The
// censored count shares the attempt denominator, so it must share its exclusion.
func TestCensoredCountExcludesUnattributableCycles(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Nothing in Singapore answered, and the inference run timed out with it.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
			{Target: probe.TargetRefSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
		},
		Infer: []probe.InferResult{failedInfer("mimo-v2.5", probe.ErrClassTimeout, 240000)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if ms := sum.Models[0]; ms.Censored != 0 {
		t.Errorf("censored = %d on an uplink cycle, want 0 — that is our outage, not their tail", ms.Censored)
	}
}

// A bucket where every run was cut off has no successful sample to group. On an
// inner reading it would not exist, and a missing bucket renders as a gap —
// identical to a stretch where the probe was not running at all. Total
// truncation is the one case a reader most needs to be told about.
func TestABucketOfNothingButCensoredRunsStillExists(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Well inside one 15-minute bucket, and nothing else in the window.
	for i := 0; i < 3; i++ {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(-time.Duration(i+1) * time.Minute), Net: okNet(),
			Infer: []probe.InferResult{failedInfer("mimo-v2.5", probe.ErrClassTTFTTimeout, 150000)},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	w, _ := LookupWindow("24h")
	pts, err := s.Series(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, w, now)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1: an all-censored bucket must not vanish", len(pts))
	}
	p := pts[0]
	if p.Censored != 3 {
		t.Errorf("censored = %d, want 3", p.Censored)
	}
	if p.N != 0 {
		t.Errorf("n = %d, want 0 — no run finished in this bucket", p.N)
	}
	if p.P50 != nil {
		t.Errorf("p50 = %v, want nil: a censored bucket has no value, and a zero would draw a floor", *p.P50)
	}
}

// The commoner case: some runs finished and some were cut off. The line is
// drawn from the survivors, which is exactly why the bucket has to say so.
func TestAPartlyCensoredBucketCarriesBothItsValueAndItsCount(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	seedCycles(t, s, "mimo-v2.5", now, 3, 900) // 3 successes, one bucket
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-30 * time.Second), Net: okNet(),
		Infer: []probe.InferResult{failedInfer("mimo-v2.5", probe.ErrClassStalled, 45000)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	pts, err := s.Series(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, w, now)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.N != 3 || p.P50 == nil || *p.P50 != 900 {
		t.Errorf("n = %d, p50 = %v, want 3 and 900", p.N, p.P50)
	}
	if p.Censored != 1 {
		t.Errorf("censored = %d, want 1", p.Censored)
	}
}

// The chart band and the card's count are two publications of ONE claim, so
// they must exclude the same cycles. The card already refuses to count a run
// cut off during our own uplink outage; a chart that bands it anyway, over a
// caption saying probes here were cut off, publishes our outage as MiMo's.
func TestTheChartBandAndTheSummaryCountAgreeOnUnattributableCycles(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Nothing in Singapore answered, and the inference run timed out with it.
	// The fault is attributed from TCP handshakes at the top of the cycle while
	// the HTTP request gets further, so this row is reachable, not contrived.
	if _, err := s.Save(ctx, Cycle{
		StartedAt: now.Add(-time.Minute),
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
			{Target: probe.TargetRefSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
		},
		Infer: []probe.InferResult{failedInfer("mimo-v2.5", probe.ErrClassTimeout, 240000)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(ctx, w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	pts, err := s.Series(ctx, "ttft_ms", "mimo-v2.5", probe.ProbeInfer, w, now)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}

	inCharts := 0
	for _, p := range pts {
		inCharts += p.Censored
	}
	if got := sum.Models[0].Censored; got != 0 || inCharts != 0 {
		t.Errorf("censored: summary %d, series %d — both must be 0 on an uplink cycle, and above all EQUAL",
			got, inCharts)
	}
}

func TestRecentPulseMatchesRecentSamplesOrderAndClamp(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	seedCycles(t, s, "mimo-v2.5", now, 30, 900)

	rows, err := s.RecentPulse(context.Background(), "mimo-v2.5", probe.ProbeInfer, 999999)
	if err != nil {
		t.Fatalf("RecentPulse: %v", err)
	}
	// Exact, not `> MaxSampleLimit`: only 30 cycles exist, so that comparison
	// can never fail and would pass a query returning nothing at all. What the
	// absurd limit has to prove is that it neither errors nor over-returns.
	if len(rows) != 30 {
		t.Errorf("returned %d rows, want the 30 that exist (clamp is %d)", len(rows), MaxSampleLimit)
	}
	// Newest first, like RecentSamples: the strip reverses on the client, and
	// the two endpoints disagreeing about direction would draw the day backwards.
	for i := 1; i < len(rows); i++ {
		if rows[i].At.After(rows[i-1].At) {
			t.Errorf("pulse rows are not newest-first at index %d", i)
		}
	}

	full, err := s.RecentSamples(context.Background(), "mimo-v2.5", probe.ProbeInfer, 999999)
	if err != nil {
		t.Fatalf("RecentSamples: %v", err)
	}
	if len(rows) != len(full) {
		t.Fatalf("pulse returned %d rows, samples %d; the projection must not drop cycles", len(rows), len(full))
	}
	for i := range rows {
		if !rows[i].At.Equal(full[i].At) || rows[i].OK != full[i].OK {
			t.Errorf("row %d disagrees with the same cycle from RecentSamples", i)
		}
	}
}

// The whole reason /api/pulse exists: a day of cycles reaches the page without
// the page being handed a day of measurements. If Pulse ever grows a latency
// column beyond ttft_ms, the narrowing has silently stopped happening.
func TestPulseTypeCarriesOnlyWhatTheStripDraws(t *testing.T) {
	buf, err := json.Marshal(Pulse{At: time.Now()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(buf, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]bool{
		"at": true, "ttft_ms": true, "ok": true, "answer_ok": true, "error_class": true,
	}
	for k := range fields {
		if !want[k] {
			t.Errorf("Pulse serialises %q, which the strip does not draw", k)
		}
	}
	for k := range want {
		if _, ok := fields[k]; !ok {
			t.Errorf("Pulse is missing %q, which the strip needs", k)
		}
	}
}

// edgeNet is a cycle where MiMo's edge did not answer but an unrelated
// Singapore host did — AttributeFault resolves that to 'edge'.
func edgeNet() []probe.NetResult {
	return []probe.NetResult{
		{Target: probe.TargetMimoSGP, OK: false, ErrorClass: probe.ErrClassConnectTimeout},
		{Target: probe.TargetRefSGP, DNSMs: 4, ConnectMs: 265, OK: true},
	}
}

// Newest first and capped, like every other "recent" query on this store. The
// verdict walks the head of this slice looking for a run of failures, so an
// oldest-first slice would score the wrong end of the day.
func TestRecentCyclesAreNewestFirstAndCapped(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	seedCycles(t, s, "mimo-v2.5", now, RecentCycleCount+14, 900)

	got, err := s.RecentCycles(context.Background(), probe.ProbeInfer)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != RecentCycleCount {
		t.Fatalf("cycles = %d, want %d", len(got), RecentCycleCount)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].At.Before(got[i-1].At) {
			t.Fatalf("cycle %d at %s is not older than %s", i, got[i].At, got[i-1].At)
		}
	}
}

// The block carries the stored attribution, because edge-vs-uplink cannot be
// reconstructed from an inference row: it is decided from the TCP handshakes.
func TestRecentCyclesCarryTheStoredFault(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	for i, net := range [][]probe.NetResult{okNet(), edgeNet(), deadNet()} {
		if _, err := s.Save(ctx, Cycle{
			StartedAt: now.Add(time.Duration(i) * time.Minute), Net: net,
			Infer: []probe.InferResult{okInfer("mimo-v2.5", 900)},
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := s.RecentCycles(ctx, probe.ProbeInfer)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	want := []string{probe.FaultUplink, probe.FaultEdge, probe.FaultOK}
	if len(got) != len(want) {
		t.Fatalf("cycles = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Fault != w {
			t.Errorf("cycle %d fault = %q, want %q", i, got[i].Fault, w)
		}
	}
}

// The whole point of the block: it is not bounded by a window, so a banner
// built on it cannot be pinned red by a fault that has aged into history but
// not yet out of the selected range.
func TestRecentCyclesIgnoreEveryWindow(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	// Older than the longest window this dashboard offers.
	seedCycles(t, s, "mimo-v2.5", now.Add(-100*24*time.Hour), 3, 900)

	got, err := s.RecentCycles(context.Background(), probe.ProbeInfer)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("cycles = %d, want 3 — there is no `since` to pass, and that is the point", len(got))
	}
}

// Per model, because a verdict about mimo-v2.5 must not be built from
// mimo-v2.5-pro's failures.
func TestRecentCyclesCarryEachModelsOutcome(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if _, err := s.Save(ctx, Cycle{
		StartedAt: now, Net: okNet(),
		Infer: []probe.InferResult{
			okInfer("mimo-v2.5", 900),
			failedInfer("mimo-v2.5-pro", probe.ErrClassTTFTTimeout, 30000),
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.RecentCycles(ctx, probe.ProbeInfer)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("cycles = %d, want 1", len(got))
	}
	if run, ok := got[0].Models["mimo-v2.5"]; !ok || !run.OK {
		t.Errorf("mimo-v2.5 = %+v, want a successful run", run)
	}
	if run, ok := got[0].Models["mimo-v2.5-pro"]; !ok || run.OK {
		t.Errorf("mimo-v2.5-pro = %+v, want a failed run", run)
	}
	if run := got[0].Models["mimo-v2.5-pro"]; run.AnswerOK != nil {
		t.Errorf("answer_ok = %v, want nil — a run that failed answered nothing", *run.AnswerOK)
	}
}

// A cycle whose inference runs were all skipped still happened, and the network
// layer still attributed it. Dropping it would turn a gap into a clean stretch,
// which is exactly the direction a monitor must never round.
func TestRecentCyclesKeepACycleWithNoRuns(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if _, err := s.Save(ctx, Cycle{StartedAt: now, Net: edgeNet()}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.RecentCycles(ctx, probe.ProbeInfer)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("cycles = %d, want 1 — the cycle ran, it just recorded no inference", len(got))
	}
	if got[0].Fault != probe.FaultEdge {
		t.Errorf("fault = %q, want %q", got[0].Fault, probe.FaultEdge)
	}
	if len(got[0].Models) != 0 {
		t.Errorf("models = %v, want empty", got[0].Models)
	}
}

// Summarize carries the block, so the client gets it on requests it already
// makes rather than on a seventh one.
func TestSummarizeCarriesTheRecentBlock(t *testing.T) {
	s := New(openTestDB(t))
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	seedCycles(t, s, "mimo-v2.5", now, 4, 900)

	w, _ := LookupWindow("24h")
	sum, err := s.Summarize(context.Background(), w, []string{"mimo-v2.5"}, probe.ProbeInfer, now)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(sum.Recent) != 4 {
		t.Fatalf("recent = %d, want 4", len(sum.Recent))
	}
}
