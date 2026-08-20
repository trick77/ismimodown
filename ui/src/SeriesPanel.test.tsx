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
  models: { "mimo-v2.5": points },
});

const panel = (points: Point[], bucketS = 900) => (
  <SeriesPanel
    title="Time to first token"
    subtitle="P50 per bucket."
    series={{ ...series(points), bucket_s: bucketS }}
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

  // Bold reads as the real line, so a chart that draws a rolling median over
  // its own readings has to say which of the two was measured.
  it("names the bold line and the hairline once it smooths", () => {
    // Four days of hourly buckets — past the 48h the smoothing waits for.
    render(panel(day(Array.from({ length: 96 }, () => 1_000)), 3_600));
    expect(screen.getByText(/rolling median/)).toBeInTheDocument();
    expect(screen.getByText(/the measurement itself/)).toBeInTheDocument();
  });

  // Below that the plot is unchanged, so a note about a line that is not there
  // would be describing the wrong picture.
  it("says nothing about a trend on a short window", () => {
    render(panel(FLAT));
    expect(screen.queryByText(/rolling median/)).not.toBeInTheDocument();
  });

  // "Smoothed" without a span is a claim of unknown strength.
  it("states the window the median ran over", () => {
    render(panel(day(Array.from({ length: 96 }, () => 1_000)), 3_600));
    // 96 hourly buckets, smoothed over an eighth of them.
    expect(screen.getByText(/rolling median/).textContent).toContain("13-hour");
  });
});
