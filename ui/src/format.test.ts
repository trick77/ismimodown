import { describe, expect, it } from "vitest";
import {
  dynamicRange,
  formatInt,
  formatMs,
  formatPct,
  formatTps,
  formatTime,
  LOG_SCALE_THRESHOLD,
  plural,
  shouldUseLogScale,
  formatUSD,
  formatUSDPrecise,
  probeName,
} from "./format";

describe("plural", () => {
  it("uses the singular only at exactly one", () => {
    expect(plural(1, "cycle")).toBe("cycle");
    expect(plural(0, "cycle")).toBe("cycles");
    expect(plural(2, "cycle")).toBe("cycles");
  });

  it("takes an irregular plural when the suffix will not do", () => {
    expect(plural(2, "query", "queries")).toBe("queries");
    expect(plural(1, "query", "queries")).toBe("query");
  });
});

describe("formatMs", () => {
  // Values auto-scale their unit, because a normal ~900ms reading and a
  // 90-second queue storm are both real and both must stay legible.
  it("scales the unit with the magnitude", () => {
    expect(formatMs(11.94)).toBe("11.9 ms");
    expect(formatMs(900)).toBe("900 ms");
    expect(formatMs(1450)).toBe("1.45 s");
    expect(formatMs(240000)).toBe("4 min");
  });

  // Below 100ms one decimal is kept, because inter-token latency lives there
  // and 11.9 vs 12 is a difference the chart can actually show.
  it("keeps a decimal below 100ms and drops it above", () => {
    expect(formatMs(8.24)).toBe("8.2 ms");
    expect(formatMs(166.3)).toBe("166 ms");
  });

  // A missing measurement must never render as 0, which would draw a floor
  // that does not exist.
  it("renders missing values as an em dash, never zero", () => {
    expect(formatMs(null)).toBe("—");
    expect(formatMs(undefined)).toBe("—");
    expect(formatMs(NaN)).toBe("—");
  });

  it("renders an actual zero as zero", () => {
    expect(formatMs(0)).toBe("0 ms");
  });
});

describe("formatPct / formatTps / formatInt", () => {
  it("formats and handles nulls", () => {
    expect(formatPct(99.87)).toBe("99.9%");
    expect(formatPct(100)).toBe("100%");
    expect(formatPct(null)).toBe("—");
    expect(formatTps(70.42)).toBe("70.4 tok/s");
    expect(formatTps(null)).toBe("—");
    expect(formatInt(1234)).toBe("1,234");
    expect(formatInt(null)).toBe("—");
  });
});

describe("formatTime", () => {
  // Europe/Zurich, 24-hour, per the locale decision.
  it("renders in Zurich time on a 24-hour clock", () => {
    // 2026-08-04T12:00:00Z is 14:00 in Zurich (CEST).
    expect(formatTime("2026-08-04T12:00:00Z")).toBe("14:00");
  });

  it("handles an unparseable timestamp", () => {
    expect(formatTime("not a date")).toBe("—");
  });
});

describe("log scale", () => {
  // A linear axis collapses either the normal reading or the spike; a log axis
  // read as linear is worse than no chart, so the switch has a hard threshold
  // and the caller announces it.
  it("switches above the threshold and not below", () => {
    expect(shouldUseLogScale([100, 200, 400])).toBe(false);
    expect(shouldUseLogScale([100, 100 * (LOG_SCALE_THRESHOLD + 1)])).toBe(
      true,
    );
  });

  it("ignores nulls and non-positive values", () => {
    // Nulls are gaps, not zeros, and a zero would make the ratio infinite and
    // force a log axis onto a perfectly linear series.
    expect(dynamicRange([100, null, 200])).toBe(2);
    expect(shouldUseLogScale([0, 100])).toBe(false);
  });

  it("treats a single point as no range", () => {
    expect(dynamicRange([500])).toBe(1);
    expect(dynamicRange([])).toBe(1);
  });
});

describe("money", () => {
  // A window total is read in cents.
  it("formats a total to two decimals", () => {
    expect(formatUSD(0.1814)).toBe("$0.18");
    expect(formatUSD(5.4)).toBe("$5.40");
  });

  // A per-inference figure is a fraction of a cent. Two decimals would print
  // $0.00 for a number that is not zero, which is the one thing this must not
  // say.
  it("keeps three significant figures on small amounts", () => {
    expect(formatUSDPrecise(0.000158)).toBe("$0.000158");
    expect(formatUSDPrecise(0.00189)).toBe("$0.00189");
    expect(formatUSDPrecise(0.013)).toBe("$0.0130");
    expect(formatUSDPrecise(5.4)).toBe("$5.40");
  });

  it("stops at six decimals rather than printing float noise", () => {
    expect(formatUSDPrecise(0.00000012345)).toBe("$0.000000");
  });

  it("renders a true zero plainly", () => {
    expect(formatUSDPrecise(0)).toBe("$0.00");
  });

  // Null is "not priced", which is not the same as free.
  it("dashes what it cannot price", () => {
    expect(formatUSD(null)).toBe("—");
    expect(formatUSDPrecise(undefined)).toBe("—");
    expect(formatUSD(Number.NaN)).toBe("—");
  });
});

describe("probeName", () => {
  // Both probes run an inference; the prompt size is what differs. The schema's
  // word is not the reader's, and one card saying "short" while another says
  // "infer" is worse than either on its own.
  it("calls the short probe short, whatever the wire calls it", () => {
    expect(probeName("infer")).toBe("short");
    expect(probeName("wide")).toBe("wide");
  });

  // A kind the page has no name for is still something an operator must see.
  it("passes an unrecognised kind through rather than blanking it", () => {
    expect(probeName("deep")).toBe("deep");
  });
});
