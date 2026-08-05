import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { CostBreakdown } from "./api/types";
import { CostPanel, perRun, savedUSD } from "./CostPanel";

// 18:00 UTC on a summer day: inside the 16:00–24:00 window, so the chip reads a
// closing time and the band is drawn.
const NOW = Date.parse("2026-08-04T18:00:00Z") / 1000;

const cost = (over: Partial<CostBreakdown> = {}): CostBreakdown => ({
  window: "24h",
  priced: true,
  currency: "USD",
  offpeak_coefficient: 0.8,
  total: {
    runs: 624,
    tokens: { prompt: 213000, cached: 0, output: 55000 },
    usd: 0.1814,
    list_usd: 0.1944,
  },
  phases: [
    {
      phase: "full",
      runs: 416,
      tokens: { prompt: 142000, cached: 0, output: 36700 },
      usd: 0.1296,
      list_usd: 0.1296,
    },
    {
      phase: "offpeak",
      runs: 208,
      tokens: { prompt: 71000, cached: 0, output: 18300 },
      usd: 0.0518,
      list_usd: 0.0648,
    },
  ],
  probes: [
    {
      probe: "infer",
      runs: 576,
      tokens: { prompt: 40320, cached: 0, output: 40320 },
      usd: 0.0908,
      list_usd: 0.0973,
    },
    {
      probe: "wide",
      runs: 48,
      tokens: { prompt: 172800, cached: 0, output: 14400 },
      usd: 0.0906,
      list_usd: 0.0971,
    },
  ],
  series: [
    { t: NOW - 7200, usd: 0.0081, runs: 26 },
    { t: NOW - 3600, usd: 0.0065, runs: 26 },
  ],
  bucket_s: 3600,
  unpriced_runs: 0,
  offpeak_spans: [[NOW - 7200, NOW]],
  offpeak_until: NOW + 6 * 3600,
  offpeak_active: true,
  generated_at: "2026-08-04T18:00:00Z",
  ...over,
});

describe("CostPanel", () => {
  it("shows the window total and a figure per probe kind", () => {
    render(<CostPanel cost={cost()} />);

    expect(screen.getByText("$0.18")).toBeInTheDocument();
    // Per probe, not one mean over both: a wide run costs many short ones, and
    // the average of the two describes no run that is ever sent.
    expect(screen.getByText("Per short run")).toBeInTheDocument();
    expect(screen.getByText("Per wide run")).toBeInTheDocument();
    // 0.0908 over 576 runs. Two decimals would print $0.00 for it.
    expect(screen.getByText("$0.000158")).toBeInTheDocument();
    // 0.0906 over 48.
    expect(screen.getByText("$0.00189")).toBeInTheDocument();
  });

  it("states the saving as list minus billed", () => {
    render(<CostPanel cost={cost()} />);

    // 0.1944 - 0.1814
    expect(screen.getByText("$0.0130")).toBeInTheDocument();
    expect(screen.getByText("208 of 624 runs at 0.8×")).toBeInTheDocument();
  });

  it("names the band in words wherever it is drawn", () => {
    render(<CostPanel cost={cost()} />);

    expect(screen.getByText(/00:00–08:00 in Beijing/)).toBeInTheDocument();
    expect(screen.getByText(/20% fewer credits/)).toBeInTheDocument();
  });

  // A shaded rectangle with no caption is not a signal. Past 48h the band is
  // dropped — ninety nightly stripes read as a hatch — and the caption must go
  // with it rather than describe shading that is not there.
  it("drops the caption when the band is not drawn", () => {
    const long = cost({
      window: "3mo",
      series: [
        { t: NOW - 80 * 86400, usd: 0.18, runs: 600 },
        { t: NOW, usd: 0.18, runs: 600 },
      ],
    });
    render(<CostPanel cost={long} />);

    expect(screen.queryByText(/in Beijing/)).not.toBeInTheDocument();
  });

  it("reports the runs it could not price", () => {
    render(<CostPanel cost={cost({ unpriced_runs: 14 })} />);

    const note = screen.getByTestId("unpriced-note");
    expect(note).toHaveTextContent("14");
    expect(note).toHaveTextContent(/cut off before reporting usage/);
  });

  it("says nothing about unpriced runs when there are none", () => {
    render(<CostPanel cost={cost()} />);

    expect(screen.queryByTestId("unpriced-note")).not.toBeInTheDocument();
  });

  // The chip names an INSTANT. The page re-renders on the 5-minute cycle, so a
  // countdown would be wrong for most of the time it was on screen.
  it("names when the current rate ends", () => {
    render(<CostPanel cost={cost()} />);

    // 00:00 UTC is 02:00 in Zurich in August; the suite runs in that zone.
    expect(screen.getByTestId("offpeak-chip")).toHaveTextContent(
      "0.8× until 02:00",
    );
  });

  describe("when there is nothing honest to show", () => {
    it("renders nothing without a price table", () => {
      const { container } = render(
        <CostPanel
          cost={cost({
            priced: false,
            total: {
              runs: 624,
              tokens: { prompt: 213000, cached: 0, output: 55000 },
              usd: null,
              list_usd: null,
            },
          })}
        />,
      );

      expect(container).toBeEmptyDOMElement();
    });

    it("renders nothing on a handful of runs", () => {
      const { container } = render(
        <CostPanel
          cost={cost({
            total: {
              runs: 3,
              tokens: { prompt: 1000, cached: 0, output: 200 },
              usd: 0.001,
              list_usd: 0.001,
            },
          })}
        />,
      );

      expect(container).toBeEmptyDOMElement();
    });

    it("renders nothing before the first response", () => {
      const { container } = render(<CostPanel cost={null} />);

      expect(container).toBeEmptyDOMElement();
    });
  });
});

describe("perRun", () => {
  const group = (over = {}) => ({
    runs: 10,
    tokens: { prompt: 0, cached: 0, output: 0 },
    usd: 1,
    list_usd: 1,
    ...over,
  });

  it("divides the group's cost by its runs", () => {
    expect(perRun(group())).toBe(0.1);
  });

  // A division that cannot be done is not a figure of nothing: zero here would
  // publish "$0.00 per run" for a window whose cost is unknown.
  it("is null when the group could not be priced", () => {
    expect(perRun(group({ usd: null }))).toBeNull();
  });

  it("is null rather than infinite on no runs", () => {
    expect(perRun(group({ runs: 0 }))).toBeNull();
  });
});

describe("savedUSD", () => {
  it("is list minus billed", () => {
    expect(
      savedUSD({
        runs: 1,
        tokens: { prompt: 0, cached: 0, output: 0 },
        usd: 0.8,
        list_usd: 1,
      }),
    ).toBeCloseTo(0.2, 10);
  });

  it("is null when either side is missing", () => {
    expect(
      savedUSD({
        runs: 1,
        tokens: { prompt: 0, cached: 0, output: 0 },
        usd: null,
        list_usd: 1,
      }),
    ).toBeNull();
  });
});
