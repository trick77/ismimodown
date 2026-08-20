import type { Trend } from "./api/types";
import type { Verdict } from "./verdict";
import { StateChip } from "./ui";
import { buildSpeedReading } from "./trend";
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
  loading,
}: {
  verdict: Verdict;
  trend?: Trend | null;
  loading: boolean;
}) {
  const speed = buildSpeedReading(trend);
  // Only when nothing is wrong. verdict.state carries the faults, and this
  // branch is what keeps "slower" from ever appearing beside one.
  const slow =
    verdict.state === "normal" &&
    (speed.state === "slower" || speed.state === "quicker");

  const tone =
    verdict.state === "degraded"
      ? "border-danger/40 bg-danger/8"
      : verdict.state === "elevated"
        ? "border-fault-edge/40 bg-fault-edge/8"
        : speed.state === "slower" && verdict.state === "normal"
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
  const detail = slow
    ? [speed.line, ...verdict.detail]
    : verdict.state === "normal" && speed.state === "steady"
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
        {slow && speed.state === "slower" ? (
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
            slow && speed.state === "slower"
              ? "text-display leading-tight"
              : "text-title"
          }`}
        >
          {verdict.state === "normal" && !slow
            ? paintState(headline, "normal")
            : headline}
        </p>
      </div>
      {detail.length > 0 && (
        <ul className="mt-2 space-y-1 text-label text-muted">
          {detail.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      )}
      {/* The shape of it, under the sentence that named it. A percentage says
          how much; only the plot says whether it is a step, a ramp, or a spike
          that is already over. Drawn only when something is off — the plot
          appearing is itself part of the signal. */}
      {slow && trend && speed.metric && (
        <TrendPlot trend={trend} metric={speed.metric} moves={speed.moves} />
      )}
    </div>
  );
}
