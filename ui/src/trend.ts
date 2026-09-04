// "Is it slower than it was?" — the reading the rest of the page cannot give.
//
// Every other latency judgement here compares a window against a 7-day baseline
// and speaks only past 1.5x (verdict.ts). That answers "is this bad" and is
// silent on the state a reader actually asks about: nothing is failing, but it
// is slower than it was this morning. This module turns the daemon's trend
// block into that sentence.
import type { ModelTrend, Trend, TrendMetric } from "./api/types";
import { plural } from "./format";

// How far a metric has to move before it is worth a word — MEASURED, not
// chosen, and per metric because the two are not comparable.
//
// Seven days of live readings were replayed as this exact statistic (a 3-hour
// median against the 24 hours before it) and the ordinary spread came out:
//
//   first token   median deviation 20-35%, p90 47-165%, worst in a week 192%
//   throughput    median deviation 10-12%, p90 18-26%,  worst in a week  35%
//
// So a round 10% floor would have put the banner in its slower state on 70% of
// readings — an amber page as the resting state, which is the failure this
// feature exists to prevent. At these floors it fires on roughly 12% of
// readings for first token and 18% for throughput.
//
// First token is simply too noisy at three hours for a polite 20% warning to
// exist: at +25% it fires half the time, which says nothing. The BOTH floor is
// the concession that buys back sensitivity — both models moving together is
// rarer than either moving alone (1-11% of readings at +40%) and is also the
// more meaningful event, since one model drifting is rarely an endpoint story.
//
// Recompute these against a month of raw samples before trusting them to the
// percentage point; a week of half-hour buckets is enough to reject 10%, not to
// defend 70% exactly.
export const TTFT_FLOOR = 0.7;
export const TTFT_FLOOR_BOTH = 0.4;
export const TPS_FLOOR = 0.2;

// How slow the reading itself has to BE before the move that produced it may
// take the banner. The floors above are relative; these two are absolute, and
// the page needs both.
//
// "mimo-v2.5 is slow to start right now" was published over a first token of
// 2016 ms, against 954 ms the day before. Every word of the measurement was
// true — it had doubled, it was well past its floor — and the claim in the
// largest type on the page was false: two seconds to first token is fast, and
// the model whose card sat underneath it starts in three and a half. A
// slowdown is only news if what it left behind is slow.
//
// So a relative floor decides whether something MOVED and these decide whether
// the reader would call the result slow. Chosen, not measured, unlike the
// floors — no replay can tell you how long a wait has to be before someone
// minds it, because that is a fact about the reader. They are set where the
// headline stops being embarrassing to say next to the figure it quotes.
//
// Per metric, because a first token and a token rate are not the same wait: an
// answer that starts inside three seconds is prompt however yesterday looked,
// and text arriving at 40 tokens a second is faster than anyone reads.
//
// Nothing is hidden by them. A move that clears its floor and not this still
// gets its sentence, with its figures and its cost, in the same slot the steady
// reading uses — it just does not get the headline, the chip or the plot.
export const SLOW_TTFT_MS = 3000;
export const SLOW_TPS = 40;

// The tail: the last hour of buckets, and the only reason the banner is allowed
// to say "right now".
//
// A median over a fixed three-hour box cannot follow the edge of the box. A
// spike that ended an hour ago still owns that median until it drops below half
// the window, so the page went on announcing a slowdown for up to another hour
// and a half — with the recovery drawn, flat, in its own plot underneath. The
// mirror of it is the same fault pointing the other way: a slowdown that
// started twenty minutes ago moves a three-hour median by almost nothing, and
// the banner was as late to fire as it was to clear.
//
// So the three hours say WHAT moved, and the last hour says whether it is still
// moving. Quarter-hour buckets carry about three successful runs each
// (TrendSeriesWindow, samples/trend.go), which makes the tail a ~12-sample
// reading: enough to withdraw a claim, and only enough to raise one when every
// bucket in it agrees.
export const TAIL_S = 3600;

// How many successful runs the tail needs before it may speak at all.
//
// Counted in SAMPLES, not in buckets, so the gate cannot silently go inert if
// bucket_s ever widens — a rule phrased as "the last four buckets" becomes a
// rule about two of them, or none. Below this the tail is null, and a null tail
// changes NOTHING in either direction: it neither withdraws a claim nor raises
// one.
export const TAIL_MIN_SAMPLES = 8;

// What the tail must fall to before it withdraws a fired reading, as a share of
// the floor that fired it. Hysteresis rather than tidiness: at 1.0 a reading
// sitting on its floor would flicker between the two states as single buckets
// rolled off the hour.
//
// This one is DERIVED from the floors above, not measured like them — the week
// of readings behind them was replayed as a three-hour statistic, never as an
// hourly one. Replay the hourly tail before trusting the halves.
export const TAIL_CLEAR = 0.5;

// A full-length answer, so a throughput move can be stated in seconds. Output
// is capped at probe.MaxTokens on the daemon; a percentage on tokens per second
// is not something anyone feels, and the wait is.
export const OUTPUT_TOKENS = 150;

export type SpeedMetric = "ttft" | "tps";

// One metric on one model that moved past its floor.
export type SpeedMove = {
  modelID: string;
  metric: SpeedMetric;
  recent: number;
  before: number;
  // The span `recent` was measured over: the compared hours normally, and
  // TAIL_S when the last hour fired this on its own. The sentence names it and
  // the plot shades it, so the two can never describe different hours.
  spanS: number;
  // Fractional change, always signed so that POSITIVE IS WORSE — a longer first
  // token and a lower throughput both come out positive. Colour is never the
  // only signal on this page, and neither is a sign.
  worseBy: number;
  // What it adds to one full-length answer, in seconds. This is what the moves
  // are ranked by: percentages lie about importance because the metrics are
  // different sizes — a 50% first-token jump adds 0.4 s, a 30% throughput drop
  // adds three.
  secondsAdded: number;
};

export type SpeedReading = {
  // "slower" is a word this page did not have. It is deliberately not
  // "elevated", which is spent on faults: a slowdown with nothing failing is a
  // different claim, and reusing the fault vocabulary for it is how a reader
  // stops believing the fault vocabulary.
  //
  // "quicker" is measured and never spoken — it carries no lead and no line. It
  // exists so that "steady" keeps meaning what it says.
  // "recovered" is a slowdown the last hour has already undone: the compared
  // median is still elevated because the spike is still inside it, and the
  // endpoint is fine. It carries no badge and no headline — it is the past
  // tense, and the page answers a present-tense question.
  // "minor" is a move that cleared its floor while the reading it produced is
  // still quick in absolute terms. It carries no badge, no headline, no plot
  // AND no sentence: 20% fewer tokens per second, costing six tenths of a
  // second on a full-length answer, is a true measurement and not news, and
  // printing it under the headline in the page's most important box asks the
  // reader to care about something the page has just decided does not matter.
  //
  // It exists for the same reason "quicker" does, and does exactly as much: it
  // keeps the steady sentence from claiming a spread the reading is outside of.
  // The plots below carry the shape for anyone who wants it.
  state: "slower" | "quicker" | "recovered" | "minor" | "steady" | "unknown";
  // The whole answer, in one line, largest type on the page.
  lead: string;
  // One sentence under it: the numbers, what they cost in seconds, and what is
  // NOT wrong. Never a bullet list — the reader should not have to assemble the
  // answer from parts.
  line: string;
  // What to plot, and what the legend says. Empty when there is nothing to draw.
  moves: SpeedMove[];
  // The metric the plot draws. One plot, so one metric: the largest move's.
  metric: SpeedMetric | null;
  // The span the plot must shade — the lead move's, which is not always the
  // payload's recent_s. Null when there is nothing to draw.
  spanS: number | null;
  // Did every model in the block produce a reading at all?
  //
  // A model whose spans are too thin leaves no candidate, and the reading is
  // then decided by the models that did produce one — which is right for
  // "slower" (something IS slow) and wrong for any sentence that speaks for
  // the fleet. The banner's "both models are behaving as usual" is exactly
  // that sentence, and without this it claimed a model nothing had measured.
  everyModelRead: boolean;
};

function value(m: TrendMetric, side: "recent" | "before"): number | null {
  const stats = m[side];
  return stats.sufficient && stats.p50_ms !== null ? stats.p50_ms : null;
}

// Higher is worse for a first token, lower is worse for a throughput, and
// nothing downstream of this has to know which is which again.
function worseOf(metric: SpeedMetric, v: number, before: number): number {
  return metric === "ttft" ? v / before - 1 : before / v - 1;
}

// Is the reading itself slow, in the units a reader waits in? Higher is slower
// for a first token and lower is slower for a throughput, the same asymmetry
// worseOf carries, and for the same reason nothing downstream has to know it.
function isSlow(m: SpeedMove): boolean {
  return m.metric === "ttft" ? m.recent >= SLOW_TTFT_MS : m.recent <= SLOW_TPS;
}

// One metric on one model before any floor has touched it: the two medians a
// sentence would quote, plus the last hour read off the plot's own buckets.
type Candidate = {
  modelID: string;
  metric: SpeedMetric;
  before: number;
  window: number;
  // The tail's median and the bucket medians behind it, or null and empty when
  // the hour is too thin to read — see TAIL_MIN_SAMPLES.
  tail: number | null;
  tailBuckets: number[];
};

// The last hour of buckets, taken by OVERLAP rather than by start: buckets are
// floored to bucket_s (samples/queries.go) and the hour boundary is not on that
// grid, so reading a straddling bucket by its start drops it from an hour it
// holds runs from.
//
// The value is the median of the bucket MEDIANS, which is what the payload
// carries at this resolution; the daemon's own nearest rank is used on them so
// that the two agree about what a median is.
function tailOf(
  m: TrendMetric,
  generatedAtS: number,
  bucketS: number,
): { value: number; buckets: number[] } | null {
  // An unparseable stamp or a missing bucket width leaves the tail unread,
  // which is the safe way to be wrong: no claim is withdrawn and none is
  // raised.
  if (!Number.isFinite(generatedAtS) || bucketS <= 0) return null;
  const fromS = generatedAtS - TAIL_S;
  const inTail = m.points.filter((p) => p.t + bucketS > fromS);
  const samples = inTail.reduce((sum, p) => sum + p.n, 0);
  // A bucket with no successful run has no median to contribute — it still
  // counts in `samples` as the zero it is, so an hour of failures reads as too
  // thin rather than as a fast one.
  const buckets = inTail.flatMap((p) => (p.p50 === null ? [] : [p.p50]));
  if (samples < TAIL_MIN_SAMPLES || buckets.length === 0) return null;
  const sorted = [...buckets].sort((a, b) => a - b);
  return { value: sorted[Math.ceil(sorted.length / 2) - 1]!, buckets };
}

function candidateFor(
  model: ModelTrend,
  metric: SpeedMetric,
  generatedAtS: number,
  bucketS: number,
): Candidate | null {
  const block = model[metric];
  const recent = value(block, "recent");
  const before = value(block, "before");
  if (recent === null || before === null || before <= 0 || recent <= 0) {
    return null;
  }
  const tail = tailOf(block, generatedAtS, bucketS);
  // Every ratio here divides by a bucket median, so a zero or negative one is
  // dropped rather than divided by. It cannot come out of a real measurement,
  // and an Infinity reaching the floors would fire the banner on nothing.
  const usable =
    tail !== null && tail.value > 0 && tail.buckets.every((b) => b > 0)
      ? tail
      : null;
  return {
    modelID: model.model_id,
    metric,
    before,
    window: recent,
    tail: usable?.value ?? null,
    tailBuckets: usable?.buckets ?? [],
  };
}

// A candidate, plus the decision about WHICH of its two readings the sentence
// quotes. Everything downstream reads the move and never the candidate, so a
// figure and the span it was measured over cannot drift apart.
function makeMove(c: Candidate, recent: number, spanS: number): SpeedMove {
  return {
    modelID: c.modelID,
    metric: c.metric,
    recent,
    before: c.before,
    spanS,
    worseBy: worseOf(c.metric, recent, c.before),
    secondsAdded:
      c.metric === "ttft"
        ? (recent - c.before) / 1000
        : OUTPUT_TOKENS / recent - OUTPUT_TOKENS / c.before,
  };
}

function floorFor(metric: SpeedMetric, bothMoved: boolean): number {
  if (metric === "tps") return TPS_FLOOR;
  return bothMoved ? TTFT_FLOOR_BOTH : TTFT_FLOOR;
}

function seconds(v: number): string {
  const abs = Math.abs(v);
  return abs < 10 ? `${abs.toFixed(1)} s` : `${Math.round(abs)} s`;
}

// The same rounding seconds() prints, as a NUMBER, so the wait a sentence
// derives can be subtracted from the two medians the same sentence just showed.
//
// Both readings used to print in milliseconds and the wait in seconds, so no
// reader ever put the three together. In one unit they will: 3449 ms and
// 1651 ms round to "3.4 s" and "1.7 s" while their true difference rounds to
// "1.8 s", and the sentence contradicts itself in a subtraction a reader can do
// in their head. Rounding first and subtracting after costs a hundredth of a
// second of accuracy and keeps the line checkable against itself — the same
// trade the multi-model cost already makes by comparing WORDS and not seconds.
function roundSeconds(ms: number): number {
  const s = Math.abs(ms) / 1000;
  return s < 10 ? Math.round(s * 10) / 10 : Math.round(s);
}

// A median in the reader's unit. Never milliseconds: the wait beside it is
// spoken in seconds, and one sentence carrying both units asks the reader to
// convert before they can tell whether the two agree.
function median(ms: number): string {
  return seconds(roundSeconds(ms));
}

// hours turns the payload's span into the words the sentence uses, so a change
// to TrendRecent on the daemon changes the copy rather than contradicting it.
// A day is said as one, not as the 24 hours it is made of: "against 2.0 s over
// the 24 hours before" makes the reader count, and the reference span is the
// one figure in the sentence nobody is checking.
export function spanWords(seconds: number): string {
  const h = Math.round(seconds / 3600);
  if (h === 24) return "day";
  return h <= 1 ? "hour" : `${h} ${plural(h, "hour")}`;
}

// Only the slow words. A speed-up is measured (see below) and never said, so
// there is no quick half of this to keep in step.
const METRIC_WORDS: Record<SpeedMetric, { slow: string }> = {
  ttft: { slow: "slow to start" },
  tps: { slow: "generating more slowly" },
};

// One model's move: the reading it is at now, against the one it was at.
//
// The per cent is gone. It said the same thing as the pair of medians beside
// it, which said the same thing as the wait in the sentence after — one
// measurement published three ways, which is why rewording the line never
// shortened it. The two medians stay because they are the only figures a reader
// can act on; the ratio between them is arithmetic they did not ask for.
//
// The compared span goes with it. The present tense carries it ("is at"), and
// naming it was a standing hazard: a reading the last hour raised on its own
// quotes the last hour, so a clause was always one refactor away from
// describing three hours it never measured.
//
// Used when BOTH models moved, so every clause carries its own model's name and
// its own figures — one pair of readings printed under "Both models" described a
// measurement only one of them took. The reference span rides on the first
// clause only; repeating it in the second says nothing new.
function movePhrase(m: SpeedMove, refSpan: string, showRef: boolean): string {
  const against = showRef ? ` over the ${refSpan} before` : "";
  return m.metric === "ttft"
    ? `${m.modelID}'s first token is at ${median(m.recent)}, against ${median(m.before)}${against}`
    : `${m.modelID} is producing ${m.recent.toFixed(1)} tokens per second, against ${m.before.toFixed(1)}${against}`;
}

// The past tense: what moved, and that it is over.
//
// It says the whole thing in one clause rather than leaving the reader to
// notice that a figure is stale — the compared median IS still elevated, and a
// page that printed it without the last hour beside it would be publishing a
// number that contradicts its own headline.
function recoveredPhrase(m: SpeedMove, refSpan: string): string {
  const span = spanWords(m.spanS);
  const tail = spanWords(TAIL_S);
  const what =
    m.metric === "ttft"
      ? `${m.modelID}'s first token was slower earlier in the last ${span} — ${median(m.recent)} against ${median(m.before)} over the ${refSpan} before`
      : `${m.modelID} was generating more slowly earlier in the last ${span} — ${m.recent.toFixed(1)} against ${m.before.toFixed(1)} over the ${refSpan} before`;
  return `${what} — and has been back to normal for the last ${tail}.`;
}

// buildSpeedReading turns the trend block into the banner's sentence.
//
// It reports SPEED and nothing else. Availability and correctness are scored
// against a stated target rather than against yesterday — a trend on those
// would let a run of bad days become the new normal — and a failure takes the
// banner outright, above this, in verdict.ts.
export function buildSpeedReading(
  trend: Trend | null | undefined,
): SpeedReading {
  const models = trend?.models ?? [];
  const span = spanWords(trend?.recent_s ?? 0);
  const refSpan = spanWords(trend?.before_s ?? 0);

  if (models.length === 0) {
    return {
      state: "unknown",
      lead: "",
      line: "",
      moves: [],
      metric: null,
      spanS: null,
      everyModelRead: false,
    };
  }

  // Off the payload rather than off a constant here — the daemon owns the
  // spans. An unparseable stamp leaves generatedAtS NaN, which makes every
  // comparison against it false: the last hour then reads as unmeasured and no
  // claim is raised, which is the safe way to be wrong.
  const generatedAtS = Date.parse(trend?.generated_at ?? "") / 1000;
  const recentS = trend?.recent_s ?? 0;
  const bucketS = trend?.bucket_s ?? 0;

  const all: Candidate[] = [];
  for (const model of models) {
    for (const metric of ["ttft", "tps"] as const) {
      const c = candidateFor(model, metric, generatedAtS, bucketS);
      if (c) all.push(c);
    }
  }
  // Both metrics on every model, not merely one candidate somewhere: a model
  // whose throughput is readable and whose first token is not has still not
  // been measured for the purposes of a sentence about the whole fleet.
  const everyModelRead =
    models.length > 0 &&
    models.every((m) =>
      (["ttft", "tps"] as const).every((metric) =>
        all.some((c) => c.modelID === m.model_id && c.metric === metric),
      ),
    );
  if (all.length === 0) {
    // Every span was too thin to produce a median. Said in words, never as a
    // zero — the page has nothing to compare, which is not the same as nothing
    // having changed.
    // Which side is thin is deliberately not claimed: moveFor returns nothing
    // when EITHER span misses the threshold, and a freshly restarted daemon has
    // the opposite problem from a fresh page — three good hours against a day
    // that barely exists.
    return {
      state: "unknown",
      lead: "Not enough answers yet to compare",
      line: `Comparing the last ${span} with the ${refSpan} before them needs more finished requests than one of those periods holds. The comparison appears once they are in.`,
      moves: [],
      metric: null,
      spanS: null,
      everyModelRead: false,
    };
  }

  // The floors are still read off the COMPARED span. What the tail decides is
  // whether the reading may be spoken in the present tense, not what counts as
  // a move — a floor measured on a three-hour statistic says nothing about an
  // hourly one.
  const windowWorse = (c: Candidate) => worseOf(c.metric, c.window, c.before);
  const worse = (metric: SpeedMetric) =>
    all.filter((c) => c.metric === metric && windowWorse(c) > 0);
  const bothMoved = (metric: SpeedMetric) =>
    models.length > 1 &&
    worse(metric).length === models.length &&
    worse(metric).every((c) => windowWorse(c) >= TTFT_FLOOR_BOTH);

  const move = (c: Candidate, recent: number, spanS: number) =>
    makeMove(c, recent, spanS);

  const fired: SpeedMove[] = [];
  const recovered: SpeedMove[] = [];
  for (const c of all) {
    const floor = floorFor(c.metric, bothMoved(c.metric));
    const tailWorse =
      c.tail === null ? null : worseOf(c.metric, c.tail, c.before);
    if (windowWorse(c) >= floor) {
      // Cleared, not merely lower: TAIL_CLEAR is the hysteresis that keeps a
      // reading sitting on its floor from flickering as buckets roll off. A
      // tail too thin to read (null) withdraws nothing.
      if (tailWorse !== null && tailWorse < floor * TAIL_CLEAR) {
        recovered.push(move(c, c.window, recentS));
      } else {
        fired.push(move(c, c.window, recentS));
      }
      continue;
    }
    // Onset: the compared median has not moved yet — a slowdown twenty minutes
    // old barely touches three hours — but every quarter-hour of the last one
    // has. An AND across the whole hour on purpose, and at the SAME floor: this
    // is a ~12-sample reading, and three samples must not be able to turn the
    // page amber on their own.
    const hot =
      c.tail !== null &&
      c.tailBuckets.length >= 2 &&
      c.tailBuckets.every((b) => worseOf(c.metric, b, c.before) >= floor);
    if (hot && c.tail !== null) fired.push(move(c, c.tail, TAIL_S));
  }
  // Ranked by seconds added to the wait, never by per cent — see SpeedMove.
  fired.sort((a, b) => b.secondsAdded - a.secondsAdded);

  // Which moves may LEAD: the ones whose reading is slow in its own units.
  //
  // Tested per move rather than on the biggest, because the ranking is
  // cross-metric and the floors are not. A throughput drop to 41 tok/s adds
  // more seconds than a first token going to 3100 ms, sorts above it, and is
  // not slow — and testing only the top of the list let it demote a first token
  // the page's own floor calls slow. Whichever slow move costs the most leads;
  // the rest ride along as they always did.
  const leadable = fired.filter(isSlow);
  if (fired.length > 0 && leadable.length === 0) {
    return {
      state: "minor",
      lead: "",
      line: "",
      moves: [],
      metric: null,
      spanS: null,
      everyModelRead: false,
    };
  }

  if (leadable.length > 0) {
    const lead = leadable[0]!;
    // "Both models" is a claim about THIS metric, not about the page. Saying it
    // whenever two models appear anywhere in the list asserts the lead's metric
    // of every model — so a page where one model started slowly and the other
    // generated slowly announced that both were generating slowly, which was
    // false about the first one.
    const sameMetric = fired.filter((m) => m.metric === lead.metric);
    const everyModel = models.length > 1 && sameMetric.length === models.length;
    const subject = everyModel ? "Both models are" : `${lead.modelID} is`;
    const words = METRIC_WORDS[lead.metric].slow;
    // Whatever the subject did not already cover. Without the metric filter a
    // model named by "Both models" was then named again on its own.
    const others = fired.filter(
      (m) => !(everyModel && m.metric === lead.metric) && m !== lead,
    );

    const parts: string[] = [];
    if (everyModel) {
      // Every model that moved, with ITS OWN readings. The pronoun this used to
      // open with — "Both models are slow to start… Its first token takes 100 %
      // longer, 1800 ms against 900 ms" — printed the lead's pair of medians as
      // though it described the whole fleet, while the other model was at 1400.
      parts.push(
        `${sameMetric
          .map((m, i) => movePhrase(m, refSpan, i === 0))
          .join(". ")}.`,
      );
    } else {
      parts.push(
        lead.metric === "ttft"
          ? `First token is at ${median(lead.recent)}, against ${median(lead.before)} over the ${refSpan} before.`
          : `It is producing ${lead.recent.toFixed(1)} tokens per second, against ${lead.before.toFixed(1)} over the ${refSpan} before.`,
      );
    }
    // One model's own cost, never the sum across models. No single request is
    // answered by both models, so adding their penalties together states a wait
    // nobody can ever experience — and it would grow with the size of the fleet
    // rather than with the slowdown.
    //
    // Off the ROUNDED medians for a first token, not off secondsAdded, so the
    // wait is the difference of the two figures the sentence printed and not a
    // third rounding of the same measurement. secondsAdded keeps its full
    // precision because the moves are ranked by it. A throughput move is left
    // alone: its cost is 150 tokens divided two ways, which is not a
    // subtraction anyone is going to check against the printed rates.
    //
    // It is still the model's WHOLE cost: a model whose first token and whose
    // decoding both moved makes the reader wait for both, and quoting half of
    // that understates a wait they are really having. That is the one case
    // where the wait is not the gap between the two medians above it — the
    // sentence says "in total" there, because the figures are now in one unit
    // and a reader who subtracts them would otherwise catch the line out.
    const costOf = (modelID: string) =>
      fired
        .filter((m) => m.modelID === modelID)
        .reduce(
          (sum, m) =>
            sum +
            (m.metric === "ttft"
              ? roundSeconds(m.recent) - roundSeconds(m.before)
              : m.secondsAdded),
          0,
        );
    const costs = (everyModel ? sameMetric : [lead]).map((m) => ({
      modelID: m.modelID,
      words: seconds(costOf(m.modelID)),
    }));
    // True the moment any quoted model is paying for more than the one metric
    // its medians were printed for.
    const summed = costs.some(
      (c) => fired.filter((m) => m.modelID === c.modelID).length > 1,
    );
    // "each" is only true when the models cost the same — the figure is one
    // model's, and handing it to the other one overstated a 0.5 s model as 0.9 s.
    // Compared as the WORDS, not the seconds: two waits that print identically
    // are identical as far as this sentence goes.
    const sameCost = costs.every((c) => c.words === costs[0]!.words);
    parts.push(
      sameCost
        ? `That is about ${costs[0]!.words} of extra waiting on a full-length answer${summed ? " in total" : ""}${everyModel ? ", each" : ""}.`
        : // The unit is said once. Repeating "of extra waiting on a full-length
          // answer" per model made the sentence longer than the reading it
          // qualifies.
          `That is about ${costs[0]!.words} of extra waiting on a full-length answer${summed ? " in total" : ""} for ${costs[0]!.modelID}, and ${costs
            .slice(1)
            .map((c) => `${c.words} for ${c.modelID}`)
            .join(", ")}.`,
    );
    if (others.length > 0) {
      parts.push(
        `${others.map((m) => `${m.modelID} is ${METRIC_WORDS[m.metric].slow}`).join(", ")} too.`,
      );
    }
    return {
      state: "slower",
      lead: `${subject} ${words} right now`,
      line: parts.join(" "),
      moves: fired,
      metric: lead.metric,
      spanS: lead.spanS,
      everyModelRead,
    };
  }

  // A slowdown the last hour has already undone. It outranks the quicker and
  // steady readings below because both of them would be false here: the
  // compared median really did move, and the reader who saw the amber banner an
  // hour ago is owed the other half of that sentence rather than silence.
  //
  // It carries no badge, no headline and no plot — the page answers a
  // present-tense question, and this is the past tense.
  if (recovered.length > 0) {
    return {
      state: "recovered",
      lead: "",
      line: recovered.map((m) => recoveredPhrase(m, refSpan)).join(" "),
      moves: [],
      metric: null,
      spanS: null,
      everyModelRead,
    };
  }

  // Quicker is measured and never said. The page answers one question, the
  // banner carries one claim, and "it is faster than it was this morning" is not
  // a thing anyone came here to read — announcing it in the same weight as a
  // slowdown is how a reader learns to skim the banner.
  //
  // It is still DETECTED, because the steady sentence below claims the last span
  // sits inside this endpoint's ordinary spread, and a reading that cleared its
  // floor in the good direction does not. Silence is the claim here; that
  // sentence would be a false one.
  //
  // Same floors, so it is exactly as rare — but measured the other way round,
  // because a fraction is not symmetric about zero: 900 ms falling to 500 is the
  // same size of move as 500 rising to 900, and only one of those is 80% by
  // division. Comparing -worseBy against the floor would make every recovery
  // look smaller than the slowdown it undid.
  const better = all.filter(
    (c) =>
      windowWorse(c) < 0 &&
      1 / (1 + windowWorse(c)) - 1 >= floorFor(c.metric, false),
  );
  if (better.length > 0) {
    return {
      state: "quicker",
      lead: "",
      line: "",
      moves: [],
      metric: null,
      spanS: null,
      everyModelRead: false,
    };
  }

  // No sentence: the banner says this in its headline now ("…and both models
  // are behaving as usual"), and printed here as well it said the same thing
  // twice, the second time as a statistic nobody asked for.
  return {
    state: "steady",
    lead: "",
    line: "",
    moves: [],
    metric: null,
    spanS: null,
    everyModelRead,
  };
}
