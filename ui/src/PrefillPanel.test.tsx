import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PrefillPanel, noDeltaReason } from "./PrefillPanel";
import type { ModelSeries, Point } from "./api/types";

// jsdom cannot exercise a canvas, so the chart itself is stubbed; what this file
// asserts is the panel's own wiring around it.
vi.mock("./charts/EChart", () => ({
  EChart: () => <div data-testid="chart" />,
}));

const H = 3_600;
const AUG4 = Date.UTC(2026, 7, 4) / 1000;

const pt = (t: number, p50: number | null, censored = 0): Point => ({
  t,
  n: p50 === null ? 0 : 12,
  censored,
  p50,
  p95: p50,
});

// 09:00 UTC to 09:00 the next day.
const day = (base: number): Point[] =>
  Array.from({ length: 25 }, (_, i) => pt(AUG4 + 9 * H + i * H, base));

const series = (models: Record<string, Point[]>): ModelSeries => ({
  window: "24h",
  bucket_s: 900,
  metric: "ttft",
  probe: "short",
  models,
});

describe("PrefillPanel", () => {
  const short = series({ "mimo-v2.5": day(900) });
  const wide = series({ "mimo-v2.5": day(2600) });

  // The delta is the chart; the two probes are the reference strip beneath it.
  it("draws the cost above and the probes it came from below", () => {
    render(<PrefillPanel short={short} wide={wide} models={["mimo-v2.5"]} />);
    expect(screen.getAllByTestId("chart")).toHaveLength(2);
    expect(
      screen.getByText(/For reference, the two probes/),
    ).toBeInTheDocument();
  });

  it("keeps the dashed-line sample beside the convention it explains", () => {
    const { container } = render(
      <PrefillPanel short={short} wide={wide} models={["mimo-v2.5"]} />,
    );
    expect(container.querySelector(".border-dashed")).not.toBeNull();
  });

  // The two axes decide independently, so each plot carries its own badge.
  // Here only the strip's spread forces one: the difference is constant.
  it("announces a log axis on the reference strip when the spread forces one", () => {
    const wideLog = series({ "mimo-v2.5": day(36_000) });
    render(
      <PrefillPanel short={short} wide={wideLog} models={["mimo-v2.5"]} />,
    );
    expect(screen.getByText(/log scale/i)).toBeInTheDocument();
  });

  it("leaves the badge off when the axis stayed linear", () => {
    render(<PrefillPanel short={short} wide={wide} models={["mimo-v2.5"]} />);
    expect(screen.queryByText(/log scale/i)).not.toBeInTheDocument();
  });

  // The wide probe runs hourly, so the panel exists before its subject does.
  it("says the cost is not plottable yet when the wide probe has no data", () => {
    render(<PrefillPanel short={short} wide={null} models={["mimo-v2.5"]} />);
    expect(screen.getByText(/takes an hour/)).toBeInTheDocument();
    // The short probe still has data, so the strip is still drawn — one chart,
    // not two.
    expect(screen.getAllByTestId("chart")).toHaveLength(1);
  });

  // Nothing has been collected at all. Blaming the wide probe's hourly cadence
  // here names the wrong reason and sends the reader away for an hour.
  it("blames collection, not the wide cadence, when neither probe has data", () => {
    render(<PrefillPanel short={null} wide={null} models={["mimo-v2.5"]} />);
    expect(
      screen.getByText(/first samples within a few minutes/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/takes an hour/)).not.toBeInTheDocument();
    expect(screen.queryAllByTestId("chart")).toHaveLength(0);
  });

  // A series of points that are all null is still a full-length series. Gated
  // on its LENGTH, the panel drew an empty grid with axes and no explanation.
  //
  // And with no chart there is no censoring band and no note under it, so the
  // placeholder is the ONLY thing that can say the runs were cut off. Calling
  // it missing data would undo the reason prefillDelta keeps valueless points
  // at all.
  it("names truncation, not absence, when every wide bucket was cut off", () => {
    const dead = series({
      "mimo-v2.5": day(0).map((p) => ({ ...p, n: 0, censored: 2, p50: null })),
    });
    render(<PrefillPanel short={short} wide={dead} models={["mimo-v2.5"]} />);
    expect(screen.getByText(/a truncated stretch/)).toBeInTheDocument();
    expect(screen.queryByText(/second wide probe/)).not.toBeInTheDocument();
    // The strip is still there — it is what says the short probe kept running.
    expect(screen.getAllByTestId("chart")).toHaveLength(1);
  });

  // Two readings an order of magnitude apart set logScale while still being one
  // short of a line, so the chip could sit over the placeholder.
  it("keeps the log badge off the header when there is no chart under it", () => {
    const both = ["mimo-v2.5", "mimo-v2.5-pro"];
    const spread = series({
      "mimo-v2.5": [pt(AUG4 + 9 * H, 2600)],
      "mimo-v2.5-pro": [pt(AUG4 + 9 * H, 40_000)],
    });
    const shortBoth = series({
      "mimo-v2.5": day(900),
      "mimo-v2.5-pro": day(900),
    });
    const { container } = render(
      <PrefillPanel short={shortBoth} wide={spread} models={both} />,
    );
    expect(screen.getByText(/second wide probe/)).toBeInTheDocument();
    // Scoped to the header. The STRIP is drawn here and its own axis does go
    // log on the same data, so its chip is correct and must survive; the
    // header's is the one that would be labelling a placeholder.
    expect(container.querySelector("header")?.textContent).not.toMatch(
      /log scale/i,
    );
  });

  // The cadence-and-zero line describes the delta's plot. With no delta plot it
  // explains a chart that is not on the page; the swatches stay, because colour
  // follows the model in the strip too.
  it("drops the delta's legend line but keeps the swatches when only the strip is drawn", () => {
    render(<PrefillPanel short={short} wide={null} models={["mimo-v2.5"]} />);
    expect(
      screen.queryByText(/One point per wide probe/),
    ).not.toBeInTheDocument();
    expect(screen.getByText("mimo-v2.5")).toBeInTheDocument();
  });

  it("drops the legend entirely when no chart is drawn at all", () => {
    render(<PrefillPanel short={null} wide={null} models={["mimo-v2.5"]} />);
    expect(screen.queryByText("mimo-v2.5")).not.toBeInTheDocument();
  });

  // One reading is not a line: with symbols off it renders as nothing at all.
  it("shows the placeholder until a second wide probe has run", () => {
    const one = series({ "mimo-v2.5": [pt(AUG4 + 9 * H, 2600)] });
    render(<PrefillPanel short={short} wide={one} models={["mimo-v2.5"]} />);
    expect(screen.getByText(/second wide probe/)).toBeInTheDocument();
  });

  // Two models, one reading each, an hour after a deploy. Counted in a single
  // pool that is two readings and clears a threshold of two — while neither
  // model has a second point to draw a segment to, so the chart is empty.
  it("counts readings per model, not pooled across them", () => {
    const both = ["mimo-v2.5", "mimo-v2.5-pro"];
    const oneEach = series({
      "mimo-v2.5": [pt(AUG4 + 9 * H, 2600)],
      "mimo-v2.5-pro": [pt(AUG4 + 9 * H, 3100)],
    });
    const shortBoth = series({
      "mimo-v2.5": day(900),
      "mimo-v2.5-pro": day(1200),
    });
    render(<PrefillPanel short={shortBoth} wide={oneEach} models={both} />);
    expect(screen.getByText(/second wide probe/)).toBeInTheDocument();
  });

  it("draws the chart once one model has two readings", () => {
    const both = ["mimo-v2.5", "mimo-v2.5-pro"];
    const uneven = series({
      "mimo-v2.5": [pt(AUG4 + 9 * H, 2600), pt(AUG4 + 10 * H, 2700)],
      "mimo-v2.5-pro": [pt(AUG4 + 9 * H, 3100)],
    });
    const shortBoth = series({
      "mimo-v2.5": day(900),
      "mimo-v2.5-pro": day(1200),
    });
    render(<PrefillPanel short={shortBoth} wide={uneven} models={both} />);
    expect(screen.queryByText(/second wide probe/)).not.toBeInTheDocument();
    expect(screen.getAllByTestId("chart")).toHaveLength(2);
  });

  // A short probe that has not reported is the BASELINE missing, not the wide
  // probe. Naming the wide one sends the reader to the wrong half.
  it("names the short probe when it is the one missing", () => {
    const wideOnly = series({ "mimo-v2.5": day(2600) });
    render(
      <PrefillPanel short={null} wide={wideOnly} models={["mimo-v2.5"]} />,
    );
    expect(
      screen.getByText(/short probe is what the cost is measured against/),
    ).toBeInTheDocument();
  });

  // The delta is driven off the wide side, so a bucket where only the SHORT
  // probe was cut off never reaches it — and that bucket still shades the strip.
  it("gives the strip its own censoring note", () => {
    // A censored bucket at an instant the wide probe never ran, so it cannot
    // reach the delta and the delta's own band count stays 0. Before the strip
    // had its own note, this shaded the strip with nothing to explain it.
    const censoredShort = series({
      "mimo-v2.5": [...day(900), pt(AUG4 + 9 * H + 450, null, 2)],
    });
    render(
      <PrefillPanel short={censoredShort} wide={wide} models={["mimo-v2.5"]} />,
    );
    expect(screen.getByText(/probes here were cut off/)).toBeInTheDocument();
  });
});

// The empty state has five outcomes and each names a different cause. Tested
// directly, because the wrong cause attached to the right state is exactly the
// failure this function was extracted to stop, and it is invisible from the
// outside — every one of these renders the same grey placeholder.
describe("noDeltaReason", () => {
  const base = {
    hasProbes: true,
    hasWide: true,
    hasShort: true,
    deltaValues: 1,
    wideCensored: 0,
  };

  it("blames collection when nothing has been probed", () => {
    expect(noDeltaReason({ ...base, hasProbes: false })).toMatch(
      /first samples within a few minutes/,
    );
  });

  it("blames the hourly cadence when the wide probe has not run", () => {
    expect(noDeltaReason({ ...base, hasWide: false })).toMatch(/takes an hour/);
  });

  it("blames the short probe when it is the missing half", () => {
    expect(noDeltaReason({ ...base, hasShort: false })).toMatch(
      /short probe is what the cost is measured against/,
    );
  });

  // With no chart there is no band and no note, so this sentence is the only
  // report of truncation there is.
  it("reports truncation when no reading survived and the wide probe was cut off", () => {
    expect(noDeltaReason({ ...base, deltaValues: 0, wideCensored: 3 })).toMatch(
      /truncated stretch/,
    );
  });

  // Censoring on the SHORT probe alone must not produce a claim about the wide
  // one. This is why wideCensored is read off the wide series rather than off
  // the delta, which sums both.
  it("does not report truncation when no wide run was cut off", () => {
    expect(noDeltaReason({ ...base, deltaValues: 0, wideCensored: 0 })).toMatch(
      /has landed in a bucket the short probe also reported/,
    );
  });

  // A reading exists; it is the SECOND that is missing. Reporting truncation
  // here would describe a probe that succeeded.
  it("asks for a second reading when one already landed, however censored", () => {
    expect(noDeltaReason({ ...base, deltaValues: 1, wideCensored: 5 })).toMatch(
      /second wide probe/,
    );
  });
});
