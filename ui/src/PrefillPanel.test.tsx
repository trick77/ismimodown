import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PrefillPanel } from "./PrefillPanel";
import type { ModelSeries, Point } from "./api/types";

// jsdom cannot exercise a canvas, so the chart itself is stubbed; what this file
// asserts is the panel's own wiring around it.
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
const day = (base: number): Point[] =>
  Array.from({ length: 25 }, (_, i) => pt(AUG4 + 9 * H + i * H, base));

const series = (models: Record<string, Point[]>): ModelSeries => ({
  window: "24h",
  bucket_s: 900,
  metric: "ttft",
  probe: "short",
  models,
});

describe("PrefillPanel", () => {
  const short = series({ "mimo-v2.5": day(900) });
  const wide = series({ "mimo-v2.5": day(2600) });

  // The delta is the chart; the two probes are the reference strip beneath it.
  it("draws the cost above and the probes it came from below", () => {
    render(<PrefillPanel short={short} wide={wide} models={["mimo-v2.5"]} />);
    expect(screen.getAllByTestId("chart")).toHaveLength(2);
    expect(
      screen.getByText(/For reference, the two probes/),
    ).toBeInTheDocument();
  });

  // The panel used to plot two probes an order of magnitude apart and switch
  // axes on the spread — which is exactly what flattened the gap it existed to
  // show. The badge now belongs to the strip, which can still go log; the chart
  // above it never can.
  it("announces a log axis on the reference strip when the spread forces one", () => {
    const wideLog = series({ "mimo-v2.5": day(36_000) });
    render(
      <PrefillPanel short={short} wide={wideLog} models={["mimo-v2.5"]} />,
    );
    expect(screen.getByText(/log scale/i)).toBeInTheDocument();
  });

  it("leaves the badge off when the axis stayed linear", () => {
    render(<PrefillPanel short={short} wide={wide} models={["mimo-v2.5"]} />);
    expect(screen.queryByText(/log scale/i)).not.toBeInTheDocument();
  });

  // The wide probe runs hourly, so the panel exists before its subject does.
  it("says the cost is not plottable yet when the wide probe has no data", () => {
    render(<PrefillPanel short={short} wide={null} models={["mimo-v2.5"]} />);
    expect(screen.getByText(/takes an hour/)).toBeInTheDocument();
    // The short probe still has data, so the strip is still drawn — one chart,
    // not two.
    expect(screen.getAllByTestId("chart")).toHaveLength(1);
  });
});
