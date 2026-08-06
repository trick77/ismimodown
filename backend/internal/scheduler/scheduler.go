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

// DispatchGap is the minimum quiet time between the end of one inference call
// and the start of the next, anywhere in the process.
//
// Serialising the calls — see RunCycle — only answers a CONCURRENCY limit. The
// 429s that motivated both were a short and a wide against mimo-v2.5 in one
// cycle, which were already strictly sequential: the second went out the
// instant the first came back. Nothing in a 429 says which kind of limiter
// produced it, and against a short-window request or token budget back-to-back
// is indistinguishable from simultaneous. This is the half of the fix that
// covers that case.
//
// Too small and it stops separating the calls at all. Too large and it eats the
// cycle: the ladder already allows a cycle to overrun on latency alone, and
// this adds (probes-1) * DispatchGap on top of that, every cycle, including the
// healthy ones. Two seconds against a 5-minute interval is under 1.5% of the
// budget for the three probes a wide cycle runs, and it is the whole of a
// plausible per-second limit's window.
const DispatchGap = 2 * time.Second

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

// OverrunSlotLimit is where a run of dropped slots stops being an overrun and
// starts being the wall clock moving.
//
// A cycle's length is bounded by the probe ladder: with the probes serialised a
// wide cycle costs at most short per model plus one wide, which is minutes even
// at the top of the timeout ladder — not hours.
// A catch-up longer than that is not an overrun at all — it is an NTP step, a
// suspended host or a restored VM snapshot, and calling it "the previous cycle
// was still running" is simply wrong.
//
// It no longer caps anything. This used to bound how many skipped_runs rows one
// overrun could insert, because a three-hour jump would otherwise put "72
// skipped" beside the availability strip under a tooltip blaming the previous
// cycle. That table and that panel are both gone; all that survives is the
// distinction itself, which still decides which of the two log lines is true.
//
// The anchor still catches all the way up in nextDelay, as it always did.
const OverrunSlotLimit = 6

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

	MimoSGPHost string
	RefSGPHost  string
	MimoAMSHost string
	RefAMSHost  string

	// OnCycle is called after each cycle is persisted, for the SSE fan-out in
	// phase 4. Optional.
	OnCycle func(cycleID int64)

	// Now and Rand are seams for tests.
	Now  func() time.Time
	Rand func() float64

	// Wait is the sleep seam, and it exists for DispatchGap. Defaults to
	// sched.Sleep, which is what production uses; a test that drove a dozen
	// cycles through the real one would spend DispatchGap per probe in wall
	// time for a gap it is not the subject of. Returns false when ctx was
	// cancelled first, exactly like sched.Sleep.
	Wait func(ctx context.Context, d time.Duration) bool
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

	// slot admits ONE inference call at a time, process-wide. Not keyed by model
	// and not by probe kind: MiMo throttles the API KEY, and the key is one.
	//
	// It was keyed by model+probe, on the reasoning that a global lock would let
	// mimo-v2.5-pro's bad day suppress mimo-v2.5's samples and manufacture a
	// correlated outage across two independent series. That reasoning was sound
	// and it is now outranked — a 429 is not a sample either, and a throttled run
	// is counted against published availability as though MiMo had failed. The
	// correlated-outage risk is real and accepted; see RunCycle.
	//
	// It BLOCKS; it does not skip. The keyed guard returned "skip" when it found
	// a run in flight, which is the one thing that must not happen here: every
	// row in a cycle is stamped with the cycle's start, so a probe that waits
	// still lands in its own bucket, while a probe that is skipped leaves that
	// bucket empty — indistinguishable from a probe that was never running.
	//
	// Only ctx cancellation gets a caller out of the queue, and that path is
	// shutdown, where the cycle is abandoned unpersisted anyway.
	slot chan struct{}

	// lastDispatch is when the most recent inference call RETURNED, and it is the
	// point DispatchGap is measured from. End-to-start rather than
	// start-to-start: the gap is there to leave the far end's limiter quiet for a
	// known stretch, and a start-to-start gap smaller than a probe's duration
	// leaves none at all.
	//
	// Guarded by slot, not by a mutex — it is read and written only by whoever
	// holds it, which is at most one goroutine by construction.
	lastDispatch time.Time

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
	if deps.Wait == nil {
		deps.Wait = sched.Sleep
	}
	return &Scheduler{
		deps:            deps,
		slot:            make(chan struct{}, 1),
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
		s.logMissedTicks(missed)

		if !sched.Sleep(ctx, d) {
			slog.Info("scheduler stopping")
			return
		}
	}
}

// logMissedTicks reports every scheduled slot a long cycle ran straight through.
//
// Without this the drop is INVISIBLE. The catch-up in nextDelay silently
// advances the anchor past the missed slots, so the cycles simply are not
// there — and a missing cycle reads as "no data", which is indistinguishable
// from a daemon that was not deployed yet.
//
// This used to ALSO write one skipped_runs row per model per slot, to drive a
// counter beside the availability strip. Both are gone (see migration 0005),
// and nothing is lost by it: the row carried no information this line does not,
// and it carried it somewhere nobody looked. The log is where an overrun is
// actually diagnosed.
//
// Past OverrunSlotLimit slots the cause is the wall clock rather than a long
// cycle, and the message says so instead of blaming the previous cycle.
func (s *Scheduler) logMissedTicks(missed []time.Time) {
	if len(missed) == 0 {
		return
	}
	// Two different events, so two different messages: relabelling a clock jump
	// as an overrun is a misattribution, and this line is now the only place the
	// distinction is recorded at all.
	msg := "cycle overran its slot; scheduled ticks dropped"
	if len(missed) > OverrunSlotLimit {
		msg = "wall clock moved past many slots; not an overrun"
	}
	slog.Warn(msg,
		"ticks", len(missed), "models", len(s.deps.Models),
		"first", missed[0], "last", missed[len(missed)-1])
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
// logMissedTicks.
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
	// still running. Collected, not just skipped — see logMissedTicks.
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
	// Sequential, not concurrent: simultaneous handshakes share one uplink and
	// would contend for it, and the whole point is to measure that uplink rather
	// than our own scheduling. Four of them now rather than two, so the network
	// layer costs at most 4 x PingTimeout (20 s) — still small against the cycle
	// interval, and the reason to keep it sequential is unchanged.
	//
	// The Singapore pair is MANDATORY: samples.Save rejects a cycle without it,
	// because fault attribution reads those two and nothing else. Adding a
	// region here is safe; removing Singapore is not.
	for _, t := range []struct{ target, host string }{
		{probe.TargetMimoSGP, s.deps.MimoSGPHost},
		{probe.TargetRefSGP, s.deps.RefSGPHost},
		{probe.TargetMimoAMS, s.deps.MimoAMSHost},
		{probe.TargetRefAMS, s.deps.RefAMSHost},
	} {
		cycle.Net = append(cycle.Net, s.deps.Pinger.Ping(ctx, t.target, t.host))
	}

	// At most ONE model goes wide in a cycle, and which one rotates. See
	// wideModel.
	wideFor := s.wideModel(ctx, started)

	// EVERY inference call in this process is serialised, across models as well as
	// within one, and DispatchGap separates consecutive calls. The loop below is
	// plainly sequential; the slot in runProbe is what actually enforces it.
	//
	// The models used to run concurrently, and the argument for it was a good
	// one. Sequentially a cycle costs the SUM of the models' latencies, so the
	// cadence breaks at a per-model latency of roughly CycleInterval divided by
	// the model count — about 145 s with two. mimo-v2.5-pro genuinely takes
	// minutes when things go bad, and the cycle that overran then ran through the
	// next slot entirely: the series thinned out precisely during the incident it
	// exists to record, and the surviving samples were the fast ones. Concurrently
	// the cycle costs the MAX instead. Sequencing also lets one model's stall
	// displace the other's reading from the cycle's start stamp — including past
	// the ping its residual is subtracted against.
	//
	// What outranks all of that: MiMo rate-limits the API KEY, and there is one
	// key for both models and one host behind BaseURL. Concurrent dispatch put
	// two calls on that key at the same instant and came back 429 —
	// error_class=rate_limited, which is not in probe.CensoringErrorClasses and
	// is not exempt from availability, so our own throttling published as a MiMo
	// outage on a page whose entire job is reporting MiMo outages. A confounded
	// sample is worse than a slow one; a fabricated outage is worse than both.
	//
	// So a cycle costs the sum again, and the worst case is real: three probes at
	// the 240 s ceiling is twelve minutes against a five-minute interval. That is
	// accepted rather than capped, because every alternative costs a sample. In
	// particular, deriving each probe's deadline from the cycle's remaining budget
	// would hold the cadence AND keep every row — but the effective timeout would
	// then depend on how slow the OTHER model had been, and censored counts and
	// percentiles would move for scheduling reasons. The timeout ladder is a
	// constant describing what this page measures. Overruns stay visible instead:
	// see logMissedTicks. Note the regime that actually hurts is slow-but-working,
	// not dead — at 240 s the rows are failures, already out of the percentiles.
	//
	// Nothing is ever SKIPPED to protect the cadence. Every row in a cycle is
	// stamped with the cycle's start, so a probe that waited its turn still lands
	// in its own bucket however late it returns; a probe that was skipped leaves
	// that bucket empty, which reads as a probe that was never running.
	//
	// The PINGS are unaffected: they all completed before the first probe is
	// dispatched, so nothing here contends with the measurement the residual is
	// subtracted against. They stay sequential for their own reason — see above.
	for _, model := range s.deps.Models {
		if res, ok := s.runProbe(ctx, model, probe.ProbeShort, n, started); ok {
			cycle.Infer = append(cycle.Infer, res)
		}
		if model == wideFor {
			if res, ok := s.runProbe(ctx, model, probe.ProbeWide, n, started); ok {
				cycle.Infer = append(cycle.Infer, res)
			}
		}
	}

	// Kept although the loop above is now deterministic and already produces this
	// order. It is one comparison per row and it pins the invariant the readers
	// depend on to the write path rather than to the shape of a loop somebody
	// might reorder later.
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

// runProbe executes one inference probe, holding the process-wide dispatch slot
// for the whole call.
//
// Returns ok=false when shutdown cut the wait short, and when the request was
// rejected before it left the process — so the caller adds nothing to the cycle
// rather than adding a zero-valued row. A cycle cut short by shutdown is
// abandoned unpersisted a few lines later anyway.
func (s *Scheduler) runProbe(
	ctx context.Context, model, kind string, n int64, started time.Time,
) (probe.InferResult, bool) {
	if !s.acquire(ctx) {
		slog.Info("probe abandoned during shutdown", "model", model, "probe", kind, "cycle", n)
		return probe.InferResult{}, false
	}
	defer s.release()

	// Recorded on DISPATCH, not on the decision and not on the write: a wide
	// abandoned in acquire never left the process and must still be retried next
	// cycle, while one that was sent has been billed whether or not the cycle it
	// belonged to survived to be persisted.
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

// acquire takes the process-wide dispatch slot and then waits out whatever is
// left of DispatchGap since the previous call returned.
//
// The wait happens INSIDE the slot, not before taking it. Outside, two waiters
// would both time their gap against the same lastDispatch, both find it
// satisfied, and both then queue for a slot they would enter back-to-back —
// which is the thing the gap exists to prevent.
//
// Returns false only if ctx was cancelled, and gives the slot back when it does.
func (s *Scheduler) acquire(ctx context.Context) bool {
	select {
	case s.slot <- struct{}{}:
	case <-ctx.Done():
		return false
	}

	// Zero on the first call of the process: nothing has been dispatched, so
	// there is nothing to be quiet after, and the first cycle after a restart
	// should not pay a gap it did not earn.
	if !s.lastDispatch.IsZero() {
		if d := DispatchGap - s.deps.Now().Sub(s.lastDispatch); d > 0 && !s.deps.Wait(ctx, d) {
			<-s.slot
			return false
		}
	}

	// Neither branch above necessarily consults ctx. The select picks at random
	// when the slot is free AND ctx is already done, and the gap is skipped
	// outright when d <= 0 — which is the ordinary case for the FIRST probe of a
	// cycle, whose predecessor returned a whole CycleInterval ago. Cancel during
	// the pings and every probe of that cycle would otherwise be dispatched into
	// a dead context, one of them possibly a wide that noteWideDispatch would
	// then record as sent.
	if ctx.Err() != nil {
		<-s.slot
		return false
	}
	return true
}

// release stamps the dispatch as finished and frees the slot. The stamp is
// taken before the slot is given up, so the next holder cannot read a
// lastDispatch older than the call it is waiting behind.
func (s *Scheduler) release() {
	s.lastDispatch = s.deps.Now()
	<-s.slot
}
