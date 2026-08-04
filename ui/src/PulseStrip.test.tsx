import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PulseStrip } from "./PulseStrip";
import type { Cycle } from "./api/types";

const sample = (over: Partial<Cycle> = {}): Cycle => ({
  at: "2026-08-04T12:00:00Z",
  ttft_ms: 900,
  ok: true,
  answer_ok: true,
  error_class: null,
  ...over,
});

describe("PulseStrip", () => {
  it("draws one bar per cycle", () => {
    render(<PulseStrip cycles={[sample(), sample(), sample()]} />);
    expect(screen.getByTestId("pulse-strip").children).toHaveLength(3);
  });

  it("renders nothing rather than an empty frame with no data", () => {
    const { container } = render(<PulseStrip cycles={[]} />);
    expect(container.firstChild).toBeNull();
  });

  // A failed cycle has no latency to plot. Drawing it short would make an
  // outage look like a fast response — the exact inversion this whole project
  // exists to avoid.
  it("gives a failed cycle a full-height bar, not a short one", () => {
    render(
      <PulseStrip
        cycles={[
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
        cycles={[sample({ ttft_ms: 1000 }), sample({ ttft_ms: 500 })]}
      />,
    );
    const bars = screen.getByTestId("pulse-strip").children;
    expect((bars[0] as HTMLElement).style.height).toBe("50%");
    expect((bars[1] as HTMLElement).style.height).toBe("100%");
  });

  it("summarises the strip for assistive tech", () => {
    render(<PulseStrip cycles={[sample(), sample({ ok: false })]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("1 succeeded, 1 failed"),
    );
  });
});
