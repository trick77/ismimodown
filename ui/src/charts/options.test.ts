import { describe, expect, it } from "vitest";
import type { Point } from "../api/types";
import { shouldUseLogScale } from "../format";
import {
  buildCostOption,
  buildDecompositionOption,
  buildLineOption,
  colorForModel,
  logAxis,
  rollingMedian,
  SERIES_COLORS,
  smoothSpanMs,
  smoothWindow,
  SMOOTHED_SUFFIX,
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
    // The same hue in both orderings — this is what lets DefaultModels be
    // reordered without the two models trading colours.
    expect(colorForModel("mimo-v2.5", a)).toBe(SERIES_COLORS[0]);
    expect(colorForModel("mimo-v2.5", b)).toBe(SERIES_COLORS[0]);
    expect(colorForModel("mimo-v2.5-pro", a)).toBe(SERIES_COLORS[1]);
    expect(colorForModel("mimo-v2.5-pro", b)).toBe(SERIES_COLORS[1]);
    // The two series must never share a colour.
    expect(colorForModel("mimo-v2.5", a)).not.toBe(
      colorForModel("mimo-v2.5-pro", a),
    );
  });

  it("falls back for an unknown model rather than returning undefined", () => {
    expect(colorForModel("nope", ["nope"])).toBe(SERIES_COLORS[0]);
  });

  // A model RENAMED in DefaultModels without an entry here must not land on the
  // hue the model beside it is already drawing with. By position it would:
  // "mimo-v3" first would take SERIES_COLORS[0], which mimo-v2.5 holds.
  it("never hands an unknown model a hue a known one holds", () => {
    const models = ["mimo-v3", "mimo-v2.5"];
    expect(colorForModel("mimo-v3", models)).not.toBe(
      colorForModel("mimo-v2.5", models),
    );
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

  // A category axis counts up from the bottom, so the first model would draw as
  // the bottom bar and read as last against the cards and legends above it.
  it("draws the first model as the top bar", () => {
    const opt = buildDecompositionOption([
      { id: "mimo-v2.5-pro", ttft: 916, edge: 180 },
      { id: "mimo-v2.5", ttft: 700, edge: 180 },
    ]);
    expect(opt.yAxis.data).toEqual(["mimo-v2.5-pro", "mimo-v2.5"]);
    expect(opt.yAxis.inverse).toBe(true);
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

  // The precision test is on the MAGNITUDE. Written against the raw value it
  // was true for every negative, so a negative kept a decimal the axis had
  // already dropped while the same magnitude positive rounded away.
  it("rounds a negative to the same precision as its positive twin", () => {
    const neg = fmt([{ value: [0, -1234.6], seriesName: "a" }]);
    const pos = fmt([{ value: [0, 1234.6], seriesName: "a" }]);
    expect(pos).toContain("1235 ms");
    expect(neg).toContain("−1235 ms");
  });

  it("keeps the decimal on a small negative, as it does on a small positive", () => {
    expect(fmt([{ value: [0, -12.5], seriesName: "a" }])).toContain("−12.5 ms");
  });

  // The same minus sign the axis prints. Two different minus signs on one card
  // is a tell that one of the numbers came from somewhere else.
  it("signs with U+2212, not a hyphen", () => {
    expect(fmt([{ value: [0, -900], seriesName: "a" }])).not.toContain("-900");
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
describe("a series that can reach zero", () => {
  // ECharts does not refuse a non-positive value on a log axis, it DROPS the
  // point — so a series touching zero would come back as a line with holes that
  // look exactly like buckets where nothing was measured.
  it("stays linear however wide the spread, when a value is not positive", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, -50), pt(2, 40_000)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.logScale).toBe(false);
  });

  // THIS is the case that makes the guard load-bearing, and the reason the two
  // above do not: dynamicRange FILTERS non-positives out and then measures what
  // is left, so it only returns 1 when fewer than two positives survive. Give it
  // two positives more than 20x apart and it reports a log-worthy spread while
  // the series still holds a negative — which is precisely the point ECharts
  // would drop. A reader checking whether this guard is dead code will look at
  // the tests above, conclude it is, and delete it. It is not.
  it("stays linear when the POSITIVE values alone would force a log axis", () => {
    const values = [pt(1, -400), pt(2, 900), pt(3, 40_000)];
    // The spread the axis would see if the negative were simply ignored.
    expect(shouldUseLogScale(values.map((p) => p.p50))).toBe(true);

    const opt = buildLineOption({
      series: { a: values },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.logScale).toBe(false);
    // And the point survives, which is the whole reason to stay linear.
    expect(opt.series[0]!.data).toContainEqual([1000, -400]);
  });

  it("stays linear on an exact zero too", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 0), pt(2, 40_000)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.logScale).toBe(false);
  });

  // The guard must not cost the log axis to the charts that have always had
  // one: a null is a gap, not a value, and says nothing about the range.
  it("still goes log when the only non-values are nulls", () => {
    const opt = buildLineOption({
      series: { a: [pt(1, 900), pt(2, null), pt(3, 40_000)] },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.logScale).toBe(true);
  });
});

// A window longer than 48h is what turns the smoothing on, so every test here
// builds its series out of hourly buckets and says how many hours it spans.
const hourly = (values: (number | null)[], startH = 0) =>
  values.map((v, i) => pt((startH + i) * 3_600, v));

describe("smoothWindow", () => {
  it("is an eighth of the points", () => {
    expect(smoothWindow(80)).toBe(11);
    expect(smoothWindow(360)).toBe(45);
  });

  // Even windows sit half a bucket to one side, and the smoothed line would lag
  // the data it is drawn over by a constant amount.
  it("is always odd, so the window is centred", () => {
    for (const n of [40, 48, 56, 64, 72, 96, 120]) {
      expect(smoothWindow(n) % 2).toBe(1);
    }
  });

  it("never drops below five buckets, however few points there are", () => {
    expect(smoothWindow(0)).toBe(5);
    expect(smoothWindow(8)).toBe(5);
  });
});

describe("smoothSpanMs", () => {
  const H = 3_600_000;
  const at = (hours: number[]): [number, number | null][] =>
    hours.map((h) => [h * H, 1]);

  it("is the wall clock a whole window reaches across", () => {
    // Eleven contiguous hourly buckets: the window at any interior point
    // reaches from five behind it to five ahead.
    const out = smoothSpanMs(at(Array.from({ length: 11 }, (_, i) => i)), 11);
    expect(out).toBe(10 * H);
  });

  // The API emits no row at all for a bucket where nothing ran, so the window
  // walks over indices and can straddle an outage. Multiplied out from the
  // bucket width, the note would understate the smoothing exactly where it
  // reaches furthest.
  it("counts the hours across a stretch the probe missed", () => {
    const hours = [0, 1, 2, 3, 4, 40, 41, 42, 43, 44, 45];
    expect(smoothSpanMs(at(hours), 11)).toBe(45 * H);
  });

  // One outage should not restate the whole line's smoothing as if every point
  // were averaged that far.
  it("takes the median reach rather than the widest", () => {
    const hours = [
      ...Array.from({ length: 20 }, (_, i) => i),
      100,
      ...Array.from({ length: 20 }, (_, i) => 101 + i),
    ];
    const span = smoothSpanMs(at(hours), 5);
    expect(span).toBe(4 * H);
  });

  it("falls back to the whole plot when it is shorter than one window", () => {
    expect(smoothSpanMs(at([0, 1, 2]), 11)).toBe(2 * H);
  });

  it("is zero with nothing to measure", () => {
    expect(smoothSpanMs([], 11)).toBe(0);
  });
});

describe("rollingMedian", () => {
  const pairs = (values: (number | null)[]): [number, number | null][] =>
    values.map((v, i) => [i * 1000, v]);

  it("returns a value per input bucket, on the same timestamps", () => {
    const out = rollingMedian(pairs([1, 2, 3, 4, 5]), 3);
    expect(out.map(([t]) => t)).toEqual([0, 1000, 2000, 3000, 4000]);
  });

  // A mean would carry this page's own spikes into the smoothed line: one
  // 56-second timeout drags the average above every reading in its window.
  it("steps over a spike rather than bending around it", () => {
    const out = rollingMedian(pairs([10, 10, 5_000, 10, 10]), 5);
    expect(out[2]![1]).toBe(10);
  });

  it("averages the middle pair when the window holds an even count", () => {
    // The ends run over a shrinking half-window, so index 1 sees four buckets
    // rather than five: [1, 2, 3, 4], with no single middle to take.
    const out = rollingMedian(pairs([1, 2, 3, 4, 5]), 5);
    expect(out[1]![1]).toBe(2.5);
  });

  // The right edge is the end a status page is read from, and a trend that
  // stops half a window before "now" is the part nobody can use.
  it("carries the line to both ends rather than trimming them", () => {
    const out = rollingMedian(pairs([1, 2, 3, 4, 5, 6, 7]), 5);
    expect(out[0]![1]).not.toBeNull();
    expect(out[out.length - 1]![1]).not.toBeNull();
  });

  // A median over a window that is mostly holes lands wherever the two
  // surviving buckets happened to be.
  it("takes a gap where the window is mostly empty", () => {
    const out = rollingMedian(pairs([1, null, null, null, null, null, 7]), 5);
    expect(out[3]![1]).toBeNull();
  });

  // Half the window is missing at the last point by construction, so the
  // coverage rule counts the buckets that point could HAVE. Measured against
  // the full window it would demand every one of them, and a single hole near
  // the right edge would end the trend before "now".
  it("survives a hole in the final half-window", () => {
    // A 13-bucket window, the size a week's worth of points asks for: the last
    // point sees seven buckets, and one of them is a hole.
    const values = Array.from({ length: 25 }, (_, i) => (i === 20 ? null : 1));
    const out = rollingMedian(pairs(values), 13);
    expect(out[out.length - 1]![1]).toBe(1);
  });

  it("still draws where the window is mostly measured", () => {
    // The hole is skipped rather than counted: the window at index 3 is
    // [2, 4, 5, 6], four measured buckets out of five.
    const out = rollingMedian(pairs([1, 2, null, 4, 5, 6, 7]), 5);
    expect(out[3]![1]).toBe(4.5);
  });
});

describe("smoothing", () => {
  const smoothed = (points: Point[], on = true) =>
    buildLineOption({
      series: { a: points },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      smoothed: on,
    });

  // 96 hourly buckets is four days — past the 48h gate.
  const long = hourly(Array.from({ length: 96 }, (_, i) => 1_000 + i));

  it("adds one smoothed line per model, after every raw series", () => {
    const opt = buildLineOption({
      series: { a: long, b: long },
      order: ["a", "b"],
      colorOf: () => "#fff",
      unit: "ms",
      smoothed: true,
    });
    expect(opt.series.map((s) => s.name)).toEqual([
      "a",
      "b",
      `a${SMOOTHED_SUFFIX}`,
      `b${SMOOTHED_SUFFIX}`,
    ]);
  });

  it("draws nothing extra unless asked, so the wire chart is untouched", () => {
    const opt = smoothed(long, false);
    expect(opt.series).toHaveLength(1);
    expect(opt.smoothed).toBe(false);
    expect(opt.smoothSpanMs).toBe(0);
    // And the measurement keeps its full weight and its fill.
    expect(opt.series[0]!.lineStyle.width).toBe(2);
    expect(opt.series[0]!.areaStyle).toBeDefined();
  });

  // Below 48h the reader is looking at what is happening now, not at where the
  // last week went — the same threshold the axis stamp switches on.
  it("stays off on a window of two days or less", () => {
    const opt = smoothed(hourly(Array.from({ length: 48 }, () => 1_000)));
    expect(opt.series).toHaveLength(1);
    expect(opt.smoothed).toBe(false);
  });

  it("comes on once the window is longer than that", () => {
    const opt = smoothed(long);
    expect(opt.smoothed).toBe(true);
    // 96 hourly buckets, smoothed over an eighth of them: a 13-bucket window
    // reaches twelve hours across.
    expect(opt.smoothSpanMs).toBe(12 * 3_600_000);
  });

  it("drops the measurement to a hairline with no fill under it", () => {
    const opt = smoothed(long);
    expect(opt.series[0]!.lineStyle.width).toBe(1);
    expect(opt.series[0]!.lineStyle.opacity).toBeLessThan(1);
    expect(opt.series[0]!.areaStyle).toBeUndefined();
    // The trend is a stroke, never a shape.
    expect(opt.series[1]!.lineStyle.width).toBe(2);
    expect(opt.series[1]!.areaStyle).toBeUndefined();
  });

  it("keeps the model's own hue on both of its lines", () => {
    const opt = buildLineOption({
      series: { a: long },
      order: ["a"],
      colorOf: () => "#abcdef",
      unit: "ms",
      smoothed: true,
    });
    expect(opt.series[0]!.lineStyle.color).toBe("#abcdef");
    expect(opt.series[1]!.lineStyle.color).toBe("#abcdef");
  });

  // The bands hang off series index 0, and a smoothed line landing there would
  // take the markArea onto a line drawn over the very stretches it is about.
  it("leaves the censoring bands on the measurement", () => {
    const censored = hourly(Array.from({ length: 96 }, () => 1_000)).map(
      (p, i) => (i === 10 ? pt(p.t, p.p50, 2) : p),
    );
    const opt = buildLineOption({
      series: { a: censored },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      bucketMs: 3_600_000,
      smoothed: true,
    });
    expect(opt.series[0]!.markArea).toBeDefined();
    expect(opt.series[1]!.markArea).toBeUndefined();
  });

  // A hover has to report what was measured, not what was drawn over it.
  it("keeps the smoothed line out of the tooltip", () => {
    const opt = smoothed(long);
    const html = opt.tooltip.formatter([
      { marker: "", seriesName: "a", value: [3_600_000, 1_000] },
      {
        marker: "",
        seriesName: `a${SMOOTHED_SUFFIX}`,
        value: [3_600_000, 1_234],
      },
    ]);
    expect(html).toContain("1000 ms");
    expect(html).not.toContain("1234");
    expect(html).not.toContain(SMOOTHED_SUFFIX.trim());
  });

  it("never takes a hover from the measurement", () => {
    const opt = smoothed(long);
    expect(opt.series[0]!.silent).toBe(false);
    expect(opt.series[1]!.silent).toBe(true);
  });

  // The axis is fitted to the readings; a smoothed line pulled in from them
  // cannot be allowed to move the bounds it is drawn inside.
  it("does not move the fitted log axis", () => {
    const spiky = hourly(
      Array.from({ length: 96 }, (_, i) => (i === 50 ? 60_000 : 1_000)),
    );
    const plain = buildLineOption({
      series: { a: spiky },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
    });
    const withTrend = buildLineOption({
      series: { a: spiky },
      order: ["a"],
      colorOf: () => "#fff",
      unit: "ms",
      smoothed: true,
    });
    expect(plain.logScale).toBe(true);
    expect(withTrend.logScale).toBe(true);
    expect(withTrend.yAxis.min).toBe(plain.yAxis.min);
    expect(withTrend.yAxis.max).toBe(plain.yAxis.max);
  });
});

describe("the wire chart, which shares this builder", () => {
  // Four hosts over four days — long enough that it would be smoothed here too
  // if the flag were not opt-in.
  const long = hourly(Array.from({ length: 96 }, () => 40));
  const targets = ["mimo-sgp", "ref-sgp", "mimo-ams", "ref-ams"];

  it("stays at one line per host", () => {
    const opt = buildLineOption({
      series: Object.fromEntries(targets.map((t) => [t, long])),
      order: targets,
      colorOf: () => "#fff",
      unit: "ms",
    });
    expect(opt.series.map((s) => s.name)).toEqual(targets);
    expect(opt.smoothed).toBe(false);
  });
});
