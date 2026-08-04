import { describe, expect, it } from "vitest";
import type { Point } from "../api/types";
import {
  buildDecompositionOption,
  buildLineOption,
  colorForModel,
  SERIES_COLORS,
} from "./options";

const pt = (t: number, p50: number | null, censored = 0): Point => ({
  t,
  n: p50 === null ? 0 : 1,
  censored,
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

describe("muted series", () => {
  // The prefill panel replots the short probe's TTFT — the same data as the
  // chart above it — because the gap to the wide probe is the measurement. At
  // equal weight the panel reads as that chart repeated, which is how it was
  // actually read. The baseline has to recede for the gap to be the figure.
  it("thins and fades the marked series, leaving the subject alone", () => {
    const opt = buildLineOption({
      series: { "m · 34 tok": [pt(1, 1)], "m · 3800 tok": [pt(1, 2)] },
      order: ["m · 34 tok", "m · 3800 tok"],
      colorOf: () => "#3987e5",
      unit: "ms",
      dashed: (name) => name.includes("3800"),
      muted: (name) => !name.includes("3800"),
    });
    const baseline = opt.series[0]!;
    const subject = opt.series[1]!;

    expect(baseline.lineStyle.width).toBeLessThan(subject.lineStyle.width);
    expect(baseline.lineStyle.opacity).toBeLessThan(1);
    expect(subject.lineStyle.opacity).toBe(1);
  });

  // The fill is what carries the eye at 0.12; thinning the stroke while leaving
  // it would mute the wrong half of the series.
  it("pulls the area fill back on a muted series", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)], b: [pt(1, 2)] },
      order: ["a", "b"],
      colorOf: () => "#3987e5",
      unit: "ms",
      muted: (name) => name === "a",
    });
    expect(opt.series[0]!.areaStyle!.opacity).toBeLessThan(
      opt.series[1]!.areaStyle!.opacity,
    );
  });

  it("leaves every series at full weight when no predicate is given", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.series[0]!.lineStyle.width).toBe(2);
    expect(opt.series[0]!.lineStyle.opacity).toBe(1);
  });
});

describe("dashed series", () => {
  // The prefill panel plots two probes PER MODEL and colour follows the model,
  // so both lines share a hue. Without a second visual channel the gap between
  // them — the entire point of that panel — cannot be read.
  it("gives a dashed line style to the marked series only", () => {
    const opt = buildLineOption({
      series: { "m · 34 tok": [pt(1, 1)], "m · 3800 tok": [pt(1, 2)] },
      order: ["m · 34 tok", "m · 3800 tok"],
      colorOf: () => "#3987e5",
      unit: "ms",
      dashed: (name) => name.includes("3800"),
    });
    expect(opt.series[0]!.lineStyle.type).toBe("solid");
    expect(opt.series[1]!.lineStyle.type).toBe("dashed");
    // Same hue is correct — colour follows the model.
    expect(opt.series[0]!.lineStyle.color).toBe(opt.series[1]!.lineStyle.color);
  });

  // Two filled areas in the same hue stack into a solid block and hide the very
  // gap the panel exists to show.
  it("drops the area fill under a dashed series", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)], b: [pt(1, 2)] },
      order: ["a", "b"],
      colorOf: () => "#3987e5",
      unit: "ms",
      dashed: (name) => name === "b",
    });
    expect(opt.series[0]!.areaStyle).toBeDefined();
    expect(opt.series[1]!.areaStyle).toBeUndefined();
  });

  it("defaults every series to solid when no predicate is given", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.series[0]!.lineStyle.type).toBe("solid");
  });
});

// The latency charts are computed over runs that FINISHED. Where the timeout
// ladder cut runs off, the line is drawn from the survivors — so it looks its
// best exactly where it is least complete, and where everything was cut off it
// is not drawn at all, which is indistinguishable from the probe not running.
describe("censoring bands", () => {
  const BUCKET = 900_000; // 15 minutes

  it("draws no band when nothing was censored", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, 900), pt(900, 950)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: BUCKET,
    });
    expect(opt.censoredBands).toBe(0);
    expect(opt.series[0]!.markArea).toBeUndefined();
  });

  it("bands a bucket that has a value but lost its slow runs", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, 900), pt(900, 950, 2)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: BUCKET,
    });
    expect(opt.censoredBands).toBe(1);
    expect(opt.series[0]!.markArea!.data).toEqual([
      [{ xAxis: 900_000 }, { xAxis: 1_800_000 }],
    ]);
  });

  // The case the band exists for: no line at all, because every run was cut
  // off. Without the band this is a plain gap.
  it("bands a bucket that produced no value at all", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, null, 3)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: BUCKET,
    });
    expect(opt.censoredBands).toBe(1);
  });

  // An incident is a stretch, not a row of stripes. Unmerged, the seams between
  // adjacent buckets read as recoveries that never happened.
  it("merges adjacent censored buckets into one span", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, 900, 1), pt(900, 900, 1), pt(1800, 900, 1)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: BUCKET,
    });
    expect(opt.censoredBands).toBe(1);
    expect(opt.series[0]!.markArea!.data).toEqual([
      [{ xAxis: 0 }, { xAxis: 2_700_000 }],
    ]);
  });

  it("keeps separate incidents separate", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, 900, 1), pt(900, 900), pt(1800, 900, 1)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: BUCKET,
    });
    expect(opt.censoredBands).toBe(2);
  });

  // markArea is per series. Painted on every one, the same rectangles stack
  // into a darker band that reads as a severity it does not carry.
  it("hangs the band off one series only", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, 900, 1)], b: [pt(0, 900, 1)] },
      order: ["a", "b"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: BUCKET,
    });
    expect(opt.series[0]!.markArea).toBeDefined();
    expect(opt.series[1]!.markArea).toBeUndefined();
  });

  // A band of unknown width would misstate how much of the window was affected.
  it("draws nothing without a bucket width", () => {
    const opt = buildLineOption({
      series: { a: [pt(0, 900, 5)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.censoredBands).toBe(0);
  });
});
