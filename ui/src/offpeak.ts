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
  return new Date(ms).getUTCHours() >= OFFPEAK_START_UTC_HOUR;
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
  // Start a day early: the band that covers `fromMs` may have opened on the
  // previous UTC day, since it runs past midnight.
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
