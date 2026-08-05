// Package scheduler drives the aligned probe cycle.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/trick77/ismimodown/internal/probe"
	"github.com/trick77/ismimodown/internal/redact"
	"github.com/trick77/ismimodown/internal/samples"
	"github.com/trick77/ismimodown/internal/sched"
)

// CycleInterval is the base cadence. Everything in a cycle happens on the same
// tick, so the network-vs-inference subtraction is exact rather than
// interpolated.
const CycleInterval = 5 * time.Minute

// The cost series rounds every run to its cycle tick before bucketing it, and
// it does that with its own copy of this value — see samples.CycleSeconds for
// why the store restates it rather than importing the scheduler.
//
// The restatement is only harmless while the two agree. Changing CycleInterval
// alone would leave the cost query rounding to the OLD tick, silently
// misfiling runs by up to half a cycle and reintroducing exactly the boundary
// straddle that rounding exists to remove — with no test failing, because both
// halves would still be self-consistent. So the compiler checks it: the two
// differences below are non-negative together only when the values are equal,
// and a negative constant does not convert to uint.
const (
	_ = uint(samples.CycleSeconds - int64(CycleInterval/time.Second))
	_ = uint(int64(CycleInterval/time.Second) - samples.CycleSeconds)
)

// CycleJitter is applied symmetrically, so the MEAN stays exactly
// CycleInterval and the day really does produce 288 cycles.
const CycleJitter = 30 * time.Second

// MinInterval floors the schedule even if jitter is misconfigured, so a bad
// value can never turn this into a tight loop against a billed endpoint.
const MinInterval = time.Minute

// WideInterval is how often the wide probe runs FOR ONE MODEL. Landing it ON a
// cycle rather than on its own timer is deliberate — it gets its own network
// reading, so its TTFT is decomposable exactly like the short probe's.
//
// Across models the runs are staggered rather than simultaneous: see wideModel.
// Each model still sees a full WideInterval between its own wide runs, so the
// per-model sample rate and the daily bill are exactly what they were when both
// models went wide together.
const WideInterval = time.Hour

// WideSlack is how early a cycle may claim the hourly slot.
//
// Cycles land every CycleInterval, jittered, so the one nearest the hour mark
// is as likely to fall a little BEFORE it as after. Without slack that cycle
// misses, the next one is a full interval later, and "hourly" quietly becomes
// every 65 minutes — drifting further every time it happens.
//
// The slack can never double-fire as long as it stays below
// WideInterval-CycleInterval: the cycle after a wide run is only one
// CycleInterval past it, which is nowhere near the WideInterval-WideSlack
// threshold. Half a cycle sits far inside that bound and is deliberately
// conservative — it is sized to cover the jitter around the hour mark, not to
// use up the available room.
const WideSlack = CycleInterval / 2

// MaxRecordedMisses bounds how many dropped slots ONE overrun may claim.
//
// A cycle's length is bounded by the probe ladder: a wide cycle costs at most
// short+wide per model with the models concurrent, which is minutes, not hours.
// A catch-up longer than that is not an overrun at all — it is the wall clock
// moving, from an NTP step, a suspended host or a restored VM snapshot. Left
// uncapped, a three-hour jump would insert one skipped_runs row per model per
// five-minute slot and put "72 skipped" beside the availability strip, under a
// tooltip saying the previous cycle was still running. That is exactly the
// misread this counter exists to prevent, in a new form.
//
// The anchor still catches all the way up in nextDelay — only the CLAIM about
// the skipped slots is capped, and the excess is logged so a real clock jump is
// still visible to whoever reads the logs.
const MaxRecordedMisses = 6

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

	Models []string

	MimoHost   string
	RefSGPHost string

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

	// cycleCount drives the question rotation, which is deterministic and
	// reproducible from the cycle number alone.
	//
	// It does NOT drive the wide cadence any more. This counter is
	// memory-resident, so it restarts at zero with the process — fine for a
	// rotation, where starting over just repeats a question, and wrong for a
	// billed hourly probe, where it re-fires on every restart. See wideModel.
	cycleCount atomic.Int64

	// lastWideNano is an in-memory FLOOR on the wide cadence: the cycle time of
	// the most recent wide probe this process actually dispatched, whether or
	// not it was ever persisted.
	//
	// The database stays the source of truth across restarts — that is the whole
	// point of wideModel — but it only knows about probes that were written. A
	// daemon whose writes fail (full disk, read-only volume, a cycle abandoned
	// mid-shutdown) keeps reading a stale timestamp, and once that is an hour
	// old EVERY five-minute cycle fires the ~3800-token probe for every model.
	// wideModel already refuses to spend on a database it cannot read; this
	// closes the same hole on the write side.
	lastWideNano atomic.Int64

	// lastWideByModel is the same floor, per model: the cycle time of the most
	// recent wide probe this process dispatched FOR THAT MODEL.
	//
	// Two floors rather than one, because the stagger is two rules and they fail
	// differently. lastWideNano bounds how close together any two wide runs may
	// be — the global slot — and this bounds how often one model's own wide run
	// comes round. A single global floor would let an unwritable database starve
	// whichever model is not next in line; a single per-model floor would let two
	// models go wide in the same cycle, which is the contention the stagger
	// exists to remove.
	wideMu          sync.Mutex
	lastWideByModel map[string]time.Time

	// inFlight is the overrun guard, keyed by model+probe.
	//
	// Per-(model, probe) rather than one global lock: mimo-v2.5-pro genuinely
	// takes 2-3 minutes when things go bad, and a global lock would let one slow
	// model suppress the other's samples — manufacturing a correlated outage
	// across two independent series.
	//
	// It is a BACKSTOP, not the thing that counts overruns. Run drives one cycle
	// at a time and each model's two probes share a goroutine, so no key is ever
	// held when it is asked for again and this guard cannot fire as the code
	// stands. Reading skipped_runs as "the guard fired" was wrong for exactly
	// that reason; overruns are counted where they actually happen, against the
	// scheduled slots a long cycle ran through. See recordMissedTicks. Keep the
	// guard: it is what makes dispatching cycles without waiting safe later.
	mu       sync.Mutex
	inFlight map[string]bool

	// nextTick is the un-jittered schedule anchor. See nextDelay for why it is
	// carried rather than recomputed from the clock each time.
	nextTick time.Time
}

// New builds a Scheduler.
func New(deps Deps) *Scheduler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Rand == nil {
		deps.Rand = sched.PseudoRand()
	}
	return &Scheduler{
		deps:            deps,
		inFlight:        map[string]bool{},
		lastWideByModel: map[string]time.Time{},
	}
}

// Run drives cycles until ctx is cancelled.
//
// The first cycle fires immediately rather than after a full interval, so a
// restart does not leave a five-minute hole in the series and a fresh deploy
// has data on the dashboard within seconds.
func (s *Scheduler) Run(ctx context.Context) {
	slog.Info("scheduler starting",
		"interval", CycleInterval, "jitter", CycleJitter,
		"models", s.deps.Models)

	for {
		s.RunCycle(ctx)

		d, missed := s.nextDelay()
		s.recordMissedTicks(ctx, missed)

		if !sched.Sleep(ctx, d) {
			slog.Info("scheduler stopping")
			return
		}
	}
}

// recordMissedTicks writes one skipped run per model for every scheduled slot a
// long cycle ran straight through.
//
// Without this the drop is INVISIBLE. The catch-up in nextDelay silently
// advances the anchor past the missed slots, so the cycles simply are not
// there — and a missing cycle reads as "no data", which is indistinguishable
// from a daemon that was not deployed yet. The overrun counter beside the
// availability strip meanwhile reported zero, because the only thing that ever
// wrote to it was the in-flight guard in runProbe, and that guard cannot fire:
// cycles run one at a time, so a run is never still in flight when the next one
// starts. The counter was structurally incapable of being non-zero.
//
// Recorded per MODEL and against `short`: every cycle runs the short probe for
// every model, so those runs really were lost. `wide` is conditional and is not
// claimed — a slot that would not have carried it did not lose it.
//
// A failed write is logged and dropped rather than retried. This is a counter
// for a human reading a dashboard, and losing a tick from it must never be able
// to stall the probe loop it is counting.
//
// More than MaxRecordedMisses slots is a clock jump rather than an overrun, and
// only the most recent MaxRecordedMisses are claimed. See that constant.
func (s *Scheduler) recordMissedTicks(ctx context.Context, missed []time.Time) {
	if len(missed) == 0 {
		return
	}
	unclaimed := 0
	if len(missed) > MaxRecordedMisses {
		unclaimed = len(missed) - MaxRecordedMisses
		missed = missed[len(missed)-MaxRecordedMisses:]
	}
	// Two different events, so two different messages: relabelling a clock jump
	// as an overrun in the logs is the same misattribution the cap removes from
	// the strip, only moved somewhere a reader trusts more.
	msg := "cycle overran its slot; scheduled ticks dropped"
	if unclaimed > 0 {
		msg = "wall clock moved past many slots; recording only the most recent"
	}
	slog.Warn(msg,
		"ticks", len(missed), "unclaimed", unclaimed, "models", len(s.deps.Models),
		"first", missed[0], "last", missed[len(missed)-1])

	for _, tick := range missed {
		for _, model := range s.deps.Models {
			if err := s.deps.Store.RecordSkip(ctx, tick, model, probe.ProbeShort); err != nil {
				// Shutdown cancels the context mid-write; that is not a fault,
				// and the rest of the batch has nowhere to go either.
				if errors.Is(err, context.Canceled) {
					return
				}
				// One lost counter tick, not the rest of the batch: a single
				// failed INSERT says nothing about the next one, and abandoning
				// them understates the drop in the direction that flatters.
				slog.Error("record missed tick failed", "err", err, "tick", tick)
			}
		}
	}
}

// nextDelay returns how long to wait for the next cycle, anchored to the wall
// clock rather than to when the last cycle happened to finish.
//
// Sleeping a fixed interval AFTER each cycle adds that cycle's own duration to
// every gap, and the error compounds in the worst possible direction: a slow
// cycle pushes the next one later, so the probe samples LESS often exactly when
// the endpoint is struggling and the data matters most. Measured on real
// cycles that is 281 cycles/day for a typical cycle and 268 for one carrying
// wide, against a design figure of 288 — and unbounded once a model starts
// taking minutes.
//
// Anchoring to epoch-aligned ticks removes the drift entirely: a cycle that
// overruns its slot simply lands on the next one, and the cadence never walks.
// The jitter is applied around the aligned instant, so it still spreads load
// without reintroducing the walk.
//
// It also returns the slots that were run straight through, so the drop can be
// counted rather than merely absorbed. Removing the drift is not the same as
// removing the loss: an overrunning cycle still samples less often exactly when
// the endpoint is struggling, and the caller records that. See
// recordMissedTicks.
//
// The FIRST call never reports a miss, and correctly so: it is the call that
// establishes the anchor, and the slots before it belong to a time when the
// process was not running. Those are a deploy gap, not an overrun.
func (s *Scheduler) nextDelay() (time.Duration, []time.Time) {
	now := s.deps.Now()

	// The anchor advances by exactly one interval per cycle and is NEVER
	// recomputed from `now`.
	//
	// Deriving it from `now` each time aliases against the jitter: negative
	// jitter fires the cycle just BEFORE its aligned tick, so the next
	// AlignedNext(now) returns that same tick again and the interval collapses.
	// Measured over 200 cycles that produced a 4m36s mean — 313 cycles/day
	// instead of 288, i.e. the drift this function exists to remove, in the
	// opposite direction and costing real tokens.
	//
	// Keeping the anchor un-jittered makes the mean exactly CycleInterval while
	// individual fires still scatter within the jitter window.
	switch {
	case s.nextTick.IsZero():
		s.nextTick = sched.AlignedNext(now, CycleInterval)
	default:
		s.nextTick = s.nextTick.Add(CycleInterval)
	}
	// Catch up when cycles overran their slots, so a slow patch does not leave
	// the schedule permanently behind the wall clock.
	//
	// Every iteration here is a slot that came and went while the cycle was
	// still running. Collected, not just skipped — see recordMissedTicks.
	var missed []time.Time
	for !s.nextTick.After(now) {
		missed = append(missed, s.nextTick)
		s.nextTick = s.nextTick.Add(CycleInterval)
	}

	// Symmetric, for the same reason JitteredInterval is: the mean must stay on
	// the tick, not drift half a jitter past it.
	jitter := time.Duration((s.deps.Rand()*2 - 1) * float64(CycleJitter))
	d := s.nextTick.Add(jitter).Sub(now)

	// Hard floor against a pathological clock or rand source, so this can never
	// become a tight loop against a billed endpoint.
	if d < MinInterval {
		d = MinInterval
	}
	return d, missed
}

// WideSlot is the gap between consecutive wide runs across ALL models.
//
// One model at a time, WideInterval apart per model, means the fleet produces a
// wide run every WideInterval/N. With two models that is every thirty minutes,
// alternating; with three, every twenty.
//
// Running both models wide in the SAME cycle was the earlier behaviour and it
// put two ~3800-token prefills on the endpoint and the uplink simultaneously,
// each measuring a share of the other's queueing. That is the confound the
// within-model sequencing already exists to avoid, and it does not stop being
// one because the two runs are aimed at different models. Staggering removes it
// without costing a single sample: the per-model cadence, the run count and the
// bill are unchanged, and the wide CYCLES get cheaper, because one now carries
// short+wide for one model instead of for every model.
func (s *Scheduler) WideSlot() time.Duration {
	if n := len(s.deps.Models); n > 1 {
		return WideInterval / time.Duration(n)
	}
	// One model — or none — is its own stagger. Falling through to a division
	// by zero would be the only way this function could fail.
	return WideInterval
}

// wideModel answers WHICH model, if any, should carry the wide probe this cycle.
// The empty string means no model does.
//
// Two rules, and both must hold:
//
//   - The global slot. No two wide runs may be closer together than WideSlot,
//     whichever models they belong to. This is what keeps the prefills apart.
//   - The model's own hour. A model is a candidate only once WideInterval has
//     passed since ITS last wide run, so staggering never raises anyone's rate.
//
// Among the candidates the one idle longest wins, which is what makes the
// rotation self-correcting: a model that missed its slot to an overrun or a
// failed lookup is first in line at the next one rather than waiting a full
// turn. Ties — including a fresh deploy where nobody has ever run — go to
// Models order, so the sequence is reproducible.
//
// The question is asked of the DATABASE, not of a counter. cycleCount lives in
// memory, so it says "cycle zero" again after every restart — and cycle zero is
// a wide cycle, by design, so the prefill panel is not empty for an hour after a
// fresh deploy. Those two facts together meant a daemon restarted three times
// during a deploy sent three ~3800-token probes per model, and re-anchored the
// hourly clock to the last restart. The intent was always "at most one per hour
// per model"; this states it directly.
//
// No wide sample at all still fires immediately, which is the fresh-deploy case
// the old cycle-zero rule existed to serve — for ONE model, with the rest
// following a slot apart.
//
// A failed lookup skips the probe rather than running it. The wide probe is the
// expensive one and the daemon is billed for it, so the safe direction when the
// database cannot answer is to wait five minutes and ask again — a missed hourly
// reading is a gap in a chart, while the other direction is an unbounded spend
// on a database that stays broken.
func (s *Scheduler) wideModel(ctx context.Context, now time.Time) string {
	last, err := s.deps.Store.LastProbeAtByModel(ctx, probe.ProbeWide)
	if err != nil {
		// A cancelled context is a shutdown, not a fault: the cycle is about to
		// be abandoned anyway, and logging it at ERROR trains the reader to
		// ignore the level that should mean something.
		if !errors.Is(err, context.Canceled) {
			slog.Error("wide cadence lookup failed; skipping the wide probe this cycle", "err", err)
		}
		return ""
	}
	// Dispatches this process made but could not store still count. See
	// lastWideByModel.
	s.wideMu.Lock()
	for model, at := range s.lastWideByModel {
		if cur, ok := last[model]; !ok || at.After(cur) {
			last[model] = at
		}
	}
	s.wideMu.Unlock()

	// The global slot, taken from the newest wide run of any model — including
	// one this process sent for a model no longer configured, which still
	// occupied the endpoint.
	newest := time.Time{}
	for _, at := range last {
		if at.After(newest) {
			newest = at
		}
	}
	if nano := s.lastWideNano.Load(); nano != 0 {
		if at := time.Unix(0, nano).UTC(); at.After(newest) {
			newest = at
		}
	}
	if !newest.IsZero() && now.Sub(newest) < s.WideSlot()-WideSlack {
		return ""
	}

	// The model's own hour. A zero time means never, which is older than any
	// real timestamp and therefore first in line.
	pick, pickAt := "", time.Time{}
	for _, model := range s.deps.Models {
		at := last[model]
		if !at.IsZero() && now.Sub(at) < WideInterval-WideSlack {
			continue
		}
		if pick == "" || at.Before(pickAt) {
			pick, pickAt = model, at
		}
	}
	return pick
}

// noteWideDispatch records that a wide probe was actually sent for `model` at
// `at`, monotonically so concurrent cycles cannot walk either floor backwards.
func (s *Scheduler) noteWideDispatch(model string, at time.Time) {
	v := at.UnixNano()
	for {
		cur := s.lastWideNano.Load()
		if v <= cur || s.lastWideNano.CompareAndSwap(cur, v) {
			break
		}
	}

	s.wideMu.Lock()
	defer s.wideMu.Unlock()
	if cur, ok := s.lastWideByModel[model]; !ok || at.After(cur) {
		s.lastWideByModel[model] = at
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

	cycle := samples.Cycle{StartedAt: started}

	// The network layer first, and always — it is free, it is fast, and every
	// inference reading in this cycle is subtracted against it.
	//
	// Sequential, not concurrent: three simultaneous handshakes share one
	// uplink and would contend for it, and the whole point is to measure that
	// uplink rather than our own scheduling.
	for _, t := range []struct{ target, host string }{
		{probe.TargetMimoSGP, s.deps.MimoHost},
		{probe.TargetRefSGP, s.deps.RefSGPHost},
	} {
		cycle.Net = append(cycle.Net, s.deps.Pinger.Ping(ctx, t.target, t.host))
	}

	// At most ONE model goes wide in a cycle, and which one rotates. See
	// wideModel.
	wideFor := s.wideModel(ctx, started)

	// The models run CONCURRENTLY, and the probes within one model do not.
	//
	// Sequentially across models, a cycle costs the SUM of the models' latencies,
	// so the cadence breaks at a per-model latency of roughly CycleInterval
	// divided by the model count — about 145 s with two models. mimo-v2.5-pro
	// genuinely takes minutes when things go bad, and the cycle that overran then
	// ran through the next slot entirely: the series thinned out precisely during
	// the incident it exists to record, and the surviving samples were the fast
	// ones. Concurrently the cycle costs the MAX instead, so one slow model no
	// longer spends the other's budget.
	//
	// It also stops one model's stall from displacing the other's reading in
	// time. Every row in a cycle is stamped with the cycle's start, and under the
	// sequential order the second model's probe could begin minutes after that
	// stamp — including minutes after the ping its residual is subtracted
	// against. Same JOIN, same cycle_id, much less staleness inside it.
	//
	// Within one model, short and wide stay strictly sequential. Two runs at once
	// against the same model contend for the same upstream node and each measures
	// the other's queueing, which is the exact confound this probe exists to
	// avoid — and it is why the in-flight guard is keyed by model+probe rather
	// than held globally.
	//
	// The PINGS are unaffected: they all completed before the first probe is
	// dispatched, so nothing here contends with the measurement the residual is
	// subtracted against.
	//
	// The probes' OWN handshakes do now overlap, and that is a real if small
	// cost. probe.NewClient sets DisableKeepAlives, so each run opens a fresh
	// DNS+TCP+TLS connection, and two of those now share the uplink for a few
	// milliseconds. It lands inside ttft_ms and therefore inside the published
	// residual. Accepted rather than hidden: the residual is hundreds to
	// thousands of milliseconds, the overlap is a fraction of one RTT, and the
	// alternative — serialising the models — is the multi-minute sampling
	// collapse this change exists to remove.
	//
	// A wide cycle costs short+wide for the ONE model carrying wide and short
	// alone for the rest, so it is bounded by the slower of those two rather than
	// by short+wide across the board. It can still run through its slot at the
	// per-model latencies that motivated this; recordMissedTicks makes that
	// visible rather than silent, which is the honest outcome, and serialising
	// less than this would cost the isolation above.
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, model := range s.deps.Models {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var got []probe.InferResult
			if res, ok := s.runProbe(ctx, model, probe.ProbeShort, n, started); ok {
				got = append(got, res)
			}
			if model == wideFor {
				if res, ok := s.runProbe(ctx, model, probe.ProbeWide, n, started); ok {
					got = append(got, res)
				}
			}

			mu.Lock()
			defer mu.Unlock()
			cycle.Infer = append(cycle.Infer, got...)
		}()
	}
	wg.Wait()

	// Completion order is now a race, and the write order below is not allowed to
	// be. Sorted so a cycle's rows land in the database in the same order every
	// time, whichever model happened to finish first.
	slices.SortFunc(cycle.Infer, func(a, b probe.InferResult) int {
		if c := strings.Compare(a.ModelID, b.ModelID); c != 0 {
			return c
		}
		return strings.Compare(a.Probe, b.Probe)
	})

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
		// The model that went wide, not a boolean: with the runs staggered, which
		// one it was is the thing a reader chasing an odd wide reading needs, and
		// "true" no longer says it.
		"net", len(cycle.Net), "infer", len(cycle.Infer), "wide", wideFor)

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
		if err := s.deps.Store.RecordSkip(ctx, started, model, kind); err != nil {
			slog.Error("record skip failed", "err", err)
		}
		return probe.InferResult{}, false
	}
	defer s.release(key)

	// Recorded on DISPATCH, not on the decision and not on the write: an
	// overrun-skipped wide never left the process and must still be retried
	// next cycle, while one that was sent has been billed whether or not the
	// cycle it belonged to survived to be persisted.
	if kind == probe.ProbeWide {
		s.noteWideDispatch(model, started)
	}

	req := probe.Request{ModelID: model, Probe: kind}
	if kind == probe.ProbeWide {
		req.Prompt = probe.WidePrompt()
		req.MaxTokens = probe.WideMaxTokens
	} else {
		q := probe.Pick(n)
		req.Prompt = q.Prompt()
		req.MaxTokens = probe.ShortMaxTokens
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

	// The only place the model's actual words are ever visible.
	//
	// A wrong answer is stored as answer_ok=0 and nothing else — the reply
	// itself is deliberately never persisted, and a bare zero cannot tell a
	// silent reroute to a smaller model from a reply cut off at MaxTokens from
	// a provider serving "we are at capacity" as a perfectly well-formed
	// stream. All three are graded wrong and look identical afterwards. So the
	// reply goes to the log, where it is readable and not queryable, rather
	// than into a column that would make it neither.
	//
	// finish_reason is what separates the truncation case from the other two,
	// and it is already on the result and stored nowhere.
	if res.AnswerOK != nil && !*res.AnswerOK {
		slog.Warn("answer graded wrong",
			"model", model, "probe", kind, "cycle", n,
			"question_id", req.QuestionID,
			"finish_reason", res.FinishReason,
			"content", truncate(res.Content, maxLoggedContent))
	}

	logInferenceCall(ctx, model, kind, n, req, res)
	return res, true
}

// logInferenceCall emits one line per inference call — every call, not only the
// wide one.
//
// "cycle complete" names the model that went wide and reduces the short runs to
// a count, so a wide run is identifiable in the log and a short run is not. That
// asymmetry is backwards for failures: a short probe that timed out, took a 401,
// or came back at four seconds becomes a row in infer_probes with an
// error_class, and nothing at all in the container log — which is the first
// place an operator looks and the only place available without sqlite3 on the
// volume.
//
// The level carries the verdict, so `level=WARN` alone finds the bad runs
// without knowing which fields to read. error_class and error_detail are
// attached only when there is one: a healthy line should not carry two empty
// strings, and a reader scanning for the failure should not have to skip them.
//
// error_detail is operator-only and stays out of the HTTP API. The provider's
// own words are the whole point of keeping it, so they are quoted here — but a
// log line is not the database column, and AGENTS.md draws that line: the MiMo
// key is live and billable and the repo is public, so it may never reach a log.
// ErrorDetail carries raw upstream bytes — the body read on a non-2xx status,
// and the preamble kept when a stream produced no deltas, both in probe's client
// — and a gateway that echoes request headers on a 4xx puts the Authorization
// value in them. So it is redacted BEFORE it is truncated, since clipping first
// would leave the first half of a live key in the log, and only then bounded on
// the same maxLoggedContent a graded-wrong reply gets, since a provider serving
// an HTML error page would otherwise put a full document on one line. The stored
// column is untouched.
func logInferenceCall(
	ctx context.Context, model, kind string, n int64, req probe.Request, res probe.InferResult,
) {
	lvl := slog.LevelInfo
	if !res.OK {
		lvl = slog.LevelWarn
	}

	attrs := []any{
		"model", model, "probe", kind, "cycle", n,
		"ok", res.OK,
		"http_status", res.HTTPStatus,
		"ttft_ms", round1(res.TTFTMs),
		"total_ms", round1(res.TotalMs),
		"output_tps", round1(res.OutputTPS),
		"prompt_tokens", res.Usage.PromptTokens,
		"output_tokens", res.Usage.CompletionTokens,
		"cached_tokens", res.Usage.PromptTokensDetails.CachedTokens,
		"reasoning_tokens", res.Usage.CompletionTokenDetails.ReasoningTokens,
		"finish_reason", res.FinishReason,
	}
	// Only the short probe carries one, and an empty key on every wide line
	// would read as a question that went missing rather than one that never
	// existed.
	if req.QuestionID != "" {
		attrs = append(attrs, "question_id", req.QuestionID)
	}
	if res.AnswerOK != nil {
		attrs = append(attrs, "answer_ok", *res.AnswerOK)
	}
	if res.ErrorClass != "" {
		attrs = append(attrs,
			"error_class", res.ErrorClass,
			"error_detail", truncate(redact.String(res.ErrorDetail), maxLoggedContent))
	}

	slog.Log(ctx, lvl, "inference call", attrs...)
}

// round1 trims a millisecond timing to one decimal for the log. The stored
// column keeps full precision; a log line reporting 143.82754100000002 ms is
// harder to read and no more true.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// maxLoggedContent bounds the reply quoted into the log. The short probe caps
// output at ShortMaxTokens, so a healthy reply is already well under this; the
// bound is for the reply that is not healthy, which is the whole reason the
// line exists.
const maxLoggedContent = 300

// truncate shortens s for logging, marking that it did so. Cut on a rune
// boundary: a reply is text, and half a multi-byte character in a JSON log line
// is a replacement glyph where the evidence should be.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
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
