import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { NetworkPanel } from "./NetworkPanel";
import type { NetSeries, Point } from "./api/types";
import { TARGET_MIMO, TARGET_REF_SGP } from "./api/types";

// jsdom cannot exercise a canvas, so the chart is stubbed; what this file
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

const line = (values: number[]): Point[] =>
  values.map((v, i) => pt(AUG4 + i * H, v));

const net = (targets: Record<string, Point[]>): NetSeries => ({
  window: "24h",
  bucket_s: 900,
  metric: "network",
  targets,
});

describe("NetworkPanel", () => {
  it("draws the chart once a target has points", () => {
    render(<NetworkPanel series={net({ [TARGET_MIMO]: line([170, 172]) })} />);
    expect(screen.getByTestId("chart")).toBeInTheDocument();
  });

  // The axis switch used to happen with nothing on the card to say so, which is
  // the one failure mode the badge exists to prevent.
  it("announces a log axis when the spread forces one", () => {
    render(
      <NetworkPanel
        series={net({
          [TARGET_MIMO]: line([170, 9000]),
          [TARGET_REF_SGP]: line([160, 165]),
        })}
      />,
    );
    expect(screen.getByText(/log scale/i)).toBeInTheDocument();
  });

  it("leaves the badge off when the axis stayed linear", () => {
    render(
      <NetworkPanel
        series={net({
          [TARGET_MIMO]: line([170, 172]),
          [TARGET_REF_SGP]: line([160, 165]),
        })}
      />,
    );
    expect(screen.queryByText(/log scale/i)).not.toBeInTheDocument();
  });

  it("says so plainly when no target answered", () => {
    render(<NetworkPanel series={net({})} />);
    expect(screen.getByText(/Not enough data yet/)).toBeInTheDocument();
  });
});
