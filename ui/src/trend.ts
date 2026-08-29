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
  // Fractional change, always signed so that POSITIVE IS WORSE — a longer first
  // token and a lower throughput both come out positive. Colour is never the
  // only signal on this page, and neither is a sign.
  worseBy: number;
  // What it adds to one full-length answer, in seconds. This is what the moves
  // are ranked by: percentages lie about importance because the metrics are
  // different sizes — a 50% first-token jump adds 0.4 s, a 30% throughput drop
  // adds three.
  secondsAdded: number;
  // Which spans had runs cut off by the timeout ladder. Both medians are drawn
  // from the runs that FINISHED, so a censored recent span understates the move
  // and a censored reference day overstates it — see censoredIn.
  censored: { recent: boolean; before: boolean };
};

export type SpeedReading = {
  // "slower" is a word this page did not have. It is deliberately not
  // "elevated", which is spent on faults: a slowdown with nothing failing is a
  // different claim, and reusing the fault vocabulary for it is how a reader
  // stops believing the fault vocabulary.
  //
  // "quicker" is measured and never spoken — it carries no lead and no line. It
  // exists so that "steady" keeps meaning what it says.
  state: "slower" | "quicker" | "steady" | "unknown";
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
};

function value(m: TrendMetric, side: "recent" | "before"): number | null {
  const stats = m[side];
  return stats.sufficient && stats.p50_ms !== null ? stats.p50_ms : null;
}

// Censoring is read off the points rather than served as a count: each bucket
// carries its own, and summing them is free.
//
// Which SIDE it fell on decides what the sentence may claim, so the two are
// counted apart. Truncation removes the slowest runs from a median, so a
// censored recent span understates the change and a censored reference day
// overstates it — the same fact, pointing opposite ways.
//
// Counted by OVERLAP, not by where the bucket starts. Buckets are floored to
// bucket_s (samples/queries.go) and the boundary is generated_at minus the span,
// which is not on that grid: a bucket straddling it holds runs from both sides,
// and reading its start alone filed every one of them under the reference day.
// A cut-off run eight minutes into the compared span would then have flipped the
// caveat to "may be smaller than this" — the inversion this counts sides to
// avoid. Straddling means both are true, which lands on the hedged sentence, and
// that is the only claim such a bucket supports.
function censoredIn(
  m: TrendMetric,
  recentFromS: number,
  bucketS: number,
): { recent: boolean; before: boolean } {
  return {
    recent: m.points.some((p) => p.censored > 0 && p.t + bucketS > recentFromS),
    before: m.points.some((p) => p.censored > 0 && p.t < recentFromS),
  };
}

function moveFor(
  model: ModelTrend,
  metric: SpeedMetric,
  recentFromS: number,
  bucketS: number,
): SpeedMove | null {
  const block = model[metric];
  const recent = value(block, "recent");
  const before = value(block, "before");
  if (recent === null || before === null || before <= 0 || recent <= 0) {
    return null;
  }
  // Higher is worse for a first token, lower is worse for a throughput, and the
  // rest of this module never has to know which is which again.
  const worseBy = metric === "ttft" ? recent / before - 1 : before / recent - 1;
  const secondsAdded =
    metric === "ttft"
      ? (recent - before) / 1000
      : OUTPUT_TOKENS / recent - OUTPUT_TOKENS / before;
  return {
    modelID: model.model_id,
    metric,
    recent,
    before,
    worseBy,
    secondsAdded,
    censored: censoredIn(block, recentFromS, bucketS),
  };
}

function floorFor(metric: SpeedMetric, bothMoved: boolean): number {
  if (metric === "tps") return TPS_FLOOR;
  return bothMoved ? TTFT_FLOOR_BOTH : TTFT_FLOOR;
}

function pct(v: number): string {
  return `${Math.round(Math.abs(v) * 100)} %`;
}

// A ratio restated against the figure it started from.
//
// Both "fewer tokens per second" sentences claim a share of the OLD reading,
// while the ratio behind them was computed the other way round, against the new
// one. Spending one as the other published "100 % fewer tokens per second" for a
// rate that had halved, with "35.0 against 70.0" printed in the same breath;
// "100 % fewer" is no tokens at all. The first-token sentence ("longer") is
// already a share of the old reading and must NOT be passed through here.
//
// The FLOORS keep testing the symmetric figure — a fraction is not symmetric
// about zero, and comparing the two directions against one threshold is what
// that measurement is for. Only the words the reader sees change.
function ofBefore(v: number): number {
  return v / (1 + v);
}

function seconds(v: number): string {
  const abs = Math.abs(v);
  return abs < 10 ? `${abs.toFixed(1)} s` : `${Math.round(abs)} s`;
}

// hours turns the payload's span into the words the sentence uses, so a change
// to TrendRecent on the daemon changes the copy rather than contradicting it.
export function spanWords(seconds: number): string {
  const h = Math.round(seconds / 3600);
  return h <= 1 ? "hour" : `${h} ${plural(h, "hour")}`;
}

// Only the slow words. A speed-up is measured (see below) and never said, so
// there is no quick half of this to keep in step.
const METRIC_WORDS: Record<SpeedMetric, { slow: string }> = {
  ttft: { slow: "slow to start" },
  tps: { slow: "generating more slowly" },
};

// One model's move, in the reader's units: the per cent, then the two medians it
// was computed from, so the sentence can always be checked against itself.
//
// Used when BOTH models moved, so every clause carries its own model's name and
// its own figures — one pair of readings printed under "Both models" described a
// measurement only one of them took. The reference span rides on the first
// clause only; repeating it in the second says nothing new.
function movePhrase(m: SpeedMove, refSpan: string, withSpan: boolean): string {
  const against = withSpan ? ` than over the ${refSpan} before` : "";
  return m.metric === "ttft"
    ? `${m.modelID}'s first token takes ${pct(m.worseBy)} longer${against} — ${Math.round(m.recent)} ms against ${Math.round(m.before)} ms`
    : `${m.modelID} produces ${pct(ofBefore(m.worseBy))} fewer tokens per second${against} — ${m.recent.toFixed(1)} against ${m.before.toFixed(1)}`;
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
    return { state: "unknown", lead: "", line: "", moves: [], metric: null };
  }

  // Where the compared span begins, off the payload rather than off a constant
  // here — the daemon owns the spans. Only the censoring caveat reads it, and an
  // unparseable stamp leaves it NaN, which makes every comparison false: no side
  // is then claimed to be censored, which is the safe way to be wrong.
  const recentFromS =
    Date.parse(trend?.generated_at ?? "") / 1000 - (trend?.recent_s ?? 0);

  const all: SpeedMove[] = [];
  for (const model of models) {
    for (const metric of ["ttft", "tps"] as const) {
      const move = moveFor(model, metric, recentFromS, trend?.bucket_s ?? 0);
      if (move) all.push(move);
    }
  }
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
    };
  }

  const worse = (metric: SpeedMetric) =>
    all.filter((m) => m.metric === metric && m.worseBy > 0);
  const bothMoved = (metric: SpeedMetric) =>
    models.length > 1 &&
    worse(metric).length === models.length &&
    worse(metric).every((m) => m.worseBy >= TTFT_FLOOR_BOTH);

  const fired = all.filter(
    (m) => m.worseBy >= floorFor(m.metric, bothMoved(m.metric)),
  );
  // Ranked by seconds added to the wait, never by per cent — see SpeedMove.
  fired.sort((a, b) => b.secondsAdded - a.secondsAdded);

  if (fired.length > 0) {
    const lead = fired[0]!;
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
        `${sameMetric.map((m, i) => movePhrase(m, refSpan, i === 0)).join(". ")}.`,
      );
    } else {
      parts.push(
        lead.metric === "ttft"
          ? `Its first token takes ${pct(lead.worseBy)} longer than over the ${refSpan} before — ${Math.round(lead.recent)} ms against ${Math.round(lead.before)} ms.`
          : `It produces ${pct(ofBefore(lead.worseBy))} fewer tokens per second than over the ${refSpan} before — ${lead.recent.toFixed(1)} against ${lead.before.toFixed(1)}.`,
      );
    }
    // One model's own cost, never the sum across models. No single request is
    // answered by both models, so adding their penalties together states a wait
    // nobody can ever experience — and it would grow with the size of the fleet
    // rather than with the slowdown.
    const costOf = (modelID: string) =>
      fired
        .filter((m) => m.modelID === modelID)
        .reduce((sum, m) => sum + m.secondsAdded, 0);
    const costs = (everyModel ? sameMetric : [lead]).map((m) => ({
      modelID: m.modelID,
      words: seconds(costOf(m.modelID)),
    }));
    // "each" is only true when the models cost the same — the figure is one
    // model's, and handing it to the other one overstated a 0.5 s model as 0.9 s.
    // Compared as the WORDS, not the seconds: two waits that print identically
    // are identical as far as this sentence goes.
    const sameCost = costs.every((c) => c.words === costs[0]!.words);
    parts.push(
      sameCost
        ? `That is about ${costs[0]!.words} of extra waiting on a full-length answer${everyModel ? ", each" : ""}.`
        : // The unit is said once. Repeating "of extra waiting on a full-length
          // answer" per model made the sentence longer than the reading it
          // qualifies.
          `That is about ${costs[0]!.words} of extra waiting on a full-length answer for ${costs[0]!.modelID}, and ${costs
            .slice(1)
            .map((c) => `${c.words} for ${c.modelID}`)
            .join(", ")}.`,
    );
    if (others.length > 0) {
      parts.push(
        `${others.map((m) => `${m.modelID} is ${METRIC_WORDS[m.metric].slow}`).join(", ")} too.`,
      );
    }
    // Which side lost runs decides which way the caveat points; saying "at least
    // this large" for censoring that sat in the reference day claimed the
    // opposite of what it does, because a flattered reference INFLATES the
    // change. With both sides truncated the two pull against each other and
    // neither direction can be claimed.
    const censoredRecent = fired.some((m) => m.censored.recent);
    const censoredBefore = fired.some((m) => m.censored.before);
    if (censoredRecent && censoredBefore) {
      parts.push(
        `Requests were cut off by the timeout limits in both periods and are in neither median, so this figure is drawn from the runs that finished and the real change could be either side of it.`,
      );
    } else if (censoredRecent) {
      parts.push(
        `Some requests in the last ${span} were cut off by the timeout limits and are not in that median, so the real change is at least this large.`,
      );
    } else if (censoredBefore) {
      parts.push(
        `Some requests in the ${refSpan} before were cut off by the timeout limits and are not in that median, so the real change may be smaller than this.`,
      );
    }

    return {
      state: "slower",
      lead: `${subject} ${words} right now`,
      line: parts.join(" "),
      moves: fired,
      metric: lead.metric,
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
    (m) =>
      m.worseBy < 0 && 1 / (1 + m.worseBy) - 1 >= floorFor(m.metric, false),
  );
  if (better.length > 0) {
    return { state: "quicker", lead: "", line: "", moves: [], metric: null };
  }

  return {
    state: "steady",
    lead: "",
    line: `First token and throughput are both inside this endpoint's ordinary spread for the last ${span}.`,
    moves: [],
    metric: null,
  };
}
