import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { NetworkPanel } from "./NetworkPanel";
import type { NetSeries, Point } from "./api/types";
import {
  TARGET_MIMO,
  TARGET_REF_SGP,
  TARGET_MIMO_AMS,
  TARGET_REF_AMS,
} from "./api/types";
import {
  MIMO_EDGE_COLOR,
  MIMO_EDGE_AMS_COLOR,
  REFERENCE_COLOR,
  REFERENCE_AMS_COLOR,
} from "./charts/options";

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

const rgb = (hex: string): string => {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};

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

  // Four lines, each legend entry naming its REGION. "MiMo edge" and
  // "Reference" unqualified would be two ambiguous labels on a chart whose
  // entire purpose is the comparison between the two regions.
  it("legends every edge and every reference, by region", () => {
    render(
      <NetworkPanel
        series={net({
          [TARGET_MIMO]: line([170, 172]),
          [TARGET_REF_SGP]: line([160, 165]),
          [TARGET_MIMO_AMS]: line([28, 30]),
          [TARGET_REF_AMS]: line([24, 25]),
        })}
      />,
    );

    for (const label of [
      "MiMo edge (Singapore)",
      "Reference (Singapore)",
      "MiMo edge (Amsterdam)",
      "Reference (Amsterdam)",
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });

  // Role is hue-versus-ink and region is which one, and the legend swatch is
  // the only place that mapping is visible outside the canvas jsdom cannot
  // render. Two targets sharing a swatch would make the chart unreadable while
  // every other assertion here still passed.
  it("gives each target its own swatch", () => {
    const { container } = render(
      <NetworkPanel
        series={net({
          [TARGET_MIMO]: line([170, 172]),
          [TARGET_REF_SGP]: line([160, 165]),
          [TARGET_MIMO_AMS]: line([28, 30]),
          [TARGET_REF_AMS]: line([24, 25]),
        })}
      />,
    );

    const swatches = [
      ...container.querySelectorAll("li span[aria-hidden]"),
    ].map((el) => (el as HTMLElement).style.background);
    expect(swatches).toHaveLength(4);
    expect(new Set(swatches).size).toBe(4);
    for (const color of [
      MIMO_EDGE_COLOR,
      REFERENCE_COLOR,
      MIMO_EDGE_AMS_COLOR,
      REFERENCE_AMS_COLOR,
    ]) {
      // jsdom normalises an inline colour to rgb(), so the constants are
      // compared in that form rather than as the hex they are written in.
      expect(swatches).toContain(rgb(color));
    }
  });

  // A region that has not reported yet must drop out rather than draw an empty
  // line: a flat series at the axis floor reads as a 0 ms handshake.
  it("omits a target with no points", () => {
    render(
      <NetworkPanel
        series={net({
          [TARGET_MIMO]: line([170, 172]),
          [TARGET_MIMO_AMS]: [],
        })}
      />,
    );

    expect(screen.getByText("MiMo edge (Singapore)")).toBeInTheDocument();
    expect(screen.queryByText("MiMo edge (Amsterdam)")).not.toBeInTheDocument();
  });
});
