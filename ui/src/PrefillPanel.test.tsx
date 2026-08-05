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

// 09:00 UTC to 09:00 the next day, so exactly one off-peak band is in range.
const day = (base: number): Point[] =>
  Array.from({ length: 25 }, (_, i) => pt(AUG4 + 9 * H + i * H, base));

const series = (models: Record<string, Point[]>): ModelSeries => ({
  window: "24h",
  bucket_s: 900,
  metric: "ttft",
  probe: "infer",
  models,
});

describe("PrefillPanel", () => {
  const infer = series({ "mimo-v2.5": day(900) });
  const wide = series({ "mimo-v2.5": day(2600) });

  // Prefill is a cost measured in latency — the subtitle says as much. What an
  // extra 3800 tokens costs and what those tokens bill at are one question.
  it("carries the rate chip in the header", () => {
    render(<PrefillPanel infer={infer} wide={wide} models={["mimo-v2.5"]} />);
    expect(screen.getByText(/0\.8× (until|from)/i)).toBeInTheDocument();
  });

  // This panel plots two probes an order of magnitude apart, so it is the one
  // most likely to switch axes — and it rendered no badge at all until now.
  it("announces a log axis when the spread forces one", () => {
    const wideLog = series({ "mimo-v2.5": day(36_000) });
    render(
      <PrefillPanel infer={infer} wide={wideLog} models={["mimo-v2.5"]} />,
    );
    expect(screen.getByText(/log scale/i)).toBeInTheDocument();
  });

  it("leaves the badge off when the axis stayed linear", () => {
    render(<PrefillPanel infer={infer} wide={wide} models={["mimo-v2.5"]} />);
    expect(screen.queryByText(/log scale/i)).not.toBeInTheDocument();
  });

  // The wide probe runs hourly, so the panel exists before its subject does.
  it("says the gap is not readable yet when the wide probe has no data", () => {
    render(<PrefillPanel infer={infer} wide={null} models={["mimo-v2.5"]} />);
    expect(screen.getByText(/takes an hour/)).toBeInTheDocument();
  });
});
