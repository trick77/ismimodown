// Package scheduler drives the aligned probe cycle.
package scheduler

import (
	"context"
	"log/slog"
	"math"
	"slices"
	"strings"
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
// 429s that motivated both were two probes against mimo-v2.5 in one cycle,
// which were already strictly sequential: the second went out the instant the
// first came back. Nothing in a 429 says which kind of limiter
// produced it, and against a short-window request or token budget back-to-back
// is indistinguishable from simultaneous. This is the half of the fix that
// covers that case.
//
// Too small and it stops separating the calls at all. Too large and it eats the
// cycle: the ladder already allows a cycle to overrun on latency alone, and
// this adds (probes-1) * DispatchGap on top of that, every cycle, including the
// healthy ones. Two seconds against a 5-minute interval is under 1.5% of the
// budget for the probes a cycle runs, and it is the whole of a plausible
// per-second limit's window.
const DispatchGap = 2 * time.Second

// OverrunSlotLimit is where a run of dropped slots stops being an overrun and
// starts being the wall clock moving.
//
// A cycle's length is bounded by the probe ladder: with the probes serialised a
// cycle costs at most one run per model, which is minutes even at the top of
// the timeout ladder — not hours.
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
	// Memory-resident, so it restarts at zero with the process. That is fine
	// for a rotation, where starting over just repeats a question sooner than
	// it would have come round.
	cycleCount atomic.Int64

	// slot admits ONE inference call at a time, process-wide. Not keyed by
	// model: MiMo throttles the API KEY, and the key is one.
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
		deps: deps,
		slot: make(chan struct{}, 1),
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
// cycles that is 281 cycles/day against a design figure of 288 — and unbounded
// once a model starts taking minutes.
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
	// So a cycle costs the sum again, and the worst case is real: one probe per
	// model at the 240 s ceiling is eight minutes against a five-minute
	// interval. That is
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
		if res, ok := s.runProbe(ctx, model, n, started); ok {
			cycle.Infer = append(cycle.Infer, res)
		}
	}

	// Kept, and it is no longer a no-op: config.DefaultModels is the DISPLAY
	// order and runs pro first, so the loop above dispatches in an order this
	// sort undoes. That is deliberate — row order in the database is alphabetical
	// and stays that way however the page is arranged, which is what the readers
	// tie-breaking on id (queries.go) depend on. Reordering DefaultModels must
	// not silently reorder stored rows.
	//
	// One key now that there is one run per model. It used to break ties on the
	// probe kind as well, which is the sort of tie that stops existing quietly.
	slices.SortFunc(cycle.Infer, func(a, b probe.InferResult) int {
		return strings.Compare(a.ModelID, b.ModelID)
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
		"net", len(cycle.Net), "infer", len(cycle.Infer))

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
	ctx context.Context, model string, n int64, started time.Time,
) (probe.InferResult, bool) {
	if !s.acquire(ctx) {
		slog.Info("probe abandoned during shutdown", "model", model, "cycle", n)
		return probe.InferResult{}, false
	}
	defer s.release()

	q := probe.Pick(n)
	req := probe.Request{
		ModelID:    model,
		Prompt:     q.Prompt(),
		MaxTokens:  probe.MaxTokens,
		QuestionID: q.ID,
		Assert:     q.Assert,
	}

	res, err := s.deps.Prober.Run(ctx, req)
	if err != nil {
		// Only a caller bug reaches here — transport failures come back as a
		// result. Do not store it: an unbuildable request measured nothing.
		slog.Error("probe request rejected", "err", err, "model", model)
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
			"model", model, "cycle", n,
			"question_id", req.QuestionID,
			"finish_reason", res.FinishReason,
			"content", truncate(res.Content, maxLoggedContent))
	}

	logInferenceCall(ctx, model, n, req, res)
	return res, true
}

// logInferenceCall emits one line per inference call.
//
// "cycle complete" reduces the runs to a count, so without this no individual
// run is identifiable in the log. That is backwards for failures: a probe that
// timed out, took a 401, or came back at four seconds becomes a row in
// infer_probes with an error_class, and nothing at all in the container log —
// which is the first place an operator looks and the only place available
// without sqlite3 on the volume.
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
	ctx context.Context, model string, n int64, req probe.Request, res probe.InferResult,
) {
	lvl := slog.LevelInfo
	if !res.OK {
		lvl = slog.LevelWarn
	}

	attrs := []any{
		"model", model, "cycle", n,
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
	// Guarded rather than assumed: an empty key would read as a question that
	// went missing rather than one that was never set.
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
// output at probe.MaxTokens, so a healthy reply is already well under this; the
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
	// a dead context and billed for nothing.
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
