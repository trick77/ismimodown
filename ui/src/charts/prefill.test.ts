import { describe, expect, it } from "vitest";
import { prefillDelta } from "./prefill";
import type { Point } from "../api/types";

const pt = (t: number, p50: number | null, censored = 0, n = 12): Point => ({
  t,
  n,
  censored,
  p50,
  p95: p50,
});

describe("prefillDelta", () => {
  it("subtracts the short probe from the wide one, bucket by bucket", () => {
    const out = prefillDelta(
      [pt(100, 800), pt(200, 900)],
      [pt(100, 2600), pt(200, 3000)],
    );
    expect(out.map((p) => p.p50)).toEqual([1800, 2100]);
    expect(out.map((p) => p.t)).toEqual([100, 200]);
  });

  // The wide probe runs hourly against the short probe's every-few-minutes, so
  // most short buckets have no wide reading. Those are not points of this
  // series: emitted, they would draw a line that is mostly holes.
  it("drives off the wide side, ignoring short buckets with no wide reading", () => {
    const out = prefillDelta(
      [pt(100, 800), pt(200, 850), pt(300, 900)],
      [pt(200, 3000)],
    );
    expect(out).toHaveLength(1);
    expect(out[0]!.t).toBe(200);
  });

  // The point that must not be dropped: where the wide probe was cut off there
  // is nothing to subtract, and dropping it would take the censoring band with
  // it — leaving blank chart exactly where the reader has to be told the top of
  // the distribution was removed.
  it("keeps a valueless point when a side is missing, so the censoring survives", () => {
    const out = prefillDelta([pt(100, 800)], [pt(100, null, 4)]);
    expect(out).toHaveLength(1);
    expect(out[0]!.p50).toBeNull();
    expect(out[0]!.censored).toBe(4);
  });

  it("keeps a valueless point when the wide bucket has no short partner at all", () => {
    const out = prefillDelta([pt(100, 800)], [pt(999, 3000, 2)]);
    expect(out).toEqual([{ t: 999, n: 0, censored: 2, p50: null, p95: null }]);
  });

  it("sums censoring across both probes", () => {
    const out = prefillDelta([pt(100, 800, 1)], [pt(100, 2600, 3)]);
    expect(out[0]!.censored).toBe(4);
  });

  it("takes the weaker of the two sample counts", () => {
    const out = prefillDelta([pt(100, 800, 0, 20)], [pt(100, 2600, 0, 1)]);
    expect(out[0]!.n).toBe(1);
  });

  // p95(wide) − p95(short) is not the 95th percentile of the difference: the
  // two tails are not the same runs. A number that looks like a percentile and
  // is not one is worse than no number.
  it("never subtracts p95", () => {
    const out = prefillDelta([pt(100, 800)], [pt(100, 2600)]);
    expect(out[0]!.p95).toBeNull();
  });

  // A negative delta is a reading, not an error: it says the short baseline
  // moved rather than prefill. Clamping it would hide the one case the zero
  // line exists to show.
  it("leaves a negative difference alone", () => {
    const out = prefillDelta([pt(100, 3000)], [pt(100, 2600)]);
    expect(out[0]!.p50).toBe(-400);
  });

  it("returns nothing when either probe has no data", () => {
    expect(prefillDelta(undefined, [pt(100, 2600)])).toEqual([]);
    expect(prefillDelta([pt(100, 800)], undefined)).toEqual([]);
    expect(prefillDelta([], [pt(100, 2600)])).toEqual([]);
  });
});
