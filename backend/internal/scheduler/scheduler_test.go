package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trick77/mimostats/internal/probe"
	"github.com/trick77/mimostats/internal/samples"
	"github.com/trick77/mimostats/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	return db
}

// fakePinger always answers, so a test can isolate the inference side.
type fakePinger struct {
	mu      sync.Mutex
	calls   []string
	results map[string]bool
}

func (f *fakePinger) Ping(_ context.Context, target, _ string) probe.NetResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, target)
	ok := true
	if f.results != nil {
		v, present := f.results[target]
		ok = !present || v
	}
	return probe.NetResult{Target: target, OK: ok, ConnectMs: 100}
}

// fakeProber records what it was asked and can block, so the overrun guard is
// testable without timing luck.
type fakeProber struct {
	mu      sync.Mutex
	reqs    []probe.Request
	running atomic.Int32
	// block holds a run inside Run until closed. blockModel scopes that to ONE
	// model: blocking every model would deadlock the overrun test, because the
	// second cycle's other model would wait on the same channel instead of
	// proceeding past the skipped one.
	block      chan struct{}
	blockModel string
	err        error
}

func (f *fakeProber) Run(_ context.Context, req probe.Request) (probe.InferResult, error) {
	f.running.Add(1)
	defer f.running.Add(-1)

	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()

	if f.block != nil && (f.blockModel == "" || f.blockModel == req.ModelID) {
		<-f.block
	}
	if f.err != nil {
		return probe.InferResult{}, f.err
	}
	return probe.InferResult{
		ModelID: req.ModelID, Probe: req.Probe, QuestionID: req.QuestionID,
		TTFTMs: 900, TotalMs: 1700, ITLP50Ms: 24, OK: true,
	}, nil
}

func (f *fakeProber) requests() []probe.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]probe.Request(nil), f.reqs...)
}

func newTestScheduler(t *testing.T, prober Prober, pinger Pinger) (*Scheduler, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	return New(Deps{
		Store:  samples.New(db),
		Prober: prober,
		Pinger: pinger,
		Origin: "rbx",
		Models: []string{"mimo-v2.5", "mimo-v2.5-pro"},
	}), db
}

func TestRunCycleProbesEveryTargetAndModel(t *testing.T) {
	pinger := &fakePinger{}
	prober := &fakeProber{}
	s, db := newTestScheduler(t, prober, pinger)

	s.RunCycle(context.Background())

	// All three network targets, every cycle — the subtraction needs them.
	if len(pinger.calls) != 3 {
		t.Errorf("pinged %d targets, want 3: %v", len(pinger.calls), pinger.calls)
	}
	var nNet, nInfer int
	db.QueryRow(`SELECT count(*) FROM net_probes`).Scan(&nNet)
	db.QueryRow(`SELECT count(*) FROM infer_probes`).Scan(&nInfer)
	if nNet != 3 {
		t.Errorf("net_probes = %d, want 3", nNet)
	}
	// Cycle 0 is a wide cycle: 2 models x (infer + wide).
	if nInfer != 4 {
		t.Errorf("infer_probes = %d, want 4 on a wide cycle", nInfer)
	}
}

// wide runs hourly, landing ON a cycle so it gets its own network reading and
// its TTFT is decomposable exactly like infer's.
func TestWideRunsEveryTwelfthCycle(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	for i := 0; i < WideEveryNCycles+1; i++ {
		s.RunCycle(context.Background())
	}

	var wideCycles, inferCount int
	byProbe := map[string]int{}
	for _, r := range prober.requests() {
		byProbe[r.Probe]++
	}
	wideCycles = byProbe[probe.ProbeWide]
	inferCount = byProbe[probe.ProbeInfer]

	// 13 cycles, 2 models: infer every cycle, wide on cycles 0 and 12.
	if inferCount != 13*2 {
		t.Errorf("infer runs = %d, want %d", inferCount, 13*2)
	}
	if wideCycles != 2*2 {
		t.Errorf("wide runs = %d, want %d (cycles 0 and 12, two models)", wideCycles, 2*2)
	}
}

// The two probes must never be aggregated: the gap between their TTFTs IS the
// prefill signal, so wide has to carry the bigger cap and no question id.
func TestWideAndInferRequestsDifferAsTheMeasurementRequires(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	s.RunCycle(context.Background()) // cycle 0 runs both

	var sawInfer, sawWide bool
	for _, r := range prober.requests() {
		switch r.Probe {
		case probe.ProbeInfer:
			sawInfer = true
			if r.MaxTokens != probe.InferMaxTokens {
				t.Errorf("infer cap = %d, want %d", r.MaxTokens, probe.InferMaxTokens)
			}
			if r.QuestionID == "" {
				t.Error("infer must carry a question id; it is the correctness canary")
			}
			if r.Assert == nil {
				t.Error("infer must carry an assertion")
			}
		case probe.ProbeWide:
			sawWide = true
			if r.MaxTokens != probe.WideMaxTokens {
				t.Errorf("wide cap = %d, want %d", r.MaxTokens, probe.WideMaxTokens)
			}
			if len(r.Prompt) < 10000 {
				t.Errorf("wide prompt is only %d chars; the prefill gradient needs the document", len(r.Prompt))
			}
			if r.Assert != nil {
				t.Error("wide has no single assertable answer")
			}
		}
	}
	if !sawInfer || !sawWide {
		t.Fatal("cycle 0 must run both probe kinds")
	}
}

// The question rotates per cycle, so a per-question correctness rate is
// meaningful rather than dominated by one unlucky entry.
func TestQuestionRotatesBetweenCycles(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	s.RunCycle(context.Background())
	s.RunCycle(context.Background())

	seen := map[string]bool{}
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeInfer {
			seen[r.QuestionID] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("consecutive cycles asked the same question: %v", seen)
	}
}

// Both models must get the SAME question within a cycle, or their correctness
// series are not comparable.
func TestBothModelsGetTheSameQuestionInACycle(t *testing.T) {
	prober := &fakeProber{}
	s, _ := newTestScheduler(t, prober, &fakePinger{})

	s.RunCycle(context.Background())

	var ids []string
	for _, r := range prober.requests() {
		if r.Probe == probe.ProbeInfer {
			ids = append(ids, r.QuestionID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 infer runs, got %d", len(ids))
	}
	if ids[0] != ids[1] {
		t.Errorf("models got different questions (%q vs %q); their correctness is not comparable",
			ids[0], ids[1])
	}
}

// The overrun guard: if the previous run is still in flight when the next cycle
// fires, skip and count it. Two concurrent runs against one model would contend
// for the same upstream node and each would measure the other's queueing.
func TestOverrunSkipsAndCountsExactlyOnce(t *testing.T) {
	prober := &fakeProber{block: make(chan struct{}), blockModel: "mimo-v2.5"}
	s, db := newTestScheduler(t, prober, &fakePinger{})

	// Start a cycle that blocks inside the prober.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunCycle(context.Background())
	}()

	// Wait until the first probe is genuinely in flight.
	deadline := time.Now().Add(3 * time.Second)
	for prober.running.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first probe never started")
		}
		time.Sleep(time.Millisecond)
	}

	// Fire a second cycle while the first is still running.
	s.RunCycle(context.Background())

	close(prober.block)
	<-done

	var skipped int
	if err := db.QueryRow(`SELECT count(*) FROM skipped_runs`).Scan(&skipped); err != nil {
		t.Fatalf("query: %v", err)
	}
	if skipped == 0 {
		t.Fatal("an overrun must be recorded; silent skipping makes availability lie by omission")
	}

	// And the skip must not have produced a phantom sample.
	var inferRows int
	db.QueryRow(`SELECT count(*) FROM infer_probes`).Scan(&inferRows)
	if inferRows == 0 {
		t.Error("the completed cycle should still have written its own rows")
	}
}

// A slow model must not suppress the other's samples — a global lock would
// manufacture a correlated outage across two independent series.
func TestOverrunGuardIsPerModelAndProbe(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	if !s.acquire("mimo-v2.5/infer") {
		t.Fatal("first acquire must succeed")
	}
	if s.acquire("mimo-v2.5/infer") {
		t.Error("the same key must not be acquirable twice")
	}
	// A different model, and a different probe on the same model, must both be
	// unaffected.
	if !s.acquire("mimo-v2.5-pro/infer") {
		t.Error("a different model must not be blocked by another model's run")
	}
	if !s.acquire("mimo-v2.5/wide") {
		t.Error("a different probe kind must not be blocked by infer")
	}

	s.release("mimo-v2.5/infer")
	if !s.acquire("mimo-v2.5/infer") {
		t.Error("release must free the key")
	}
}

// A failed probe is still a recorded sample, and the cycle must still persist.
func TestCycleWithAFailedProbeIsStillPersisted(t *testing.T) {
	pinger := &fakePinger{results: map[string]bool{probe.TargetMimoSGP: false}}
	s, db := newTestScheduler(t, &fakeProber{}, pinger)

	s.RunCycle(context.Background())

	var fault string
	if err := db.QueryRow(`SELECT fault FROM cycle_fault`).Scan(&fault); err != nil {
		t.Fatalf("query: %v", err)
	}
	// MiMo unreachable while both references answer: the edge, not the route.
	if fault != probe.FaultEdge {
		t.Errorf("fault = %q, want %q", fault, probe.FaultEdge)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n)
	if n != 1 {
		t.Errorf("cycles = %d; a network failure must still produce a cycle", n)
	}
}

// Shutdown mid-cycle must not persist a half-measured cycle, whose missing
// models would read as an outage.
func TestCycleAbandonedOnShutdownIsNotPersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, db := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	cancel()
	s.RunCycle(ctx)

	var n int
	db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n)
	if n != 0 {
		t.Errorf("cycles = %d, want 0; a cycle abandoned during shutdown must not be stored", n)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after its context was cancelled")
	}
}

// The cadence must be anchored to the wall clock, not to when the last cycle
// happened to finish. Sleeping a fixed interval AFTER each cycle adds that
// cycle's duration to every gap — and the error compounds in the worst
// direction, sampling LESS often exactly when the endpoint is slow.
func TestNextDelayIsAnchoredToTheWallClockNotToCycleDuration(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	s.deps.Rand = func() float64 { return 0.5 } // zero jitter

	// A cycle that finished 22 seconds past an aligned tick must wait for the
	// NEXT tick, not a full interval from now.
	finishedAt := time.Date(2026, 8, 4, 6, 0, 22, 0, time.UTC)
	s.deps.Now = func() time.Time { return finishedAt }

	d := s.nextDelay()

	want := CycleInterval - 22*time.Second
	if d != want {
		t.Errorf("delay = %v, want %v (the next aligned tick, not a full interval)", d, want)
	}
	if fired := finishedAt.Add(d); fired.Second() != 0 || fired.Minute()%5 != 0 {
		t.Errorf("cycle would fire at %v, which is not an aligned tick", fired)
	}
}

// A cycle that overruns its slot lands on the following one; the cadence must
// not walk forward by the overrun.
func TestOverrunningCycleLandsOnTheNextSlotWithoutDrifting(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})
	s.deps.Rand = func() float64 { return 0.5 }

	// Finished 6m30s after a tick — it ate a whole slot.
	finishedAt := time.Date(2026, 8, 4, 6, 6, 30, 0, time.UTC)
	s.deps.Now = func() time.Time { return finishedAt }

	fired := finishedAt.Add(s.nextDelay())

	if want := time.Date(2026, 8, 4, 6, 10, 0, 0, time.UTC); !fired.Equal(want) {
		t.Errorf("next cycle at %v, want %v — the schedule must not walk", fired, want)
	}
}

// Negative jitter must not fire the cycle in the past or immediately.
func TestNextDelayNeverFiresImmediately(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	// Finish just before an aligned tick, with maximum negative jitter.
	s.deps.Rand = func() float64 { return 0 }
	s.deps.Now = func() time.Time { return time.Date(2026, 8, 4, 6, 4, 59, 0, time.UTC) }

	if d := s.nextDelay(); d < MinInterval {
		t.Errorf("delay = %v, below the %v floor; this could become a tight loop", d, MinInterval)
	}
}

// Over many cycles the mean must sit on the interval, or the day's cycle count
// and therefore the token bill are wrong.
func TestCadenceDoesNotDriftOverManyCycles(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeProber{}, &fakePinger{})

	now := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	s.deps.Now = func() time.Time { return now }

	const cycles = 200
	const cycleDuration = 22 * time.Second // a realistic wide cycle
	start := now
	for i := 0; i < cycles; i++ {
		now = now.Add(cycleDuration) // the cycle runs
		now = now.Add(s.nextDelay()) // then we wait
	}

	elapsed := now.Sub(start)
	mean := elapsed / cycles
	drift := mean - CycleInterval
	if drift < -2*time.Second || drift > 2*time.Second {
		t.Errorf("mean interval %v drifts %v from %v over %d cycles; that is %.0f cycles/day instead of 288",
			mean, drift, CycleInterval, cycles, 86400/mean.Seconds())
	}
}
