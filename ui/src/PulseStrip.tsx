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

// A tick on the strip's hour axis: which bar it points at, and what it says.
export type Tick = {
  // Percentage across the strip, at the CENTRE of the bar it marks.
  left: number;
  label: string;
  // The whole hour it stands for, which is what the narrow set thins by.
  hour: number;
};

// How close to an edge a centred label may sit before half of it hangs outside
// the strip. A window that happens to start on the hour puts its first tick at
// the very first bar, which is the case this catches.
const EDGE_GUARD = 2;

// One label per three hours on a desktop, per six on a phone. 288 bars over
// ~1116 px is ~46 px of strip per hour, which a 11 px "18:00" cannot be printed
// under hourly; a 320 px column has room for four labels and no more.
const WIDE_STEP_HOURS = 3;
const NARROW_STEP_HOURS = 6;

// hourTicks picks the bars that get a label, one per whole hour divisible by
// `step`.
//
// Anchored on the clock rather than on the window start: ticks at 00, 03, 06 …
// are the same landmarks from one visit to the next, where "every 3 hours from
// wherever this window happens to begin" moves them on every render.
//
// Positioned by INDEX, because the bars are: they are laid out with flex-1, so
// a cycle the daemon never recorded does not leave a hole, it packs the rest
// closer. An axis drawn on even hourly spacing would then point at the wrong
// bars — which is exactly the day a reader most needs it to be right. Position
// comes from the bar, so the labels come out slightly uneven on a day with
// gaps, and that unevenness is the truth.
//
// The label, on the other hand, is the WHOLE hour and not the bar's own stamp.
// Cycles land wherever the daemon's cadence puts them — 09:02 today, 09:47
// after a restart — and a row reading "09:02 · 12:02 · 15:02" invites a reader
// to find meaning in a phase that is an artefact of the last deploy. Position
// from the bar, text from the hour.
export function hourTicks(ordered: Cycle[], step: number): Tick[] {
  const out: Tick[] = [];
  let previousHour: number | null = null;
  ordered.forEach((c, i) => {
    const at = new Date(c.at);
    if (Number.isNaN(at.getTime())) return;
    // Local, like every other time on this page: formatTime leaves the zone
    // unset so it follows the reader, and an axis in UTC beside a table in
    // Zurich would be two clocks on one screen.
    const hour = at.getHours();
    const first = hour !== previousHour;
    previousHour = hour;
    // Not "the bar at :00" — cycles do not land on the hour. The first bar of
    // the hour is the one that does exist, and after a gap that swallows a
    // whole hour it is simply the first bar of the next one.
    if (!first || hour % step !== 0) return;
    const left = ((i + 0.5) / ordered.length) * 100;
    if (left < EDGE_GUARD || left > 100 - EDGE_GUARD) return;
    at.setMinutes(0, 0, 0);
    out.push({ left, label: formatTime(at), hour });
  });
  return out;
}

// The axis, as a layer of absolutely-positioned labels over a fixed height.
//
// Two sets rather than a resize listener: ~46 px of strip per hour on a desktop
// is comfortable for a label every 3 hours and impossible for one every hour,
// and a 320 px phone has room for four. The 6-hour set is a subset of the
// 3-hour one, so one rule produces both.
function Axis({ ticks }: { ticks: Tick[] }) {
  return (
    <div
      className="relative mt-1 h-4"
      // Decorative: the strip is a single role="img" whose aria-label already
      // carries the window, and both sets of labels are in the DOM at once, so
      // without this a screen reader reads every hour twice.
      aria-hidden="true"
      data-testid="pulse-axis"
    >
      <div className="max-[720px]:hidden">
        {ticks.map((t) => (
          <TickMark key={`w-${t.label}`} tick={t} />
        ))}
      </div>
      <div className="hidden max-[720px]:block">
        {ticks
          // Thinned by the CLOCK, not by index: every other tick of a list that
          // happens to open at 03:00 would label 03, 09, 15 — six hours apart,
          // but no longer the landmarks the wide set uses, and one dropped
          // edge tick would flip them all.
          .filter((t) => t.hour % NARROW_STEP_HOURS === 0)
          .map((t) => (
            <TickMark key={`n-${t.label}`} tick={t} />
          ))}
      </div>
    </div>
  );
}

function TickMark({ tick }: { tick: Tick }) {
  return (
    <>
      <span
        className="absolute -top-1 h-1 w-px bg-ghost"
        style={{ left: `${tick.left}%` }}
      />
      <span
        className="absolute top-0 -translate-x-1/2 text-micro text-faint"
        style={{ left: `${tick.left}%` }}
      >
        {tick.label}
      </span>
    </>
  );
}

export function PulseStrip({
  perModel,
  pending = false,
}: {
  perModel: Cycle[][];
  // Whether a first response is still on its way, which is what decides
  // between an empty frame and no strip at all.
  pending?: boolean;
}) {
  const ordered = worstPerCycle(perModel);
  // While the response is in flight, this renders the strip's FRAME rather than
  // nothing. Returning null unconditionally meant the whole block — 48 px of
  // bars plus its caption — appeared the moment the dashboard fetch landed,
  // near the top of the page, shoving the window pills and both model cards
  // down with it. That single insertion was most of a 0.43 layout shift. An
  // empty frame holds the same ground it will occupy once the bars arrive, so
  // the arrival costs nothing.
  //
  // Once the answer is in, an empty strip is a strip with nothing to draw, and
  // it goes back to rendering nothing: there is no arrival left to hold ground
  // for, and a bordered 48 px void beside an error message is not a
  // reservation. Nothing below it has been laid out yet either way — this is
  // the same instant the rest of the page resolves.
  if (ordered.length === 0 && !pending) {
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
        // The empty frame gets a surface. Reserving the height alone left a
        // caption sitting under nothing at all, which reads as a strip that
        // failed rather than one that has not arrived — and the ground it is
        // holding is invisible, so the reservation looks like a mistake. Only
        // while empty: once there are bars, they ARE the surface.
        className={`flex h-12 items-end gap-px overflow-hidden max-[720px]:gap-0 ${
          ordered.length === 0 ? "rounded-sm bg-panel/60" : ""
        }`}
        role="img"
        // "Last 0 cycles: 0 succeeded, 0 failed" is a reading, and the empty
        // frame has not read anything yet. A screen reader gets the honest
        // version of the same distinction a sighted reader gets from a strip
        // with no bars in it.
        // The window is stated as its two endpoints rather than as a duration.
        // A sighted reader now has an axis for it, and the honest form of the
        // same fact is the pair of times: the backend asks for the last 288
        // cycles, not for a time range, so after any stretch where the daemon
        // was not running the strip reaches back further than a day.
        aria-label={
          ordered.length === 0
            ? "Cycle history, still loading"
            : `Last ${ordered.length} ${plural(
                ordered.length,
                "cycle",
              )}, ${formatTime(ordered[0]!.at)} to ${formatTime(
                ordered[ordered.length - 1]!.at,
              )}: ${ordered.filter((s) => s.ok).length} succeeded, ${
                ordered.filter((s) => !s.ok).length
              } failed`
        }
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
      {/* Rendered while the strip is still empty too, so the frame it holds is
          the frame the bars will arrive into. Reserving only the bars' 48 px
          would put the axis's 20 px back into the layout at the moment the
          fetch lands, which is most of the shift the empty frame exists to
          avoid. */}
      <Axis ticks={hourTicks(ordered, WIDE_STEP_HOURS)} />
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
