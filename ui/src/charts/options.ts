// Pure ECharts option builders.
//
// Separated from the render wrapper on purpose: these are the part with real
// logic — axis type, gap handling, series colour — and they are testable as
// plain functions, where the canvas-backed renderer is not reachable from jsdom.
import type { Point } from "../api/types";
import {
  formatAxisMs,
  formatDate,
  formatDateTime,
  formatTime,
  formatUSDPrecise,
  shouldUseLogScale,
} from "../format";

// The height every plot draws at unless it asks for another one.
//
// Here rather than in the render wrapper beside it, even though that is where it
// is applied: the wrapper is mocked in the panel tests, and a placeholder that
// imported its height from a mock would be reserving whatever the mock happened
// to return. It has to be the SAME number as the chart it stands in for — the
// two agreeing is the whole reason a chart arriving costs no layout shift — and
// a literal 240 in seven files is a number that drifts apart silently.
export const CHART_HEIGHT = 240;

// Series colour follows the MODEL, never its rank, so a model keeps its hue
// when the ordering changes. Validated against the #1f1f1e surface: CVD
// separation ΔE 26.8, normal-vision ΔE 31.8.
export const SERIES_COLORS = ["#3987e5", "#d95926"] as const;
// The network is drawn in neutral ink because it is not a model.
export const WIRE_COLOR = "#9c9a92";
// MiMo's own edge in "The wire itself". Both lines there were neutral ink at
// first, on the reasoning that neither of them is a model and a series hue is
// how a line ends up read as one. That held while the pair was ink against
// grey; it stopped being the best trade once the reference went darker, since
// two greys separated only by lightness is the hardest pair to tell apart at a
// glance.
//
// This WAS --color-online, the page's one green, accepted with the known
// overlap that the pulse strip and the availability strip both used that value
// to mean "up". The chart going from two lines to four ended
// that: with an edge per region the green would have had to be joined by a
// second hue anyway, and spending the health colour on one of two peer lines
// made the overlap harder to defend rather than easier. It is now a muted
// violet, and the health green is no longer used here at all.
//
// Deliberately NOT a SERIES_COLORS value: those two are model identities, and
// this chart draws no model. Deliberately not the warm accent family either —
// #d97757, #c98500 and #c6613f all collapse against #d95926 (mimo-v2.5-pro)
// under deuteranopia, ΔE00 2.8 to 5.3, so a warm edge line would be
// indistinguishable from the pro model's hue for a red-green colourblind
// reader moving between the two charts.
//
// 4.64:1 against #1f1f1e. See MIMO_EDGE_AMS_COLOR for the full matrix.
export const MIMO_EDGE_COLOR = "#8f7ad4";
// The Singapore reference host, one step darker than the neutral above. It is
// the control, not the measurement — the line the reader checks against rather
// than reads — so it recedes behind MiMo's own edge. Its own constant rather
// than a darker WIRE_COLOR: that value is also the decomposition's "to the
// edge" segment, where it is paired with SERVER_COLOR and its contrast is
// already spent.
//
// #6b6963 (= --color-faint) against #1f1f1e is 3.005:1 — the darkest this can
// go and still clear the 3:1 a chart line needs to stay a line. It was #6f6d67,
// which this comment claimed was "near 3.6:1" and is in fact 3.19:1; there is
// no headroom left below, so any further darkening is a contrast decision, not
// a styling one. Measured against the panel, not the page: .card is a gradient
// from #1f1f1e, and the lighter end is the one to check.
export const REFERENCE_COLOR = "#6b6963";

// The Amsterdam pair, added when "The wire itself" went from two lines to four.
//
// The chart encodes two facts about every line, on two separate channels, so
// neither has to be read off the legend:
//
//   ROLE   — an EDGE carries a hue, a REFERENCE is neutral grey. This is the
//            rule the chart already had when it was one pair, kept intact: the
//            reference is the control the reader checks against rather than
//            reads, and ink is what makes it recede.
//   REGION — which hue, and which grey. Violet and its darker grey are
//            Singapore; teal and its lighter grey are Amsterdam.
//
// The two greys separate on lightness alone, which is the weaker channel, and
// they only have room to because they go in OPPOSITE directions from the
// panel: REFERENCE_COLOR is as dark as 3:1 permits, so Amsterdam's had to be
// the lighter one.
//
// Measured against #1f1f1e, the lighter end of the .card gradient. Worst case
// across the chart is the two EDGES against each other under deuteranopia,
// ΔE00 11.7 — above the 10.2 the green/grey pair shipped with before this
// change, which is the bar anything here has to clear.
//
//   #8f7ad4 vs #4f93a8 (the two edges)       ΔE00 21.6  deut 11.7
//   #6b6963 vs #8f8d85 (the two references)  ΔE00 14.1  deut 14.0  prot 14.1
//   #4f93a8 vs #8f8d85 (own pair)            ΔE00 20.1  deut 28.3
//   #8f7ad4 vs #6b6963 (sgp pair)            ΔE00 31.6  deut 27.2  prot 28.7
//   #4f93a8 vs #3987e5 (mimo-v2.5)           ΔE00 15.8  deut 10.5
//   #8f7ad4 vs #d95926 (mimo-v2.5-pro)       ΔE00 42.6  deut 55.3
//
// Both edge hues are muted rather than saturated, which is what keeps them
// inside the warm-editorial palette's voice while staying clear of the two
// model identities — see MIMO_EDGE_COLOR for why the accent family could not
// be used for this.
export const MIMO_EDGE_AMS_COLOR = "#4f93a8";

// Amsterdam's reference: neutral, like Singapore's, and LIGHTER rather than
// darker.
//
// The instinct is to mirror REFERENCE_COLOR by going darker still, and there is
// nowhere to go — that value sits at 3.005:1, the floor for a chart line. So
// the second grey moves the other way, to 4.96:1, and the pair reads as two
// distinct neutrals rather than one grey and one smudge.
//
// ΔE00 4.3 from WIRE_COLOR (#9c9a92), which is close. Accepted because
// WIRE_COLOR never appears on this chart: it is the decomposition's neutral,
// and the two are never on screen in the same axes. Do not narrow that gap
// further without checking that constraint still holds.
export const REFERENCE_AMS_COLOR = "#8f8d85";

// The server-side remainder in the decomposition. Deliberately NOT
// SERIES_COLORS[0]: that hue is mimo-v2.5's identity, and the decomposition
// paints this segment on every model's row — including mimo-v2.5-pro's — so
// borrowing it made one colour mean two different things on the same page.
// This is the page accent, which encodes emphasis rather than identity.
// Against #1f1f1e, paired with WIRE_COLOR: CVD ΔE 10.8, normal-vision ΔE 15.3.
export const SERVER_COLOR = "#c6613f";

export function colorForModel(modelID: string, models: string[]): string {
  const i = models.indexOf(modelID);
  return SERIES_COLORS[i >= 0 ? i % SERIES_COLORS.length : 0]!;
}

const AXIS = "#6b6963";
const GRID = "#2e2e2b";
const INK = "#faf9f5";
// --color-panel, the top stop of .card's surface. Canvas cannot read a CSS
// variable, so the surface a chart sits on has to be restated here whenever a
// mark needs to be cut out of it. .card is a gradient (#1f1f1e → #1c1c1b), so
// this is an approximation rather than an exact match — the two ends differ by
// three units per channel, which is below the threshold at which a 1px seam is
// visible.
const PANEL = "#1f1f1e";

// toPairs maps points to [timestampMs, value].
//
// A null p50 becomes a null VALUE rather than a dropped point, so ECharts draws
// a gap. Dropping the point would join the line across the hole and invent
// continuity that was never measured; a zero would draw a floor.
function toPairs(points: Point[], key: "p50" | "p95" = "p50") {
  return points.map((p) => [p.t * 1000, p[key]] as [number, number | null]);
}

function allValues(series: Record<string, Point[]>): (number | null)[] {
  return Object.values(series).flatMap((points) => points.map((p) => p.p50));
}

// BOUND_MANTISSAS is the ladder a log axis's ends are allowed to land on.
//
// Left to itself ECharts rounds a log axis out to whole DECADES, and one spike
// is enough to buy an entire empty one: a chart reading 830 ms to 56 s was
// drawn from 100 ms to 100 s, with the data squeezed into the middle half and
// two of its four gridlines standing over nothing at all. Snapping the ends to
// a finer ladder gives that space back — the same chart runs 700 ms to 70 s.
//
// Finer than 1-2-5 because 1-2-5 cannot express a bound just past a half
// decade: the 56 s reading above falls between 50 and 100, so it would still
// have taken the whole decade.
const BOUND_MANTISSAS = [1, 1.5, 2, 3, 5, 7];

// TICK_MANTISSAS are the gridline ladders, sparse first.
//
// Decades alone once the plot spans more than a couple of them, and finer as it
// narrows, so that the count stays readable rather than the spacing staying
// constant. Every entry is a round number, which is the whole reason the ticks
// are chosen here: ECharts, handed a min and a max, splits the span evenly in
// log space and puts gridlines at 5011.87 and 50118.72.
const TICK_MANTISSAS = [[1], [1, 3], [1, 2, 5]];

// A log axis wants its lowest reading off the floor and its highest off the
// ceiling, or the line is drawn along an edge and reads as clipped.
const BOUND_PAD = 1.05;

function niceLogBound(v: number, up: boolean): number {
  const decade = Math.pow(10, Math.floor(Math.log10(v)));
  const candidates = BOUND_MANTISSAS.map((m) => m * decade);
  if (up) {
    return candidates.find((c) => c >= v) ?? decade * 10;
  }
  return [...candidates].reverse().find((c) => c <= v) ?? decade;
}

// logAxis fits a log axis to the values it has to hold: ends snapped outward to
// the nearest nice bound, gridlines on round numbers strictly inside them.
//
// Null when the data cannot support a fitted axis — no positive values, or a
// range so narrow that no round number falls strictly inside the bounds — in
// which case the caller leaves the axis to ECharts rather than handing it a
// range with not a single gridline in it.
export function logAxis(
  values: (number | null)[],
): { min: number; max: number; ticks: number[] } | null {
  const finite = values.filter(
    (v): v is number => v !== null && Number.isFinite(v) && v > 0,
  );
  if (finite.length === 0) {
    return null;
  }
  const min = niceLogBound(Math.min(...finite) / BOUND_PAD, false);
  const max = niceLogBound(Math.max(...finite) * BOUND_PAD, true);
  if (!(max > min)) {
    return null;
  }

  const ticksFor = (mantissas: number[]) => {
    const out: number[] = [];
    const from = Math.floor(Math.log10(min));
    const to = Math.ceil(Math.log10(max));
    for (let k = from; k <= to; k++) {
      for (const m of mantissas) {
        const v = m * Math.pow(10, k);
        // Strictly inside: a gridline drawn ON the bound is the plot's own
        // edge, so it costs a label without drawing a line.
        if (v > min && v < max) {
          out.push(v);
        }
      }
    }
    return out.sort((a, b) => a - b);
  };

  // Three gridlines is the fewest that still reads as a scale rather than as a
  // single annotated height. The densest ladder is the floor: below ~20x the
  // axis would not have gone log in the first place, and the loop's last pass
  // leaves that ladder in place whether or not it reached three.
  let ticks: number[] = [];
  for (const mantissas of TICK_MANTISSAS) {
    ticks = ticksFor(mantissas);
    if (ticks.length >= 3) {
      break;
    }
  }
  // Not one round number inside the bounds — a range narrower than the gap
  // between two ladder rungs. customValues: [] would draw an axis with no
  // gridlines and no labels at all, which is worse than ECharts' own nicing.
  if (ticks.length === 0) {
    return null;
  }
  return { min, max, ticks };
}

// SMOOTHED_SUFFIX marks a series as the smoothed twin of a measurement rather
// than a measurement of its own. The tooltip filters on it: hovering a chart
// with two models must report two numbers, and a rolling median is not a
// reading anything was ever measured at.
export const SMOOTHED_SUFFIX = " trend";

// SMOOTH_DIVISOR sets the rolling window as a fraction of the points on screen.
//
// A fraction rather than a fixed duration, because the bucket width already
// varies by an order of magnitude across the windows (2h on 7d, 6h on 3mo) and
// a constant number of hours would smooth 7d into a straight line while barely
// touching 3mo. An eighth leaves roughly eight bends in the smoothed line —
// enough to show where a change began, too few to be read as a shape.
const SMOOTH_DIVISOR = 8;

// smoothWindow is the odd bucket count the rolling median runs over.
//
// Odd so the window is CENTRED: an even one sits half a bucket to one side, and
// the smoothed line would lag the data it is drawn over by a constant amount.
export function smoothWindow(pointCount: number): number {
  return Math.max(5, (pointCount / SMOOTH_DIVISOR) | 0 | 1);
}

// smoothSpanMs is how much WALL CLOCK a window of that many buckets covers.
//
// Not `buckets × bucket_s`, which is the obvious arithmetic and is wrong
// whenever the probe stopped: the API emits a row only for buckets that had a
// sample or a censored run (the series query unions the two), so a stretch
// where nothing ran is not a row of nulls — it is no rows at all, and the
// window walks over indices, not over hours. Multiplied out, an 11-bucket
// window spanning a 12-hour collector outage would still be published as
// "22-hour", understating the smoothing exactly where it is strongest.
//
// The MEDIAN reach rather than the largest, because one outage should not
// restate the whole line's smoothing as if every point were averaged that far;
// the median describes what the window does across the plot. Measured on the
// interior points where the window is whole — the ends run over a shrinking
// half-window and would drag the figure below what the line actually does.
export function smoothSpanMs(
  pairs: [number, number | null][],
  window: number,
): number {
  const half = window >> 1;
  const reaches: number[] = [];
  for (let i = half; i < pairs.length - half; i++) {
    reaches.push(pairs[i + half]![0] - pairs[i - half]![0]);
  }
  if (reaches.length === 0) {
    // Fewer points than the window is wide: the whole plot is one window.
    const first = pairs[0]?.[0];
    const last = pairs[pairs.length - 1]?.[0];
    return first !== undefined && last !== undefined ? last - first : 0;
  }
  reaches.sort((a, b) => a - b);
  return reaches[reaches.length >> 1]!;
}

// MIN_COVERAGE is how much of a window has to be measured for its median to be
// drawn, as a fraction of the buckets that window actually spans.
//
// A centred median over a window that is mostly holes is a median of two
// buckets pretending to speak for eight, and it lands wherever those two
// happened to be. Below the threshold the smoothed line takes a gap, like the
// raw line under it.
const MIN_COVERAGE = 0.5;
// Under any coverage rule, three points is the fewest a median can be taken
// from and still be a median rather than a reading.
const MIN_SAMPLES = 3;

// rollingMedian is the smoothed twin of a series: a centred rolling median over
// `window` buckets, in the same [ms, value] pair shape as the raw line.
//
// Median rather than mean, because this page's own spikes are what a mean would
// carry into the smoothed line: one 56-second timeout in an eight-bucket window
// drags the average above every reading in it, and the smoothed line then bends
// around an outlier that the raw line already shows perfectly well.
//
// The ENDS are kept rather than trimmed, over a shrinking half-window. Trimmed,
// the smoothed line would stop short of the right edge — which is the end a
// status page is read from, and a trend that stops half a window before "now"
// is the one part of it nobody can use.
export function rollingMedian(
  pairs: [number, number | null][],
  window: number,
): [number, number | null][] {
  const half = window >> 1;
  return pairs.map(([t], i) => {
    const candidates = pairs.slice(Math.max(0, i - half), i + half + 1);
    const slice = candidates
      .map(([, v]) => v)
      .filter((v): v is number => v !== null && Number.isFinite(v));
    // Measured against the buckets this point could HAVE — not against the full
    // window, which the ends never get. Half a window is missing at the last
    // point by construction, so a full-window rule would demand 100% coverage
    // exactly at "now": one hole anywhere in the final half-window and the
    // trend would stop short of the right edge, which is the end this line
    // exists to reach.
    const needed = Math.max(
      MIN_SAMPLES,
      Math.ceil(candidates.length * MIN_COVERAGE),
    );
    if (slice.length < needed) {
      return [t, null];
    }
    const sorted = [...slice].sort((a, b) => a - b);
    const mid = sorted.length >> 1;
    const value =
      sorted.length % 2 === 1
        ? sorted[mid]!
        : (sorted[mid - 1]! + sorted[mid]!) / 2;
    return [t, value];
  });
}

// How far the measurement recedes once a smoothed line is drawn over it.
//
// Low enough that the two cannot be confused for a pair of equals, high enough
// that a spike still reads as a spike: below about a quarter the raw line
// disappears into the gridlines on the log panels, where its own excursions are
// compressed to begin with.
const RAW_UNDER_SMOOTH_OPACITY = 0.35;

// The censoring band colour. The fault amber, not a series hue: a stretch where
// measurements were cut off is not a measurement.
const CENSORED = "#c98500";

// SPAN_HHMM_MS is where the axis stops being able to go without a date.
//
// Below it every tick falls on the same day or the one before, and HH:mm is
// unambiguous; above it a bare time repeats itself across the plot. 48h is the
// longest window whose ticks are still hours.
const SPAN_HHMM_MS = 48 * 3_600_000;

// censoredBuckets collects the bucket starts, in ms, where any series had a
// sample cut off by the timeout ladder.
//
// Across all series rather than per series: the band says "the top of the
// distribution was cut off in this stretch", and drawing one per model would
// stack two translucent amber rectangles into a darker one that means nothing
// more than the single band does. The per-model count is on the model card.
export function censoredBuckets(series: Record<string, Point[]>): number[] {
  const starts = new Set<number>();
  for (const points of Object.values(series)) {
    for (const p of points) {
      if (p.censored > 0) starts.add(p.t * 1000);
    }
  }
  return [...starts].sort((a, b) => a - b);
}

// censoredBands merges adjacent censored buckets into single spans.
//
// Merged because an incident is a stretch, not a row of stripes: eight
// neighbouring bucket-wide rectangles read as eight separate events, and the
// seams between them read as recovery that never happened.
export function censoredBands(
  series: Record<string, Point[]>,
  bucketMs: number,
): [number, number][] {
  const bands: [number, number][] = [];
  for (const start of censoredBuckets(series)) {
    const last = bands[bands.length - 1];
    if (last && start <= last[1]) {
      last[1] = start + bucketMs;
      continue;
    }
    bands.push([start, start + bucketMs]);
  }
  return bands;
}

type LineOpts = {
  series: Record<string, Point[]>;
  order: string[];
  colorOf: (name: string) => string;
  unit: string;
  // forceLinear pins the axis for values that are not latency (percentages,
  // token counts), where a log axis would be nonsense.
  forceLinear?: boolean;
  // bucketMs is the window's bucket width, needed to give a censoring band a
  // right edge. Without it no bands are drawn — a band of unknown width is worse
  // than none, because it would misstate how much of the window was affected.
  bucketMs?: number;
  // smoothed draws a rolling median over each series and drops the raw line to a
  // hairline behind it. Opt-in, and off by default, because the wire chart
  // shares this builder: there the reader is comparing an edge against its
  // reference at a given instant, not asking where the last month went, and a
  // second line per host would put eight lines on a four-line plot.
  smoothed?: boolean;
};

// timeExtent is the first and last timestamp, in ms, across every series.
//
// The real data range rather than whatever ECharts settles on, because that is
// what decides whether the axis can label ticks without a date.
function timeExtent(series: Record<string, Point[]>): [number, number] | null {
  let min = Infinity;
  let max = -Infinity;
  for (const points of Object.values(series)) {
    for (const p of points) {
      const t = p.t * 1000;
      if (t < min) min = t;
      if (t > max) max = t;
    }
  }
  return Number.isFinite(min) && Number.isFinite(max) ? [min, max] : null;
}

// escapeHTML guards the tooltip, which is rendered as HTML by ECharts.
//
// Series names reach it from the API response — today they are model IDs, but a
// tooltip that interpolates a server-supplied string into markup is a hole
// whether or not anything currently fits through it.
function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

type AxisTooltipParam = {
  marker?: string;
  seriesName?: string;
  value?: unknown;
};

// buildLineOption is the shared shape for every time series on the page.
export function buildLineOption({
  series,
  order,
  colorOf,
  unit,
  forceLinear = false,
  bucketMs,
  smoothed = false,
}: LineOpts) {
  // The y-axis switches to log automatically when the window's dynamic range
  // exceeds 20x, because a linear axis collapses either the normal reading or
  // the spike. The caller stamps "LOG SCALE" on the plot when this is true — a
  // log axis read as linear is worse than no chart.
  const values = allValues(series);
  // A log axis cannot render a zero or a negative, and ECharts does not refuse
  // them — it drops the points, leaving a line with holes in it that look
  // exactly like buckets where nothing was measured. Nothing plotted here could
  // reach zero while every series was a latency or a rate; the prefill delta
  // can, being a DIFFERENCE, and a delta at or below zero is the one reading
  // that says the baseline moved rather than prefill. So the guard lives with
  // the axis rather than with the caller: any series that can go non-positive
  // stays linear, whatever its spread.
  const plottableOnLog = values.every((v) => v === null || v > 0);
  const log = !forceLinear && plottableOnLog && shouldUseLogScale(values);
  // Fitted to the data rather than rounded out to decades — see logAxis.
  const fitted = log ? logAxis(values) : null;

  // Where the timeout ladder cut runs off, the line is drawn from the runs that
  // FINISHED — so it is at its most flattering exactly where it is least
  // complete, and where every value is missing it is not drawn at all, which
  // looks identical to the probe not running. The band is what makes the
  // difference visible; without it the chart's best-looking stretches are
  // unreadable.
  const bands = bucketMs ? censoredBands(series, bucketMs) : [];
  const names = order.filter((name) => series[name] !== undefined);

  const extent = timeExtent(series);
  // A bare HH:mm repeats itself once the plot spans more than two days, and a
  // reader cannot tell the Tuesday spike from the Thursday one.
  const spansDays = extent !== null && extent[1] - extent[0] > SPAN_HHMM_MS;
  // Date OR time, never both. ECharts picks its own tick spacing, and the full
  // "04 Aug, 07:00" stamp is wide enough that on 3mo the labels overlapped into
  // an unreadable smear. Above the threshold the ticks are days apart, so the
  // date alone identifies them; the tooltip still carries the exact time.
  const stamp = spansDays ? formatDate : formatTime;

  // The smoothing is gated on the SAME 48h threshold the axis stamp is, and for
  // a related reason: below it the window is short enough that the reader is
  // looking at what is happening now, not at where the last week went, and a
  // rolling median over a couple of hours only redraws the noise slightly
  // rounder. 24h and 48h keep the plain chart; 7d and up get the trend.
  const smoothing = smoothed && spansDays;
  // Sized off the LONGEST series, so two models with different coverage are
  // smoothed over the same window and their lines stay comparable.
  const window = smoothWindow(
    Math.max(0, ...Object.values(series).map((points) => points.length)),
  );
  // Read off the longest series for the same reason, and off its TIMESTAMPS
  // rather than off the bucket width — see smoothSpanMs.
  const longest = Object.values(series).reduce<Point[]>(
    (best, points) => (points.length > best.length ? points : best),
    [],
  );
  const spanMs = smoothing ? smoothSpanMs(toPairs(longest), window) : 0;

  return {
    animation: false,
    grid: { left: 52, right: 16, top: 16, bottom: 28 },
    tooltip: {
      trigger: "axis",
      backgroundColor: "#242422",
      borderColor: GRID,
      textStyle: { color: INK, fontSize: 12 },
      // Written out rather than left to valueFormatter, because the HEADER is
      // the part that was wrong: ECharts stamps a time-axis tooltip in the
      // BROWSER's zone, so a reader outside Switzerland got one clock here and
      // another in the samples table below. Everything the old valueFormatter
      // did is carried over — in particular null rendering as "no data" and
      // never as a number, which is what keeps a gap from reading as a zero.
      formatter: (raw: AxisTooltipParam | AxisTooltipParam[]) => {
        // The smoothed twins are dropped before anything is read off the rows,
        // including the header: they carry the same timestamps as the raw
        // lines, so the stamp is unaffected, and a hover has to report what was
        // measured rather than what was drawn over it.
        const rows = (Array.isArray(raw) ? raw : [raw]).filter(
          (r) => !(r.seriesName ?? "").endsWith(SMOOTHED_SUFFIX),
        );
        const first = rows[0]?.value;
        const t = Array.isArray(first) ? first[0] : undefined;
        const head =
          typeof t === "number"
            ? // A Date, not the raw number: formatTime/formatDateTime read a
              // bare number as epoch SECONDS, and the axis deals in ms.
              `<div style="color:${AXIS};font-size:11px">${formatDateTime(new Date(t))}</div>`
            : "";
        const body = rows
          .map((r) => {
            const pair = r.value;
            const v = Array.isArray(pair) ? pair[1] : undefined;
            const text =
              typeof v === "number" && Number.isFinite(v)
                ? `${round(v)} ${unit}`
                : "no data";
            return `${r.marker ?? ""}${escapeHTML(r.seriesName ?? "")} <b>${text}</b>`;
          })
          .join("<br/>");
        return head + body;
      },
    },
    xAxis: {
      type: "time",
      axisLine: { lineStyle: { color: GRID } },
      axisLabel: {
        color: AXIS,
        fontSize: 10,
        // Every tick is stamped in the reader's own zone, like every other time
        // on the page — see format.ts. ECharts has no per-axis timezone, only a
        // global useUTC, so the label goes through our formatters here rather
        // than being left to the library. What an axis must never do is
        // disagree with the other times on the card — the tooltip header, the
        // cost axis, the pulse strip's hover titles — and routing all of them
        // through the same formatters is what guarantees it cannot.
        // A Date, not the raw number — see the tooltip header.
        formatter: (value: number) => stamp(new Date(value)),
        // ECharts adds a tick at each month boundary on top of its regular
        // spacing, which on 3mo lands one right beside another and prints the
        // two labels over each other. A dropped label costs nothing here; an
        // unreadable one costs the whole axis.
        hideOverlap: true,
      },
      splitLine: { show: false },
    },
    yAxis: {
      // A log axis cannot render a zero or a negative, and ECharts silently
      // drops such points; min is left to ECharts rather than pinned to 0 for
      // the same reason.
      type: log ? "log" : "value",
      // Undefined rather than absent when the axis is linear or unfittable, so
      // ECharts falls back to its own nicing instead of being handed a bound.
      min: fitted?.min,
      max: fitted?.max,
      axisLine: { show: false },
      axisLabel: {
        color: AXIS,
        fontSize: 10,
        // customValues is the only way to hold BOTH a fitted range and round
        // gridlines: ECharts, given a min and a max, divides the span evenly in
        // log space and lands its ticks on values like 5011.87.
        customValues: fitted?.ticks,
        // Milliseconds are what the wire carries and what every tick is
        // counted in, but a spike axis runs to five digits, and "100,000" is
        // not a latency anybody reads. On the linear axes too, where the same
        // ticks were printing as "1,000" and "2,000". Only for the ms charts:
        // the unit is the caller's, and a token count formatted as a duration
        // would be a lie.
        formatter: unit === "ms" ? formatAxisMs : undefined,
      },
      // The gridlines follow THIS list, not splitLine's own: ECharts resolves
      // custom ticks from the axisTick model whatever component asks for them
      // (createAxisTicks reads axis.getTickModel()). splitLine repeats it below
      // so the two cannot drift apart if that ever changes.
      axisTick: { customValues: fitted?.ticks },
      splitLine: {
        customValues: fitted?.ticks,
        lineStyle: { color: GRID, type: "dashed" },
      },
    },
    series: names
      .map((name, i) => {
        return {
          name,
          type: "line",
          showSymbol: false,
          // Gaps are gaps: never connect across a bucket with no data.
          connectNulls: false,
          // Under a smoothed twin the measurement becomes a hairline. It is not
          // hidden and never averaged away — every spike the smoothing steps over
          // is still on the plot, and the tooltip still reads from THIS line — but
          // the bold stroke belongs to the line the reader is meant to follow.
          lineStyle: smoothing
            ? {
                width: 1,
                color: colorOf(name),
                opacity: RAW_UNDER_SMOOTH_OPACITY,
              }
            : { width: 2, color: colorOf(name) },
          itemStyle: { color: colorOf(name) },
          // Hoverable, unlike its smoothed twin: this is the line the tooltip
          // reads from. Stated rather than left off so both halves of the series
          // array have the same shape.
          silent: false,
          // No fill under a hairline: an area is a solid shape carrying the same
          // weight as ever, so keeping it would undo the recession the thinner
          // stroke is there to create, and two translucent fills would sit under
          // the smoothed lines as a wash nobody can read a value out of.
          areaStyle: smoothing
            ? undefined
            : { opacity: 0.12, color: colorOf(name) },
          data: toPairs(series[name]!),
          // Hung off the FIRST series only. markArea is per series, so attaching
          // it to each would paint the same rectangles once per model and darken
          // the band into something that reads as a severity it does not carry.
          // silent, so it never takes over the tooltip from the data.
          //
          // The styling is carried PER ITEM rather than on the markArea itself:
          // ECharts allows only one markArea per series, so a second kind of band
          // added here later has to be able to style itself independently.
          markArea:
            i === 0 && bands.length > 0
              ? {
                  silent: true,
                  data: [
                    ...bands.map(([from, to]) => [
                      {
                        xAxis: from,
                        // ECharts paints markArea BENEATH the series, so this fill
                        // is read through the line's own area fill and loses much
                        // of its chroma on the way. Checked on the rendered plot:
                        // below ~0.3 it arrives as a grey shadow, which reads as a
                        // rendering artefact rather than as the caution the legend
                        // swatch promises.
                        itemStyle: { color: CENSORED, opacity: 0.3 },
                      },
                      { xAxis: to },
                    ]),
                  ],
                }
              : undefined,
        };
      })
      // Appended AFTER every raw series, never interleaved: the censoring bands
      // hang off series index 0, and a smoothed line landing there would take
      // the markArea with it — onto a series that is drawn over the very
      // stretches the bands are about.
      .concat(
        smoothing
          ? names.map((name) => ({
              name: `${name}${SMOOTHED_SUFFIX}`,
              type: "line",
              showSymbol: false,
              connectNulls: false,
              // silent, so the smoothed line never takes a hover from the
              // measurement underneath it.
              silent: true,
              lineStyle: { width: 2, color: colorOf(name) },
              itemStyle: { color: colorOf(name) },
              // Never a fill, and never the censoring bands: the trend is a
              // stroke, and the bands belong to the measurement below it.
              areaStyle: undefined,
              markArea: undefined,
              data: rollingMedian(toPairs(series[name]!), window),
            }))
          : [],
      ),
    logScale: log,
    // Surfaced so the panel can announce the bands in words. A colour-only
    // signal is not a signal here: the whole point is a reader who would
    // otherwise take the plot at face value.
    censoredBands: bands.length,
    // Surfaced for the same reason, and it is the stronger case of the two: the
    // panel has to be able to say which of the two lines per model was measured
    // and which was drawn, and it must only say it on the windows that actually
    // got a trend.
    smoothed: smoothing,
    // How much wall clock that window covers, so the note can state it rather
    // than leaving "smoothed" to mean whatever the reader assumes. In ms and
    // measured off the data, never derived from the bucket width — a stretch
    // where the probe did not run has no buckets at all to multiply.
    smoothSpanMs: spanMs,
  };
}

// buildDecompositionOption draws TTFT split into the measured edge RTT and the
// residual.
//
// Stacked, because the whole claim is that the two sum to the observed TTFT.
// The residual is labelled "server-side" and never "model" — the handshake
// terminates at the TLS edge, and any edge-to-compute backhaul sits inside it.
export function buildDecompositionOption(
  models: { id: string; ttft: number | null; edge: number | null }[],
) {
  const names = models.map((m) => m.id);
  const edge = models.map((m) => m.edge ?? null);
  const residual = models.map((m) =>
    m.ttft !== null && m.edge !== null ? Math.max(0, m.ttft - m.edge) : null,
  );

  return {
    animation: false,
    grid: { left: 8, right: 16, top: 8, bottom: 24, containLabel: true },
    tooltip: {
      trigger: "axis",
      axisPointer: { type: "shadow" },
      backgroundColor: "#242422",
      borderColor: GRID,
      textStyle: { color: INK, fontSize: 12 },
      valueFormatter: (v: number | null) =>
        v === null || v === undefined ? "no data" : `${round(v)} ms`,
    },
    legend: {
      textStyle: { color: AXIS, fontSize: 10 },
      bottom: 0,
      itemHeight: 8,
      itemWidth: 12,
    },
    xAxis: {
      type: "value",
      axisLine: { show: false },
      axisLabel: { color: AXIS, fontSize: 10 },
      splitLine: { lineStyle: { color: GRID, type: "dashed" } },
    },
    yAxis: {
      type: "category",
      data: names,
      axisLine: { lineStyle: { color: GRID } },
      axisLabel: { color: AXIS, fontSize: 11 },
    },
    series: [
      // barMaxWidth caps the bar rather than sizing it: with two categories in
      // a short plot the ECharts default band left each bar ~50px tall, which
      // read as a status meter instead of a measurement.
      //
      // The 1px border in the surface colour is what separates the two stacked
      // segments — 1px from each puts a 2px cut between them. ECharts draws the
      // border on all four sides, so it also shaves 1px off every other edge;
      // that is invisible at this width, and the alternative (a transparent
      // spacer series) would show up in the tooltip as a measurement.
      {
        name: "to the edge",
        type: "bar",
        stack: "ttft",
        barMaxWidth: 18,
        itemStyle: { color: WIRE_COLOR, borderColor: PANEL, borderWidth: 1 },
        data: edge,
      },
      {
        name: "server-side",
        type: "bar",
        stack: "ttft",
        barMaxWidth: 18,
        itemStyle: { color: SERVER_COLOR, borderColor: PANEL, borderWidth: 1 },
        data: residual,
      },
    ],
  };
}

// round is the tooltip's number, and the sibling of formatAxisMs — the two sit
// on the same card and have to agree.
//
// The precision test is on the MAGNITUDE. Written as `v < 100` it was correct
// for as long as every plotted value was a positive latency, and silently wrong
// the moment one could go below zero: every negative satisfies `v < 100`, so
// −1234.6 kept a decimal the axis had already dropped while +1234.6 rounded to
// 1235. The prefill delta is the first series here that can be negative.
//
// U+2212 for the sign, not the hyphen toFixed emits, because the axis beside it
// deliberately uses U+2212 and two minus signs on one card is a tell that one of
// the two numbers came from somewhere else.
function round(v: number): string {
  const abs = Math.abs(v);
  const digits = Number(abs.toFixed(abs < 100 ? 1 : 0)).toString();
  // Number() has already collapsed a rounded −0.04 to "0", so a sign is only
  // ever printed against a magnitude that survived rounding.
  return v < 0 && digits !== "0" ? `−${digits}` : digits;
}

// The off-peak band. Green, because cheap reads as green before it reads as
// anything else.
//
// The same hex as the online green, and that overlap was the reason the band was
// taken off the latency charts: there, green already meant "up", and a wash
// behind a latency line had to be read through the measurement it was not about.
// Here it sits behind money, which is the quantity the rate actually governs,
// and it is the only green on the card.
const OFFPEAK = "#5aa06a";

// The band is painted BENEATH the cost line and its own area fill, so the green
// arrives twice diluted. At 0.13 it landed as a grey smudge on the panel — read
// on the rendered plot as a lighting artefact rather than as the shaded hours
// the caption names. 0.3 is the same weight the censoring bands carry, for the
// same reason, and the 1px edge fixes where the reduced rate starts and stops:
// a wash this soft has no readable boundary on its own.
const OFFPEAK_FILL = 0.3;

// SPAN_HHMM_MS is where a bare HH:mm stops being unambiguous — past two days it
// repeats across the plot.
const COST_SPAN_HHMM_MS = 48 * 3_600_000;

// buildCostOption draws what each bucket of the window cost, with the
// reduced-rate spans shaded behind it.
//
// One line, not one per model. The panel answers "what did this cost", and a
// run's cadence is not a fact about its bill: every run lands in whichever
// bucket it happened in and is summed there.
export function buildCostOption(
  points: { t: number; usd: number | null }[],
  spans: [number, number][],
) {
  const data = points.map(
    (p) => [p.t * 1000, p.usd] as [number, number | null],
  );
  const first = data[0]?.[0];
  const last = data[data.length - 1]?.[0];
  const spansDays =
    first !== undefined &&
    last !== undefined &&
    last - first > COST_SPAN_HHMM_MS;
  const stamp = spansDays ? formatDate : formatTime;

  // Past 48 hours the band becomes one thin stripe per day — seven on 7d, ninety
  // on 3mo — which reads as a hatch pattern rather than as a nightly window and
  // buries the line under it. The rate is still stated in words below the chart.
  const bands = spansDays ? [] : spans;

  return {
    animation: false,
    grid: { left: 64, right: 16, top: 16, bottom: 28 },
    tooltip: {
      trigger: "axis",
      backgroundColor: "#242422",
      borderColor: GRID,
      textStyle: { color: INK, fontSize: 12 },
      formatter: (raw: AxisTooltipParam | AxisTooltipParam[]) => {
        const rows = Array.isArray(raw) ? raw : [raw];
        const pair = rows[0]?.value;
        const t = Array.isArray(pair) ? pair[0] : undefined;
        const v = Array.isArray(pair) ? pair[1] : undefined;
        const head =
          typeof t === "number"
            ? `<div style="color:${AXIS};font-size:11px">${formatDateTime(new Date(t))}</div>`
            : "";
        const body =
          typeof v === "number" && Number.isFinite(v)
            ? `${rows[0]?.marker ?? ""}cost <b>${formatUSDPrecise(v)}</b>`
            : "no data";
        return head + body;
      },
    },
    xAxis: {
      type: "time",
      axisLine: { lineStyle: { color: GRID } },
      axisLabel: {
        color: AXIS,
        fontSize: 10,
        // The reader's zone, like every other axis on the page — ECharts has no
        // per-axis timezone, so the label is formatted here.
        formatter: (value: number) => stamp(new Date(value)),
        hideOverlap: true,
      },
      splitLine: { show: false },
    },
    yAxis: {
      // Always linear and always anchored at zero. This is money over a fixed
      // workload: a log axis would turn a 20% rebate into a shrug, and a floating
      // baseline would turn it into a cliff.
      type: "value",
      min: 0,
      axisLine: { show: false },
      axisLabel: {
        color: AXIS,
        fontSize: 10,
        formatter: (v: number) => formatUSDPrecise(v),
      },
      splitLine: { lineStyle: { color: GRID, type: "dashed" } },
    },
    series: [
      {
        name: "cost",
        type: "line",
        // Stepped: a bucket's cost is a quantity for the whole bucket, not a
        // reading at its left edge, and sloping between them would draw a
        // gradual change the billing does not have.
        step: "end",
        showSymbol: false,
        // A bucket with no runs is a gap, never a zero — the probe not running
        // is not the same as it costing nothing.
        connectNulls: false,
        lineStyle: { width: 2, color: SERVER_COLOR },
        itemStyle: { color: SERVER_COLOR },
        areaStyle: { opacity: 0.12, color: SERVER_COLOR },
        data,
        markArea:
          bands.length > 0
            ? {
                // silent, so the band never takes the tooltip from the data.
                silent: true,
                data: bands.map(([from, to]) => [
                  {
                    xAxis: from * 1000,
                    itemStyle: {
                      color: OFFPEAK,
                      opacity: OFFPEAK_FILL,
                      borderColor: OFFPEAK,
                      borderWidth: 1,
                    },
                  },
                  { xAxis: to * 1000 },
                ]),
              }
            : undefined,
      },
    ],
    // Surfaced so the panel knows whether to caption a band it can see.
    banded: bands.length > 0,
  };
}

// TREND_HEIGHT is the banner plot. Short on purpose: it sits under a sentence
// that has already said what happened, so its job is the SHAPE — a step, a
// ramp, a spike that is already over — and a full-height chart there would push
// the rest of the page below the fold on exactly the day the page matters.
export const TREND_HEIGHT = 120;

// buildTrendOption draws the whole span the speed reading compares, with the
// recent side picked out.
//
// One metric, and the models the caller says moved on it — a claim about "both
// models" still arrives as two lines that can be checked rather than taken on
// trust, while a reading about one model no longer drags a steady second line
// onto the shared axis. The shading marks the recent span and the dashed line
// marks where the reference level sat — the gap between the line and the dashes
// IS the change, read without arithmetic.
export function buildTrendOption({
  series,
  order,
  colorOf,
  unit,
  recentFromMs,
  referenceLevel,
}: {
  series: Record<string, Point[]>;
  order: string[];
  colorOf: (name: string) => string;
  unit: string;
  recentFromMs: number;
  referenceLevel: number | null;
}) {
  const names = order.filter((name) => series[name] !== undefined);
  const extent = timeExtent(series);
  return {
    animation: false,
    // Tighter than the panels' grid on every side, and with no y-axis labels:
    // the sentence above this plot already states both values, and a second
    // copy on an axis is noise on a chart that exists to show a shape.
    grid: { left: 8, right: 8, top: 10, bottom: 22 },
    tooltip: {
      trigger: "axis",
      backgroundColor: "#242422",
      borderColor: GRID,
      textStyle: { color: INK, fontSize: 12 },
      // The same hand-written formatter the panels use, and for the same
      // reason: ECharts stamps a time-axis tooltip in the BROWSER's zone, which
      // would disagree with every other time on this page.
      formatter: (raw: AxisTooltipParam | AxisTooltipParam[]) => {
        const rows = Array.isArray(raw) ? raw : [raw];
        const first = rows[0]?.value;
        const t = Array.isArray(first) ? first[0] : undefined;
        const head =
          typeof t === "number"
            ? `<div style="color:${AXIS};font-size:11px">${formatDateTime(new Date(t))}</div>`
            : "";
        return (
          head +
          rows
            .map((r) => {
              const pair = r.value;
              const v = Array.isArray(pair) ? pair[1] : undefined;
              const text =
                typeof v === "number" && Number.isFinite(v)
                  ? `${round(v)} ${unit}`
                  : "no data";
              return `${r.marker ?? ""}${escapeHTML(r.seriesName ?? "")} <b>${text}</b>`;
            })
            .join("<br/>")
        );
      },
    },
    xAxis: {
      type: "time",
      min: extent?.[0],
      max: extent?.[1],
      axisLine: { lineStyle: { color: GRID } },
      axisTick: { show: false },
      axisLabel: {
        color: AXIS,
        fontSize: 10,
        formatter: (value: number) => formatTime(new Date(value)),
        hideOverlap: true,
      },
      splitLine: { show: false },
    },
    yAxis: {
      type: "value",
      // Scaled to the data rather than anchored at zero: this plot is about a
      // change, and a zero floor flattens every change worth drawing.
      scale: true,
      axisLine: { show: false },
      axisLabel: { show: false },
      splitLine: { show: false },
    },
    series: names.map((name, i) => ({
      name,
      type: "line",
      showSymbol: false,
      // A bucket with no finished run is a gap, exactly as on every other plot
      // here: joining across it would draw a measurement never taken.
      connectNulls: false,
      lineStyle: { width: 2, color: colorOf(name) },
      itemStyle: { color: colorOf(name) },
      data: toPairs(series[name] ?? []),
      // The shading and the reference line ride on the FIRST series only —
      // ECharts draws one set per series, and two identical bands stack into a
      // darker one that reads as a third state.
      markArea:
        i === 0
          ? {
              silent: true,
              data: [
                [
                  {
                    xAxis: recentFromMs,
                    // Ink on the panel, and the only thing on the plot that says
                    // WHICH hours the sentence above it is about. At 0.05 it was
                    // invisible on the rendered chart — a band nobody can find
                    // labels nothing, and the legend still promised one.
                    itemStyle: { color: INK, opacity: 0.12 },
                  },
                  { xAxis: extent?.[1] ?? recentFromMs },
                ],
              ],
            }
          : undefined,
      markLine:
        i === 0 && referenceLevel !== null
          ? {
              silent: true,
              symbol: "none",
              label: { show: false },
              lineStyle: { color: AXIS, type: "dashed", width: 1 },
              data: [{ yAxis: referenceLevel }],
            }
          : undefined,
    })),
  };
}
