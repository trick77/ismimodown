import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SamplesTable } from "./SamplesTable";
import type { Sample } from "./api/types";
import { formatTime } from "./format";

const sample = (over: Partial<Sample> = {}): Sample => ({
  at: "2026-08-04T12:00:00Z",
  model_id: "mimo-v2.5",
  probe: "infer",
  ttft_ms: 900,
  total_ms: 1700,
  itl_p50_ms: 24,
  output_tps: 41,
  ok: true,
  answer_ok: true,
  error_class: null,
  ...over,
});

const bodyRows = () =>
  screen.getByRole("table").querySelectorAll("tbody tr").length;

describe("SamplesTable", () => {
  // The caller hands over a whole day so PulseStrip can draw it. The table is
  // not the place to re-read that day.
  it("renders at most the ten most recent cycles", () => {
    render(
      <SamplesTable
        samples={Array.from({ length: 288 }, (_, i) => sample({ ttft_ms: i }))}
      />,
    );
    expect(bodyRows()).toBe(10);
  });

  // Newest-first from the API, so the head of the array is what to keep.
  it("keeps the newest cycles, not the oldest", () => {
    render(
      <SamplesTable
        samples={[
          sample({ at: "2026-08-04T12:00:00Z" }),
          ...Array.from({ length: 20 }, () =>
            sample({ at: "2026-08-03T00:00:00Z" }),
          ),
        ]}
      />,
    );
    const first = screen.getByRole("table").querySelector("tbody tr td");
    expect(first?.textContent).toBe(formatTime("2026-08-04T12:00:00Z"));
  });

  it("shows fewer rows without complaint when fewer cycles exist", () => {
    render(<SamplesTable samples={[sample(), sample()]} />);
    expect(bodyRows()).toBe(2);
  });

  // The tick is decoration; assistive tech still has to hear the outcome.
  it("marks a good cycle with a tick that still reads as ok", () => {
    render(<SamplesTable samples={[sample()]} />);
    expect(screen.getByText("✓")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByText("ok")).toBeInTheDocument();
  });

  it("names the error class instead of a tick when a cycle failed", () => {
    render(
      <SamplesTable
        samples={[sample({ ok: false, error_class: "timeout" })]}
      />,
    );
    expect(screen.getByText("timeout")).toBeInTheDocument();
    expect(screen.queryByText("✓")).not.toBeInTheDocument();
  });

  it("says so plainly when there is nothing to show", () => {
    render(<SamplesTable samples={[]} />);
    expect(screen.getByText(/Not enough data yet/)).toBeInTheDocument();
  });
});
