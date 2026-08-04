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

type LineOpts = {
  series: Record<string, Point[]>;
  order: string[];
  colorOf: (name: string) => string;
  unit: string;
  // forceLinear pins the axis for values that are not latency (percentages,
  // token counts), where a log axis would be nonsense.
  forceLinear?: boolean;
};

// buildLineOption is the shared shape for every time series on the page.
export function buildLineOption({
  series,
  order,
  colorOf,
  unit,
  forceLinear = false,
}: LineOpts) {
  // The y-axis switches to log automatically when the window's dynamic range
  // exceeds 20x, because a linear axis collapses either the normal reading or
  // the spike. The caller stamps "LOG SCALE" on the plot when this is true — a
  // log axis read as linear is worse than no chart.
  const log = !forceLinear && shouldUseLogScale(allValues(series));

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
    series: order
      .filter((name) => series[name] !== undefined)
      .map((name) => ({
        name,
        type: "line",
        showSymbol: false,
        // Gaps are gaps: never connect across a bucket with no data.
        connectNulls: false,
        lineStyle: { width: 2, color: colorOf(name) },
        itemStyle: { color: colorOf(name) },
        areaStyle: {
          opacity: 0.12,
          color: colorOf(name),
        },
        data: toPairs(series[name]!),
      })),
    logScale: log,
  };
}

// buildDecompositionOption is the headline chart: TTFT split into the measured
// edge RTT and the residual.
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
      {
        name: "to the edge",
        type: "bar",
        stack: "ttft",
        itemStyle: { color: WIRE_COLOR },
        data: edge,
      },
      {
        name: "server-side",
        type: "bar",
        stack: "ttft",
        itemStyle: { color: SERIES_COLORS[0] },
        data: residual,
      },
    ],
  };
}

function round(v: number): string {
  return Number(v.toFixed(v < 100 ? 1 : 0)).toString();
}
