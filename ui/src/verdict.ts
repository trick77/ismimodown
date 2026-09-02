// Scoring. "Is 996 ms good?" is unanswerable in the abstract, so no figure is
// published without the context needed to read it.
import type { ModelSummary, RecentCycle, Summary } from "./api/types";
import { FAULT_EDGE, FAULT_OK, FAULT_ROUTE, FAULT_UPLINK } from "./api/types";
import { plural } from "./format";

export type State = "normal" | "elevated" | "degraded" | "unknown";

// Bands for higher-is-worse metrics, scored against the metric's own rolling
// baseline rather than an absolute number.
//
// Deliberately generous. Latency is noisy, and a dashboard that cries wolf at
// +20% gets ignored — which is worse than no dashboard. A rolling baseline also
// means the page keeps working if MiMo gets permanently faster or slower.
export const ELEVATED_RATIO = 1.5;
export const DEGRADED_RATIO = 2.5;

export function scoreRatio(
  current: number | null,
  baseline: number | null,
): State {
  if (
    current === null ||
    baseline === null ||
    !Number.isFinite(current) ||
    !Number.isFinite(baseline) ||
    baseline <= 0
  ) {
    return "unknown";
  }
  const ratio = current / baseline;
  if (ratio >= DEGRADED_RATIO) return "degraded";
  if (ratio >= ELEVATED_RATIO) return "elevated";
  return "normal";
}

// Lower-is-worse metrics are scored against ABSOLUTE expectations, not a
// rolling baseline: a model that has been 97% available all week has not made
// 97% acceptable, and a drifting baseline would quietly normalise a fault.

// A band this project chose, at the same altitude as the correctness pair below
// it, and named to match. It was once named for a target and the card printed it
// as "target 99%", which claimed a commitment that does not exist: nothing the
// operator publishes states an availability target, and neither does anything
// here. The number was picked, not sourced. Do not restore the target name or
// the target copy without a real published figure to point at.
//
// 99%, not 99.5% and emphatically not 100%. This is one endpoint probed from one
// vantage over the public internet, so a resting expectation of perfection
// reports weather, not faults.
export const AVAILABILITY_ELEVATED = 99;
export const AVAILABILITY_DEGRADED = 97;
export const CORRECTNESS_ELEVATED = 98;
export const CORRECTNESS_DEGRADED = 95;

// The floor under the correctness percentage and the censored note.
//
// A percentage over a small numerator is a rounding error with a decimal point,
// and the bands alone paint a chip on a single wrong answer. The band says how
// bad; this says whether anything actually happened.
//
// Availability used to be scored behind this same floor and no longer is — see
// scoreAvailability, which asks the sharper version of the same question.
export const MIN_FAILURES_FOR_STATE = 3;

// The floor under the availability score, in ATTEMPTS rather than failures.
//
// The confidence bound below needs no protection from a small numerator, but it
// does need protection from a small DENOMINATOR: three of five runs succeeding
// is a genuinely low bound, so a window that started twenty minutes ago would
// publish DEGRADED off five samples. Twenty matches MinSamplesForPercentile on
// the daemon (backend/internal/samples/queries.go) — the same judgement about
// the same cadence.
//
// Below it the answer is "unknown", not "normal". Leaning on scoreModelRecent
// to cover the gap does not work: that only reaches the header chip, so the
// Availability FIGURE would print its percentage with nothing beside it, and
// this is not only a cold start. `attempts` counts attributable cycles only —
// a cycle nobody could pin on MiMo is excluded from the denominator on the
// daemon (backend/internal/samples/queries.go) — so a long local uplink outage
// drops a 24h window under this floor, and modelTracks filters those same
// cycles out of the recent track, leaving the fold clean at the same moment.
// A card reading 20.0% in green is exactly what that combination produced.
export const MIN_ATTEMPTS_FOR_STATE = 20;

// One-sided 95%. Two-sided z would demand more evidence than the question needs
// — nobody is asking whether availability is suspiciously HIGH.
const WILSON_Z = 1.645;

// wilsonUpper is the optimistic end of the confidence interval on the TRUE
// availability, given `succeeded` of `attempts`, as a percentage.
//
// Wilson rather than the textbook normal interval because the whole point is
// behaviour near p = 1, where the normal interval is worthless: it is symmetric,
// so at 592 of 592 it happily reports a bound above 100%.
//
// Clamped, because Wilson does too. At succeeded === attempts the radical is
// still non-zero (the z²/4n² term survives), and an unclamped bound would put a
// number over 100 into a percentage.
export function wilsonUpper(succeeded: number, attempts: number): number {
  if (attempts <= 0) return 100;
  const p = succeeded / attempts;
  const z2 = WILSON_Z * WILSON_Z;
  const centre = p + z2 / (2 * attempts);
  const margin =
    WILSON_Z *
    Math.sqrt((p * (1 - p)) / attempts + z2 / (4 * attempts * attempts));
  const upper = (centre + margin) / (1 + z2 / attempts);
  return Math.min(100, upper * 100);
}

// scoreAvailability scores the BOUND, not the measurement.
//
// The measurement alone cannot carry a verdict, because the same three failures
// mean different things at different sample sizes and the bands cannot tell them
// apart. Three cut-off runs in 48 hours is 99.49% — under a 99.5 band — and is
// also indistinguishable from an endpoint that meets it. A count-based floor did
// not fix that: at 288 cycles a day a floor of three failures IS the threshold
// over the short windows, so any three runs painted the card and the band never
// got a word in.
//
// So the band stays absolute, exactly as the comment above it demands — nothing
// here drifts with observed behaviour — and the sample size decides a different
// question: is there enough evidence to CLAIM we are under the band. Only when
// even the optimistic end of the interval sits below the band does the page say
// so.
//
// Counts, not the percentage the API also carries: available_pct is computed
// from these two integers on the daemon, so taking it as well would add a
// rounding path and a second source of truth for one number.
//
// The bound is always at least the measurement, so a chip can never appear while
// the percentage printed beside it reads above the band. The two can only
// diverge the safe way.
export function scoreAvailability(succeeded: number, attempts: number): State {
  if (!Number.isFinite(attempts) || attempts <= 0) return "unknown";
  if (attempts < MIN_ATTEMPTS_FOR_STATE) return "unknown";
  const upper = wilsonUpper(succeeded, attempts);
  if (upper < AVAILABILITY_DEGRADED) return "degraded";
  if (upper < AVAILABILITY_ELEVATED) return "elevated";
  return "normal";
}

export function scoreCorrectness(pct: number | null, wrong: number): State {
  if (pct === null || !Number.isFinite(pct)) return "unknown";
  if (wrong < MIN_FAILURES_FOR_STATE) return "normal";
  if (pct < CORRECTNESS_DEGRADED) return "degraded";
  if (pct < CORRECTNESS_ELEVATED) return "elevated";
  return "normal";
}

export function worst(...states: State[]): State {
  if (states.includes("degraded")) return "degraded";
  if (states.includes("elevated")) return "elevated";
  if (states.includes("normal")) return "normal";
  return "unknown";
}

// ---------------------------------------------------------------------------
// "Right now"
//
// Everything below scores summary.recent — the last cycles, in order, NOT
// scoped to the selected window. The banner used to fire on fault COUNTS over
// that window, which had two failure modes at once: one bad cycle out of sixty
// published DEGRADED, and it kept publishing it until the cycle aged out of the
// range. On the 3-month view that is three months of red for one dropped
// connection.
//
// The horizon and the thresholds live here rather than on the daemon for the
// same reason the baseline window does (see App.tsx): the client is the only
// thing that acts on them, and the daemon has no opinion.
// ---------------------------------------------------------------------------

// How far back "right now" reaches. Twelve cycles is one hour at the 5-minute
// cadence: long enough that a flapping endpoint cannot hide between two clean
// checks, short enough that a fault which stopped an hour ago stops being news.
// The banner is allowed to forget. The pulse strip and the errors card below it
// do not.
export const RECENT_CYCLES = 12;

// One red cycle is an anecdote; two in a row is a state. A single failed
// handshake from one vantage point is indistinguishable from one retransmit
// storm on one connection, and a dashboard that calls that DEGRADED gets
// closed.
export const DEGRADED_STREAK = 2;

// ...or three inside the hour, consecutive or not. An endpoint that alternates
// pass/fail never builds a streak and is exactly as broken as one that fails
// twice in a row.
export const DEGRADED_RECENT = 3;

// TWO inside the hour before the banner says anything at all.
//
// One dropped run from one vantage point is an anecdote at every layer, which
// is the rule the floors above state for the window figures — and this used
// to be 1, so the banner escalated on a single failure while the sentence
// underneath it read "One run is not yet a pattern". The banner contradicting
// itself in public is worse than the banner staying quiet, and the model cards
// held the opposite line the whole time.
//
// A lone failure is not forgotten, it moves: the normal branch names when the
// last failed cycle was and how many clean ones have run since, which is the
// honest weight for one anecdote.
export const ELEVATED_RECENT = 2;

// Wrong answers are noisier than failures — a model can miss a fact once — so
// they need one more before they count.
export const DEGRADED_WRONG_RECENT = 3;
export const ELEVATED_WRONG_RECENT = 2;

// Three missed cycles. Past this the newest measurement is not "now", and the
// only honest banner is one that says so: a daemon that died mid-incident must
// not pin its last red cycle on screen forever, which is the same bug the
// window-scoped counts had, wearing a different hat. Well clear of the 30s
// response cache, so a cached body can never trip it.
//
// THREE, not two, because a cycle is stamped with its START and only becomes
// visible when it is SAVED. The budget a live daemon actually has is this minus
// the running cycle's own duration, and a cycle whose probes time out costs up
// to config.ProbeTimeout each — 240 s. At two intervals a single cycle whose
// probes all timed out pushes the newest stored started_at past the threshold
// and the banner announces "the probe itself may be down"
// during the exact incident it should be reporting as degraded, which is the
// severity inversion this constant exists to avoid.
export const STALE_AFTER_MS = 3 * 5 * 60 * 1000;

// A fault nobody could pin on MiMo. Both classes travel together everywhere in
// this codebase: route is no longer produced, but stored cycles carry it, and
// handling only uplink would silently misread them. Exported so every surface
// that has to make this call shares the ONE predicate rather than a comment
// asking two copies to stay in step — see RecentErrors.tsx.
export function unattributableFault(fault: string): boolean {
  return fault === FAULT_UPLINK || fault === FAULT_ROUTE;
}

// A cycle nobody could attribute. Both classes travel together everywhere in
// this codebase: route is no longer produced, but stored cycles carry it, and
// handling only uplink would silently misread them.
function unattributable(cycle: RecentCycle): boolean {
  return unattributableFault(cycle.fault);
}

// A cycle that failed at the network layer. The empty string is what a cycle
// with no stored attribution carries, and it is NOT red: absence of evidence is
// not evidence of failure, and a monitor must never round in that direction.
function infraRed(cycle: RecentCycle): boolean {
  return cycle.fault !== FAULT_OK && cycle.fault !== "";
}

export type Track = {
  // Consecutive reds counting back from the newest cycle.
  streak: number;
  // Reds anywhere inside the horizon.
  count: number;
  // How many cycles back the newest red is, or null if there is none in the
  // whole served block — which reaches further back than the horizon, so a
  // quiet banner can still say when the last failure was.
  //
  // A COUNT of cycles, and only ever used as one. It is not a clock: a dropped
  // slot leaves no cycle behind, so the block is not guaranteed to be evenly
  // spaced. See lastRedMinutes.
  lastRedAgo: number | null;
  // How long ago the newest red actually was, in minutes, read off the stored
  // timestamps rather than multiplied out of the index above.
  //
  // The two disagree exactly when a slot was dropped — which the scheduler logs
  // rather than pretending it did not happen — so an index-derived "20 minutes
  // ago" can describe a failure from an hour back, understating it in the
  // direction that flatters.
  lastRedMinutes: number | null;
};

export function track(
  cycles: RecentCycle[],
  isRed: (cycle: RecentCycle) => boolean,
): Track {
  // Clamped to what was actually served: on a cold database there are three
  // cycles, and counting a horizon of twelve against them would score nine
  // cycles that do not exist.
  const horizon = cycles.slice(0, RECENT_CYCLES);
  let streak = 0;
  for (const cycle of horizon) {
    if (!isRed(cycle)) break;
    streak++;
  }
  const idx = cycles.findIndex(isRed);
  let lastRedMinutes: number | null = null;
  if (idx !== -1) {
    // Measured from the NEWEST served cycle, not from the browser clock, for
    // the same reason isStale is: a skewed client must not be able to age a
    // failure it did not observe.
    const delta = Date.parse(cycles[0]!.at) - Date.parse(cycles[idx]!.at);
    lastRedMinutes = Number.isFinite(delta) ? Math.round(delta / 60000) : null;
  }
  return {
    streak,
    count: horizon.filter(isRed).length,
    lastRedAgo: idx === -1 ? null : idx,
    lastRedMinutes,
  };
}

export function scoreTrack(
  t: Track,
  thresholds: { streak: number; degraded: number; elevated: number },
): State {
  if (t.streak >= thresholds.streak || t.count >= thresholds.degraded) {
    return "degraded";
  }
  if (t.count >= thresholds.elevated) return "elevated";
  return "normal";
}

const FAILURE_THRESHOLDS = {
  streak: DEGRADED_STREAK,
  degraded: DEGRADED_RECENT,
  elevated: ELEVATED_RECENT,
};

// Wrong answers cannot form a "streak" worth acting on the way dropped cycles
// can — two wrong answers in a row is the same evidence as two in the hour —
// so the streak rule is set out of reach and density decides.
const WRONG_THRESHOLDS = {
  streak: Number.POSITIVE_INFINITY,
  degraded: DEGRADED_WRONG_RECENT,
  elevated: ELEVATED_WRONG_RECENT,
};

// Which fault class the reds inside the horizon are mostly made of.
//
// Ties break OUTWARD — uplink over route over edge — because each layer makes
// the ones beyond it unreadable. If nothing in Singapore answered we cannot say
// anything about MiMo, and saying it anyway is how a monitor publishes an
// outage that never happened.
//
// Only those three classes are recognised, while infraRed accepts any non-ok,
// non-empty string. That coupling is deliberate and it is held by the CHECK
// constraint on cycle_fault: a fourth class stored server-side would score as
// red here and then fall through to the edge headline, so adding one means
// adding it to this list in the same change.
export function dominantFault(cycles: RecentCycle[]): {
  fault: string | null;
  counts: Record<string, number>;
} {
  const counts: Record<string, number> = {};
  for (const cycle of cycles.slice(0, RECENT_CYCLES)) {
    if (!infraRed(cycle)) continue;
    counts[cycle.fault] = (counts[cycle.fault] ?? 0) + 1;
  }
  let fault: string | null = null;
  for (const candidate of [FAULT_UPLINK, FAULT_ROUTE, FAULT_EDGE]) {
    if ((counts[candidate] ?? 0) > (fault ? (counts[fault] ?? 0) : 0)) {
      fault = candidate;
    }
  }
  return { fault, counts };
}

// isStale is true when the newest cycle is old enough that nothing on the page
// is current. Measured against the server's own generated_at rather than the
// browser clock, so a skewed client cannot manufacture an outage.
export function isStale(summary: Summary): boolean {
  const newest = (summary.recent ?? [])[0];
  if (!newest) return false;
  const age = Date.parse(summary.generated_at) - Date.parse(newest.at);
  return Number.isFinite(age) && age > STALE_AFTER_MS;
}

// How long ago, in words. Takes MINUTES off the stored timestamps rather than a
// cycle index: the two only agree while every slot was actually run.
function agoWords(minutes: number): string {
  if (minutes < 5) return "just now";
  if (minutes < 60) return `${minutes} minutes ago`;
  const hours = Math.round(minutes / 60);
  return hours === 1 ? "an hour ago" : `${hours} hours ago`;
}

export type Verdict = {
  state: State;
  headline: string;
  detail: string[];
};

// buildVerdict states the situation in plain English before any number appears.
//
// `summary` is the FIXED window the banner reads (App.tsx NOW_WINDOW), never
// the selected one: the banner answers "how is it right now" and the cards
// answer "over the selected window". Tying the first to the second is what made
// one failed cycle publish DEGRADED for as long as the range held it.
export function buildVerdict(
  summary: Summary | null,
  baseline: Summary | null,
): Verdict {
  // `?? []` because a payload that predates the field must read as "nothing to
  // say", never crash the page it is the headline of. (`faults` carried one for
  // the same reason, until the panel that read it was removed.)
  const recent = summary?.recent ?? [];
  // Deliberately NOT `summary.cycles === 0`. cycles counts the fixed window;
  // recent does not. A daemon dead for longer than that window has cycles = 0
  // and a full recent block, and testing cycles here would publish "first
  // samples within a few minutes" over a stack of hours-old cycles — swallowing
  // the stale branch below, which exists for precisely that case. An empty recent
  // block is the only thing that actually means "no data yet".
  if (!summary || recent.length === 0) {
    return {
      state: "unknown",
      headline: "Collecting data — first samples within a few minutes",
      detail: [],
    };
  }

  // Above every other branch, including the reasoning override below. A dead
  // daemon leaves its last cycles on record, and if those were red then every
  // rule further down would keep publishing an outage that stopped being
  // measured hours ago.
  if (isStale(summary)) {
    const minutes = Math.round(
      (Date.parse(summary.generated_at) - Date.parse(recent[0]!.at)) / 60000,
    );
    return {
      state: "unknown",
      headline: "No fresh measurement — the probe itself may be down",
      detail: [
        `The last cycle landed ${minutes} minutes ago, and they run every few minutes. Nothing on this page is current.`,
      ],
    };
  }

  const infra = track(recent, infraRed);
  const infraState = scoreTrack(infra, FAILURE_THRESHOLDS);
  if (infraState !== "normal") {
    return faultVerdict(infraState, recent, infra);
  }

  // A non-zero reasoning count invalidates every latency figure in the window,
  // so it outranks any LATENCY verdict rather than sitting beside it — but it
  // sits BELOW the network branch, and that order is load-bearing.
  //
  // max_reasoning_tokens is a `max` over the whole fixed window, so one run
  // twenty-three hours ago holds this branch open for a further day. Above the
  // network branch it would swallow a live uplink outage under a stale caveat,
  // which is the window-scoped stickiness this whole banner was rewritten to
  // remove, reintroduced one branch higher. Below it, a fault happening NOW
  // still speaks first, and the caveat is still the loudest thing said about
  // the latency figures themselves.
  const reasoning = summary.models.filter((m) => m.max_reasoning_tokens > 0);
  if (reasoning.length > 0) {
    return {
      state: "degraded",
      headline:
        "Reasoning is switched on — the latency figures are not measuring what they claim",
      detail: reasoning.map(
        (m) =>
          `${m.model_id} returned up to ${m.max_reasoning_tokens} reasoning ${plural(m.max_reasoning_tokens, "token")} despite thinking being disabled.`,
      ),
    };
  }

  const detail: string[] = [];
  let state: State = "normal";
  const struggling: string[] = [];

  for (const model of summary.models) {
    const base = baseline?.models.find((m) => m.model_id === model.model_id);
    const modelState = scoreModel(model, base ?? null, recent);
    state = worst(state, modelState.state);
    detail.push(...modelState.detail);
    if (modelState.state !== "normal" && modelState.state !== "unknown") {
      struggling.push(model.model_id);
    }
  }

  if (state === "normal" || struggling.length === 0) {
    return {
      state: "normal",
      // The question this page is named after, answered in the present tense.
      // It used to read "Everything looks normal right now" — true, and a
      // sentence about the page rather than about the endpoint: a visitor
      // arrives asking whether Xiaomi MiMo is up and how long they will wait,
      // and "normal" answers neither. The wait itself is added underneath by
      // the banner, which is the surface that holds the trend block.
      //
      // Forgetting is only acceptable if the page says what it forgot. Without
      // the lines below, a banner that has gone quiet is indistinguishable from
      // one that never had anything to report.
      headline: "Xiaomi MiMo is answering",
      // Concatenated, never an either/or. lastRedAgo indexes the whole served
      // block — three hours of it — while a failed inference run is a separate
      // event from a failed cycle, so one network red aged well past the
      // horizon used to swallow a run that had just dropped, under a sentence
      // claiming every cycle since had been clean.
      detail: [
        ...cleanHour(summary, recent),
        ...(infra.lastRedAgo !== null
          ? [
              // This is where a LONE failure inside the horizon lands, and it
              // is the whole reason ELEVATED_RECENT is 2 rather than 1: one red
              // is reported here, in the past tense, with the clean cycles
              // since counted beside it — not as an amber banner hedged by a
              // sentence admitting it is not yet a pattern.
              //
              // So the singular is reachable, and it is grammar rather than
              // decoration: lastRedAgo is a count of cycles since the red, and
              // "the 1 since have all been clean" is the sentence a reader
              // stops at. It was unreachable while the threshold was 1, and
              // the comment here said so.
              //
              // Zero is reachable too, and it is the newest cycle itself: one
              // red at the head of the block is still one red, and there is no
              // "since" to count at all.
              infra.lastRedAgo === 0
                ? "The most recent cycle failed. One cycle is not yet a pattern."
                : infra.lastRedAgo === 1
                  ? `The last failed cycle was ${agoWords(infra.lastRedMinutes ?? 5)}; the one since was clean.`
                  : `The last failed cycle was ${agoWords(infra.lastRedMinutes ?? infra.lastRedAgo * 5)}; the ${infra.lastRedAgo} since have all been clean.`,
            ]
          : []),
        ...quietFailures(summary, recent),
      ],
    };
  }
  // The headline follows the severity. At elevated the evidence is two failures
  // inside the hour, and "having problems" claims more than that — the same
  // over-claim the network branch makes a point of not making one line above.
  //
  // The elevated phrase also names no CAUSE, and cannot: this branch fires on
  // worst(availability, correctness, ttft), so it speaks with zero failed runs
  // whenever the trigger was a slow first token or a wrong answer. It read
  // "showing the odd failure right now" — a failure the reader would then hunt
  // for under a detail line about latency, in an idiom half the audience reads
  // as "strange" rather than "occasional".
  return {
    state,
    headline: `${struggling.join(" and ")} ${struggling.length > 1 ? "are" : "is"} ${
      state === "degraded"
        ? "having problems right now"
        : "showing early signs of trouble"
    }`,
    // The quiet lines ride along here too: one model can be having problems
    // while the other drops a single run, and the second one is not less true
    // for arriving on a bad day.
    detail: [...detail, ...quietFailures(summary, recent)],
  };
}

// faultVerdict speaks for the network layer, where the precedence that matters
// is which layer failed rather than how much: uplink -> route -> edge, because
// each one makes the ones beyond it unreadable.
//
// The HEADLINE still follows the severity, exactly as the model branch above
// does. At elevated the whole evidence is two failed cycles inside the hour,
// and "MiMo's edge is unreachable" over a detail line counting two of them is
// the banner contradicting itself in public — the present-tense absolute claim
// being the half a reader remembers. So the elevated headlines carry the COUNT
// instead: it hedges without a softener, and it cannot drift out of step with
// the thresholds the way a hand-written "a couple" would.
//
// Nothing here branches on a single cycle any more. ELEVATED_RECENT is 2, so
// this function is unreachable below two reds in the horizon and every "one
// cycle is not yet a pattern" softener it used to carry described a state it
// can no longer be in. A lone red now leaves the banner normal, where the
// detail line says when it was and how many clean cycles have run since.
function faultVerdict(state: State, recent: RecentCycle[], t: Track): Verdict {
  const { fault, counts } = dominantFault(recent);
  const horizon = Math.min(RECENT_CYCLES, recent.length);
  const when =
    t.lastRedMinutes === null ? "just now" : agoWords(t.lastRedMinutes);
  const sustained = state === "degraded";

  const detail: string[] = [];
  if (t.streak >= DEGRADED_STREAK) {
    detail.push(
      `The last ${t.streak} cycles in a row failed — the most recent ${when}.`,
    );
  } else {
    detail.push(
      `${t.count} of the last ${horizon} ${plural(horizon, "cycle")} failed, the most recent ${when}.`,
    );
  }

  // "the far end", never "the endpoint". This page has already bound "the
  // endpoint" to MiMo's API — the masthead subtitle, the errors card's status
  // codes, the samples note — so "the route to the endpoint is fine" beside
  // "MiMo's edge is unreachable" reads as the banner contradicting itself, and
  // "the route to the endpoint is degraded — not MiMo" fights its own
  // disclaimer. The far end is the whole far side of the path, which is what
  // these two classes are actually about. RecentErrors carries the same pair
  // of words.
  if (fault === FAULT_UPLINK || fault === FAULT_ROUTE) {
    // counts[fault], NOT t.count. The two are the same number only when every
    // red in the horizon carries the dominant class, and a MIXED horizon is
    // exactly what the disclosure line below exists for: one uplink cycle
    // beside one edge cycle is t.count = 2 with a dominant class of one, and
    // the headline would say "2 cycles reached nothing at the far end" three
    // lines above "1 of them did reach the reference host". Every sentence in
    // this branch speaks for the dominant class alone, so all of them count it.
    //
    // Which puts the singular back within reach, for this count and no other:
    // the elevated floor is two reds of ANY class, so one of them can be the
    // only uplink cycle in the window.
    const dominant = counts[fault] ?? 0;
    const those = dominant === 1 ? "That cycle" : "Those cycles";
    const headline =
      fault === FAULT_UPLINK
        ? sustained
          ? "Nothing at the far end was reachable — this says nothing about MiMo"
          : `${dominant} ${plural(dominant, "cycle")} reached nothing at the far end — this says nothing about MiMo`
        : sustained
          ? "The route to the far end is degraded — not MiMo, and not us"
          : `${dominant} ${plural(dominant, "cycle")} found no route to the far end — not MiMo, and not us`;
    detail.push(
      fault === FAULT_UPLINK
        ? `${those} reached neither MiMo nor the reference host, so ${dominant === 1 ? "it is" : "they are"} excluded from availability. From one vantage point our own connection and the route to the far end look identical, and neither is MiMo's to answer for.`
        : `${those} could not reach MiMo's edge OR an unrelated host beside it.`,
    );
    // A mixed run has to disclose the mix, or the headline claims more than the
    // evidence supports.
    const edge = counts[FAULT_EDGE] ?? 0;
    if (edge > 0) {
      detail.push(
        `${edge} of them did reach the reference host, so MiMo's own edge was down for ${edge === 1 ? "that cycle" : "those cycles"} too.`,
      );
    }
    return { state, headline, detail };
  }

  // Plural unconditionally, unlike the branch above, because a singular is not
  // reachable here: edge only becomes the dominant class by strictly beating
  // both outward classes, so a lone edge cycle wins the tie only when it is the
  // ONLY red — and one red does not reach this function at all.
  detail.push("They failed to reach MiMo while the reference host answered.");
  const unattributed = (counts[FAULT_UPLINK] ?? 0) + (counts[FAULT_ROUTE] ?? 0);
  if (unattributed > 0) {
    detail.push(
      `${unattributed} of them reached neither host, so ${unattributed === 1 ? "it cannot" : "they cannot"} be attributed to MiMo.`,
    );
  }
  return {
    state,
    headline: sustained
      ? "MiMo's edge is unreachable — the route to the far end is fine"
      : `MiMo's edge missed ${t.count} cycles — the route to the far end is fine`,
    detail,
  };
}

// modelTracks is the pair of recent-scoped tracks a model is judged on, in one
// place because two callers need them and only one of them wants the sentences
// that go with them.
//
// The unattributable filter is part of the rule, not part of scoreModel: a
// model cannot be charged for a cycle where nothing in Singapore answered — the
// run failed on connect, before it ever reached MiMo — and a second caller
// scoring these without it would quietly manufacture provider downtime out of
// our own outage.
function modelTracks(
  modelId: string,
  recent: RecentCycle[],
): { failures: Track; wrong: Track } {
  return {
    failures: track(
      recent,
      (c) => !unattributable(c) && c.models[modelId]?.ok === false,
    ),
    wrong: track(recent, (c) => c.models[modelId]?.answer_ok === false),
  };
}

// scoreModelRecent is one model's state over the last cycles — the same reading
// the banner takes, without the window-scoped latency comparison and without
// the prose.
//
// It exists so the model cards can never publish a greener state than the
// banner above them. The card scores the SELECTED window behind floors of its
// own — MIN_ATTEMPTS_FOR_STATE and the confidence bound for availability,
// MIN_FAILURES_FOR_STATE for correctness — while the banner scores the last
// RECENT_CYCLES behind a floor of ELEVATED_RECENT, and those two disagreeing is
// not a bug in either number: it is two questions with two honest answers,
// printed as one word in the same chip. Matching the thresholds cannot fix it
// either — a bound over a day of cycles and a count over the last hour are not
// commensurable, and any banner floor low enough to close the gap leaves the
// same gap one notch up, where DEGRADED_RECENT already claims the count. So the
// card folds this in instead, and the invariant holds whatever the thresholds
// become. That matters more now than it did: the window score was deliberately
// loosened, and this fold is the only reason loosening it cannot leave the chip
// greener than the sentence above it.
//
// The fold now runs both ways — scoreModel reads the window's availability and
// correctness off the fixed `now` block — so the two surfaces disagree in one
// place only, and deliberately: the card's figures describe the SELECTED
// window, the banner's the fixed 24 hours.
// stillHappening is "is this track still producing events": one failed run, or
// one wrong answer, anywhere in the SERVED block. It does not judge — it says
// whether a judgement about the WINDOW is still describing the present, which
// is why it sits below every threshold that makes a state on its own.
//
// Per track and over the whole block, for the two reasons scoreModel sets out:
// a wrong answer is no evidence about dropped runs, and an hour-wide cutoff
// makes a steady low failure rate blink amber and green all day.
export function stillHappening(
  modelId: string,
  recent: RecentCycle[],
  track: "failures" | "wrong",
): boolean {
  return modelTracks(modelId, recent)[track].lastRedAgo !== null;
}

export function scoreModelRecent(
  modelId: string,
  recent: RecentCycle[],
): State {
  const { failures, wrong } = modelTracks(modelId, recent);
  return worst(
    scoreTrack(failures, FAILURE_THRESHOLDS),
    scoreTrack(wrong, WRONG_THRESHOLDS),
  );
}

// cleanHour is the headline's evidence: the reader is told the endpoint is
// answering, and this is the count behind it.
//
// Only when the hour really is clean — a lone failure has its own sentence in
// quietFailures below, and printing both would have the banner congratulating
// itself one line above the run it lost.
function cleanHour(summary: Summary, recent: RecentCycle[]): string[] {
  const horizon = Math.min(RECENT_CYCLES, recent.length);
  if (horizon === 0) return [];
  for (const model of summary.models) {
    const { failures, wrong } = modelTracks(model.model_id, recent);
    if (failures.count > 0 || wrong.count > 0) return [];
  }
  if (track(recent, infraRed).count > 0) return [];
  const when =
    horizon === RECENT_CYCLES
      ? "the last hour"
      : `the last ${horizon} ${plural(horizon, "cycle")}`;
  return [`Every run finished and every answer was correct over ${when}.`];
}

// quietFailures is what a lone failed RUN gets instead of a chip.
//
// The network layer has its own version of this sentence in the normal branch
// above, keyed on infra.lastRedAgo, and it only speaks for cycles that failed
// at the network layer. A run that failed after the handshake — MiMo answered
// the connection and then the inference call did not come back — leaves that
// track clean, so below ELEVATED_RECENT it used to leave the banner with
// nothing at all to say. That is the forgetting the headline's own comment
// forbids: a banner gone quiet must not be indistinguishable from one that
// never had anything to report.
//
// Only failures, and deliberately not wrong answers: a single wrong answer has
// been silent since ELEVATED_WRONG_RECENT was set to 2, and silence is the
// softener there.
//
// Scored, not merely counted, so this can be appended to ANY verdict without
// saying the same thing twice: a model at or above the threshold already has
// its own sentence from scoreModel, and only the ones the threshold silenced
// come through here. That matters because the banner can be elevated for one
// model while another quietly drops a run, and the second one vanishing is the
// same forgetting this function exists to prevent.
function quietFailures(summary: Summary, recent: RecentCycle[]): string[] {
  const horizon = Math.min(RECENT_CYCLES, recent.length);
  const lines: string[] = [];
  for (const model of summary.models) {
    const { failures } = modelTracks(model.model_id, recent);
    if (failures.count === 0) continue;
    if (scoreTrack(failures, FAILURE_THRESHOLDS) !== "normal") continue;
    // ...and not while the WINDOW has already spoken for this model. "One run
    // is not yet a pattern" under "did not finish 9 of its last 288 runs"
    // disclaims the evidence the chip beside it is standing on — the same
    // self-contradiction the softener exists to prevent, pointing the other
    // way.
    if (
      failures.lastRedAgo !== null &&
      scoreAvailability(model.succeeded, model.attempts) !== "normal"
    ) {
      continue;
    }
    const when =
      failures.lastRedMinutes === null
        ? "just now"
        : agoWords(failures.lastRedMinutes);
    lines.push(
      `${model.model_id} failed ${failures.count} of the last ${horizon} ${plural(horizon, "run")}, ${when}. One run is not yet a pattern.`,
    );
  }
  return lines;
}

// scoreModel judges one model on BOTH horizons: the recent cycles, which say
// whether something is happening now, and the fixed window's own figures,
// which say whether enough went wrong today to be worth a word. The two ask
// different questions and neither answers the other.
//
// The window half was missing, and its absence was a severity inversion the
// page published: six runs of 288 cut off by the timeout ladder put an
// ELEVATED chip on the model card, while the banner over it announced that the
// OTHER model — the fast one, still answering in two seconds — was slower than
// it had been that morning. The louder claim was the smaller problem, and the
// bigger one was a chip the reader had to scroll to. The banner is the only
// surface allowed to state a state, so it has to know everything the chips
// below it know.
//
// Same floors as the card, because it is the same reading: the confidence
// bound for availability (scoreAvailability) and MIN_FAILURES_FOR_STATE for
// correctness. Same fixed window too — `now` is 24h and never the selected
// range, so nothing here can be held open for three months by one bad cycle,
// which is the stickiness the recent-scoped rules above were written to end.
function scoreModel(
  model: ModelSummary,
  baseline: ModelSummary | null,
  recent: RecentCycle[],
): { state: State; detail: string[] } {
  const detail: string[] = [];
  const horizon = Math.min(RECENT_CYCLES, recent.length);

  const { failures, wrong } = modelTracks(model.model_id, recent);
  const availability = scoreTrack(failures, FAILURE_THRESHOLDS);
  if (availability !== "normal") {
    // No softener, and none is reachable to need one: FAILURE_THRESHOLDS puts
    // elevated at ELEVATED_RECENT, which is 2, so a lone failed run scores
    // normal and this sentence never speaks about one. Silence is the softener
    // here, exactly as it already was for wrong answers below.
    detail.push(
      `${model.model_id} failed ${failures.count} of the last ${horizon} ${plural(horizon, "run")}.`,
    );
  }

  const correctness = scoreTrack(wrong, WRONG_THRESHOLDS);
  if (correctness !== "normal") {
    // No "one is not yet a pattern" softener here, unlike the two branches
    // above, and none is needed: ELEVATED_WRONG_RECENT is 2, so a single wrong
    // answer scores normal and this sentence never speaks at all. Silence is
    // the softener.
    detail.push(
      `${model.model_id} answered ${wrong.count} of the last ${horizon} questions wrongly.`,
    );
  }

  const ttft = scoreRatio(model.ttft.p50_ms, baseline?.ttft.p50_ms ?? null);
  if (ttft !== "normal" && ttft !== "unknown") {
    const pct = Math.round(
      ((model.ttft.p50_ms ?? 0) / (baseline?.ttft.p50_ms ?? 1) - 1) * 100,
    );
    detail.push(
      `${model.model_id} first tokens are taking ${pct}% longer than its 7-day normal.`,
    );
  }

  // The window's own two figures, scored exactly as the model card scores
  // them. They reach back a fixed 24 hours, so they keep speaking after the
  // recent block has forgotten — which is the point: a burst of cut-off runs
  // three hours ago is over as an event and still the most important thing
  // about the day.
  //
  // Counts, not available_pct: scoreAvailability needs both integers to tell
  // three cut-off runs in a day from an endpoint that simply sits under the
  // band, and the percentage is derived from the same two anyway.
  //
  // Gated on the failures still being LIVE. Six cut-off runs that all landed
  // ten hours ago are a fact about the day and not a state anything is in, and
  // a banner that says "showing early signs of trouble" over a morning that has
  // been clean since is answering a question nobody asked. The window decides
  // whether the day was bad enough to matter; the block below decides whether
  // it is still going. Both, or neither speaks.
  //
  // Per TRACK, not one flag for the pair: a wrong answer says nothing about
  // whether the endpoint is still dropping runs, and one shared gate let a
  // single wrong answer twenty minutes old unlock a sentence about six runs cut
  // off before breakfast — the stale claim this gate exists to stop, wearing
  // the other track's evidence.
  //
  // Dated off the WHOLE served block rather than the last RECENT_CYCLES.
  // A one-hour cutoff sounds tighter and flaps: at nine scattered failures a
  // day, each one holds the gate open for an hour and drops it at minute 61, so
  // the banner and both card chips blink amber and green all day over a window
  // score that never moved. The served block reaches back hours, which is long
  // enough that an ongoing low rate reads as ongoing, and still far short of
  // the day the window figure covers.
  const windowAvailability =
    failures.lastRedAgo !== null
      ? scoreAvailability(model.succeeded, model.attempts)
      : "normal";
  if (windowAvailability !== "normal" && windowAvailability !== "unknown") {
    const unfinished = model.attempts - model.succeeded;
    // Cut off and dropped are not the same event, and one must not be rounded
    // into the other: a censored run reached MiMo and was still going when our
    // own timeout ladder ended it, which is an answer we refused to wait for
    // rather than an endpoint that never came back.
    const cut =
      model.censored <= 0
        ? ""
        : model.censored >= unfinished
          ? " — all of them cut off by the timeout limits"
          : ` — ${model.censored} of them cut off by the timeout limits`;
    detail.push(
      `${model.model_id} did not finish ${unfinished} of its last ${model.attempts} ${plural(model.attempts, "run")}${cut}.`,
    );
  }

  const windowCorrectness =
    wrong.lastRedAgo !== null
      ? scoreCorrectness(model.correct_pct, model.answered - model.correct)
      : "normal";
  if (windowCorrectness !== "normal" && windowCorrectness !== "unknown") {
    const missed = model.answered - model.correct;
    detail.push(
      `${model.model_id} missed the expected fact in ${missed} of its last ${model.answered} ${plural(model.answered, "answer")}.`,
    );
  }

  return {
    state: worst(
      availability,
      correctness,
      ttft,
      windowAvailability,
      windowCorrectness,
    ),
    detail,
  };
}
