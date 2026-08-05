import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SeriesPanel } from "./SeriesPanel";
import type { ModelSeries, Point } from "./api/types";

// jsdom cannot exercise a canvas, so the chart itself is stubbed; what this file
// asserts is the panel's own header wiring around it.
vi.mock("./charts/EChart", () => ({
  EChart: () => <div data-testid="chart" />,
}));

const H = 3_600;
const AUG4 = Date.UTC(2026, 7, 4) / 1000;

const pt = (t: number, p50: number): Point => ({
  t,
  n: 12,
  censored: 0,
  p50,
  p95: p50,
});

// 09:00 UTC to 09:00 the next day.
const day = (values: number[]): Point[] =>
  values.map((v, i) => pt(AUG4 + 9 * H + i * H, v));

const series = (points: Point[]): ModelSeries => ({
  window: "24h",
  bucket_s: 900,
  metric: "ttft",
  probe: "infer",
  models: { "mimo-v2.5": points },
});

const panel = (points: Point[]) => (
  <SeriesPanel
    title="Time to first token"
    subtitle="P50 per bucket."
    series={series(points)}
    models={["mimo-v2.5"]}
    unit="ms"
  />
);

// A flat-ish spread stays linear; a 20x-plus spread switches the axis.
const flat = Array.from({ length: 25 }, (_, i) => 900 + i * 10);
const FLAT = day(flat);
const WIDE = day([...flat.slice(0, 24), 60_000]);

describe("SeriesPanel", () => {
  // A log axis read as a linear one is worse than no chart.
  it("announces the switch to a log axis on the plot", () => {
    render(panel(WIDE));
    expect(screen.getByText("log scale")).toBeInTheDocument();
  });

  it("says nothing about the axis while it is linear", () => {
    render(panel(FLAT));
    expect(screen.queryByText("log scale")).not.toBeInTheDocument();
  });
});
