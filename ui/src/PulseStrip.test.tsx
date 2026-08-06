import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { hourTicks, PulseStrip, worstPerCycle } from "./PulseStrip";
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

  // The inverse of what this asserted before. Rendering nothing meant the strip
  // and its caption appeared the instant the fetch landed, near the top of the
  // page, pushing everything under them down — most of the page's layout shift
  // came from this one component. An empty frame holds that ground.
  it("holds its frame while pending, so the bars cost no layout shift", () => {
    render(<PulseStrip perModel={[]} pending />);
    const strip = screen.getByTestId("pulse-strip");
    expect(strip.children).toHaveLength(0);
    expect(strip).toHaveClass("h-12");
    expect(screen.getByTestId("pulse-note")).toBeInTheDocument();
    // The axis is part of the ground being held. Reserving the bars alone puts
    // its 20px back into the layout the moment the fetch lands, which is the
    // shift this frame exists to prevent.
    expect(screen.getByTestId("pulse-axis")).toHaveClass("h-4");
  });

  // "Last 0 cycles: 0 succeeded, 0 failed" is a reading. The empty frame has
  // not read anything yet, and must not claim a clean day.
  it("does not report an empty frame as zero failures", () => {
    render(<PulseStrip perModel={[]} pending />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      "Cycle history, still loading",
    );
  });

  // The frame is a promise that bars are coming. Once the answer is in and
  // there are none — a failed load, or a database with nothing in it yet —
  // there is nothing left to hold ground for, and the strip must not go on
  // telling a screen reader it is loading.
  it("renders nothing once the answer is in and there is nothing to draw", () => {
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

  // The case the strip is for: a normal day with one timeout in it. Linear,
  // 900ms against 150s is 0.6% and every healthy bar clamps to the floor —
  // a row of dots. Asserted as a bound rather than a string, because the
  // anchor arithmetic should be tunable without rewriting the test.
  it("keeps the healthy bars legible when one cycle times out", () => {
    render(
      <PulseStrip
        perModel={[
          [
            sample(0, { ttft_ms: 900 }),
            sample(5, { ttft_ms: 950 }),
            sample(10, { ttft_ms: 150_000 }),
          ],
        ]}
      />,
    );
    expect(barAt(2).style.height).toBe("100%");
    expect(parseFloat(barAt(0).style.height)).toBeGreaterThan(20);
    expect(parseFloat(barAt(0).style.height)).toBeLessThan(
      parseFloat(barAt(1).style.height),
    );
  });

  // A log scale read as linear is worse than no chart at all, and the strip
  // has no axis to give it away.
  it("says so when it switches to a log scale, and only then", () => {
    const { unmount } = render(
      <PulseStrip
        perModel={[
          [sample(0, { ttft_ms: 900 }), sample(5, { ttft_ms: 150_000 })],
        ]}
      />,
    );
    expect(screen.getByTestId("pulse-note")).toHaveTextContent("log-scaled");
    unmount();

    render(
      <PulseStrip
        perModel={[[sample(0, { ttft_ms: 900 }), sample(5, { ttft_ms: 1800 })]]}
      />,
    );
    expect(screen.getByTestId("pulse-note")).not.toHaveTextContent(
      "log-scaled",
    );
  });

  // A window with nothing to spread has no log domain: dividing by that range
  // renders NaN%, which the browser drops, and the bars vanish.
  it("draws a real height when every reading is identical", () => {
    render(
      <PulseStrip
        perModel={[[sample(0, { ttft_ms: 900 }), sample(5, { ttft_ms: 900 })]]}
      />,
    );
    for (const i of [0, 1]) {
      expect(barAt(i).style.height).toBe("100%");
    }
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

  // The axis says which hour a bar belongs to; a screen reader gets the same
  // fact as the window it is looking at. Stated as its two endpoints, not as
  // "24 hours": the backend asks for the last 288 cycles, so after a stretch
  // with the daemon down the strip reaches back further than a day.
  it("names the window it is showing", () => {
    render(<PulseStrip perModel={[run("2026-08-04T06:00:00Z", 12, 30)]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("08:00 to 13:30"),
    );
  });

  // Two clock times alone are only a window while they share a day. The strip
  // is the last 288 CYCLES, so a stretch with the daemon down reaches back past
  // midnight, and "08:00 to 09:55" then states a two-hour window where the
  // truth is twenty-six — a worse claim than the "24 hours" this label exists
  // to stop making.
  it("dates the window when it reaches back past midnight", () => {
    render(<PulseStrip perModel={[run("2026-08-04T06:00:00Z", 53, 30)]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("04 Aug, 08:00 to 05 Aug, 10:00"),
    );
  });

  // Both sets of labels are in the DOM at once — one shown per breakpoint — so
  // an axis that was not hidden would read every hour to a screen reader twice,
  // inside a role="img" whose own label already carries the window.
  it("keeps the axis out of the accessible name", () => {
    render(<PulseStrip perModel={[run("2026-08-04T06:00:00Z", 48, 30)]} />);
    expect(screen.getByTestId("pulse-axis")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
  });

  // A cold start, or the first cycle of a fresh window. Read aloud, "Last 1
  // cycles" is the kind of thing that makes a screen-reader user stop trusting
  // the rest of the label.
  it("says one cycle in the singular", () => {
    render(<PulseStrip perModel={[[sample()]]} />);
    expect(screen.getByTestId("pulse-strip")).toHaveAttribute(
      "aria-label",
      expect.stringContaining("Last 1 cycle,"),
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

// Tests run under TZ=Europe/Zurich (package.json), so an August stamp in UTC
// reads two hours later on the axis: 07:00Z is 09:00 to the reader, and the
// hours the ticks are anchored to are those local ones.
const at = (iso: string, over: Partial<Cycle> = {}): Cycle => ({
  at: iso,
  ttft_ms: 900,
  ok: true,
  answer_ok: true,
  error_class: null,
  ...over,
});

// `count` cycles `stepMin` apart, starting at `startIso`.
const run = (startIso: string, count: number, stepMin = 5): Cycle[] => {
  const t0 = Date.parse(startIso);
  return Array.from({ length: count }, (_, i) =>
    at(new Date(t0 + i * stepMin * 60_000).toISOString()),
  );
};

describe("hourTicks", () => {
  // Anchored on the clock — 09:00, 12:00, 15:00 — rather than on the window's
  // own start, so the landmarks are the same from one visit to the next.
  it("marks every third hour, counting from midnight", () => {
    // 08:15 to 16:15 local.
    const ticks = hourTicks(run("2026-08-04T06:15:00Z", 17, 30), 3);
    expect(ticks.map((t) => t.label)).toEqual(["09:00", "12:00", "15:00"]);
  });

  // Cycles land wherever the daemon's cadence puts them, and that phase moves
  // on every restart. An axis reading "09:02 · 12:02" would invite a reader to
  // find meaning in it.
  it("labels the whole hour, not the stamp of the bar it points at", () => {
    const ticks = hourTicks(run("2026-08-04T06:47:00Z", 60), 3);
    expect(ticks[0]!.label).toBe("09:00");
  });

  // The bars are packed by index, so the tick has to be placed by index too:
  // one drawn at "three hours across" would point at the wrong bar on exactly
  // the day the reader needs it to be right.
  it("positions a tick on its own bar, not on where the clock says it should be", () => {
    // 08:00 local, then a two-hour hole, then 10:00 local onwards. The 12:00
    // tick sits on bar 26 of 33 — where its bar is — rather than at the
    // four-fifths mark an evenly-spaced axis would put it.
    const cycles = [
      ...run("2026-08-04T06:00:00Z", 1),
      ...run("2026-08-04T08:00:00Z", 32),
    ];
    const ticks = hourTicks(cycles, 3);

    expect(ticks.map((t) => t.label)).toEqual(["12:00"]);
    expect(ticks[0]!.left).toBeCloseTo((25.5 / 33) * 100, 5);
  });

  // No bar, no tick. After a gap that swallows a labelled hour whole there is
  // nothing under it to point at, and the next tick is simply the first bar of
  // the next multiple of three.
  it("drops an hour the daemon never recorded", () => {
    const cycles = [
      ...run("2026-08-04T06:00:00Z", 48), // 08:00–11:55 local
      ...run("2026-08-04T11:00:00Z", 48), // 13:00–16:55 local, 12:00 missing
    ];
    const ticks = hourTicks(cycles, 3);

    expect(ticks.map((t) => t.label)).toEqual(["09:00", "15:00"]);
  });

  // A label is centred on its bar, so one sitting against the left end of the
  // strip hangs half of itself outside the frame.
  it("gives up a label that would hang off the edge", () => {
    // Opens at 08:58 local, so 09:00 falls on bar 1 of 288 — half a per cent
    // across, which is inside the guard band.
    const ticks = hourTicks(run("2026-08-04T06:58:00Z", 288), 3);
    expect(ticks.map((t) => t.label)).not.toContain("09:00");
    expect(ticks[0]!.label).toBe("12:00");
  });

  // The strip's first bar is not the first bar of its hour, only the first one
  // that survived the window. A tick reading "09:00" over a 09:47 bar is off by
  // three quarters of an hour, and it is the bar a reader anchors the whole
  // strip on.
  it("says nothing about an hour the window opens in the middle of", () => {
    // 09:47 local onwards, few enough bars that the edge guard does not cover
    // for this — a daemon that has only just started recording.
    const ticks = hourTicks(run("2026-08-04T07:47:00Z", 20), 3);
    expect(ticks).toEqual([]);
  });

  // An hour compared by its number alone reads the far side of a day-long gap
  // as a continuation of the near side, and prints no tick at all — on exactly
  // the window whose length makes the axis worth having.
  it("marks an hour that resumes at the same clock time a day later", () => {
    const cycles = [
      ...run("2026-08-04T06:00:00Z", 10), // 08:00–08:45 local
      ...run("2026-08-05T07:05:00Z", 40), // 09:05 local the NEXT day onwards
    ];
    const ticks = hourTicks(cycles, 3);
    expect(ticks.map((t) => t.label)).toContain("09:00");
  });

  // The strip is the last 288 CYCLES, not a fixed 24 hours, so any day the
  // daemon spent a while down reaches back past its own start hour and labels
  // that hour twice. Two ticks, two positions, two identities — a tick keyed on
  // its label would collide here and React would reconcile the pair as one.
  it("labels an hour twice when the window is longer than a day", () => {
    // 08:00 local yesterday through 09:55 local today, 26 hours.
    const ticks = hourTicks(run("2026-08-04T06:00:00Z", 312), 3);
    const nine = ticks.filter((t) => t.label === "09:00");

    expect(nine).toHaveLength(2);
    expect(nine[0]!.index).not.toBe(nine[1]!.index);
    expect(nine[0]!.left).toBeLessThan(nine[1]!.left);
  });

  // What the phone renders is this list thinned. Thinned by the CLOCK: every
  // other tick of a list that opens at 03:00 is six hours apart but lands on
  // 03, 09, 15 — no longer the landmarks the wide set uses, and one label
  // dropped at an edge flips them all.
  it("thins to the same landmarks on a narrow screen", () => {
    const ticks = hourTicks(run("2026-08-04T08:10:00Z", 288), 3);

    expect(ticks.map((t) => t.label)).toEqual([
      "12:00",
      "15:00",
      "18:00",
      "21:00",
      "00:00",
      "03:00",
      "06:00",
      "09:00",
    ]);
    expect(ticks.filter((t) => t.hour % 6 === 0).map((t) => t.label)).toEqual([
      "12:00",
      "18:00",
      "00:00",
      "06:00",
    ]);
  });

  it("reads the strip as empty rather than throwing", () => {
    expect(hourTicks([], 3)).toEqual([]);
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

  // Go's RFC3339Nano drops trailing zeros, so a fractional stamp and a whole
  // one differ in length — and as text "." sorts before "Z", putting the later
  // instant first. Five-minute cycles never collide inside a second today, so
  // this pins the rule rather than a live bug.
  it("orders on the instant, not on the text", () => {
    const whole = { ...sample(0), at: "2026-08-04T12:00:00Z" };
    const fraction = { ...sample(0), at: "2026-08-04T12:00:00.5Z" };

    const merged = worstPerCycle([[fraction], [whole]]);

    expect(merged.map((c) => c.at)).toEqual([
      "2026-08-04T12:00:00Z",
      "2026-08-04T12:00:00.5Z",
    ]);
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
