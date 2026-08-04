import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PulseStrip } from "./PulseStrip";
import type { Sample } from "./api/types";

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

describe("PulseStrip", () => {
  it("draws one bar per cycle", () => {
    render(<PulseStrip samples={[sample(), sample(), sample()]} />);
    expect(screen.getByTestId("pulse-strip").children).toHaveLength(3);
  });

  it("renders nothing rather than an empty frame with no data", () => {
    const { container } = render(<PulseStrip samples={[]} />);
    expect(container.firstChild).toBeNull();
  });

  // A failed cycle has no latency to plot. Drawing it short would make an
  // outage look like a fast response — the exact inversion this whole project
  // exists to avoid.
  it("gives a failed cycle a full-height bar, not a short one", () => {
    render(
      <PulseStrip
        samples={[
          sample({ ttft_ms: 100 }),
          sample({ ok: false, ttft_ms: null, error_class: "timeout" }),
        ]}
      />,
    );
    const bars = screen.getByTestId("pulse-strip").children;
    // Oldest first: the failure is rendered last.
    expect((bars[0] as HTMLElement).style.height).toBe("100%");
  });

  // Height is relative to the window's own peak, so the shape stays readable
  // whether the day topped out at 900ms or 90s.
  it("scales heights against the worst reading in the window", () => {
    render(
      <PulseStrip
        samples={[sample({ ttft_ms: 1000 }), sample({ ttft_ms: 500 })]}
      />,
    );
    const bars = screen.getByTestId("pulse-strip").children;
    expect((bars[0] as HTMLElement).style.height).toBe("50%");
    expect((bars[1] as HTMLElement).style.height).toBe("100%");
  });

  it("summarises the strip for assistive tech", () => {
    render(<PulseStrip samples={[sample(), sample({ ok: false })]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("1 succeeded, 1 failed"),
    );
  });
});
