import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PrefillPanel, describeChange, hintFor } from "./PrefillPanel";
import type {
  DashboardPrefill,
  ModelSeries,
  Point,
  PrefillCost,
} from "./api/types";

// jsdom cannot exercise a canvas, so the chart itself is stubbed; what this file
// asserts is the panel's own wiring around it.
vi.mock("./charts/EChart", () => ({
  EChart: () => <div data-testid="chart" />,
}));

const H = 3_600;
const AUG4 = Date.UTC(2026, 7, 4) / 1000;

const pt = (t: number, p50: number | null): Point => ({
  t,
  n: p50 === null ? 0 : 12,
  censored: 0,
  p50,
  p95: p50,
});

const day = (base: number): Point[] =>
  Array.from({ length: 25 }, (_, i) => pt(AUG4 + 9 * H + i * H, base));

const series = (models: Record<string, Point[]>): ModelSeries => ({
  window: "7d",
  bucket_s: 7200,
  metric: "ttft",
  probe: "short",
  models,
});

const cost = (over: Partial<PrefillCost> = {}): PrefillCost => ({
  model_id: "mimo-v2.5",
  pairs: 191,
  sufficient: true,
  p50_ms: 245,
  lo_ms: 160,
  hi_ms: 330,
  wide_p50_ms: 2600,
  censored: 0,
  ...over,
});

const prefill = (
  current: PrefillCost[],
  previous: PrefillCost[] = [],
): DashboardPrefill => ({ window: "7d", min_pairs: 150, current, previous });

const short = series({ "mimo-v2.5": day(900) });
const wide = series({ "mimo-v2.5": day(2600) });

describe("PrefillPanel", () => {
  it("leads with the cost as a figure, not a chart", () => {
    render(
      <PrefillPanel
        prefill={prefill([cost()])}
        short={short}
        wide={wide}
        models={["mimo-v2.5"]}
      />,
    );
    expect(screen.getByText("+245 ms")).toBeInTheDocument();
    // One chart only: the reference strip. The delta line is gone.
    expect(screen.getAllByTestId("chart")).toHaveLength(1);
  });

  // The share is the sentence the panel exists for. A cost with no total to sit
  // inside is a number nobody can act on.
  it("states the cost as a share of the wait it sits inside", () => {
    render(
      <PrefillPanel
        prefill={prefill([cost()])}
        short={short}
        wide={wide}
        models={["mimo-v2.5"]}
      />,
    );
    expect(screen.getByText(/9% of a 2.6 s wait/)).toBeInTheDocument();
  });

  it("suppresses the figure and says why when there are too few pairs", () => {
    render(
      <PrefillPanel
        prefill={prefill([
          cost({
            sufficient: false,
            pairs: 24,
            p50_ms: null,
            lo_ms: null,
            hi_ms: null,
            wide_p50_ms: null,
          }),
        ])}
        short={short}
        wide={wide}
        models={["mimo-v2.5"]}
      />,
    );
    expect(screen.getByTestId("insufficient")).toBeInTheDocument();
    // The reason is a fact about the cadence the reader can act on — a bare
    // blank teaches nothing.
    expect(screen.getByText(/needs 150 paired runs/)).toBeInTheDocument();
  });

  // Excluded pairs are the SLOW ones, so the figure reads better than the
  // window was. Same rule as every other percentile on the page.
  it("publishes the censored pair count beside the figures", () => {
    render(
      <PrefillPanel
        prefill={prefill([cost({ censored: 3 })])}
        short={short}
        wide={wide}
        models={["mimo-v2.5"]}
      />,
    );
    expect(screen.getByText(/3 pairs were cut off/)).toBeInTheDocument();
  });

  it("keeps the reference strip and its dashed-line sample", () => {
    const { container } = render(
      <PrefillPanel
        prefill={prefill([cost()])}
        short={short}
        wide={wide}
        models={["mimo-v2.5"]}
      />,
    );
    expect(container.querySelector(".border-dashed")).not.toBeNull();
  });

  it("renders without a prefill block at all", () => {
    render(
      <PrefillPanel
        prefill={null}
        short={short}
        wide={wide}
        models={["mimo-v2.5"]}
      />,
    );
    // Twice: the figure's label and the strip's colour swatch. The strip is
    // drawn from the series props alone, so it survives a missing prefill
    // block — which is what a client on a cached older payload would see.
    expect(screen.getAllByText("mimo-v2.5")).toHaveLength(2);
    expect(screen.getAllByTestId("chart")).toHaveLength(1);
  });
});

// The gate that keeps this panel from repeating the mistake the chart made:
// claiming a resolution the data does not have.
describe("describeChange", () => {
  it("says unchanged when the intervals overlap", () => {
    const now = cost({ p50_ms: 245, lo_ms: 160, hi_ms: 330 });
    const prev = cost({ p50_ms: 300, lo_ms: 210, hi_ms: 390 });
    expect(describeChange(now, prev, "7d")).toBe(
      "unchanged from the previous 7d",
    );
  });

  // 55 ms of drift between periods is what this endpoint does with nothing
  // having happened. Reporting it as movement is the whole failure mode.
  it("calls a move inside the noise unchanged, however far the estimates drifted", () => {
    const now = cost({ p50_ms: 245, lo_ms: 160, hi_ms: 330 });
    const prev = cost({ p50_ms: 190, lo_ms: 110, hi_ms: 270 });
    expect(describeChange(now, prev, "7d")).toMatch(/unchanged/);
  });

  it("reports a rise once the intervals miss each other", () => {
    const now = cost({ p50_ms: 900, lo_ms: 820, hi_ms: 980 });
    const prev = cost({ p50_ms: 245, lo_ms: 160, hi_ms: 330 });
    expect(describeChange(now, prev, "7d")).toBe(
      "up 655 ms from the previous 7d",
    );
  });

  it("reports a fall the same way", () => {
    const now = cost({ p50_ms: 245, lo_ms: 160, hi_ms: 330 });
    const prev = cost({ p50_ms: 900, lo_ms: 820, hi_ms: 980 });
    expect(describeChange(now, prev, "7d")).toBe(
      "down 655 ms from the previous 7d",
    );
  });

  // A suppressed previous period is not a zero to compare against.
  it("claims nothing when the previous period cannot support a figure", () => {
    const now = cost();
    expect(describeChange(now, undefined, "7d")).toMatch(
      /no comparable previous/,
    );
    expect(
      describeChange(
        now,
        cost({ sufficient: false, p50_ms: null, lo_ms: null, hi_ms: null }),
        "7d",
      ),
    ).toMatch(/no comparable previous/);
  });
});

describe("hintFor", () => {
  it("carries the interval, the share, the count and the comparison", () => {
    const out = hintFor(
      cost(),
      cost({ p50_ms: 250, lo_ms: 170, hi_ms: 340 }),
      "7d",
      150,
    );
    expect(out).toContain("160 ms to 330 ms");
    expect(out).toContain("9% of a 2.6 s wait");
    expect(out).toContain("n=191");
    expect(out).toContain("unchanged");
  });

  it("says nothing at all without a figure to describe", () => {
    expect(hintFor(undefined, undefined, "7d", 150)).toBe("");
  });
});
