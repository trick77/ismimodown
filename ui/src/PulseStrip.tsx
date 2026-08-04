import type { Cycle } from "./api/types";
import { formatMs, formatTime } from "./format";
import { isOffPeak, OFFPEAK_COEFFICIENT } from "./offpeak";
import { OffPeakChip } from "./ui";

// One bar per cycle: colour by health, height by latency.
//
// The point is that a whole day is legible before scrolling — a shape, not a
// number. It sits above the charts because a glance at it answers "was today
// normal?" faster than any percentile can.
export function PulseStrip({ cycles }: { cycles: Cycle[] }) {
  if (cycles.length === 0) {
    return null;
  }

  // Oldest on the left, so time runs the same way as every chart below.
  const ordered = [...cycles].reverse();
  const successes = ordered
    .map((s) => s.ttft_ms)
    .filter((v): v is number => v !== null && Number.isFinite(v));
  // Scaled against the window's own worst reading rather than a fixed ceiling,
  // so the shape stays readable whether the day peaked at 900 ms or 90 s.
  const peak = successes.length > 0 ? Math.max(...successes) : 1;

  // Off-peak is decided PER CYCLE from its own timestamp, never from its
  // position in the row.
  //
  // The strip has no time axis: every cycle gets an equal slice whether or not
  // the one before it ran, so a stretch of missed runs closes up instead of
  // leaving a hole. That makes position and time two different things, and a
  // band drawn at a computed offset would drift off the very bars it claims to
  // describe the first time the daemon misses a cycle.
  const offPeak = ordered.map((s) => isOffPeak(new Date(s.at).getTime()));
  const shaded = offPeak.some(Boolean);

  return (
    <div>
      {/* The one place on the page that carries the rate as a badge.
          It used to sit in the header of each token chart, tied to the band
          actually being drawn there — on 7d and wider the band is dropped, and
          a chip promising a rate the plot does not show is worse than no chip.
          Three copies of one fact is two too many, so it is stated once, here.

          Here it is NOT tied to a band, and needs no gate: the chip says what
          the rate is doing right now, and now is a thing the page always has.
          That is also why the strip's missing time axis costs it nothing —
          "0.8× until 02:00" names a wall-clock instant, not a position in the
          row. The strip is a fixed 288-cycle day, so the window it refers to is
          in view regardless of which window the charts below are showing. */}
      <div className="mb-2 flex justify-end">
        <OffPeakChip />
      </div>
      <div
        className="flex h-12 items-end gap-px overflow-hidden max-[720px]:gap-0"
        role="img"
        aria-label={`Last ${ordered.length} cycles: ${
          ordered.filter((s) => s.ok).length
        } succeeded, ${ordered.filter((s) => !s.ok).length} failed${
          shaded ? ", shaded cycles billed at MiMo's off-peak rate" : ""
        }`}
        data-testid="pulse-strip"
      >
        {ordered.map((s, i) => {
          // A failed cycle gets a full-height bar in the danger colour: it has
          // no latency to plot, and drawing it short would make an outage look
          // like a fast response.
          const failed = !s.ok;
          const height = failed
            ? 100
            : Math.max(8, ((s.ttft_ms ?? 0) / peak) * 100);
          const color = failed
            ? "var(--color-danger)"
            : s.answer_ok === false
              ? "var(--color-fault-edge)"
              : "var(--color-online)";
          const cheap = offPeak[i] === true;
          return (
            // The cell is the full height of the strip and carries the tint;
            // the bar inside it is exactly what it was before. Tinting the bar
            // itself would shade it only as high as that cycle happened to
            // reach, so the band would ripple with the data instead of marking
            // a stretch of the clock.
            <span
              key={`${s.at}-${i}`}
              // h-full, because the strip is items-end: without a height of its
              // own the cell shrink-wraps its bar, and the bar's percentage
              // height then has nothing to resolve against.
              className={`flex h-full min-w-px flex-1 items-end ${
                cheap ? "bg-online/16" : ""
              }`}
              title={`${formatTime(s.at)} · ${
                failed ? (s.error_class ?? "failed") : formatMs(s.ttft_ms)
              }${cheap ? ` · ${OFFPEAK_COEFFICIENT}× off-peak` : ""}`}
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
      {/* A rail rather than a heavier tint: the tint has to stay faint enough
          to read the bars through it, which leaves it too faint to find the
          band's edges. The rail is the same span at full strength, drawn
          somewhere no data is. */}
      {shaded && (
        // No gap on this row, unlike the strip above it. The strip's gaps
        // separate one cycle's bar from the next; carried down here they would
        // cut the rail into a dotted line. Both rows are anchored at both ends
        // and divide the same width evenly, so dropping the gap costs under a
        // pixel of alignment in the middle.
        <div
          className="mt-[2px] flex"
          aria-hidden="true"
          data-testid="pulse-rail"
        >
          {offPeak.map((cheap, i) => (
            <span
              key={i}
              className={`h-[2px] min-w-px flex-1 ${cheap ? "bg-online" : ""}`}
            />
          ))}
        </div>
      )}
    </div>
  );
}
