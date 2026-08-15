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

// formatAxisMs is formatMs's two-unit sibling, for a y-axis tick.
//
// Deliberately NOT formatMs: that one rolls over to minutes above 60 s, and a
// log axis whose gridlines read "300 ms", "3 s", "1.7 min" asks the reader to
// convert between three units to see that the steps are even. An axis is read
// as a ladder, so it gets one break and no more — milliseconds below a second,
// seconds above, however large.
export function formatAxisMs(ms: number): string {
  if (!Number.isFinite(ms)) {
    return "";
  }
  // Zero is the baseline of a linear axis, not a duration. "0 ms" sitting
  // under "1 s" reads as though the axis means something different down there.
  if (ms === 0) {
    return "0";
  }
  // A negative tick USED to return "", on the reasoning that a duration below
  // zero is not a value this can label. That held while every series here was a
  // latency or a rate. The prefill delta is a DIFFERENCE and reaches below zero
  // — a delta under zero is the reading that says the short baseline moved
  // rather than prefill, which the panel's copy sends the reader to look for —
  // so the blank string printed a column of empty gridlines in exactly the
  // region being pointed at. The sign is carried; the magnitude is formatted
  // exactly as it is above zero.
  //
  // U+2212, not a hyphen: this sits in the same axis ladder as the em dashes
  // and middots elsewhere on the page, and a hyphen reads as a list bullet at
  // 10px.
  const sign = ms < 0 ? "−" : "";
  const abs = Math.abs(ms);
  // A decimal below 100 ms, as formatMs keeps one: a linear axis over a
  // few-millisecond handshake gets ticks half a millisecond apart, and rounding
  // those to whole milliseconds prints "1 ms" twice under two different lines.
  return abs < 1000
    ? `${sign}${round(abs, abs < 100 ? 1 : 0)} ms`
    : `${sign}${round(abs / 1000, 2)} s`;
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

// The reader's own zone, 24-hour, per the locale decision.
//
// No timeZone option, deliberately: every instant the API serves is an instant —
// RFC3339Nano in UTC, or unix seconds — so Intl resolves each of these against
// the browser's zone and a reader in Tokyo sees Tokyo hours. These formatters
// were pinned to Europe/Zurich, the probe host's zone, which put Swiss
// wall-clock times in front of every reader on earth without a word saying so.
//
// The LOCALE stays fixed at en-GB while the zone follows the reader. The two
// decide different things: the locale sets the shape — 24-hour, "04 Aug" — and
// that shape is what the page's columns, axes and copy are written around. Only
// the zone is a fact about the reader.
const timeFmt = new Intl.DateTimeFormat("en-GB", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});
const dateTimeFmt = new Intl.DateTimeFormat("en-GB", {
  day: "2-digit",
  month: "short",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

// Date without a time, for an axis whose ticks are days apart.
//
// The 3mo axis stamped with a full date AND time overlapped its own labels into
// an unreadable smear; at a 6-hour bucket the hour on a weekly tick is noise
// anyway, and the tooltip still carries the exact time on hover.
const dateFmt = new Intl.DateTimeFormat("en-GB", {
  day: "2-digit",
  month: "short",
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

// formatAgo is a stamp as a distance rather than a clock reading: "3 min ago".
//
// The raw-cycles table reads down a column of runs a few minutes apart, and
// what a reader takes from that column is how fresh the top of it is — a
// question "21:35" only answers after they have looked at their own clock and
// done the subtraction. The exact instant is not lost: the cell keeps it as its
// title, which is also what a reader needs when they are matching a row against
// something outside this page.
//
// NOT verdict.ts's agoWords, and the difference is deliberate. That one speaks
// in a banner's register — "just now" for anything under five minutes — which
// is exactly the resolution this table cannot use: cycles are five minutes
// apart, so it would print "just now" against the newest several rows and say
// nothing about the order a reader is scanning. This counts every minute, and
// abbreviates its units, because it lives in a narrow column beside numbers.
//
// `now` is passed in rather than read here so the caller owns the clock: the
// table re-reads it on a timer, and a component that cannot be handed an
// instant cannot be tested against one either.
export function formatAgo(
  iso: string | number | Date,
  now: number = Date.now(),
): string {
  const d = toDate(iso);
  if (!d) return "—";
  // Clamped at zero. A run stamped a second into the future is a client clock
  // a second behind the daemon's, not a measurement from the future, and
  // "in 1 min ago" is not a thing to print at anyone.
  const ms = Math.max(0, now - d.getTime());
  // Floored at every step, never rounded: a run 90 seconds old is a minute and
  // a half old, and "2 min ago" claims half a minute that has not happened yet.
  // An age that counts up is the one a reader can check against their own
  // clock; an age that rounds up arrives at the next unit before they do.
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

// zoneLabel names the zone the three formatters above are resolving into:
// "Europe/Zurich (CEST)". The footer is its only caller, and it exists because
// a page of times in an unnamed zone is a page a reader has to guess at.
//
// The IANA name carries the place and the abbreviation carries the current
// offset, which is the pair a reader needs: "Europe/Zurich" alone does not say
// whether summer time is on, and "CEST" alone means nothing to anyone outside
// the zone. Read out of Intl rather than computed — the abbreviation flips with
// DST on a date this code should never have to know, and zones with no common
// abbreviation report a "GMT+9" form instead, which is the right answer there.
//
// Called on render rather than memoized at module load: everything else here is
// a formatter worth building once, and this is one string on one line.
export function zoneLabel(): string {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const short = new Intl.DateTimeFormat("en-GB", { timeZoneName: "short" })
    .formatToParts(new Date())
    .find((p) => p.type === "timeZoneName")?.value;
  // The zone alone when the abbreviation is missing or is just the zone again:
  // "Europe/Zurich (Europe/Zurich)" says nothing twice.
  return short && short !== zone ? `${zone} (${short})` : zone;
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

// Money comes in two magnitudes on this page and one formatter cannot serve
// both. A window total is dollars and cents; a per-inference figure is a
// hundredth of a cent, and rendering that at two decimals prints $0.00 for a
// number that is not zero.

// formatUSD is the headline form: two decimals, always. Totals and phase
// figures use it, because a bill is read in cents.
export function formatUSD(usd: number | null | undefined): string {
  if (usd === null || usd === undefined || !Number.isFinite(usd)) return "—";
  return `$${usd.toFixed(2)}`;
}

// formatUSDPrecise keeps three significant figures wherever the value lands, so
// a per-run cost and an axis tick on a fraction of a cent both survive. Capped
// at six decimals: past that the digits are noise from float arithmetic rather
// than anything MiMo billed.
export function formatUSDPrecise(usd: number | null | undefined): string {
  if (usd === null || usd === undefined || !Number.isFinite(usd)) return "—";
  if (usd === 0) return "$0.00";
  const magnitude = Math.floor(Math.log10(Math.abs(usd)));
  const decimals = Math.min(6, Math.max(2, 2 - magnitude));
  return `$${usd.toFixed(decimals)}`;
}
