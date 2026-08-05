import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ModelCards } from "./ModelCards";
import { MIN_FAILURES_FOR_STATE } from "./verdict";
import type { ModelSummary, Summary } from "./api/types";

function model(over: Partial<ModelSummary> = {}): ModelSummary {
  return {
    model_id: "mimo-v2.5",
    probe: "infer",
    ttft: { n: 288, sufficient: true, p50_ms: 916, p95_ms: 1400 },
    itl: { n: 288, sufficient: true, p50_ms: 24, p95_ms: 40 },
    tps: { n: 288, sufficient: true, p50_ms: 41, p95_ms: 60 },
    attempts: 288,
    succeeded: 288,
    available_pct: 100,
    censored: 0,
    answered: 288,
    correct: 288,
    correct_pct: 100,
    max_reasoning_tokens: 0,
    max_cached_tokens: 0,
    ...over,
  };
}

const summary = (models: ModelSummary[]): Summary => ({
  window: "24h",
  cycles: 288,
  models,
  net: [],
  faults: { ok: 288 },
  recent: [],
  skipped_runs: 0,
  generated_at: "2026-08-04T12:00:00Z",
});

describe("ModelCards", () => {
  it("renders one card per model", () => {
    render(
      <ModelCards
        summary={summary([model(), model({ model_id: "mimo-v2.5-pro" })])}
        baseline={null}
      />,
    );
    expect(screen.getByTestId("model-card-mimo-v2.5")).toBeInTheDocument();
    expect(screen.getByTestId("model-card-mimo-v2.5-pro")).toBeInTheDocument();
  });

  it("shows the headline figures", () => {
    render(<ModelCards summary={summary([model()])} baseline={null} />);
    expect(screen.getByText("916 ms")).toBeInTheDocument();
    expect(screen.getByText("41 tok/s")).toBeInTheDocument();
  });

  // A thin window must never render a number, because a P50 from three samples
  // is exactly what gets quoted out of context.
  it("suppresses a thin percentile", () => {
    render(
      <ModelCards
        summary={summary([
          model({
            ttft: { n: 3, sufficient: false, p50_ms: null, p95_ms: null },
          }),
        ])}
        baseline={null}
      />,
    );
    expect(screen.getAllByTestId("insufficient").length).toBeGreaterThan(0);
  });

  // Reasoning coming back on invalidates every latency figure on the card, so
  // it must be stated on the card rather than only in the banner.
  it("warns loudly when reasoning tokens appear", () => {
    render(
      <ModelCards
        summary={summary([model({ max_reasoning_tokens: 512 })])}
        baseline={null}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/not measuring what/i);
  });

  // The percentiles on this card are computed over runs that finished, so the
  // runs the ladder cut off were removed from the TOP of the distribution — and
  // the card gets more flattering as truncation gets worse. Nothing in the
  // numbers themselves can say that.
  it("says so when runs were cut off by the timeout limits", () => {
    render(
      <ModelCards
        summary={summary([
          model({ censored: 12, attempts: 288, succeeded: 276 }),
        ])}
        baseline={null}
      />,
    );
    const note = screen.getByTestId("censored-mimo-v2.5");
    expect(note).toHaveTextContent(/12 of 288/);
    // Both halves, because they are different claims and the old copy ran them
    // together: the runs ARE in the availability figure beside this, and they
    // are NOT in the percentiles above it.
    expect(note).toHaveTextContent(/count as failures in Availability/i);
    expect(note).toHaveTextContent(/p50s do not include them/i);
  });

  it("stays quiet when nothing was cut off", () => {
    render(<ModelCards summary={summary([model()])} baseline={null} />);
    expect(screen.queryByTestId("censored-mimo-v2.5")).toBeNull();
  });

  // The floor the Availability chip already uses. Without it the card refuses
  // to paint a state on one dropped run and then paints an amber box about that
  // same run — and at 288 cycles a day it does that essentially forever.
  //
  // A censored run is a subset of a failed one, so this threshold also makes it
  // impossible for the banner to appear while the chip beside it is suppressed.
  it("stays quiet when too few runs were cut off to mean anything", () => {
    render(
      <ModelCards
        summary={summary([
          model({
            censored: MIN_FAILURES_FOR_STATE - 1,
            attempts: 221,
            // A censored run is a failed one, so the failure count can never be
            // below the censored count — a fixture that says otherwise would
            // test a state the backend cannot produce.
            succeeded: 219,
          }),
        ])}
        baseline={null}
      />,
    );
    expect(screen.queryByTestId("censored-mimo-v2.5")).toBeNull();
  });

  it("counts a single reasoning token in the singular", () => {
    render(
      <ModelCards
        summary={summary([model({ max_reasoning_tokens: 1 })])}
        baseline={null}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/returned 1 token\b/);
  });

  it("renders nothing but stays stable with no summary", () => {
    const { container } = render(<ModelCards summary={null} baseline={null} />);
    expect(container.querySelectorAll("section")).toHaveLength(0);
  });
});
