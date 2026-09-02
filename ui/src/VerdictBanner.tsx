import type { ReactNode } from "react";
import type { Trend } from "./api/types";
import type { Verdict } from "./verdict";
import { StateChip } from "./ui";
import { buildSpeedReading } from "./trend";
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
  models = [],
  loading,
}: {
  verdict: Verdict;
  trend?: Trend | null;
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

  // The good day, said in full: nothing is failing AND nothing moved past a
  // floor, which is the one state in which the page may claim the readings sit
  // where they usually do.
  //
  // Only for "steady". A "minor" reading has crossed a measured floor — that is
  // why it was detected — so "behaving as usual" would be a false sentence in
  // exactly the state it looks most at home in, and "quicker" and "recovered"
  // are each carrying news of their own. They keep the bare headline.
  //
  // No figures anywhere in this branch. The wait, the run counts and the
  // week's medians all sit on the cards below; a visitor asking whether MiMo
  // is up is not asking for a statistic, and the banner used to hand them two
  // lines of them under a headline that had already answered the question.
  const usual = verdict.state === "normal" && !slow && speed.state === "steady";
  const subject =
    models.length > 1 ? `both models are` : models.length === 1 ? `it is` : "";
  const headline =
    loading && verdict.state === "unknown"
      ? "Loading…"
      : slow
        ? speed.lead
        : usual && subject !== ""
          ? `${verdict.headline}, and ${subject} behaving as usual`
          : verdict.headline;

  // The detail lines the verdict already carries, plus the speed sentence — and
  // the speed sentence FIRST when it is the thing being announced, because it
  // is what the headline just claimed.
  //
  // "recovered" rides in the same slot as "steady", and for the same reason:
  // the page has said something about speed, so its silence is readable. It
  // sits UNDER the verdict rather than over it, because what it reports is over.
  //
  // "steady" no longer rides here: its sentence became the headline's own
  // clause above, and printed as well it said the same thing twice. "minor"
  // says nothing at all (trend.ts). So the only speed line left under a normal
  // verdict is the past tense one.
  const detail = slow
    ? [speed.line, ...verdict.detail]
    : verdict.state === "normal" && speed.state === "recovered"
      ? [...verdict.detail, speed.line]
      : verdict.detail;

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
