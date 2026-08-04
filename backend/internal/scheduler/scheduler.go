// Package scheduler drives the aligned probe cycle.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/trick77/mimostats/internal/probe"
	"github.com/trick77/mimostats/internal/samples"
	"github.com/trick77/mimostats/internal/sched"
)

// CycleInterval is the base cadence. Everything in a cycle happens on the same
// tick, so the network-vs-inference subtraction is exact rather than
// interpolated.
const CycleInterval = 5 * time.Minute

// CycleJitter is applied symmetrically, so the MEAN stays exactly
// CycleInterval and the day really does produce 288 cycles.
const CycleJitter = 30 * time.Second

// MinInterval floors the schedule even if jitter is misconfigured, so a bad
// value can never turn this into a tight loop against a billed endpoint.
const MinInterval = time.Minute

// WideEveryNCycles runs the wide probe hourly: every 12th cycle at a 5-minute
// cadence. Landing it ON a cycle rather than on its own timer is deliberate —
// it gets its own network reading, so its TTFT is decomposable exactly like
// infer's.
const WideEveryNCycles = 12

// Prober is the inference client seam. *probe.Client satisfies it.
type Prober interface {
	Run(ctx context.Context, req probe.Request) (probe.InferResult, error)
}

// Pinger is the network probe seam. *probe.Pinger satisfies it.
type Pinger interface {
	Ping(ctx context.Context, target, host string) probe.NetResult
}

// Deps are the scheduler's dependencies.
type Deps struct {
	Store  *samples.Store
	Prober Prober
	Pinger Pinger

	Origin string
	Models []string

	MimoHost   string
	RefSGPHost string
	RefEUHost  string

	// OnCycle is called after each cycle is persisted, for the SSE fan-out in
	// phase 4. Optional.
	OnCycle func(cycleID int64)

	// Now and Rand are seams for tests.
	Now  func() time.Time
	Rand func() float64
}

// Scheduler runs probe cycles until its context is cancelled.
type Scheduler struct {
	deps Deps

	// cycleCount drives both the question rotation and the wide cadence, so
	// both are deterministic and reproducible from the cycle number alone.
	cycleCount atomic.Int64

	// inFlight is the overrun guard, keyed by model+probe.
	//
	// Per-(model, probe) rather than one global lock: mimo-v2.5-pro genuinely
	// takes 2-3 minutes when things go bad, and a global lock would let one slow
	// model suppress the other's samples — manufacturing a correlated outage
	// across two independent series.
	mu       sync.Mutex
	inFlight map[string]bool
}

// New builds a Scheduler.
func New(deps Deps) *Scheduler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Rand == nil {
		deps.Rand = sched.PseudoRand()
	}
	return &Scheduler{deps: deps, inFlight: map[string]bool{}}
}

// Run drives cycles until ctx is cancelled.
//
// The first cycle fires immediately rather than after a full interval, so a
// restart does not leave a five-minute hole in the series and a fresh deploy
// has data on the dashboard within seconds.
func (s *Scheduler) Run(ctx context.Context) {
	slog.Info("scheduler starting",
		"interval", CycleInterval, "jitter", CycleJitter,
		"models", s.deps.Models, "origin", s.deps.Origin)

	for {
		s.RunCycle(ctx)

		d := sched.JitteredInterval(CycleInterval, CycleJitter, MinInterval, s.deps.Rand)
		if !sched.Sleep(ctx, d) {
			slog.Info("scheduler stopping")
			return
		}
	}
}

// RunCycle executes exactly one cycle: three pings, then one inference run per
// model, then a single atomic write.
//
// Exported so the smoke check and the tests can force a cycle without waiting
// on the timer.
func (s *Scheduler) RunCycle(ctx context.Context) {
	n := s.cycleCount.Add(1) - 1
	started := s.deps.Now().UTC()

	cycle := samples.Cycle{StartedAt: started, Origin: s.deps.Origin}

	// The network layer first, and always — it is free, it is fast, and every
	// inference reading in this cycle is subtracted against it.
	//
	// Sequential, not concurrent: three simultaneous handshakes share one
	// uplink and would contend for it, and the whole point is to measure that
	// uplink rather than our own scheduling.
	for _, t := range []struct{ target, host string }{
		{probe.TargetMimoSGP, s.deps.MimoHost},
		{probe.TargetRefSGP, s.deps.RefSGPHost},
		{probe.TargetRefEU, s.deps.RefEUHost},
	} {
		cycle.Net = append(cycle.Net, s.deps.Pinger.Ping(ctx, t.target, t.host))
	}

	wide := n%WideEveryNCycles == 0

	for _, model := range s.deps.Models {
		if res, ok := s.runProbe(ctx, model, probe.ProbeInfer, n, started); ok {
			cycle.Infer = append(cycle.Infer, res)
		}
		if wide {
			if res, ok := s.runProbe(ctx, model, probe.ProbeWide, n, started); ok {
				cycle.Infer = append(cycle.Infer, res)
			}
		}
	}

	// Shutdown mid-cycle: do not persist a half-measured cycle whose missing
	// models would read as an outage.
	if ctx.Err() != nil {
		slog.Info("cycle abandoned during shutdown", "cycle", n)
		return
	}

	cycleID, err := s.deps.Store.Save(ctx, cycle)
	if err != nil {
		slog.Error("persist cycle failed", "err", err, "cycle", n)
		return
	}

	slog.Info("cycle complete",
		"cycle", n, "cycle_id", cycleID,
		"net", len(cycle.Net), "infer", len(cycle.Infer), "wide", wide)

	if s.deps.OnCycle != nil {
		s.deps.OnCycle(cycleID)
	}
}

// runProbe executes one inference probe under the overrun guard.
//
// Returns ok=false when the run was skipped, so the caller adds nothing to the
// cycle rather than adding a zero-valued row.
func (s *Scheduler) runProbe(
	ctx context.Context, model, kind string, n int64, started time.Time,
) (probe.InferResult, bool) {
	key := model + "/" + kind
	if !s.acquire(key) {
		// The previous run is still in flight. Skip rather than pile up: two
		// concurrent runs against one model would contend for the same upstream
		// node and each would measure the other's queueing.
		slog.Warn("probe overrun; skipping", "model", model, "probe", kind, "cycle", n)
		if err := s.deps.Store.RecordSkip(ctx, started, s.deps.Origin, model, kind); err != nil {
			slog.Error("record skip failed", "err", err)
		}
		return probe.InferResult{}, false
	}
	defer s.release(key)

	req := probe.Request{ModelID: model, Probe: kind}
	if kind == probe.ProbeWide {
		req.Prompt = probe.WidePrompt()
		req.MaxTokens = probe.WideMaxTokens
	} else {
		q := probe.Pick(n)
		req.Prompt = q.Prompt()
		req.MaxTokens = probe.InferMaxTokens
		req.QuestionID = q.ID
		req.Assert = q.Assert
	}

	res, err := s.deps.Prober.Run(ctx, req)
	if err != nil {
		// Only a caller bug reaches here — transport failures come back as a
		// result. Do not store it: an unbuildable request measured nothing.
		slog.Error("probe request rejected", "err", err, "model", model, "probe", kind)
		return probe.InferResult{}, false
	}
	return res, true
}

func (s *Scheduler) acquire(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[key] {
		return false
	}
	s.inFlight[key] = true
	return true
}

func (s *Scheduler) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, key)
}
