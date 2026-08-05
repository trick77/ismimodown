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
});

describe("SamplesTable", () => {
  // The caller hands over a whole day so PulseStrip can draw it. The table is
  // not the place to re-read that day.
  it("renders at most the twenty most recent cycles", () => {
    render(
      <SamplesTable
        perProbe={[
          Array.from({ length: 288 }, (_, i) => sample({ ttft_ms: i })),
        ]}
      />,
    );
    expect(bodyRows()).toBe(20);
  });

  it("keeps the newest cycles, not the oldest", () => {
    render(
      <SamplesTable
        perProbe={[
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
    render(<SamplesTable perProbe={[[sample(), sample()]]} />);
    expect(bodyRows()).toBe(2);
  });

  // The whole point of the column: the pair shares a timestamp, so this is what
  // tells the two rows apart.
  it("names the probe beside the time", () => {
    render(
      <SamplesTable
        perProbe={[[sample()], [sample({ probe: "wide", answer_ok: null })]]}
      />,
    );
    expect(cellsOfFirstRow()[1]).toBe("short");
    expect(screen.getByText("wide")).toBeInTheDocument();
  });

  // The column is the wire value, so a kind the table has never heard of is
  // still a row an operator must see.
  it("shows an unrecognised probe verbatim rather than dropping it", () => {
    render(<SamplesTable perProbe={[[sample({ probe: "deep" })]]} />);
    expect(screen.getByText("deep")).toBeInTheDocument();
  });

  // Wide has no single assertable answer, so it is never graded. A dash, not a
  // "wrong": ungraded and incorrect are the distinction the nil verdict exists
  // to keep.
  it("leaves the answer blank on a run that was never graded", () => {
    render(
      <SamplesTable
        perProbe={[[sample({ probe: "wide", answer_ok: null })]]}
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
        perProbe={[[sample({ output_tokens: 1234, output_tps: 41 })]]}
      />,
    );
    const cells = cellsOfFirstRow();
    expect(cells).toContain("1,234");
    expect(cells[cells.indexOf("1,234") + 1]).toBe("41 tok/s");
  });

  // A failed run generated nothing measurable. A dash, not a 0: zero tokens is
  // a claim about an answer that was never produced.
  it("dashes the token count on a run that failed", () => {
    render(
      <SamplesTable
        perProbe={[
          [sample({ ok: false, error_class: "timeout", output_tokens: null })],
        ]}
      />,
    );
    expect(cellsOfFirstRow()).toContain("—");
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  // The tick is decoration; assistive tech still has to hear the outcome.
  it("marks a good cycle with a tick that still reads as ok", () => {
    render(<SamplesTable perProbe={[[sample()]]} />);
    expect(screen.getByText("✓")).toHaveAttribute("aria-hidden", "true");
    expect(screen.getByText("ok")).toBeInTheDocument();
  });

  it("names the error class instead of a tick when a cycle failed", () => {
    render(
      <SamplesTable
        perProbe={[[sample({ ok: false, error_class: "timeout" })]]}
      />,
    );
    expect(screen.getByText("timeout")).toBeInTheDocument();
    expect(screen.queryByText("✓")).not.toBeInTheDocument();
  });

  it("says so plainly when there is nothing to show", () => {
    render(<SamplesTable perProbe={[[], []]} />);
    expect(screen.getByText(/Not enough data yet/)).toBeInTheDocument();
  });
});
