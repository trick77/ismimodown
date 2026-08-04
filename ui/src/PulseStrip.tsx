import type { Cycle } from "./api/types";
import { formatMs, formatTime } from "./format";

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

  return (
    <div
      className="flex h-12 items-end gap-px overflow-hidden max-[720px]:gap-0"
      role="img"
      aria-label={`Last ${ordered.length} cycles: ${
        ordered.filter((s) => s.ok).length
      } succeeded, ${ordered.filter((s) => !s.ok).length} failed`}
      data-testid="pulse-strip"
    >
      {ordered.map((s, i) => {
        // A failed cycle gets a full-height bar in the danger colour: it has no
        // latency to plot, and drawing it short would make an outage look like
        // a fast response.
        const failed = !s.ok;
        const height = failed
          ? 100
          : Math.max(8, ((s.ttft_ms ?? 0) / peak) * 100);
        const color = failed
          ? "var(--color-danger)"
          : s.answer_ok === false
            ? "var(--color-fault-edge)"
            : "var(--color-online)";
        return (
          <span
            key={`${s.at}-${i}`}
            className="min-w-px flex-1"
            style={{
              height: `${height}%`,
              background: color,
              opacity: failed ? 1 : 0.75,
            }}
            title={`${formatTime(s.at)} · ${
              failed ? (s.error_class ?? "failed") : formatMs(s.ttft_ms)
            }`}
          />
        );
      })}
    </div>
  );
}
