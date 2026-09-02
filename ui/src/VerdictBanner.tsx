import type { ReactNode } from "react";
import type { Summary, Trend } from "./api/types";
import type { Verdict } from "./verdict";
import { StateChip } from "./ui";
import {
  buildSpeedReading,
  currentWaits,
  spanWords,
  TAIL_S,
  type CurrentWait,
} from "./trend";
import { formatMs } from "./format";
import { colorForModel } from "./charts/options";
import { TrendPlot } from "./TrendPlot";

// The one word in the headline that is also a state, painted the colour of the
// chip beside it.
//
// Only when the chip actually says it: the tie is between the green pill and
// the green word, so a headline mentioning "normal" while the chip reads
// degraded — "reasoning is switched on", say — must not borrow the colour and
// claim a state the page is not in. Colour is never the only signal here; the
// word carries itself, and this only makes the pill and the sentence read as
// one statement rather than two.
// The number a visitor came for, in the units they wait in.
//
// Under the headline rather than in it: "Xiaomi MiMo is answering" is the
// answer, and this is how long it takes. Both models, always named, because a
// single figure over a fleet of two is the one thing this page has learned not
// to print — they are three seconds apart on an ordinary day.
//
// Silent when the trend block cannot produce a reading: the window figures on
// the cards below are a day's medians and would answer a different question in
// the same sentence.
function waitLine(
  waits: CurrentWait[],
  baseline: Summary | null | undefined,
): string[] {
  if (waits.length === 0) return [];
  const clauses = waits.map((w) => `${formatMs(w.ttftMs)} on ${w.modelID}`);
  const last = clauses[clauses.length - 1]!;
  const said =
    clauses.length === 1
      ? last
      : `${clauses.slice(0, -1).join(", ")} and ${last}`;
  // "right now" is a claim only the last hour earns — the same rule the speed
  // reading follows (trend.ts, TAIL_S). A median over three hours is quoted as
  // the three hours it is, or the sentence is naming a span it never measured.
  const span = Math.max(...waits.map((w) => w.spanS));
  const when =
    span === TAIL_S ? "right now" : `over the last ${spanWords(span)}`;

  // ...against the week behind it, in the same order, so the reader can answer
  // "is that good" without scrolling to a chart. Dropped entirely when the
  // baseline cannot cover every model named — half a comparison is worse than
  // none, since the eye pairs the lists by position.
  const usual = waits.map(
    (w) =>
      baseline?.models.find((m) => m.model_id === w.modelID)?.ttft.p50_ms ??
      null,
  );
  const reference = usual.every((v) => v !== null && v > 0)
    ? `, against ${usual.map((v) => formatMs(v)).join(" and ")} over the past week`
    : "";
  return [`First token in about ${said} ${when}${reference}.`];
}

// Model IDs carry their series colour wherever the banner names them, the same
// tie the masthead subline makes: the reader meets "mimo-v2.5-pro" in the
// sentence and finds the same orange on the card, the chart line and the
// legend below. Monospace with it, because a model ID is an identifier and the
// rest of the sentence is prose.
//
// Longest ID first in the alternation — "mimo-v2.5" is a prefix of
// "mimo-v2.5-pro", and matching the short one first paints half a name and
// leaves "-pro" in body text. Possessives ("mimo-v2.5's") fall out of that for
// free: the match ends at the ID and the apostrophe stays prose.
function paintModels(text: string, models: string[]): ReactNode {
  const ids = [...models].sort((a, b) => b.length - a.length);
  if (ids.length === 0) return text;
  const pattern = new RegExp(
    `(${ids.map((id) => id.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})`,
    "g",
  );
  return text.split(pattern).map((part, i) =>
    ids.includes(part) ? (
      <span
        key={`${part}-${i}`}
        className="num whitespace-nowrap text-[0.95em]"
        style={{ color: colorForModel(part, models) }}
      >
        {part}
      </span>
    ) : (
      part
    ),
  );
}

function paintState(headline: string, word: string) {
  const at = headline.indexOf(word);
  if (at === -1) return headline;
  return (
    <>
      {headline.slice(0, at)}
      <span className="text-online" data-testid="headline-state">
        {word}
      </span>
      {headline.slice(at + word.length)}
    </>
  );
}

// The verdict sits under the masthead and says what is happening in words,
// before any number appears. "Is 996 ms good?" is unanswerable in the abstract.
//
// It makes exactly ONE claim, and it is the only surface on the page that makes
// any. A green "everything looks normal" with "slower than usual" underneath it
// is the page failing to answer its own question — is it normal or is it
// slower? — so the speed reading is folded into this sentence rather than given
// a surface of its own, and every panel below prints numbers and no verdicts.
//
// Order is severity: a fault outranks a slowdown and takes the banner outright,
// with speed unmentioned. Being slow is the smaller problem, and a sentence
// that hedges an outage with a note about throughput reads as neither.
export function VerdictBanner({
  verdict,
  trend,
  baseline,
  models = [],
  loading,
}: {
  verdict: Verdict;
  trend?: Trend | null;
  // The 7-day block, so the wait can be read against what it usually is. The
  // banner states no verdict on it — that is scoreRatio's job, in verdict.ts —
  // it just prints the pair.
  baseline?: Summary | null;
  // The IDs the sentences may name, in the page's own order — the same list the
  // charts colour from, so a name reads the same wherever it appears.
  models?: string[];
  loading: boolean;
}) {
  const speed = buildSpeedReading(trend);
  // Only when nothing is wrong. verdict.state carries the faults, and this
  // branch is what keeps "slower" from ever appearing beside one.
  //
  // A slowdown and nothing else: speed.state can also be "quicker", which this
  // page measures and never mentions, "recovered", which is a slowdown the last
  // hour has already undone, and "minor", which left a wait nobody would call
  // slow. None of the three takes the headline or the plot — a banner shouting
  // about hours that are over is the reason the tail gate exists (trend.ts,
  // TAIL_S), and one shouting about a two-second first token is the reason
  // SLOW_TTFT_MS does.
  const slow = verdict.state === "normal" && speed.state === "slower";

  const tone =
    verdict.state === "degraded"
      ? "border-danger/40 bg-danger/8"
      : verdict.state === "elevated"
        ? "border-fault-edge/40 bg-fault-edge/8"
        : slow
          ? "border-fault-edge/25 bg-fault-edge/5"
          : "border-border bg-panel/60";

  const headline =
    loading && verdict.state === "unknown"
      ? "Loading…"
      : slow
        ? speed.lead
        : verdict.headline;

  // The detail lines the verdict already carries, plus the speed sentence — and
  // the speed sentence FIRST when it is the thing being announced, because it
  // is what the headline just claimed.
  //
  // "recovered" rides in the same slot as "steady", and for the same reason:
  // the page has said something about speed, so its silence is readable. It
  // sits UNDER the verdict rather than over it, because what it reports is over.
  //
  // "minor" rides there too: a move that cleared its floor and left a reading
  // still quick in absolute terms (trend.ts, SLOW_TTFT_MS / SLOW_TPS) is a real
  // change and a poor headline, so it is stated and not announced. Under the
  // verdict rather than over it, because whatever the verdict is talking about
  // is the larger claim by construction.
  //
  // The wait leads the detail on an ordinary day: it is what the headline just
  // claimed, quantified. Never beside a fault — a wait is not the news then,
  // and the banner takes the fault alone.
  const waits =
    verdict.state === "normal" && !slow
      ? waitLine(currentWaits(trend), baseline)
      : [];
  const detail = slow
    ? [speed.line, ...verdict.detail]
    : verdict.state === "normal" &&
        (speed.state === "steady" ||
          speed.state === "recovered" ||
          speed.state === "minor")
      ? [...waits, ...verdict.detail, speed.line]
      : [...waits, ...verdict.detail];

  return (
    <div
      // mb-10, against the masthead's tighter pb above: the banner reads as a
      // verdict on the page, not as a label for the strip it happens to sit on
      // top of, and it needs the larger gap on the side it is NOT about.
      //
      // min-h holds two lines of headline, but only on a narrow screen and only
      // because that is where the headline needs two. "Loading…" is one line
      // everywhere; "Everything looks normal right now" is one line on a
      // desktop and two on a phone, so on a phone the banner grew by a line the
      // moment the fetch landed and carried the entire page down with it. Above
      // the sm breakpoint there is nothing to reserve, so nothing is: a fixed
      // floor there would be dead space under every headline that ever renders.
      className={`mb-10 min-h-[99px] rounded-xl border px-5 py-4 sm:min-h-0 ${tone}`}
      role="status"
      aria-live="polite"
      data-testid="verdict"
    >
      <div className="flex flex-wrap items-center gap-3">
        {slow ? (
          // A word this page did not have, and deliberately not "elevated",
          // which is spent on faults. There is only ever one chip, so this can
          // never appear beside a green "normal" — which is the contradiction
          // the whole arrangement exists to make unrepresentable.
          <span
            className="num inline-block rounded-full border border-fault-edge/40 bg-fault-edge/10 px-2 py-[2px] text-micro uppercase tracking-wider text-fault-edge"
            data-testid="state-chip"
            data-state="slower"
          >
            slower
          </span>
        ) : (
          <StateChip state={verdict.state} />
        )}
        <p
          // Display size when something is off, title size otherwise: the
          // finding is meant to be the loudest thing on the page, and a
          // slowdown announced in the same weight as "everything looks normal"
          // is a slowdown a reader scrolls past.
          className={`font-serif text-ink ${
            slow ? "text-display leading-tight" : "text-title"
          }`}
        >
          {verdict.state === "normal" && !slow
            ? paintState(headline, "answering")
            : paintModels(headline, models)}
        </p>
      </div>
      {/* Serif at body size, in the near-white ink, because these lines ARE
          the answer — they rendered at 13px in muted grey, the smallest type on
          the page, inside the one box a visitor reads first. The headline says
          what is happening; these say how much, and a reader who has to squint
          at them is being told they do not matter. */}
      {detail.length > 0 && (
        <ul className="mt-3 space-y-1.5 font-serif text-body leading-snug text-ink-dim">
          {detail.map((line) => (
            <li key={line}>{paintModels(line, models)}</li>
          ))}
        </ul>
      )}
      {/* The shape of it, under the sentence that named it. A percentage says
          how much; only the plot says whether it is a step, a ramp, or a spike
          that is already over. Drawn only when something is off — the plot
          appearing is itself part of the signal. */}
      {slow && trend && speed.metric && (
        <TrendPlot
          trend={trend}
          metric={speed.metric}
          moves={speed.moves}
          spanS={speed.spanS ?? undefined}
        />
      )}
    </div>
  );
}
