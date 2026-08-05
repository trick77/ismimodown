import { describe, expect, it } from "vitest";
import type { Point } from "../api/types";
import {
  buildCostOption,
  buildDecompositionOption,
  buildLineOption,
  colorForModel,
  logAxis,
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

  // Left to itself ECharts rounds a log axis out to whole decades, and the
  // real 24h chart — 830 ms to 56 s — was drawn from 100 ms to 100 s with two
  // of its four gridlines standing over nothing.
  it("fits the log axis to the data instead of rounding out to decades", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 830), pt(2, 56365)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.yAxis.min).toBe(700);
    expect(opt.yAxis.max).toBe(70000);
    expect(opt.yAxis.splitLine.customValues).toEqual([
      1000, 3000, 10000, 30000,
    ]);
  });

  // The ticks and the labels have to be the same set, or a gridline is drawn
  // without a number against it.
  it("labels the fitted ticks in ms and s, never in minutes", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 830), pt(2, 56365)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.yAxis.axisTick.customValues).toEqual(
      opt.yAxis.axisLabel.customValues,
    );
    const label = opt.yAxis.axisLabel.formatter!;
    expect(opt.yAxis.axisLabel.customValues!.map(label)).toEqual([
      "1 s",
      "3 s",
      "10 s",
      "30 s",
    ]);
  });

  // A linear axis is ECharts' own nicing from zero, and handing it a fitted
  // min would cut the baseline off.
  it("leaves a linear axis unbounded and unticked", () => {
    const opt = buildLineOption({
      series,
      order: ["mimo-v2.5"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.yAxis.min).toBeUndefined();
    expect(opt.yAxis.max).toBeUndefined();
    expect(opt.yAxis.splitLine.customValues).toBeUndefined();
    // It still gets the unit, though: "1,000" was no more a latency on a
    // linear axis than it was on a log one.
    expect(opt.yAxis.axisLabel.formatter!(1000)).toBe("1 s");
  });

  // The unit is the caller's; a token count formatted as a duration is a lie.
  it("does not format the axis as a duration for a non-ms unit", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1), pt(2, 9000)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "tok/s",
      forceLinear: true,
    });
    expect(opt.yAxis.axisLabel.formatter).toBeUndefined();
  });
});

describe("logAxis", () => {
  it("snaps the ends past a half decade rather than taking the whole one", () => {
    // 57275 sits between 50000 and 100000; a 1-2-5 ladder would have to take
    // the decade, which is the bug this ladder exists to avoid.
    expect(logAxis([1601, 57275])).toMatchObject({ min: 1500, max: 70000 });
  });

  it("keeps every gridline strictly inside the bounds", () => {
    const axis = logAxis([1601, 57275])!;
    for (const tick of axis.ticks) {
      expect(tick).toBeGreaterThan(axis.min);
      expect(tick).toBeLessThan(axis.max);
    }
  });

  // Fewer than three reads as a single annotated height rather than a scale;
  // more than a handful crowds a 240px plot.
  it("thins the ladder to a readable number of gridlines", () => {
    for (const values of [
      [830, 56365],
      [1601, 57275],
      [100, 90000],
      [3.7, 900],
      [1, 1_000_000],
    ]) {
      const axis = logAxis(values)!;
      expect(axis.ticks.length).toBeGreaterThanOrEqual(3);
      expect(axis.ticks.length).toBeLessThanOrEqual(7);
    }
  });

  // A log axis cannot render a zero or a negative, and a range of zero width
  // would pin the plot to a single row.
  it("returns null when the values cannot support an axis", () => {
    expect(logAxis([])).toBeNull();
    expect(logAxis([0, -5, null])).toBeNull();
  });

  // customValues: [] would leave the axis with no gridlines and no labels at
  // all, which is worse than the nicing it was meant to replace.
  it("returns null when no round number falls inside the bounds", () => {
    expect(logAxis([900, 900])).toBeNull();
  });

  it("still fits an axis when every value is identical", () => {
    const axis = logAxis([1000, 1000])!;
    expect(axis.min).toBeLessThan(1000);
    expect(axis.max).toBeGreaterThan(1000);
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

  // Both segments are drawn on EVERY model's row, so neither may wear a model
  // hue: doing so made one colour mean "mimo-v2.5" in the cards and
  // "server-side" here, on the same screen, including on the pro row.
  it("paints neither segment in a model colour", () => {
    const opt = buildDecompositionOption([
      { id: "mimo-v2.5", ttft: 916, edge: 180 },
      { id: "mimo-v2.5-pro", ttft: 1400, edge: 180 },
    ]);
    for (const s of opt.series) {
      expect(SERIES_COLORS as readonly string[]).not.toContain(
        s.itemStyle.color,
      );
    }
  });

  // A bar sized purely by the category band read as a status meter rather than
  // a measurement once the plot got short.
  it("caps bar thickness on both stacked segments", () => {
    const opt = buildDecompositionOption([{ id: "m", ttft: 900, edge: 100 }]);
    expect(opt.series[0]!.barMaxWidth).toBe(opt.series[1]!.barMaxWidth);
    expect(opt.series[0]!.barMaxWidth).toBeLessThanOrEqual(20);
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

describe("the time axis", () => {
  // The axis followed the VIEWER's machine, while the samples table below it
  // rendered Europe/Zurich — two clocks on one page, so a spike a reader
  // located on the plot was an hour or nine off the row describing it.
  it("stamps ticks in Europe/Zurich rather than the viewer's zone", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 1)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    // 16:00 UTC is 18:00 in Zurich in August.
    expect(opt.xAxis.axisLabel.formatter(Date.UTC(2026, 7, 4, 16))).toBe(
      "18:00",
    );
  });

  // Date OR time, never both: on 3mo the full "04 Aug, 07:00" stamp overlapped
  // its neighbours into a smear. Above the threshold the ticks are days apart,
  // so the date alone identifies them and the tooltip carries the time.
  it("switches to a bare date once a bare time would repeat across the plot", () => {
    const week = Array.from({ length: 8 }, (_, i) =>
      pt(Date.UTC(2026, 7, 4) / 1000 + i * 86_400, 900),
    );
    const opt = buildLineOption({
      series: { a: week },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.xAxis.axisLabel.formatter(Date.UTC(2026, 7, 4, 16))).toBe(
      "04 Aug",
    );
  });
});

describe("the tooltip", () => {
  const opt = buildLineOption({
    series: { a: [pt(1, 1)] },
    order: ["a"],
    colorOf: () => "#fff",
    unit: "ms",
  });
  const fmt = opt.tooltip.formatter;

  it("heads the tooltip with the Zurich time, not the browser's", () => {
    const out = fmt([
      { value: [Date.UTC(2026, 7, 4, 16), 900], seriesName: "a" },
    ]);
    expect(out).toContain("18:00");
  });

  // Carried over from the valueFormatter this replaced. A gap is a gap: a null
  // bucket must never render as a number.
  it("still says no data rather than drawing a zero", () => {
    const out = fmt([{ value: [0, null], seriesName: "a" }]);
    expect(out).toContain("no data");
    expect(out).not.toContain("0 ms");
  });

  // Series names arrive from the API. Interpolating a server-supplied string
  // into markup is a hole whether or not anything currently fits through it.
  it("escapes the series name", () => {
    const out = fmt([{ value: [0, 900], seriesName: "<img src=x onerror=1>" }]);
    expect(out).not.toContain("<img");
    expect(out).toContain("&lt;img");
  });
});

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
    // The band carries its colour PER ITEM: ECharts allows only one markArea
    // per series, so a second kind of band would have to style itself there.
    expect(opt.series[0]!.markArea!.data).toEqual([
      [
        { xAxis: 900_000, itemStyle: { color: "#c98500", opacity: 0.3 } },
        { xAxis: 1_800_000 },
      ],
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
      [
        { xAxis: 0, itemStyle: { color: "#c98500", opacity: 0.3 } },
        { xAxis: 2_700_000 },
      ],
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

describe("buildCostOption", () => {
  const HOUR = 3600;
  const T0 = Date.parse("2026-08-04T10:00:00Z") / 1000;
  const points = [
    { t: T0, usd: 0.008 },
    { t: T0 + HOUR, usd: 0.008 },
    { t: T0 + 2 * HOUR, usd: 0.0064 },
  ];

  it("plots one line in seconds-to-milliseconds", () => {
    const o = buildCostOption(points, []);

    expect(o.series).toHaveLength(1);
    expect(o.series[0]!.data[0]).toEqual([T0 * 1000, 0.008]);
  });

  // A bucket with no runs is a gap. Joining across it would draw a cost that was
  // never billed, and a zero would draw a floor.
  it("leaves a bucket with no data as a hole", () => {
    const o = buildCostOption([...points, { t: T0 + 3 * HOUR, usd: null }], []);

    expect(o.series[0]!.connectNulls).toBe(false);
    expect(o.series[0]!.data[3]).toEqual([(T0 + 3 * HOUR) * 1000, null]);
  });

  // A bucket's cost belongs to the whole bucket, not to a reading at its left
  // edge; sloping between them would draw a gradual change the billing has not.
  it("steps rather than slopes", () => {
    expect(buildCostOption(points, []).series[0]!.step).toBe("end");
  });

  // Money over a fixed workload. A floating baseline turns a 20% rebate into a
  // cliff; a log axis turns it into a shrug.
  it("anchors the axis at zero and never goes logarithmic", () => {
    const o = buildCostOption(
      [
        { t: T0, usd: 0.0001 },
        { t: T0 + HOUR, usd: 5 },
      ],
      [],
    );

    expect(o.yAxis.min).toBe(0);
    expect(o.yAxis.type).toBe("value");
  });

  it("shades the reduced-rate spans behind the line", () => {
    const spans: [number, number][] = [[T0 + HOUR, T0 + 2 * HOUR]];
    const o = buildCostOption(points, spans);

    expect(o.banded).toBe(true);
    const area = o.series[0]!.markArea!.data[0]!;
    expect(area[0]!.xAxis).toBe((T0 + HOUR) * 1000);
    expect(area[1]!.xAxis).toBe((T0 + 2 * HOUR) * 1000);
    // Silent, so a backdrop never takes the tooltip from the data.
    expect(o.series[0]!.markArea!.silent).toBe(true);
  });

  // Ninety nightly stripes are a hatch pattern, which is what took the band off
  // the latency charts in the first place. The panel drops its caption with it.
  it("drops the band past two days", () => {
    const long = [
      { t: T0, usd: 0.18 },
      { t: T0 + 80 * 86400, usd: 0.18 },
    ];
    const o = buildCostOption(long, [[T0, T0 + HOUR]]);

    expect(o.banded).toBe(false);
    expect(o.series[0]!.markArea).toBeUndefined();
  });
});
