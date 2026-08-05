import type { Cycle } from "./api/types";
import { formatMs, formatTime, plural, shouldUseLogScale } from "./format";

// One bar per cycle: colour by health, height by latency, both taken from the
// WORSE of the models probed in that cycle.
//
// The point is that a whole day is legible before scrolling — a shape, not a
// number. It sits above the charts because a glance at it answers "was today
// normal?" faster than any percentile can, and that only works if it cannot
// miss: it used to draw one model, so a cycle in which the OTHER one failed was
// painted green by the page's loudest "is anything wrong" surface.
//
// Worse in BOTH channels, rather than colour from either model and height from
// one of them. The models have different baselines, so the taller bar is
// usually whichever is slower by nature — which is the shape a reader wants
// anyway — and when the other spikes past it the bar follows. One rule, and the
// caption can state it in a line.

// worstPerCycle collapses each cycle's runs into the single reading the strip
// draws.
//
// Keyed on the cycle's OWN timestamp, never on position: both models are probed
// in one cycle and share it, but a model missing from a cycle must not shift
// every later bar of the other one out of alignment.
export function worstPerCycle(perModel: Cycle[][]): Cycle[] {
  const byTime = new Map<string, Cycle>();
  for (const cycles of perModel) {
    for (const c of cycles) {
      const seen = byTime.get(c.at);
      if (seen === undefined) {
        byTime.set(c.at, { ...c });
        continue;
      }
      byTime.set(c.at, {
        at: c.at,
        // A failure anywhere fails the cycle. This is the whole reason the
        // strip merges: a run that did not answer is the thing a glance must
        // not be able to miss.
        ok: seen.ok && c.ok,
        // Likewise a wrong answer — and null is "not graded", which must not
        // outrank a false. A cycle where one model answered wrongly and the
        // other was not graded is a cycle with a wrong answer in it.
        answer_ok:
          seen.answer_ok === false || c.answer_ok === false
            ? false
            : (seen.answer_ok ?? c.answer_ok),
        // The slower of the two. A null is not a zero: a model that reported no
        // TTFT contributes nothing to the height, and the other one's reading
        // stands rather than being averaged down to nothing.
        ttft_ms: maxOrNull(seen.ttft_ms, c.ttft_ms),
        // Whichever reason was recorded first. The strip needs A reason for its
        // hover text; which model produced it is a question for the table.
        error_class: seen.error_class ?? c.error_class,
      });
    }
  }
  // Sorted by time rather than by arrival: these came out of a map fed by two
  // responses, and the strip's whole grammar is that left is older.
  //
  // Parsed, never compared as strings. The daemon stamps cycles with Go's
  // RFC3339Nano, which DROPS trailing zeros from the fraction — so "12:00:00Z"
  // and "12:00:00.5Z" are half a second apart and sort the wrong way round as
  // text, because "." precedes "Z". Cycles are five minutes apart, so today the
  // difference always lands in a field before the fraction and the bug cannot
  // fire; the cadence is not something this function should have to know.
  // localeCompare was doubly wrong for it: collation is locale-dependent, and
  // these are machine timestamps.
  return [...byTime.values()].sort(
    (a, b) => Date.parse(a.at) - Date.parse(b.at),
  );
}

function maxOrNull(a: number | null, b: number | null): number | null {
  if (a === null) return b;
  if (b === null) return a;
  return Math.max(a, b);
}

export function PulseStrip({ perModel }: { perModel: Cycle[][] }) {
  const ordered = worstPerCycle(perModel);
  if (ordered.length === 0) {
    return null;
  }

  const successes = ordered
    .map((s) => s.ttft_ms)
    .filter((v): v is number => v !== null && Number.isFinite(v));
  // Scaled against the strip's own worst reading rather than a fixed ceiling,
  // so the shape stays readable whether the day peaked at 900 ms or 90 s. The
  // caption says so: without it the tallest bar reads as a threshold rather
  // than as whatever today happened to be.
  const peak = successes.length > 0 ? Math.max(...successes) : 1;

  // Linear collapses the case this strip exists to show. A normal reading is
  // under a second and the probe's TTFT timeout is 150 s, so ONE slow cycle
  // puts the peak two decades above the baseline and every healthy bar lands on
  // the 8% floor: a row of dots under a spike, with the day's actual shape
  // squeezed out of it. Same 20x threshold as the charts, from the same helper
  // — a second rule for when a scale stops being readable would be a second
  // answer to the same question.
  const positives = successes.filter((v) => v > 0);
  const log = shouldUseLogScale(successes);
  // The domain is anchored on the decade BELOW the fastest reading rather than
  // on the reading itself: anchored on the minimum, the fastest bar is pinned
  // flat every render and the whole shape shifts whenever the day's quietest
  // cycle changes. A decade only moves when the baseline crosses a power of
  // ten, so the strip means the same thing from one visit to the next.
  const floor =
    log && positives.length > 0
      ? Math.pow(10, Math.floor(Math.log10(Math.min(...positives))))
      : 0;
  // Zero when every reading is identical, or when there is only one: that
  // window has no range to spread, and dividing by it would render NaN%.
  const span = floor > 0 && peak > floor ? Math.log(peak / floor) : 0;

  return (
    <div>
      <div
        className="flex h-12 items-end gap-px overflow-hidden max-[720px]:gap-0"
        role="img"
        aria-label={`Last ${ordered.length} ${plural(
          ordered.length,
          "cycle",
        )}: ${ordered.filter((s) => s.ok).length} succeeded, ${
          ordered.filter((s) => !s.ok).length
        } failed`}
        data-testid="pulse-strip"
      >
        {ordered.map((s, i) => {
          // A failed cycle gets a full-height bar in the danger colour: it has
          // no latency to plot, and drawing it short would make an outage look
          // like a fast response.
          const failed = !s.ok;
          const ttft = s.ttft_ms ?? 0;
          // The 8% floor survives both scales: a fast cycle is still a bar.
          // Clamped up to the floor before the log, so a reading below the
          // anchored decade draws short rather than negative.
          const height = failed
            ? 100
            : span > 0
              ? Math.max(
                  8,
                  8 + 92 * (Math.log(Math.max(ttft, floor) / floor) / span),
                )
              : Math.max(8, (ttft / peak) * 100);
          const color = failed
            ? "var(--color-danger)"
            : s.answer_ok === false
              ? "var(--color-fault-edge)"
              : "var(--color-online)";
          return (
            <span
              key={`${s.at}-${i}`}
              // h-full, because the strip is items-end: without a height of its
              // own the cell shrink-wraps its bar, and the bar's percentage
              // height then has nothing to resolve against.
              className="flex h-full min-w-px flex-1 items-end"
              title={`${formatTime(s.at)} · ${
                failed ? (s.error_class ?? "failed") : formatMs(s.ttft_ms)
              }`}
            >
              <span
                className="w-full"
                style={{
                  height: `${height}%`,
                  background: color,
                  opacity: failed ? 1 : 0.75,
                }}
              />
            </span>
          );
        })}
      </div>
      {/* Three things a reader cannot otherwise discover: what the height is,
          that it is a whole day regardless of the window pill, and what the two
          other colours mean. The colours especially — ui.tsx opens by saying
          colour is never the only signal here, and until this line the strip
          had three states told apart by hue alone, readable only by hovering,
          which does not exist on a phone.
          
          The scale is relative to the tallest bar, and that is deliberately NOT
          said: it is the reading a reader takes from an unlabelled strip
          anyway, and it was the clause that made this a paragraph.

          A log scale is the exception, and only while it is on. Relative-to-
          tallest is what an unlabelled strip already implies; proportional
          spacing between the bars is what it implies NEXT, and that one stops
          being true — a log scale read as linear is worse than no chart at all
          (charts/options.ts). Two words, and only on the days that earn them. */}
      <p className="mt-2 text-label text-muted" data-testid="pulse-note">
        One bar per cycle, 24 hours. Height is the slower model&apos;s TTFT
        {span > 0 ? ", log-scaled" : ""}.{" "}
        <span className="text-danger">Red</span>: a run failed.{" "}
        <span className="text-fault-edge">Amber</span>: a wrong answer.
      </p>
    </div>
  );
}
