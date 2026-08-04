// MiMo's off-peak billing window.
//
// The platform applies a 0.8x consumption coefficient — 20% fewer credits —
// between 00:00 and 08:00 Beijing time. Beijing is UTC+8 year-round and observes
// no DST, so the window is exactly 16:00–24:00 UTC every day, which is the form
// everything here is derived from.
//
// It is deliberately NOT stored as a local-clock constant. The server reads
// Europe/Zurich, which does observe DST: the same window lands at 18:00–02:00 in
// summer and 17:00–01:00 in winter, and a 48-hour chart can straddle the
// changeover and contain both. Anything that needs the local times asks
// formatTime for them, per band edge.
//
// This is a BILLING window and nothing else. MiMo publishes no load or demand
// figures, so nothing here should be read as — or grow into — a claim about when
// the platform is busy.
export const OFFPEAK_START_UTC_HOUR = 16;
export const OFFPEAK_END_UTC_HOUR = 24;
export const OFFPEAK_COEFFICIENT = 0.8;

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;

// isOffPeak answers, for one instant, whether it billed at the reduced rate.
//
// Takes epoch MILLISECONDS. The sample feed carries RFC3339 strings and the
// series feed carries epoch seconds, so callers convert; a helper that guessed
// between the two would silently mis-band one of them.
export function isOffPeak(ms: number): boolean {
  if (!Number.isFinite(ms)) return false;
  // Both edges read off the constants, even though the upper test cannot fail
  // while the window closes at 24:00: it is what keeps the pulse strip's tint
  // and the chart's band from drifting apart if the window ever narrows.
  const hour = new Date(ms).getUTCHours();
  return hour >= OFFPEAK_START_UTC_HOUR && hour < OFFPEAK_END_UTC_HOUR;
}

// offPeakBands returns the [start, end) spans, in epoch ms, that overlap the
// given range — clipped to it, so a band that runs past either end stops at the
// edge of the plot rather than extending beyond the data it describes.
//
// Walks whole UTC days rather than stepping by hours: the window is defined
// against the UTC clock, and a day boundary is the only place a new band can
// start.
export function offPeakBands(fromMs: number, toMs: number): [number, number][] {
  if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || toMs <= fromMs) {
    return [];
  }

  const bands: [number, number][] = [];
  // Start a day early. The window closes exactly at UTC midnight, so today's
  // band cannot reach back over the range's left edge and this iteration
  // contributes nothing as the constants stand; it is here so that a window
  // moved past 24:00 would still be found rather than silently half-drawn.
  let day = Math.floor(fromMs / DAY_MS) * DAY_MS - DAY_MS;

  for (; day < toMs; day += DAY_MS) {
    const start = day + OFFPEAK_START_UTC_HOUR * HOUR_MS;
    const end = day + OFFPEAK_END_UTC_HOUR * HOUR_MS;
    const clipped: [number, number] = [
      Math.max(start, fromMs),
      Math.min(end, toMs),
    ];
    if (clipped[1] > clipped[0]) {
      bands.push(clipped);
    }
  }
  return bands;
}

// offPeakWindowFor returns the WHOLE band the given instant belongs to, in
// epoch ms, unclipped.
//
// offPeakBands clips its spans to the plot, which is right for drawing and wrong
// for naming: a chart whose left edge falls inside the window gets a first span
// starting at the edge, and quoting that back would report "22:00–02:00 here"
// for a window that opens at 18:00. That case is not rare — on the 24h chart it
// is exactly the eight hours the rate is live, which is when a reader is most
// likely to be reading the note.
//
// Derived from the span's own UTC day, so the local hours still come out of the
// band that was painted and the DST changeover needs no second rule.
export function offPeakWindowFor(ms: number): [number, number] {
  const day = Math.floor(ms / DAY_MS) * DAY_MS;
  return [
    day + OFFPEAK_START_UTC_HOUR * HOUR_MS,
    day + OFFPEAK_END_UTC_HOUR * HOUR_MS,
  ];
}

// currentOffPeak describes where `nowMs` sits relative to the window, for the
// badge on the chart header.
//
// It reports the boundary as an INSTANT, never as a countdown. The dashboard
// re-renders on the 5-minute probe cycle, so "in 3h 10m" would be wrong for most
// of the time it was on screen; "until 02:00" is true until it is not.
export function currentOffPeak(nowMs: number): {
  active: boolean;
  boundaryMs: number;
} {
  const active = isOffPeak(nowMs);
  const day = Math.floor(nowMs / DAY_MS) * DAY_MS;
  return {
    active,
    // Ends at the next UTC midnight; otherwise opens at 16:00 UTC the same day.
    boundaryMs: active ? day + DAY_MS : day + OFFPEAK_START_UTC_HOUR * HOUR_MS,
  };
}
