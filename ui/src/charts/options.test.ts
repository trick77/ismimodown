import { describe, expect, it } from "vitest";
import type { Point } from "../api/types";
import {
  buildDecompositionOption,
  buildLineOption,
  colorForModel,
  SERIES_COLORS,
} from "./options";

const pt = (t: number, p50: number | null): Point => ({
  t,
  n: 1,
  p50,
  p95: p50,
});

describe("colorForModel", () => {
  // Colour follows the MODEL, never its rank, so a model keeps its hue when the
  // ordering changes.
  it("is stable per model regardless of position", () => {
    const a = ["mimo-v2.5", "mimo-v2.5-pro"];
    const b = ["mimo-v2.5-pro", "mimo-v2.5"];
    expect(colorForModel("mimo-v2.5", a)).toBe(SERIES_COLORS[0]);
    expect(colorForModel("mimo-v2.5", b)).toBe(SERIES_COLORS[1]);
    // The two series must never share a colour.
    expect(colorForModel("mimo-v2.5", a)).not.toBe(
      colorForModel("mimo-v2.5-pro", a),
    );
  });

  it("falls back for an unknown model rather than returning undefined", () => {
    expect(colorForModel("nope", ["mimo-v2.5"])).toBe(SERIES_COLORS[0]);
  });
});

describe("buildLineOption", () => {
  const series = { "mimo-v2.5": [pt(1000, 900), pt(2000, 950)] };

  it("emits one series per model in the given order", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)], b: [pt(1, 2)] },
      order: ["a", "b"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.series.map((s) => s.name)).toEqual(["a", "b"]);
  });

  it("skips models with no data rather than emitting an empty series", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)] },
      order: ["a", "missing"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.series).toHaveLength(1);
  });

  // A null bucket is a GAP. Dropping the point would join the line across the
  // hole and invent continuity that was never measured; a zero would draw a
  // floor.
  it("keeps null buckets as nulls and never connects across them", () => {
    const opt = buildLineOption({
      series: { a: [pt(1000, 900), pt(2000, null), pt(3000, 910)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.series[0]!.data).toEqual([
      [1000000, 900],
      [2000000, null],
      [3000000, 910],
    ]);
    expect(opt.series[0]!.connectNulls).toBe(false);
  });

  it("uses a linear axis within the threshold", () => {
    const opt = buildLineOption({
      series,
      order: ["mimo-v2.5"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.yAxis.type).toBe("value");
    expect(opt.logScale).toBe(false);
  });

  // Latency spans decades here; a linear axis collapses one of the two real
  // cases. The flag is what the panel uses to stamp LOG SCALE on the plot.
  it("switches to a log axis past a 20x range and flags it", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 100), pt(2, 90000)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.yAxis.type).toBe("log");
    expect(opt.logScale).toBe(true);
  });

  // Percentages and token counts are not latency, and a log axis on them is
  // nonsense.
  it("honours forceLinear even past the threshold", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1), pt(2, 9000)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "tok/s",
      forceLinear: true,
    });
    expect(opt.yAxis.type).toBe("value");
    expect(opt.logScale).toBe(false);
  });
});

describe("buildDecompositionOption", () => {
  // The whole claim is that the two halves sum to the observed TTFT, which is
  // why they are stacked.
  it("splits TTFT into the edge and the residual", () => {
    const opt = buildDecompositionOption([
      { id: "mimo-v2.5", ttft: 916, edge: 180 },
    ]);
    expect(opt.series[0]!.name).toBe("to the edge");
    expect(opt.series[0]!.data).toEqual([180]);
    expect(opt.series[1]!.name).toBe("server-side");
    expect(opt.series[1]!.data).toEqual([736]);
    expect(opt.series[0]!.stack).toBe(opt.series[1]!.stack);
  });

  // Never "model time": the handshake terminates at the TLS edge, and any
  // edge-to-compute backhaul sits inside the residual.
  it("labels the residual as server-side, never model time", () => {
    const opt = buildDecompositionOption([{ id: "m", ttft: 900, edge: 100 }]);
    const names = opt.series.map((s) => s.name).join(" ");
    expect(names).toContain("server-side");
    expect(names).not.toMatch(/model/i);
  });

  // Clock skew or a slow handshake can make the edge exceed the measured TTFT.
  // A negative bar would render below the axis and read as nonsense.
  it("never produces a negative residual", () => {
    const opt = buildDecompositionOption([{ id: "m", ttft: 100, edge: 180 }]);
    expect(opt.series[1]!.data).toEqual([0]);
  });

  it("emits nulls rather than zeros when a half is missing", () => {
    const opt = buildDecompositionOption([{ id: "m", ttft: null, edge: 180 }]);
    expect(opt.series[1]!.data).toEqual([null]);
  });
});
