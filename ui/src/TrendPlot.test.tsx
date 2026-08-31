import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TrendPlot } from "./TrendPlot";
import type { ModelTrend, Point, Trend } from "./api/types";
import type { SpeedMove } from "./trend";

// The option is what this component produces; the canvas that renders it is not
// reachable from jsdom. So the wrapper is mocked into something that publishes
// the option it was handed, and the assertions read that.
const captured: { option: TrendOption | null } = { option: null };

type TrendOption = {
  yAxis: { type: string; min?: number; max?: number };
  series: {
    data: [number, number | null][];
    markLine?: { data: unknown[] };
    markArea?: { data: unknown[] };
  }[];
  logScale: boolean;
};

vi.mock("./charts/EChart", () => ({
  EChart: ({
    option,
    ariaLabel,
  }: {
    option: TrendOption;
    ariaLabel: string;
  }) => {
    captured.option = option;
    return <div data-testid="echart" aria-label={ariaLabel} />;
  },
}));

const GENERATED_AT = "2026-08-20T12:00:00Z";
const AT_S = Date.parse(GENERATED_AT) / 1000;
const RECENT_S = 3 * 3600;
const BUCKET_S = 900;

// 27 hours of quarter-hour buckets, oldest first — the payload the daemon
// serves, of which the plot draws the tail.
const points = (value: number, spike?: { agoS: number; value: number }) => {
  const out: Point[] = [];
  for (let ago = 27 * 3600; ago > 0; ago -= BUCKET_S) {
    const t = AT_S - ago;
    const isSpike =
      spike !== undefined && Math.abs(ago - spike.agoS) < BUCKET_S;
    out.push({
      t,
      n: 6,
      censored: 0,
      p50: isSpike ? spike.value : value,
      p95: value,
    });
  }
  return out;
};

const model = (
  id: string,
  recent: number,
  before: number,
  spike?: { agoS: number; value: number },
): ModelTrend => ({
  model_id: id,
  ttft: {
    recent: { n: 36, sufficient: true, p50_ms: recent, p95_ms: recent },
    before: { n: 288, sufficient: true, p50_ms: before, p95_ms: before },
    points: points(recent, spike),
  },
  tps: {
    recent: { n: 36, sufficient: true, p50_ms: 70, p95_ms: 70 },
    before: { n: 288, sufficient: true, p50_ms: 70, p95_ms: 70 },
    points: points(70),
  },
});

const trend = (models: ModelTrend[]): Trend => ({
  recent_s: RECENT_S,
  before_s: 24 * 3600,
  bucket_s: BUCKET_S,
  models,
  generated_at: GENERATED_AT,
});

const move = (modelID: string, spanS = RECENT_S): SpeedMove => ({
  modelID,
  metric: "ttft",
  recent: 1800,
  before: 900,
  spanS,
  worseBy: 1,
  secondsAdded: 0.9,
  censored: { recent: false, before: false },
});

describe("TrendPlot", () => {
  // The reference day is a LEVEL in this reading, not a shape. Drawing its
  // buckets put a whole night on the axis, and one cut-off bucket in it decided
  // the y-range for a sentence about the last three hours.
  it("draws the compared span and as much again, not the whole payload", () => {
    render(
      <TrendPlot
        trend={trend([model("mimo-v2.5", 1800, 900)])}
        metric="ttft"
        moves={[move("mimo-v2.5")]}
      />,
    );
    const data = captured.option!.series[0]!.data;
    const oldest = data[0]![0];
    expect(oldest).toBeGreaterThanOrEqual((AT_S - 2 * RECENT_S) * 1000);
    // Six hours of quarter-hour buckets, give or take the boundary one.
    expect(data.length).toBeLessThanOrEqual(25);
    expect(data.length).toBeGreaterThan(20);
  });

  // The failure this plot shipped with: one timeout bucket inside the drawn
  // hours flattens a doubled first token into a straight line on a linear axis.
  it("switches to a log axis when a spike would flatten the line", () => {
    render(
      <TrendPlot
        trend={trend([
          model("mimo-v2.5", 1800, 900, { agoS: 2 * 3600, value: 240_000 }),
        ])}
        metric="ttft"
        moves={[move("mimo-v2.5")]}
      />,
    );
    expect(captured.option!.yAxis.type).toBe("log");
    // A log axis read as a linear one is worse than no chart, so it is named.
    expect(screen.getByText("log scale")).toBeInTheDocument();
  });

  it("stays linear, and unlabelled, when the hours are ordinary", () => {
    render(
      <TrendPlot
        trend={trend([model("mimo-v2.5", 1800, 900)])}
        metric="ttft"
        moves={[move("mimo-v2.5")]}
      />,
    );
    expect(captured.option!.yAxis.type).toBe("value");
    expect(screen.queryByText("log scale")).not.toBeInTheDocument();
    // The dashed level is a markLine, and ECharts fits a linear axis to the
    // SERIES only: a reference below every drawn hour — the ordinary case, a
    // slowdown older than the six hours on the plot — would be positioned
    // outside the grid and never drawn.
    expect(captured.option!.yAxis.min).toBeLessThanOrEqual(900);
    expect(captured.option!.yAxis.max).toBeGreaterThanOrEqual(1800);
  });

  // One level line: the normal the sentence measured against. A second one for
  // the recent median read as another axis rule on a plot this short.
  it("marks the reference level once, and shades the compared span", () => {
    render(
      <TrendPlot
        trend={trend([model("mimo-v2.5", 1800, 900)])}
        metric="ttft"
        moves={[move("mimo-v2.5")]}
      />,
    );
    expect(captured.option!.series[0]!.markLine!.data).toHaveLength(1);
    expect(captured.option!.series[0]!.markArea!.data).toHaveLength(1);
    expect(screen.getByText(/dashed: the 24 hours before/)).toBeInTheDocument();
  });

  // The shading names WHICH hours the sentence is about, so it follows the
  // sentence: a reading the last hour raised on its own shades one hour, not
  // the three the median it never quoted was taken over.
  it("shades the span the sentence measured, not always the compared hours", () => {
    render(
      <TrendPlot
        trend={trend([model("mimo-v2.5", 1800, 900)])}
        metric="ttft"
        moves={[move("mimo-v2.5", 3600)]}
        spanS={3600}
      />,
    );
    const area = captured.option!.series[0]!.markArea!.data as [
      { xAxis: number },
      { xAxis: number },
    ][];
    expect(area[0]![0]!.xAxis).toBe((AT_S - 3600) * 1000);
    // ...while the lead-in still opens on the compared hours, or an hourly
    // reading would be drawn on two hours of context instead of six.
    const data = captured.option!.series[0]!.data;
    expect(data[0]![0]).toBeGreaterThanOrEqual((AT_S - 2 * RECENT_S) * 1000);
    expect(data.length).toBeGreaterThan(20);
  });

  // The legend prints the figure the sentence quotes. They are the same number
  // until the tail fires alone, and then the compared median labels a line the
  // sentence never mentioned.
  it("labels the line with the move's own figure", () => {
    render(
      <TrendPlot
        trend={trend([model("mimo-v2.5", 1100, 900)])}
        metric="ttft"
        moves={[move("mimo-v2.5", 3600)]}
        spanS={3600}
      />,
    );
    expect(screen.getByText("1.8 s")).toBeInTheDocument();
    expect(screen.queryByText("1.1 s")).not.toBeInTheDocument();
  });
});
