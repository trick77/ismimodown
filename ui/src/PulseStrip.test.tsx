import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PulseStrip, worstPerCycle } from "./PulseStrip";
import type { Cycle } from "./api/types";

// Cycles are keyed by their timestamp, so a fixture that reuses one is a
// fixture of a single cycle. `n` is minutes past the hour, oldest first.
const sample = (n = 0, over: Partial<Cycle> = {}): Cycle => ({
  at: `2026-08-04T12:${String(n).padStart(2, "0")}:00Z`,
  ttft_ms: 900,
  ok: true,
  answer_ok: true,
  error_class: null,
  ...over,
});

// Each cycle is a full-height CELL with the bar inside it — so the bar is one
// level down from the strip.
const barAt = (i: number): HTMLElement =>
  screen.getByTestId("pulse-strip").children[i]!.firstChild as HTMLElement;

describe("PulseStrip", () => {
  it("draws one bar per cycle", () => {
    render(<PulseStrip perModel={[[sample(0), sample(5), sample(10)]]} />);
    expect(screen.getByTestId("pulse-strip").children).toHaveLength(3);
  });

  it("renders nothing rather than an empty frame with no data", () => {
    const { container } = render(<PulseStrip perModel={[]} />);
    expect(container.firstChild).toBeNull();
  });

  // A failed cycle has no latency to plot. Drawing it short would make an
  // outage look like a fast response — the exact inversion this whole project
  // exists to avoid.
  it("gives a failed cycle a full-height bar, not a short one", () => {
    render(
      <PulseStrip
        perModel={[
          [
            sample(0, { ttft_ms: 100 }),
            sample(5, { ok: false, ttft_ms: null, error_class: "timeout" }),
          ],
        ]}
      />,
    );
    expect(barAt(1).style.height).toBe("100%");
  });

  // Height is relative to the strip's own peak, so the shape stays readable
  // whether the day topped out at 900ms or 90s.
  it("scales heights against the worst reading in the window", () => {
    render(
      <PulseStrip
        perModel={[[sample(0, { ttft_ms: 1000 }), sample(5, { ttft_ms: 500 })]]}
      />,
    );
    expect(barAt(0).style.height).toBe("100%");
    expect(barAt(1).style.height).toBe("50%");
  });

  it("summarises the strip for assistive tech", () => {
    render(<PulseStrip perModel={[[sample(0), sample(5, { ok: false })]]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("2 cycles"),
    );
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("1 succeeded, 1 failed"),
    );
  });

  // A cold start, or the first cycle of a fresh window. Read aloud, "Last 1
  // cycles" is the kind of thing that makes a screen-reader user stop trusting
  // the rest of the label.
  it("says one cycle in the singular", () => {
    render(<PulseStrip perModel={[[sample()]]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("Last 1 cycle:"),
    );
  });

  // Colour is never the only signal here, and the strip's three states were
  // told apart by hue alone — readable by hovering, which a phone does not do.
  it("says in words what the strip is and what its colours mean", () => {
    render(<PulseStrip perModel={[[sample()]]} />);

    const note = screen.getByTestId("pulse-note");
    expect(note).toHaveTextContent("TTFT");
    expect(note).toHaveTextContent("24 hours");
    expect(note).toHaveTextContent("a run failed");
    expect(note).toHaveTextContent("a wrong answer");
  });
});

describe("worstPerCycle", () => {
  // The reason the strip merges at all: one model failing while the other is
  // healthy used to be painted green by the page's loudest surface.
  it("fails a cycle when either model failed", () => {
    const merged = worstPerCycle([
      [sample(0)],
      [sample(0, { ok: false, ttft_ms: null, error_class: "timeout" })],
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]!.ok).toBe(false);
  });

  it("takes the slower of the two as the height", () => {
    const merged = worstPerCycle([
      [sample(0, { ttft_ms: 400 })],
      [sample(0, { ttft_ms: 1600 })],
    ]);

    expect(merged[0]!.ttft_ms).toBe(1600);
  });

  // A model that reported no TTFT contributes nothing. Treated as a zero it
  // would drag the pair down; treated as absent, the reading that exists
  // stands.
  it("ignores a missing reading rather than counting it as fast", () => {
    const merged = worstPerCycle([
      [sample(0, { ttft_ms: null })],
      [sample(0, { ttft_ms: 700 })],
    ]);

    expect(merged[0]!.ttft_ms).toBe(700);
  });

  // null is "not graded" and must not outrank a false.
  it("marks the answer wrong when either model answered wrongly", () => {
    const merged = worstPerCycle([
      [sample(0, { answer_ok: null })],
      [sample(0, { answer_ok: false })],
    ]);

    expect(merged[0]!.answer_ok).toBe(false);
  });

  // Keyed on the timestamp, never on position: a model missing from one cycle
  // must not shift every later bar of the other one out of alignment.
  it("aligns on the cycle, not on the index", () => {
    const merged = worstPerCycle([
      [sample(0), sample(5), sample(10)],
      [sample(5, { ok: false })],
    ]);

    expect(merged.map((c) => c.at)).toEqual([
      "2026-08-04T12:00:00Z",
      "2026-08-04T12:05:00Z",
      "2026-08-04T12:10:00Z",
    ]);
    expect(merged[1]!.ok).toBe(false);
    expect(merged[0]!.ok).toBe(true);
  });

  it("orders by time, whichever response arrived first", () => {
    const merged = worstPerCycle([[sample(10)], [sample(0)]]);

    expect(merged.map((c) => c.at)).toEqual([
      "2026-08-04T12:00:00Z",
      "2026-08-04T12:10:00Z",
    ]);
  });

  it("keeps a reason for the hover text", () => {
    const merged = worstPerCycle([
      [sample(0, { ok: false, ttft_ms: null, error_class: "stalled" })],
      [sample(0)],
    ]);

    expect(merged[0]!.error_class).toBe("stalled");
  });
});
