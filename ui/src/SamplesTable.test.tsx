import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SamplesTable, newestFirst } from "./SamplesTable";
import type { Sample } from "./api/types";
import { formatDateTime } from "./format";

// The column reads as a distance from now, so every expectation below is
// relative to a pinned now — the same instant the fixtures are stamped at.
const NOW = new Date("2026-08-04T12:00:00Z");

const sample = (over: Partial<Sample> = {}): Sample => ({
  at: "2026-08-04T12:00:00Z",
  model_id: "mimo-v2.5",
  ttft_ms: 900,
  total_ms: 1700,
  itl_p50_ms: 24,
  output_tps: 41,
  prompt_tokens: 34,
  output_tokens: 59,
  ok: true,
  answer_ok: true,
  error_class: null,
  ...over,
});

const bodyRows = () =>
  screen.getByRole("table").querySelectorAll("tbody tr").length;

const cellsOfFirstRow = () =>
  [...screen.getByRole("table").querySelectorAll("tbody tr td")].map(
    (td) => td.textContent,
  );

describe("newestFirst", () => {
  it("interleaves the groups by time rather than concatenating them", () => {
    const merged = newestFirst([
      [
        sample({ at: "2026-08-04T13:00:00Z" }),
        sample({ at: "2026-08-04T11:55:00Z" }),
      ],
      [sample({ at: "2026-08-04T12:00:00Z" })],
    ]);
    expect(merged.map((s) => s.at)).toEqual([
      "2026-08-04T13:00:00Z",
      "2026-08-04T12:00:00Z",
      "2026-08-04T11:55:00Z",
    ]);
  });

  // Go's RFC3339Nano drops trailing zeros from the fraction, so "." lands
  // before "Z" and a string sort puts the later instant first.
  it("orders on the instant, not on the timestamp as text", () => {
    const merged = newestFirst([
      [sample({ at: "2026-08-04T12:00:00Z" })],
      [sample({ at: "2026-08-04T12:00:00.5Z" })],
    ]);
    expect(merged[0]!.at).toBe("2026-08-04T12:00:00.5Z");
  });

  // Every run in a cycle carries that CYCLE'S instant rather than its own, so
  // the two models' runs tie on the timestamp. Without a second key the pair
  // would swap places between renders on a table a reader scans down.
  it("breaks a tie between models the same way every time", () => {
    const at = "2026-08-04T12:00:00Z";
    const a = sample({ at, model_id: "mimo-v2.5" });
    const b = sample({ at, model_id: "mimo-v2.5-pro" });
    expect(newestFirst([[a], [b]]).map((s) => s.model_id)).toEqual(
      newestFirst([[b], [a]]).map((s) => s.model_id),
    );
  });

  // Every run a cycle produces, one instant. Both survive the merge — neither
  // is aggregated away.
  it("keeps every run of a cycle that both models ran", () => {
    const at = "2026-08-04T12:00:00Z";
    const merged = newestFirst([
      [sample({ at, model_id: "mimo-v2.5" })],
      [sample({ at, model_id: "mimo-v2.5-pro" })],
    ]);
    expect(merged).toHaveLength(2);
    expect(merged.map((s) => s.model_id)).toEqual([
      "mimo-v2.5",
      "mimo-v2.5-pro",
    ]);
  });
});

describe("SamplesTable", () => {
  // shouldAdvanceTime, so React's own scheduling still runs while the clock the
  // ages are measured against is the pinned one.
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // The caller hands over a whole day so PulseStrip can draw it. The table is
  // not the place to re-read that day.
  it("renders at most the twenty most recent runs", () => {
    render(
      <SamplesTable
        perGroup={[
          Array.from({ length: 288 }, (_, i) => sample({ ttft_ms: i })),
        ]}
      />,
    );
    expect(bodyRows()).toBe(20);
  });

  // What the cap costs now that the table draws every model, asserted rather
  // than left implied. The cap counts rows; a reader reads time. Two models at
  // 12 runs an hour each is ~24 rows an hour — so 20 rows is ~50 minutes,
  // against the ~90 the same cap covered while the table held a single model.
  // This test is what tells anyone who moves the number what they are trading.
  it("covers about an hour of both models", () => {
    const at = (min: number) =>
      new Date(Date.UTC(2026, 7, 4, 12, 0) - min * 60_000).toISOString();
    const groups = ["mimo-v2.5", "mimo-v2.5-pro"].map((model_id) =>
      Array.from({ length: 20 }, (_, i) => sample({ model_id, at: at(i * 5) })),
    );
    render(<SamplesTable perGroup={groups} />);

    const times = [...screen.getByRole("table").querySelectorAll("tbody tr")]
      .map((tr) => tr.querySelector("td")!.textContent)
      .filter((t): t is string => t !== null);
    // 20 rows of two models is 45 minutes here: 10 five-minute ticks at two
    // runs each.
    expect(times[times.length - 1]).toBe("45 min ago");
  });

  it("keeps the newest cycles, not the oldest", () => {
    render(
      <SamplesTable
        perGroup={[
          [
            sample({ at: "2026-08-04T12:00:00Z" }),
            ...Array.from({ length: 20 }, () =>
              sample({ at: "2026-08-03T00:00:00Z" }),
            ),
          ],
        ]}
      />,
    );
    expect(cellsOfFirstRow()[0]).toBe("just now");
  });

  // The stamp a distance cannot carry. A reader matching a row against a log
  // line elsewhere needs the instant, so it stays on the cell rather than being
  // traded away for the column's readability.
  it("keeps the exact stamp on the cell it no longer prints", () => {
    render(
      <SamplesTable perGroup={[[sample({ at: "2026-08-04T11:35:00Z" })]]} />,
    );

    const cell = screen.getByText("25 min ago");
    expect(cell).toHaveAttribute("datetime", "2026-08-04T11:35:00Z");
    expect(cell).toHaveAttribute(
      "title",
      formatDateTime("2026-08-04T11:35:00Z"),
    );
  });

  // The rows only change when a cycle completes, five minutes apart. Without a
  // clock of its own the column would sit at the age it had on the last render
  // and quietly lie for the rest of the gap.
  it("ages the rows while the page sits open", () => {
    render(<SamplesTable perGroup={[[sample()]]} />);
    expect(screen.getByText("just now")).toBeInTheDocument();

    // When four minutes pass with no new data
    act(() => {
      vi.advanceTimersByTime(4 * 60_000);
    });

    // Then
    expect(screen.getByText("4 min ago")).toBeInTheDocument();
  });

  it("shows fewer rows without complaint when fewer cycles exist", () => {
    render(<SamplesTable perGroup={[[sample(), sample()]]} />);
    expect(bodyRows()).toBe(2);
  });

  // Every run of a cycle carries the same timestamp, so the model column is
  // what tells the rows apart.
  it("names the model beside the time", () => {
    render(
      <SamplesTable
        perGroup={[[sample()], [sample({ model_id: "mimo-v2.5-pro" })]]}
      />,
    );
    expect(cellsOfFirstRow()[1]).toBe("mimo-v2.5");
    expect(screen.getByText("mimo-v2.5-pro")).toBeInTheDocument();
  });

  // The omission this table was fixed for: it fetched and drew the first model
  // only, so half the fleet's runs — and, once wide alternates between models,
  // half its wide runs — were missing from the page's only raw record.
  it("draws every model's runs, not just the first", () => {
    render(
      <SamplesTable
        perGroup={[
          [sample({ model_id: "mimo-v2.5" })],
          [sample({ model_id: "mimo-v2.5-pro" })],
        ]}
      />,
    );
    expect(bodyRows()).toBe(2);
    expect(screen.getByText("mimo-v2.5-pro")).toBeInTheDocument();
  });

  // A run that failed before an answer existed is never graded. A dash, not a
  // "wrong": ungraded and incorrect are the distinction the nil verdict exists
  // to keep.
  it("leaves the answer blank on a run that was never graded", () => {
    render(<SamplesTable perGroup={[[sample({ answer_ok: null })]]} />);
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("wrong")).not.toBeInTheDocument();
  });

  // The count is what makes the rate beside it readable: tok/s is measured over
  // the decode window, so two runs of very different lengths can post the same
  // throughput. Grouped with the row, not asserted positionally on its own, so
  // the pairing is what breaks if the column is ever moved away from it.
  it("shows how many tokens a run generated, next to the rate", () => {
    render(
      <SamplesTable
        perGroup={[[sample({ output_tokens: 1234, output_tps: 41 })]]}
      />,
    );
    const cells = cellsOfFirstRow();
    expect(cells).toContain("1,234");
    expect(cells[cells.indexOf("1,234") + 1]).toBe("41 tok/s");
  });

  // In before Out, and both before the rate: what went in, what came out, and
  // the rate the last two imply. The input side is what separates the probes —
  // ~20 tokens on short against ~3800 on wide — and reading it off the cost
  // panel means reconstructing one run from a daily sum.
  it("shows what a run sent as well as what it generated", () => {
    render(
      <SamplesTable
        perGroup={[
          [
            sample({ answer_ok: null }),
            sample({ prompt_tokens: 3801, output_tokens: 300 }),
          ],
        ]}
      />,
    );
    const cells = cellsOfFirstRow();
    expect(cells).toContain("3,801");
    expect(cells[cells.indexOf("3,801") + 1]).toBe("300");
  });

  // Same rule as every other measurement: a run that never reached the model
  // sent nothing measurable, which is not zero.
  it("dashes the input count on a run that failed", () => {
    render(
      <SamplesTable
        perGroup={[
          [
            sample({
              ok: false,
              error_class: "timeout",
              prompt_tokens: null,
              output_tokens: null,
            }),
          ],
        ]}
      />,
    );
    const cells = cellsOfFirstRow();
    expect(cells.filter((c) => c === "—")).toHaveLength(2);
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  // A failed run generated nothing measurable. A dash, not a 0: zero tokens is
  // a claim about an answer that was never produced.
  it("dashes the token count on a run that failed", () => {
    render(
      <SamplesTable
        perGroup={[
          [sample({ ok: false, error_class: "timeout", output_tokens: null })],
        ]}
      />,
    );
    expect(cellsOfFirstRow()).toContain("—");
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  // The tick is decoration; assistive tech still has to hear the outcome.
  it("marks a good cycle with a tick that still reads as ok", () => {
    render(<SamplesTable perGroup={[[sample()]]} />);
    expect(screen.getByText("✓")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByText("ok")).toBeInTheDocument();
  });

  it("names the error class instead of a tick when a cycle failed", () => {
    render(
      <SamplesTable
        perGroup={[[sample({ ok: false, error_class: "timeout" })]]}
      />,
    );
    expect(screen.getByText("timeout")).toBeInTheDocument();
    expect(screen.queryByText("✓")).not.toBeInTheDocument();
  });

  it("says so plainly when there is nothing to show", () => {
    render(<SamplesTable perGroup={[[], []]} />);
    expect(screen.getByText(/Not enough data yet/)).toBeInTheDocument();
  });
});
