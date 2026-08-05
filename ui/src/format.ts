// Value formatting. Latency here spans decades — a normal ~900 ms reading and a
// 90-second queue storm are both real — so nothing is rendered with a fixed
// unit or a fixed precision.

// formatMs auto-scales its unit: milliseconds below a second, seconds below a
// minute, minutes above.
//
// Below 100 ms one decimal is kept, because inter-token latency lives there and
// 11.9 vs 12 is a difference the chart can actually show.
export function formatMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) {
    return "—";
  }
  if (ms < 0) {
    return "—";
  }
  if (ms < 100) {
    return `${round(ms, 1)} ms`;
  }
  if (ms < 1000) {
    return `${Math.round(ms)} ms`;
  }
  if (ms < 60_000) {
    return `${round(ms / 1000, 2)} s`;
  }
  return `${round(ms / 60_000, 1)} min`;
}

export function formatPct(pct: number | null | undefined, digits = 1): string {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) {
    return "—";
  }
  return `${round(pct, digits)}%`;
}

export function formatTps(tps: number | null | undefined): string {
  if (tps === null || tps === undefined || !Number.isFinite(tps)) {
    return "—";
  }
  return `${round(tps, 1)} tok/s`;
}

export function formatInt(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) {
    return "—";
  }
  return new Intl.NumberFormat("en-GB").format(Math.round(n));
}

// plural picks the noun for a count, for the sentences that interpolate one.
//
// Only for nouns that differ by a suffix. Where the singular case says
// something DIFFERENT — "One run is not yet a pattern" — the sentence is
// written out by hand at the call site instead; see verdict.ts.
export function plural(n: number, one: string, many = `${one}s`): string {
  return Math.abs(n) === 1 ? one : many;
}

function round(v: number, digits: number): string {
  // toFixed then strip trailing zeros, so 900.00 renders as 900 rather than
  // implying a precision the measurement does not have.
  return Number(v.toFixed(digits)).toString();
}

// Europe/Zurich, 24-hour, per the locale decision.
const timeFmt = new Intl.DateTimeFormat("en-GB", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
  timeZone: "Europe/Zurich",
});
const dateTimeFmt = new Intl.DateTimeFormat("en-GB", {
  day: "2-digit",
  month: "short",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
  timeZone: "Europe/Zurich",
});

// Date without a time, for an axis whose ticks are days apart.
//
// The 3mo axis stamped with a full date AND time overlapped its own labels into
// an unreadable smear; at a 6-hour bucket the hour on a weekly tick is noise
// anyway, and the tooltip still carries the exact time on hover.
const dateFmt = new Intl.DateTimeFormat("en-GB", {
  day: "2-digit",
  month: "short",
  timeZone: "Europe/Zurich",
});

export function formatDate(iso: string | number | Date): string {
  const d = toDate(iso);
  return d ? dateFmt.format(d) : "—";
}

export function formatTime(iso: string | number | Date): string {
  const d = toDate(iso);
  return d ? timeFmt.format(d) : "—";
}

export function formatDateTime(iso: string | number | Date): string {
  const d = toDate(iso);
  return d ? dateTimeFmt.format(d) : "—";
}

function toDate(v: string | number | Date): Date | null {
  const d =
    v instanceof Date ? v : new Date(typeof v === "number" ? v * 1000 : v);
  return Number.isNaN(d.getTime()) ? null : d;
}

// dynamicRange is the ratio between the largest and smallest positive value in
// a series. It decides whether a chart switches to a log axis.
export function dynamicRange(values: (number | null)[]): number {
  const finite = values.filter(
    (v): v is number => v !== null && Number.isFinite(v) && v > 0,
  );
  if (finite.length < 2) {
    return 1;
  }
  const min = Math.min(...finite);
  const max = Math.max(...finite);
  return min > 0 ? max / min : 1;
}

// LOG_SCALE_THRESHOLD is where a linear axis stops being readable.
//
// A linear axis collapses one of the two real cases: at 20x, a normal reading
// becomes a flat line against a spike. The chart says LOG SCALE on the plot
// whenever it switches, because a log axis read as linear is worse than no
// chart at all.
export const LOG_SCALE_THRESHOLD = 20;

export function shouldUseLogScale(values: (number | null)[]): boolean {
  return dynamicRange(values) > LOG_SCALE_THRESHOLD;
}
