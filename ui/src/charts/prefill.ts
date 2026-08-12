// The prefill delta: what a ~3800-token prompt adds to TTFT over a ~34-token
// one, per bucket.
//
// This is the quantity the prefill panel exists to show, and until now it was
// the one thing the panel never drew — it was the whitespace between two lines,
// measured by eye, and flattened outright whenever the axis went log.
//
// Subtracting here, on the client, does NOT breach "probe is a filter, never an
// aggregation" (AGENTS.md). That rule is about the query layer: no percentile
// may ever be computed over a mixture of short and wide samples. These two
// series were queried separately, bucketed separately and percentiled
// separately; what happens below is arithmetic on two finished figures, and
// either one is still available on its own.
import type { Point } from "../api/types";

// prefillDelta pairs the two probes bucket by bucket and returns wide − short.
//
// Driven by the WIDE side. The wide probe runs hourly per model while the 24h
// window buckets at 15 minutes, so roughly three buckets in four have a short
// reading and no wide one; those are not points of this series at all, and
// emitting them would draw a line that is three-quarters holes.
export function prefillDelta(
  short: Point[] | undefined,
  wide: Point[] | undefined,
): Point[] {
  if (!short?.length || !wide?.length) {
    return [];
  }
  const byBucket = new Map(short.map((p) => [p.t, p]));

  return wide.map((w) => {
    const s = byBucket.get(w.t);
    // A point is emitted even when there is nothing to subtract. The censored
    // count below is why: where the wide probe was cut off entirely its bucket
    // carries a null p50 and a censored count, and dropping the point would
    // take the censoring band down with it — leaving an empty stretch of chart
    // exactly where the reader most needs to be told the top of the
    // distribution was removed. Same reasoning as the UNION that builds the
    // bucket universe server-side (samples.Store.Series).
    const value =
      s !== undefined && s.p50 !== null && w.p50 !== null
        ? w.p50 - s.p50
        : null;
    return {
      t: w.t,
      // The weaker of the two sides. A difference is no better supported than
      // the thinner of the readings it came from.
      n: Math.min(s?.n ?? 0, w.n),
      // Both sides. Either probe being truncated truncates the difference.
      censored: (s?.censored ?? 0) + w.censored,
      p50: value,
      // Never a subtraction. p95(wide) − p95(short) is not the 95th percentile
      // of the difference — the two tails are not the same runs — and a number
      // that looks like a percentile but is not one is worse than no number.
      // Nothing on the page plots p95 today; this stays null so that nothing
      // can start.
      p95: null,
    };
  });
}
