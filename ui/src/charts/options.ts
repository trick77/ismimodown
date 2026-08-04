// Pure ECharts option builders.
//
// Separated from the render wrapper on purpose: these are the part with real
// logic — axis type, gap handling, series colour — and they are testable as
// plain functions, where the canvas-backed renderer is not reachable from jsdom.
import type { Point } from "../api/types";
import {
  formatDate,
  formatDateTime,
  formatTime,
  shouldUseLogScale,
} from "../format";
import { offPeakBands } from "../offpeak";

// Series colour follows the MODEL, never its rank, so a model keeps its hue
// when the ordering changes. Validated against the #1f1f1e surface: CVD
// separation ΔE 26.8, normal-vision ΔE 31.8.
export const SERIES_COLORS = ["#3987e5", "#d95926"] as const;
// The network is drawn in neutral ink because it is not a model.
export const WIRE_COLOR = "#9c9a92";
// The Singapore reference host, one step darker than the neutral above. It is
// the control, not the measurement — the line the reader checks against rather
// than reads — so it recedes behind MiMo's own edge. Its own constant rather
// than a darker WIRE_COLOR: that value is also the decomposition's "to the
// edge" segment, where it is paired with SERVER_COLOR and its contrast is
// already spent. Against #1f1f1e this sits near 3.6:1, above the 3:1 a chart
// line needs to stay a line.
export const REFERENCE_COLOR = "#6f6d67";

// The server-side remainder in the decomposition. Deliberately NOT
// SERIES_COLORS[0]: that hue is mimo-v2.5's identity, and the decomposition
// paints this segment on every model's row — including mimo-v2.5-pro's — so
// borrowing it made one colour mean two different things on the same page.
// This is the page accent, which encodes emphasis rather than identity.
// Against #1f1f1e, paired with WIRE_COLOR: CVD ΔE 10.8, normal-vision ΔE 15.3.
export const SERVER_COLOR = "#c6613f";

export const FAULT_COLORS: Record<string, string> = {
  ok: "#5aa06a",
  edge: "#c98500",
  route: "#9085e9",
  uplink: "#c14638",
};

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

// The censoring band colour. The fault amber, not a series hue: a stretch where
// measurements were cut off is not a measurement.
const CENSORED = "#c98500";

// The off-peak band colour. Green, because cheap reads as green before it reads
// as anything else — chosen deliberately over the warm accent and over neutral
// ink, both of which were mocked against it.
//
// The same hex as the online green, and that overlap is the known cost: green
// means "up" elsewhere on this page, and on the pulse strip the healthy bars are
// this exact colour, so the band there sits behind marks of its own hue. What
// keeps the two apart is weight, not colour — the band is a low-alpha wash on
// the surface and the bars are near-solid strokes on top of it — plus the note
// under every chart that says in words what the shading is. Colour was never
// the only signal here; it is carrying less of the load than it looks.
//
// If this ever does get misread as "these hours were healthy", the fix is the
// neutral: same structure, swap this constant and the four Tailwind classes
// that shadow it.
const OFFPEAK = "#5aa06a";

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
  // offPeak draws MiMo's reduced-rate billing hours behind the series. Off by
  // default and passed only by the panel that is about token cost: the rate does
  // not apply to the network panel, which measures the wire.
  offPeak?: boolean;
};

// timeExtent is the first and last timestamp, in ms, across every series.
//
// Used for two things that both need the real data range rather than whatever
// ECharts settles on: how far the off-peak bands have to be generated, and
// whether the axis can label ticks without a date.
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
  dashed,
  muted,
  bucketMs,
  offPeak = false,
}: LineOpts) {
  // The y-axis switches to log automatically when the window's dynamic range
  // exceeds 20x, because a linear axis collapses either the normal reading or
  // the spike. The caller stamps "LOG SCALE" on the plot when this is true — a
  // log axis read as linear is worse than no chart.
  const log = !forceLinear && shouldUseLogScale(allValues(series));

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

  // Past 48 hours the bands become one thin stripe per day — seven of them on
  // the 7d window, ninety on 3mo — which reads as a hatch pattern rather than as
  // a nightly window, and buries the data under it.
  const offPeakSpans =
    offPeak && extent !== null && !spansDays
      ? offPeakBands(extent[0], extent[1])
      : [];

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
      axisLine: { lineStyle: { color: GRID } },
      axisLabel: {
        color: AXIS,
        fontSize: 10,
        // Every tick is stamped in Europe/Zurich, the zone the probe host runs
        // in and the one the samples table already uses. ECharts has no
        // per-axis timezone — only a global useUTC — so the label is formatted
        // here instead. Without this the axis silently followed the viewer's
        // machine, which put the off-peak band under a tick reading noon.
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
      axisLine: { show: false },
      axisLabel: { color: AXIS, fontSize: 10 },
      splitLine: { lineStyle: { color: GRID, type: "dashed" } },
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
        // Both band kinds share this ONE markArea because ECharts allows only
        // one per series, so they carry their styling per item instead. The
        // off-peak spans go first and the censoring bands after, so that where
        // the two overlap the censoring amber is the one on top: a stretch with
        // cut-off measurements has to stay legible as such no matter what it
        // was billed at.
        markArea:
          i === 0 && (bands.length > 0 || offPeakSpans.length > 0)
            ? {
                silent: true,
                data: [
                  ...offPeakSpans.map(([from, to]) => [
                    {
                      xAxis: from,
                      // Kept well under the censoring stripe's 0.3. That one is
                      // a caution about the data itself; this is standing
                      // context about the clock, and it shares a hue with the
                      // healthy pulse bar — the further it stays from that
                      // colour's normal weight, the less it can be mistaken for
                      // a reading rather than a backdrop.
                      itemStyle: { color: OFFPEAK, opacity: 0.13 },
                    },
                    { xAxis: to },
                  ]),
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
    }),
    logScale: log,
    // Surfaced so the panel can announce the bands in words. A colour-only
    // signal is not a signal here: the whole point is a reader who would
    // otherwise take the plot at face value.
    //
    // Counts the CENSORING bands only, never the off-peak spans sharing the
    // markArea with them: CensoredNote renders off this, and it must not start
    // claiming measurements were cut off merely because it got dark in Beijing.
    censoredBands: bands.length,
    // The off-peak spans as drawn, so the panel can name them in words and
    // quote the local hours off the same edges the band was painted from —
    // which is what makes the DST case come out right without a second rule.
    offPeakSpans,
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

function round(v: number): string {
  return Number(v.toFixed(v < 100 ? 1 : 0)).toString();
}
