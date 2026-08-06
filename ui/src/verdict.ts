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
export const AVAILABILITY_ELEVATED = 99.5;
export const AVAILABILITY_DEGRADED = 98;
export const CORRECTNESS_ELEVATED = 98;
export const CORRECTNESS_DEGRADED = 95;

// The floor under every percentage on this page.
//
// A percentage over a small numerator is a rounding error with a decimal point.
// At 288 cycles a day, ONE failed run is 99.65% — under the elevated band — so
// the bands alone paint a chip on a single dropped connection, every day,
// forever. The band says how bad; this says whether anything actually happened.
export const MIN_FAILURES_FOR_STATE = 3;

export function scoreAvailability(pct: number | null, failures: number): State {
  if (pct === null || !Number.isFinite(pct)) return "unknown";
  if (failures < MIN_FAILURES_FOR_STATE) return "normal";
  if (pct < AVAILABILITY_DEGRADED) return "degraded";
  if (pct < AVAILABILITY_ELEVATED) return "elevated";
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
// The banner is allowed to forget. The availability strip below it does not.
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

// One failure in the hour is worth saying out loud and nothing more.
export const ELEVATED_RECENT = 1;

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
// to config.ProbeTimeout each — 240 s, doubled on a wide cycle. At two
// intervals a single timed-out wide cycle pushes the newest stored started_at
// past the threshold and the banner announces "the probe itself may be down"
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
  // The two disagree exactly when a slot was dropped — which the scheduler
  // logs rather than pretending it did not happen — so an
  // index-derived "20 minutes ago" can describe a failure from an hour back,
  // understating it in the direction that flatters.
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
      // Forgetting is only acceptable if the page says what it forgot. Without
      // this line, a banner that has gone quiet is indistinguishable from one
      // that never had anything to report.
      headline: "Everything looks normal right now",
      detail:
        infra.lastRedAgo !== null
          ? [
              // No singular branch, and none is reachable: lastRedAgo is an
              // index into the whole served block while count covers the first
              // RECENT_CYCLES of it, so a red anywhere near the front makes the
              // network branch above return first. Getting here at all means
              // the last red is at least RECENT_CYCLES back.
              `The last failed cycle was ${agoWords(infra.lastRedMinutes ?? infra.lastRedAgo * 5)}; the ${infra.lastRedAgo} since have all been clean.`,
            ]
          : detail,
    };
  }
  // The headline follows the severity. At elevated the evidence is one failure
  // inside the hour, and "having problems" claims more than that — the same
  // over-claim the network branch makes a point of not making one line above.
  return {
    state,
    headline: `${struggling.join(" and ")} ${struggling.length > 1 ? "are" : "is"} ${
      state === "degraded"
        ? "having problems right now"
        : "showing the odd failure right now"
    }`,
    detail,
  };
}

// faultVerdict speaks for the network layer, where the precedence that matters
// is which layer failed rather than how much: uplink -> route -> edge, because
// each one makes the ones beyond it unreadable.
//
// The HEADLINE still follows the severity, exactly as the model branch above
// does. At elevated the whole evidence is one failed cycle inside the hour, and
// "MiMo's edge is unreachable" over a detail line reading "One cycle is not yet
// a pattern" is the banner contradicting itself in public — the present-tense
// absolute claim being the half a reader remembers.
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
  } else if (t.count > 1) {
    detail.push(
      `${t.count} of the last ${horizon} ${plural(horizon, "cycle")} failed, the most recent ${when}.`,
    );
  } else {
    detail.push(
      `1 of the last ${horizon} ${plural(horizon, "cycle")} failed, ${when}. One cycle is not yet a pattern.`,
    );
  }

  if (fault === FAULT_UPLINK || fault === FAULT_ROUTE) {
    const headline =
      fault === FAULT_UPLINK
        ? sustained
          ? "Nothing in Singapore was reachable — this says nothing about MiMo"
          : "A cycle reached nothing in Singapore — this says nothing about MiMo"
        : sustained
          ? "The route to Singapore is degraded — not MiMo, and not us"
          : "A cycle found no route to Singapore — not MiMo, and not us";
    // Singular at elevated, like the edge branch below: one cycle described in
    // the plural is the same over-claim the headline just stopped making.
    const those = t.count === 1 ? "That cycle" : "Those cycles";
    detail.push(
      fault === FAULT_UPLINK
        ? `${those} reached neither MiMo nor the reference host, so ${t.count === 1 ? "it is" : "they are"} excluded from availability. From one vantage point our own connection and the route to Singapore look identical, and neither is MiMo's to answer for.`
        : `${those} could not reach MiMo's edge OR an unrelated Singapore host.`,
    );
    // A mixed run has to disclose the mix, or the headline claims more than the
    // evidence supports.
    const edge = counts[FAULT_EDGE] ?? 0;
    if (edge > 0) {
      detail.push(
        `${edge} of them did reach a second Singapore host, so MiMo's own edge was down for ${edge === 1 ? "that cycle" : "those cycles"} too.`,
      );
    }
    return { state, headline, detail };
  }

  detail.push(
    t.count === 1
      ? "It failed to reach MiMo while a second Singapore host answered."
      : "They failed to reach MiMo while a second Singapore host answered.",
  );
  const unattributed = (counts[FAULT_UPLINK] ?? 0) + (counts[FAULT_ROUTE] ?? 0);
  if (unattributed > 0) {
    detail.push(
      `${unattributed} of them reached neither host, so ${unattributed === 1 ? "it cannot" : "they cannot"} be attributed to MiMo.`,
    );
  }
  return {
    state,
    headline: sustained
      ? "MiMo's edge is unreachable — the route to Singapore is fine"
      : "MiMo's edge missed a cycle — the route to Singapore is fine",
    detail,
  };
}

// scoreModel reads availability and correctness from the recent cycles rather
// than from the window's percentages: the banner is about now, and a percentage
// over a day of cycles cannot say whether the failures in it are still
// happening. The latency comparison stays on the window, because a percentile
// needs more samples than an hour holds.
function scoreModel(
  model: ModelSummary,
  baseline: ModelSummary | null,
  recent: RecentCycle[],
): { state: State; detail: string[] } {
  const detail: string[] = [];
  const horizon = Math.min(RECENT_CYCLES, recent.length);

  // A model cannot be charged for a cycle where nothing in Singapore answered:
  // the run failed on connect, before it ever reached MiMo.
  const failures = track(
    recent,
    (c) => !unattributable(c) && c.models[model.model_id]?.ok === false,
  );
  const availability = scoreTrack(failures, FAILURE_THRESHOLDS);
  if (availability !== "normal") {
    // The same softener the network branch carries, for the same reason: one
    // failure is an anecdote whichever layer it came from, and a sentence that
    // does not say so reads as a verdict.
    detail.push(
      failures.count === 1
        ? `${model.model_id} failed 1 of the last ${horizon} ${plural(horizon, "run")}. One run is not yet a pattern.`
        : `${model.model_id} failed ${failures.count} of the last ${horizon} ${plural(horizon, "run")}.`,
    );
  }

  const wrong = track(
    recent,
    (c) => c.models[model.model_id]?.answer_ok === false,
  );
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

  return { state: worst(availability, correctness, ttft), detail };
}
