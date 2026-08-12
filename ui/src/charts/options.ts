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

// The height of a plot that is REFERENCE rather than subject — kept because
// another chart is not attributable without it, not because it is what the
// reader came for.
//
// It was 96, which is where the idea stops working: the grid spends 44 px on
// axis furniture whatever the plot's height, so 96 left 52 px of drawing, the
// y-labels crushed into an illegible column and the two probes' lines lay on
// top of each other. A strip you cannot read cannot say which probe moved,
// which is the only reason it is on the page. 140 is the smallest height that
// still separates them, and still half the chart above it.
export const REFERENCE_HEIGHT = 140;

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
  // dashed marks series that share a colour with another and must still be
  // told apart. The prefill panel plots two probes PER MODEL, and colour
  // follows the model — so without a second channel both lines are the same
  // hue and the gap between them, which is the entire point of that panel,
  // cannot be read.
  dashed?: (name: string) => boolean;
  // muted marks series that are the GROUND rather than the figure: present
  // because another series is unreadable without them, not because they are
  // what the panel is about.
  //
  // The prefill panel is the case. It replots the short probe's TTFT — the same
  // data as the chart directly above it — because the gap to the wide probe is
  // the measurement, and a lone wide line says nothing. Drawn at equal weight,
  // the panel reads as the previous chart repeated, and the gap disappears into
  // it. Muting the baseline is what makes the gap the thing you see.
  muted?: (name: string) => boolean;
  // bucketMs is the window's bucket width, needed to give a censoring band a
  // right edge. Without it no bands are drawn — a band of unknown width is worse
  // than none, because it would misstate how much of the window was affected.
  bucketMs?: number;
  // xRange pins the time axis instead of letting it fit the data.
  //
  // For charts stacked one above the other and meant to be read against each
  // other. The prefill panel is the case: the delta above is sampled at the
  // wide probe's hourly cadence and the reference strip below at the short
  // probe's, so their own extents differ by up to an hour and the two grids
  // land offset. A vertical line through both plots has to mean one instant,
  // or the strip cannot say anything about the chart it sits under.
  xRange?: [number, number];
  // compact thins the y-axis for a plot drawn at REFERENCE_HEIGHT.
  //
  // ECharts picks its tick count from the axis range, not from how tall the
  // axis actually is, so a short plot gets the same five gridlines as a full
  // one and prints them a few pixels apart. Only the linear axis needs telling:
  // a log axis here already has its ticks chosen by logAxis, sparsest ladder
  // first.
  compact?: boolean;
  // zeroLine draws a rule at y = 0.
  //
  // For a series whose values are DIFFERENCES, where zero is not the bottom of
  // the axis but the place the reading changes meaning: above it the wide
  // prompt cost something, at or below it the short baseline moved instead —
  // which is not a prefill regression, however wide the gap got.
  zeroLine?: boolean;
};

// timeExtent is the first and last timestamp, in ms, across every series.
//
// The real data range rather than whatever ECharts settles on, because that is
// what decides whether the axis can label ticks without a date.
//
// Exported because it is also how a caller works out the xRange to pin two
// stacked plots to: the range has to come from ONE of the two series sets, and
// deciding which is the caller's business.
export function timeExtent(
  series: Record<string, Point[]>,
): [number, number] | null {
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
  dashed,
  muted,
  bucketMs,
  xRange,
  compact = false,
  zeroLine = false,
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

  // The pinned range wins where there is one, so that two stacked charts agree
  // on the tick FORMAT as well as the tick positions: a strip whose own data
  // spans less than 48h would print HH:mm under a chart printing dates.
  const extent = xRange ?? timeExtent(series);
  // A bare HH:mm repeats itself once the plot spans more than two days, and a
  // reader cannot tell the Tuesday spike from the Thursday one.
  const spansDays = extent !== null && extent[1] - extent[0] > SPAN_HHMM_MS;
  // Date OR time, never both. ECharts picks its own tick spacing, and the full
  // "04 Aug, 07:00" stamp is wide enough that on 3mo the labels overlapped into
  // an unreadable smear. Above the threshold the ticks are days apart, so the
  // date alone identifies them; the tooltip still carries the exact time.
  const stamp = spansDays ? formatDate : formatTime;

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
        const rows = Array.isArray(raw) ? raw : [raw];
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
      // Undefined rather than absent when unpinned, so ECharts fits the data as
      // it always has — see xRange on LineOpts for when it is pinned.
      min: xRange?.[0],
      max: xRange?.[1],
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
      // A hint, not a rule — ECharts still nices around it — and only on the
      // linear axis, since a log one takes its ticks from logAxis. See compact.
      splitNumber: compact && !log ? 2 : undefined,
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
    series: names.map((name, i) => {
      // Ground, not figure — see `muted` on LineOpts. Thinner and dimmer, and
      // with the area fill pulled most of the way out: at 0.12 the fill is
      // what carries the eye, so leaving it while thinning the stroke would
      // mute the wrong half of the series.
      const isMuted = muted?.(name) ?? false;
      return {
        name,
        type: "line",
        showSymbol: false,
        // Gaps are gaps: never connect across a bucket with no data.
        connectNulls: false,
        lineStyle: {
          width: isMuted ? 1 : 2,
          opacity: isMuted ? 0.5 : 1,
          color: colorOf(name),
          type: dashed?.(name) ? "dashed" : "solid",
        },
        itemStyle: { color: colorOf(name) },
        // No area fill under a dashed series: two filled areas in the same hue
        // stack into a solid block and hide the gap between the lines.
        areaStyle: dashed?.(name)
          ? undefined
          : { opacity: isMuted ? 0.04 : 0.12, color: colorOf(name) },
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
        // Hung off the first series only, for the same reason markArea is:
        // one rule, however many models are plotted. silent so it never takes
        // the tooltip, and drawn in grid ink rather than a series hue — it is
        // the axis speaking, not a measurement.
        // Never on a log axis, which has no zero to rule — and cannot have one,
        // since the axis only exists when every value is above it.
        markLine:
          i === 0 && zeroLine && !log
            ? {
                silent: true,
                symbol: "none",
                label: { show: false },
                lineStyle: { color: AXIS, width: 1, type: "solid" },
                data: [{ yAxis: 0 }],
              }
            : undefined,
      };
    }),
    logScale: log,
    // Surfaced so the panel can announce the bands in words. A colour-only
    // signal is not a signal here: the whole point is a reader who would
    // otherwise take the plot at face value.
    censoredBands: bands.length,
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

// SPAN_HHMM_MS is where a bare HH:mm stops being unambiguous — past two days it
// repeats across the plot.
const COST_SPAN_HHMM_MS = 48 * 3_600_000;

// buildCostOption draws what each bucket of the window cost, with the
// reduced-rate spans shaded behind it.
//
// One line, not one per model or per probe. The panel answers "what did this
// cost", and a run's cadence is not a fact about its bill: the 5-minute short
// runs and the hourly wide ones land in whichever bucket they happened in and
// are summed there.
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
                    itemStyle: { color: OFFPEAK, opacity: 0.13 },
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
