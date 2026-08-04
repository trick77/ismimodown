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

// Each cycle is a full-height CELL carrying the off-peak tint, with the bar
// inside it — so the bar is one level down from the strip.
const barAt = (i: number): HTMLElement =>
  screen.getByTestId("pulse-strip").children[i]!.firstChild as HTMLElement;

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
    // Oldest first: the failure is rendered last.
    expect(barAt(0).style.height).toBe("100%");
  });

  // Height is relative to the window's own peak, so the shape stays readable
  // whether the day topped out at 900ms or 90s.
  it("scales heights against the worst reading in the window", () => {
    render(
      <PulseStrip
        cycles={[sample({ ttft_ms: 1000 }), sample({ ttft_ms: 500 })]}
      />,
    );
    expect(barAt(0).style.height).toBe("50%");
    expect(barAt(1).style.height).toBe("100%");
  });

  // 16:00–24:00 UTC is MiMo's reduced-rate window.
  it("tints only the cycles that billed off-peak", () => {
    render(
      <PulseStrip
        cycles={[
          sample({ at: "2026-08-04T20:00:00Z" }),
          sample({ at: "2026-08-04T12:00:00Z" }),
        ]}
      />,
    );
    const cells = screen.getByTestId("pulse-strip").children;
    // Oldest first, so the 12:00 cycle leads.
    expect((cells[0] as HTMLElement).className).not.toContain("bg-accent");
    expect((cells[1] as HTMLElement).className).toContain("bg-accent");
  });

  // The strip has no time axis — a missed cycle closes up rather than leaving a
  // hole — so position and time are different things. Banding by position would
  // drift off the bars the moment the daemon skips a run.
  it("bands by each cycle's own timestamp, not by its position", () => {
    render(
      <PulseStrip
        cycles={[
          sample({ at: "2026-08-04T20:00:00Z" }),
          // A four-hour hole where the probe did not run.
          sample({ at: "2026-08-04T12:00:00Z" }),
          sample({ at: "2026-08-04T11:55:00Z" }),
        ]}
      />,
    );
    const cells = [...screen.getByTestId("pulse-strip").children];
    expect(cells.map((c) => c.className.includes("bg-accent"))).toEqual([
      false,
      false,
      true,
    ]);
  });

  it("draws no rail when nothing in the window was off-peak", () => {
    render(<PulseStrip cycles={[sample({ at: "2026-08-04T12:00:00Z" })]} />);
    expect(screen.queryByTestId("pulse-rail")).toBeNull();
  });

  // Colour is never the only signal here.
  it("names the shading for assistive tech when any cycle is off-peak", () => {
    render(<PulseStrip cycles={[sample({ at: "2026-08-04T20:00:00Z" })]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("off-peak"),
    );
  });

  it("summarises the strip for assistive tech", () => {
    render(<PulseStrip cycles={[sample(), sample({ ok: false })]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("1 succeeded, 1 failed"),
    );
  });
});
