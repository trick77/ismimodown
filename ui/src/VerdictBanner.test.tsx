import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { VerdictBanner } from "./VerdictBanner";
import type { ModelTrend, Trend } from "./api/types";
import type { Verdict } from "./verdict";

// The plot renders to a canvas jsdom cannot exercise, so the wrapper is mocked
// here as it is in every other panel test — what this file asserts is the
// SENTENCE and which chip carries it.
vi.mock("./charts/EChart", () => ({
  EChart: ({ ariaLabel }: { ariaLabel: string }) => (
    <div data-testid="echart" aria-label={ariaLabel} />
  ),
}));

const stats = (p50: number | null) => ({
  n: p50 === null ? 4 : 36,
  sufficient: p50 !== null,
  p50_ms: p50,
  p95_ms: p50,
});

const model = (id: string, ttft: [number, number]): ModelTrend => ({
  model_id: id,
  ttft: {
    recent: stats(ttft[0]),
    before: stats(ttft[1]),
    points: [{ t: 1, n: 6, censored: 0, p50: ttft[0], p95: ttft[0] }],
  },
  tps: {
    recent: stats(70),
    before: stats(70),
    points: [{ t: 1, n: 6, censored: 0, p50: 70, p95: 70 }],
  },
});

const trend = (models: ModelTrend[]): Trend => ({
  recent_s: 3 * 3600,
  before_s: 24 * 3600,
  bucket_s: 1800,
  models,
  generated_at: "2026-08-20T12:00:00Z",
});

const normal: Verdict = {
  state: "normal",
  headline: "Everything looks normal right now",
  detail: [],
};

describe("VerdictBanner", () => {
  // The contradiction this whole arrangement exists to make unrepresentable: a
  // green "everything looks normal" with "slower than usual" underneath it does
  // not answer the page's own question. One chip, one sentence, one claim.
  it("never says normal and slower at the same time", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [1700, 900])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "slower",
    );
    expect(screen.queryAllByTestId("state-chip")).toHaveLength(1);
    expect(screen.queryByText(/Everything looks normal/)).toBeNull();
    expect(screen.getByText(/slow to start right now/)).toBeInTheDocument();
  });

  // A fault outranks a slowdown and takes the banner outright. Hedging an
  // outage with a note about latency reads as neither.
  it("says nothing about speed while something is failing", () => {
    render(
      <VerdictBanner
        verdict={{
          state: "degraded",
          headline: "mimo-v2.5 is failing requests right now",
          detail: ["Nine of the last twenty came back as errors."],
        }}
        trend={trend([model("mimo-v2.5", [1700, 900])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "degraded",
    );
    expect(screen.queryByText(/slow to start/)).toBeNull();
    expect(screen.queryByTestId("trend-plot")).toBeNull();
  });

  // The plot appearing is itself part of the signal, so a quiet day must not
  // draw one.
  it("plots the slowdown, and only when there is one", () => {
    const { rerender } = render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [1700, 900])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("trend-plot")).toBeInTheDocument();

    rerender(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [900, 900])])}
        loading={false}
      />,
    );
    expect(screen.queryByTestId("trend-plot")).toBeNull();
    expect(screen.getByText(/Everything looks normal/)).toBeInTheDocument();
  });

  // Forgetting is only acceptable if the page says what it forgot: a resting
  // banner that mentions speed at all is what makes its silence readable.
  it("says it is running at its usual speed on a quiet day", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [900, 900])])}
        loading={false}
      />,
    );
    expect(screen.getByText(/ordinary spread/)).toBeInTheDocument();
  });

  // A payload from an older daemon has no trend block at all, and the banner is
  // the one thing on the page that must never fail to render.
  it("renders the plain verdict when the block is missing", () => {
    render(<VerdictBanner verdict={normal} loading={false} />);
    expect(
      screen.getByText("Everything looks normal right now"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "normal",
    );
  });
});
