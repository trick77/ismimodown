// Pure ECharts option builders.
//
// Separated from the render wrapper on purpose: these are the part with real
// logic — axis type, gap handling, series colour — and they are testable as
// plain functions, where the canvas-backed renderer is not reachable from jsdom.
import type { Point } from "../api/types";
import { shouldUseLogScale } from "../format";

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

  return {
    animation: false,
    grid: { left: 52, right: 16, top: 16, bottom: 28 },
    tooltip: {
      trigger: "axis",
      backgroundColor: "#242422",
      borderColor: GRID,
      textStyle: { color: INK, fontSize: 12 },
      valueFormatter: (v: number | null) =>
        v === null || v === undefined ? "no data" : `${round(v)} ${unit}`,
    },
    xAxis: {
      type: "time",
      axisLine: { lineStyle: { color: GRID } },
      axisLabel: { color: AXIS, fontSize: 10 },
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
        markArea:
          i === 0 && bands.length > 0
            ? {
                silent: true,
                // ECharts paints markArea BENEATH the series, so this fill is
                // read through the line's own area fill and loses much of its
                // chroma on the way. Checked on the rendered plot: below ~0.3 it
                // arrives as a grey shadow, which reads as a rendering artefact
                // rather than as the caution the legend swatch promises.
                itemStyle: { color: CENSORED, opacity: 0.3 },
                data: bands.map(([from, to]) => [
                  { xAxis: from },
                  { xAxis: to },
                ]),
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

function round(v: number): string {
  return Number(v.toFixed(v < 100 ? 1 : 0)).toString();
}
