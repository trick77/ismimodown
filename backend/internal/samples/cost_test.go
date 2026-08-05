package samples

import (
	"context"
	"testing"
	"time"

	"github.com/trick77/mimostats/internal/config"
	"github.com/trick77/mimostats/internal/probe"
)

// testPrices are round numbers so every expected figure below can be worked out
// on paper: $1 per million in, $10 per million out, $0.10 per million cached.
var testPrices = map[string]config.ModelPrice{
	"mimo-v2.5": {In: 1, Out: 10, Cached: 0.10},
}

// costRun builds one usage-carrying run. Token counts are the knobs; everything
// else is a healthy run.
func costRun(model, kind string, prompt, cached, output int) probe.InferResult {
	yes := true
	return probe.InferResult{
		ModelID: model, Probe: kind,
		TTFTMs: 900, TTFATMs: 900, TotalMs: 1700,
		ITLP50Ms: 24, ITLP95Ms: 30, OutputTPS: 41,
		Usage: probe.TokenUsage{
			PromptTokens:     prompt,
			CompletionTokens: output,
			PromptTokensDetails: probe.PromptTokenDetails{
				CachedTokens: cached,
			},
		},
		QuestionID: "capital-france", OK: true, AnswerOK: &yes,
	}
}

func saveAt(t *testing.T, s *Store, at time.Time, runs ...probe.InferResult) {
	t.Helper()
	if _, err := s.Save(context.Background(), Cycle{
		StartedAt: at, Net: okNet(), Infer: runs,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func usd(t *testing.T, p *float64) float64 {
	t.Helper()
	if p == nil {
		t.Fatal("expected a priced figure, got null")
	}
	return *p
}

// near compares money. Cents are not representable in binary floating point, so
// an exact comparison here would fail on arithmetic that is correct.
func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

// The whole pricing rule in one case: cached tokens are a SUBSET of the prompt
// and get their own rate, and the off-peak coefficient multiplies the result.
func TestCostPricesTheUncachedRemainder(t *testing.T) {
	s := New(openTestDB(t))
	// 10:00 UTC — outside the 16:00-24:00 window, so full rate.
	at := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)
	w, _ := LookupWindow("24h")

	// 1000 prompt of which 400 cached, 200 output:
	//   600 uncached @ $1/M   = 0.0006
	//   400 cached   @ $0.10/M = 0.00004
	//   200 output   @ $10/M  = 0.002
	//                          = 0.00264
	saveAt(t, s, at, costRun("mimo-v2.5", probe.ProbeInfer, 1000, 400, 200))

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if !got.Priced {
		t.Fatal("Priced = false with a price table configured")
	}
	if want := 0.00264; !near(usd(t, got.Total.USD), want) {
		t.Errorf("USD = %v, want %v", *got.Total.USD, want)
	}
	// Full rate, so billed and list agree.
	if !near(usd(t, got.Total.USD), usd(t, got.Total.ListUSD)) {
		t.Errorf("full-rate run billed %v against list %v", *got.Total.USD, *got.Total.ListUSD)
	}
	if got.Total.Tokens.Prompt != 1000 || got.Total.Tokens.Cached != 400 || got.Total.Tokens.Output != 200 {
		t.Errorf("tokens = %+v", got.Total.Tokens)
	}
	// Prompt already contains Cached; counting the subset again would inflate
	// every token figure on the page.
	if got.Total.Tokens.Total() != 1200 {
		t.Errorf("Total() = %d, want 1200 — cached must not be added twice", got.Total.Tokens.Total())
	}
}

func TestCostAppliesTheCoefficientOnlyOffPeak(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")
	now := time.Date(2026, 8, 4, 23, 0, 0, 0, time.UTC)

	// 10:00 UTC is full rate, 20:00 UTC is inside 16:00-24:00.
	saveAt(t, s, time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC),
		costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0))
	saveAt(t, s, time.Date(2026, 8, 4, 20, 0, 0, 0, time.UTC),
		costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0))

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}

	byPhase := map[string]CostGroup{}
	for _, p := range got.Phases {
		byPhase[p.Phase] = p.CostGroup
	}
	if len(byPhase) != 2 {
		t.Fatalf("phases = %+v, want both", got.Phases)
	}
	// 1000 prompt tokens at $1/M is $0.001 at list.
	if want := 0.001; !near(usd(t, byPhase[PhaseFull].USD), want) {
		t.Errorf("full phase = %v, want %v", *byPhase[PhaseFull].USD, want)
	}
	if want := 0.0008; !near(usd(t, byPhase[PhaseOffPeak].USD), want) {
		t.Errorf("off-peak phase = %v, want %v", *byPhase[PhaseOffPeak].USD, want)
	}
	// List is what the off-peak run WOULD have cost, which is what makes the
	// saving quotable.
	if want := 0.001; !near(usd(t, byPhase[PhaseOffPeak].ListUSD), want) {
		t.Errorf("off-peak list = %v, want %v", *byPhase[PhaseOffPeak].ListUSD, want)
	}
	if want := 0.0018; !near(usd(t, got.Total.USD), want) {
		t.Errorf("total = %v, want %v", *got.Total.USD, want)
	}
}

// A run whose phase differs from its bucket's must be billed on its own
// timestamp. With a 15-minute bucket that cannot happen at the boundary today,
// but the rule is what stops a wider bucket from mispricing half a window.
func TestCostDecidesThePhasePerRunNotPerBucket(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("30d") // 6-hour buckets, so 14:00 and 20:00 share one
	now := time.Date(2026, 8, 4, 23, 0, 0, 0, time.UTC)

	saveAt(t, s, time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC),
		costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0))
	saveAt(t, s, time.Date(2026, 8, 4, 17, 0, 0, 0, time.UTC),
		costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0))

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if len(got.Phases) != 2 {
		t.Fatalf("phases = %+v; a bucket spanning the boundary must still split", got.Phases)
	}
	if want := 0.0018; !near(usd(t, got.Total.USD), want) {
		t.Errorf("total = %v, want %v", *got.Total.USD, want)
	}
}

func TestCostSplitsByProbe(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")
	at := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)

	saveAt(t, s, at,
		costRun("mimo-v2.5", probe.ProbeInfer, 100, 0, 0),
		costRun("mimo-v2.5", probe.ProbeWide, 10000, 0, 0))

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	byProbe := map[string]CostGroup{}
	for _, p := range got.Probes {
		byProbe[p.Probe] = p.CostGroup
	}
	if want := 0.0001; !near(usd(t, byProbe[probe.ProbeInfer].USD), want) {
		t.Errorf("infer = %v, want %v", *byProbe[probe.ProbeInfer].USD, want)
	}
	if want := 0.01; !near(usd(t, byProbe[probe.ProbeWide].USD), want) {
		t.Errorf("wide = %v, want %v", *byProbe[probe.ProbeWide].USD, want)
	}
}

// A failed run reports no usage — the usage chunk arrives last — but it was
// still billed. It must be counted and named, never folded in as free.
func TestCostCountsRunsItCannotPrice(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")
	at := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)

	failed := costRun("mimo-v2.5", probe.ProbeInfer, 0, 0, 0)
	failed.OK = false
	failed.AnswerOK = nil
	failed.ErrorClass = "ttft_timeout"
	saveAt(t, s, at, costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0), failed)

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if got.Unpriced != 1 {
		t.Errorf("Unpriced = %d, want 1", got.Unpriced)
	}
	if got.Total.Runs != 1 {
		t.Errorf("Runs = %d, want 1 — an unpriced run must not join the priced total", got.Total.Runs)
	}
	if want := 0.001; !near(usd(t, got.Total.USD), want) {
		t.Errorf("total = %v, want %v", *got.Total.USD, want)
	}
}

// No price table is a supported state, not an error: tokens are served and every
// money field is null, so the client shows counts rather than a $0.00 bill.
func TestCostWithoutPricesServesTokensAndNoMoney(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")
	at := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)
	saveAt(t, s, at, costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 200))

	got, err := s.Cost(context.Background(), w, nil, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if got.Priced {
		t.Error("Priced = true with no price table")
	}
	if got.Total.USD != nil || got.Total.ListUSD != nil {
		t.Errorf("money served without prices: %+v", got.Total)
	}
	if got.Total.Tokens.Prompt != 1000 || got.Total.Tokens.Output != 200 {
		t.Errorf("tokens = %+v, want them served regardless", got.Total.Tokens)
	}
	if len(got.Series) != 1 || got.Series[0].USD != nil || got.Series[0].Runs != 1 {
		t.Errorf("series = %+v", got.Series)
	}
	if got.Prices != nil {
		t.Error("an empty price table must not be published")
	}
}

// One model without a price makes the totals it belongs to unpriced, rather than
// publishing the other model's cost as if it were the whole bill.
func TestCostRefusesToTotalAPartiallyPricedWindow(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")
	at := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)

	saveAt(t, s, at,
		costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0),
		costRun("mimo-v2.5-pro", probe.ProbeInfer, 1000, 0, 0))

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if got.Priced {
		t.Error("Priced = true while one probed model has no price")
	}
	if got.Total.USD != nil {
		t.Errorf("a partially priced window published a total: %v", *got.Total.USD)
	}
	if got.Total.Runs != 2 {
		t.Errorf("Runs = %d, want 2 — the runs still happened", got.Total.Runs)
	}
}

func TestCostSeriesIsBucketedAndOrdered(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h") // 15-minute buckets
	base := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := base.Add(3 * time.Hour)

	for i := 0; i < 6; i++ {
		saveAt(t, s, base.Add(time.Duration(i)*20*time.Minute),
			costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 0))
	}

	got, err := s.Cost(context.Background(), w, testPrices, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if got.BucketSeconds != 900 {
		t.Errorf("BucketSeconds = %d, want 900", got.BucketSeconds)
	}
	if len(got.Series) == 0 {
		t.Fatal("no series points")
	}
	var sum float64
	for i, p := range got.Series {
		if p.T%got.BucketSeconds != 0 {
			t.Errorf("point %d at %d is not aligned to the bucket", i, p.T)
		}
		if i > 0 && p.T <= got.Series[i-1].T {
			t.Errorf("point %d is not after its predecessor", i)
		}
		sum += usd(t, p.USD)
	}
	// The series must account for the same money as the total, or the chart and
	// the figure above it are describing different windows.
	if !near(sum, usd(t, got.Total.USD)) {
		t.Errorf("series sums to %v, total is %v", sum, *got.Total.USD)
	}
}

func TestOffPeakSpansClipToTheWindow(t *testing.T) {
	// 12:00 to 20:00 UTC: the window opens at 16:00 and the span must stop at
	// the right edge rather than running to midnight.
	from := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2026, 8, 4, 20, 0, 0, 0, time.UTC).Unix()

	spans := offPeakSpans(from, to)
	if len(spans) != 1 {
		t.Fatalf("spans = %v, want one", spans)
	}
	wantFrom := time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC).Unix()
	if spans[0][0] != wantFrom || spans[0][1] != to {
		t.Errorf("span = %v, want [%d %d]", spans[0], wantFrom, to)
	}
}

func TestOffPeakBoundaryIsAnInstant(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")

	inside := time.Date(2026, 8, 4, 20, 0, 0, 0, time.UTC)
	got, err := s.Cost(context.Background(), w, testPrices, inside)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if !got.OffPeakActive {
		t.Error("20:00 UTC must be inside the window")
	}
	if want := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC).Unix(); got.OffPeakUntil != want {
		t.Errorf("OffPeakUntil = %d, want the next midnight %d", got.OffPeakUntil, want)
	}

	outside := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	got, err = s.Cost(context.Background(), w, testPrices, outside)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if got.OffPeakActive {
		t.Error("09:00 UTC must be outside the window")
	}
	if want := time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC).Unix(); got.OffPeakUntil != want {
		t.Errorf("OffPeakUntil = %d, want today's opening %d", got.OffPeakUntil, want)
	}
}

// A model on a free tier is configured at 0/0. Its cost is genuinely nothing,
// which must not be reported as "no price configured" — the difference is
// between a total of $0.00 that is true and one that is unknown.
func TestCostTreatsAZeroPriceAsAPrice(t *testing.T) {
	s := New(openTestDB(t))
	w, _ := LookupWindow("24h")
	at := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)
	saveAt(t, s, at, costRun("mimo-v2.5", probe.ProbeInfer, 1000, 0, 200))

	got, err := s.Cost(context.Background(), w,
		map[string]config.ModelPrice{"mimo-v2.5": {}}, now)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if !got.Priced {
		t.Fatal("a free tier is a configured price")
	}
	if v := usd(t, got.Total.USD); v != 0 {
		t.Errorf("total = %v, want 0", v)
	}
}
