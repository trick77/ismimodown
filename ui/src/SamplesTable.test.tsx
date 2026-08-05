import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SamplesTable, newestFirst } from "./SamplesTable";
import type { Sample } from "./api/types";
import { formatTime } from "./format";

const sample = (over: Partial<Sample> = {}): Sample => ({
  at: "2026-08-04T12:00:00Z",
  model_id: "mimo-v2.5",
  probe: "short",
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
  it("interleaves the probes by time rather than concatenating them", () => {
    const merged = newestFirst([
      [
        sample({ at: "2026-08-04T13:00:00Z" }),
        sample({ at: "2026-08-04T11:55:00Z" }),
      ],
      [sample({ at: "2026-08-04T12:00:00Z", probe: "wide" })],
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
      [sample({ at: "2026-08-04T12:00:00.5Z", probe: "wide" })],
    ]);
    expect(merged[0]!.at).toBe("2026-08-04T12:00:00.5Z");
  });

  // A wide run shares its cycle's timestamp exactly with the short run beside
  // it, so without a tie-break the pair would swap places between renders.
  it("breaks a shared timestamp the same way every time", () => {
    const at = "2026-08-04T12:00:00Z";
    const short = sample({ at });
    const wide = sample({ at, probe: "wide" });
    expect(newestFirst([[short], [wide]]).map((s) => s.probe)).toEqual(
      newestFirst([[wide], [short]]).map((s) => s.probe),
    );
  });

  // Probe alone stopped being a total order when the table started drawing
  // every model: the two models run CONCURRENTLY within a cycle and carry that
  // cycle's instant, so their short runs tie on both timestamp and probe. The
  // pair would swap places between renders on a table a reader scans down.
  it("breaks a tie between models the same way every time", () => {
    const at = "2026-08-04T12:00:00Z";
    const a = sample({ at, model_id: "mimo-v2.5" });
    const b = sample({ at, model_id: "mimo-v2.5-pro" });
    expect(newestFirst([[a], [b]]).map((s) => s.model_id)).toEqual(
      newestFirst([[b], [a]]).map((s) => s.model_id),
    );
  });

  // The full cross product a cycle produces once wide is due: two models, two
  // probes, one instant. All four survive the merge — none is aggregated away.
  it("keeps every run of a cycle that both models and both probes ran", () => {
    const at = "2026-08-04T12:00:00Z";
    const merged = newestFirst([
      [sample({ at, model_id: "mimo-v2.5" })],
      [sample({ at, model_id: "mimo-v2.5", probe: "wide" })],
      [sample({ at, model_id: "mimo-v2.5-pro" })],
      [sample({ at, model_id: "mimo-v2.5-pro", probe: "wide" })],
    ]);
    expect(merged).toHaveLength(4);
    expect(merged.map((s) => `${s.model_id}/${s.probe}`)).toEqual([
      "mimo-v2.5/short",
      "mimo-v2.5/wide",
      "mimo-v2.5-pro/short",
      "mimo-v2.5-pro/wide",
    ]);
  });
});

describe("SamplesTable", () => {
  // The caller hands over a whole day so PulseStrip can draw it. The table is
  // not the place to re-read that day.
  it("renders at most the forty most recent runs", () => {
    render(
      <SamplesTable
        perGroup={[
          Array.from({ length: 288 }, (_, i) => sample({ ttft_ms: i })),
        ]}
      />,
    );
    expect(bodyRows()).toBe(40);
  });

  // The cap is a row count, but what it buys a reader is a stretch of time, and
  // two models produce rows twice as fast as one. This is the arithmetic that
  // decided 40: an hour of two models is ~26 rows, and the old cap of 20 would
  // have shown less than an hour with a single wide run in it — fewer than the
  // table held before it drew every model.
  it("reaches back past an hour of both models, wide runs included", () => {
    const at = (min: number) =>
      new Date(Date.UTC(2026, 7, 4, 12, 0) - min * 60_000).toISOString();
    const groups = ["mimo-v2.5", "mimo-v2.5-pro"].flatMap((model_id) => [
      Array.from({ length: 20 }, (_, i) => sample({ model_id, at: at(i * 5) })),
      Array.from({ length: 2 }, (_, i) =>
        sample({ model_id, probe: "wide", at: at(i * 60), answer_ok: null }),
      ),
    ]);
    render(<SamplesTable perGroup={groups} />);

    const times = [...screen.getByRole("table").querySelectorAll("tbody tr")]
      .map((tr) => tr.querySelector("td")!.textContent)
      .filter((t): t is string => t !== null);
    // 40 rows of two models is 85 minutes here: 20 five-minute ticks at two
    // short runs each, plus the four wide runs sharing two of those ticks. The
    // number to hold on to is that it clears the hour, and with it more than
    // one wide run — at the old cap of 20 this reached 45 minutes.
    expect(times[times.length - 1]).toBe(formatTime(at(85)));
    expect(screen.getAllByText("wide").length).toBeGreaterThanOrEqual(3);
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
    expect(cellsOfFirstRow()[0]).toBe(formatTime("2026-08-04T12:00:00Z"));
  });

  it("shows fewer rows without complaint when fewer cycles exist", () => {
    render(<SamplesTable perGroup={[[sample(), sample()]]} />);
    expect(bodyRows()).toBe(2);
  });

  // The whole point of the two columns: every run of a cycle carries the same
  // timestamp, so these are what tell the rows apart.
  it("names the model and the probe beside the time", () => {
    render(
      <SamplesTable
        perGroup={[[sample()], [sample({ probe: "wide", answer_ok: null })]]}
      />,
    );
    expect(cellsOfFirstRow()[1]).toBe("mimo-v2.5");
    expect(cellsOfFirstRow()[2]).toBe("short");
    expect(screen.getByText("wide")).toBeInTheDocument();
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

  // The column is the wire value, so a kind the table has never heard of is
  // still a row an operator must see.
  it("shows an unrecognised probe verbatim rather than dropping it", () => {
    render(<SamplesTable perGroup={[[sample({ probe: "deep" })]]} />);
    expect(screen.getByText("deep")).toBeInTheDocument();
  });

  // Wide has no single assertable answer, so it is never graded. A dash, not a
  // "wrong": ungraded and incorrect are the distinction the nil verdict exists
  // to keep.
  it("leaves the answer blank on a run that was never graded", () => {
    render(
      <SamplesTable
        perGroup={[[sample({ probe: "wide", answer_ok: null })]]}
      />,
    );
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
  // ~20 tokens on short against ~3800 on wide — and reading it off /api/cost
  // means reconstructing one run from a daily sum.
  it("shows what a run sent as well as what it generated", () => {
    render(
      <SamplesTable
        perGroup={[
          [
            sample({ probe: "wide", answer_ok: null }),
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
