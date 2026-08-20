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
  // Either span had runs cut off by the timeout ladder, so both medians are
  // drawn from the runs that FINISHED and the move is understated.
  censored: boolean;
};

export type SpeedReading = {
  // "slower" is a word this page did not have. It is deliberately not
  // "elevated", which is spent on faults: a slowdown with nothing failing is a
  // different claim, and reusing the fault vocabulary for it is how a reader
  // stops believing the fault vocabulary.
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
function censoredIn(m: TrendMetric): boolean {
  return m.points.some((p) => p.censored > 0);
}

function moveFor(model: ModelTrend, metric: SpeedMetric): SpeedMove | null {
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
    censored: censoredIn(block),
  };
}

function floorFor(metric: SpeedMetric, bothMoved: boolean): number {
  if (metric === "tps") return TPS_FLOOR;
  return bothMoved ? TTFT_FLOOR_BOTH : TTFT_FLOOR;
}

function pct(v: number): string {
  return `${Math.round(Math.abs(v) * 100)} %`;
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

const METRIC_WORDS: Record<SpeedMetric, { slow: string; quick: string }> = {
  ttft: { slow: "slow to start", quick: "quicker to start" },
  tps: { slow: "generating more slowly", quick: "generating faster" },
};

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

  const all: SpeedMove[] = [];
  for (const model of models) {
    for (const metric of ["ttft", "tps"] as const) {
      const move = moveFor(model, metric);
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
    parts.push(
      lead.metric === "ttft"
        ? `Its first token takes ${pct(lead.worseBy)} longer than over the ${refSpan} before — ${Math.round(lead.recent)} ms against ${Math.round(lead.before)} ms.`
        : `It produces ${pct(lead.worseBy)} fewer tokens per second than over the ${refSpan} before — ${lead.recent.toFixed(1)} against ${lead.before.toFixed(1)}.`,
    );
    // The lead MODEL's own cost, not the sum across models. No single request is
    // answered by both models, so adding their penalties together states a wait
    // nobody can ever experience — and it would grow with the size of the fleet
    // rather than with the slowdown.
    const leadCost = fired
      .filter((m) => m.modelID === lead.modelID)
      .reduce((sum, m) => sum + m.secondsAdded, 0);
    parts.push(
      `That is about ${seconds(leadCost)} of extra waiting on a full-length answer${everyModel ? ", each" : ""}.`,
    );
    if (others.length > 0) {
      parts.push(
        `${others.map((m) => `${m.modelID} is ${METRIC_WORDS[m.metric].slow}`).join(", ")} too.`,
      );
    }
    parts.push(
      fired.some((m) => m.censored)
        ? "Some requests in these periods were cut off by the timeout limits and are not in either median, so the real change is at least this large."
        : "Nothing failed, and every answer was right.",
    );

    return {
      state: "slower",
      lead: `${subject} ${words} right now`,
      line: parts.join(" "),
      moves: fired,
      metric: lead.metric,
    };
  }

  // Quicker is worth saying for one reason: it is how a reader knows a slowdown
  // ended. Same floors, so it is exactly as rare — but measured the other way
  // round, because a fraction is not symmetric about zero: 900 ms falling to
  // 500 is the same size of move as 500 rising to 900, and only one of those is
  // 80% by division. Comparing -worseBy against the floor would make every
  // recovery look smaller than the slowdown it undid.
  const better = all
    .filter(
      (m) =>
        m.worseBy < 0 && 1 / (1 + m.worseBy) - 1 >= floorFor(m.metric, false),
    )
    .sort((a, b) => a.secondsAdded - b.secondsAdded);
  if (better.length > 0) {
    const lead = better[0]!;
    // The same symmetric figure the floor was tested against, so the sentence
    // and the threshold are talking about one number.
    const quickerBy = 1 / (1 + lead.worseBy) - 1;
    return {
      state: "quicker",
      lead: `${lead.modelID} is ${METRIC_WORDS[lead.metric].quick} right now`,
      line:
        lead.metric === "ttft"
          ? `Its first token arrives ${pct(quickerBy)} sooner than over the ${refSpan} before — ${Math.round(lead.recent)} ms against ${Math.round(lead.before)} ms.`
          : `It produces ${pct(quickerBy)} more tokens per second than over the ${refSpan} before — ${lead.recent.toFixed(1)} against ${lead.before.toFixed(1)}.`,
      moves: better,
      metric: lead.metric,
    };
  }

  return {
    state: "steady",
    lead: "",
    line: `First token and throughput are both inside this endpoint's ordinary spread for the last ${span}.`,
    moves: [],
    metric: null,
  };
}

// What one figure on a model card says about its own recent past.
//
// A NUMBER, never a verdict: the banner has already made the page's one claim,
// and a second opinion under a figure is how the page ends up arguing with
// itself. So this line states the change and colours it only when it cleared
// the floor — below that it is ordinary movement, printed in the same ink as
// everything else on the card.
export type FigureDelta = { words: string; past: boolean };

// Anything below this is not worth a number at all: on the measured spread it
// is what a quiet afternoon looks like, and printing "4 % slower" under every
// figure forever teaches a reader to stop reading the line.
const SAME_BAND = 0.1;

export function figureDelta(
  trend: Trend | null | undefined,
  modelID: string,
  metric: SpeedMetric,
): FigureDelta | null {
  const model = trend?.models.find((m) => m.model_id === modelID);
  if (!model) return null;
  const move = moveFor(model, metric);
  const refSpan = spanWords(trend?.before_s ?? 0);
  if (!move) {
    // Either span was too thin for a median — and which one is not claimed,
    // because both are reachable. Said in words rather than left blank, since a
    // missing line reads as "nothing changed".
    return { words: "not enough runs yet to compare", past: false };
  }
  if (Math.abs(move.worseBy) < SAME_BAND) {
    return { words: `about the same as the ${refSpan} before`, past: false };
  }
  const worse = move.worseBy > 0;
  const direction =
    metric === "ttft"
      ? worse
        ? "slower"
        : "quicker"
      : worse
        ? "fewer tok/s"
        : "more tok/s";
  return {
    words: `${pct(move.worseBy)} ${direction} than the ${refSpan} before`,
    past: move.worseBy >= floorFor(metric, false),
  };
}
