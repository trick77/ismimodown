import type { ReactNode } from "react";
import type { State } from "./verdict";
import { formatTime } from "./format";
import {
  currentOffPeak,
  offPeakWindowFor,
  OFFPEAK_COEFFICIENT,
} from "./offpeak";

// Shared primitives. Colour is NEVER the only signal — every state chip carries
// its word, and every delta its sign — so the dashboard stays readable to a
// colour-blind reader and in a greyscale screenshot.

const stateStyles: Record<State, string> = {
  normal: "text-online border-online/40 bg-online/10",
  elevated: "text-fault-edge border-fault-edge/40 bg-fault-edge/10",
  degraded: "text-danger border-danger/40 bg-danger/10",
  unknown: "text-faint border-border bg-panel-hi/40",
};

const stateWords: Record<State, string> = {
  normal: "normal",
  elevated: "elevated",
  degraded: "degraded",
  unknown: "no data",
};

export function StateChip({ state }: { state: State }) {
  return (
    <span
      className={`num inline-block rounded-full border px-2 py-[2px] text-micro uppercase tracking-wider ${stateStyles[state]}`}
      data-testid="state-chip"
      data-state={state}
    >
      {stateWords[state]}
    </span>
  );
}

// CensoredNote explains the amber bands on a chart.
//
// In WORDS, beside the colour, for the same reason every state chip carries its
// word: the reader this exists for is the one who would otherwise take the plot
// at face value, and a shaded rectangle they cannot name does not reach them.
// It also cannot be a tooltip — the stretch it describes is often exactly where
// there is no line to hover.
export function CensoredNote({ bands }: { bands: number }) {
  if (bands < 1) return null;
  return (
    <p className="mt-3 flex items-start gap-2 text-label text-muted">
      <span
        className="mt-[5px] inline-block h-3 w-4 shrink-0 rounded-sm bg-fault-edge/25"
        aria-hidden="true"
      />
      <span>
        Shaded: probes here were cut off by the timeout limits and are not in
        the percentiles. The line is drawn from the runs that finished, so the
        slowest ones are missing from it.
      </span>
    </p>
  );
}

// OffPeakNote names the accent bands, and gives the hours in the LOCAL clock.
//
// Same reasoning as CensoredNote: a shaded rectangle nobody can name is not a
// signal. The hours are quoted off the drawn band's own DAY rather than from a
// local-clock constant, so a window straddling the DST changeover reports what
// it actually painted instead of a rule that was true on one side of it — and
// off the whole band, not the clipped span, since the span stops at the edge of
// the plot and the window does not.
//
// It says billing and only billing. MiMo publishes no load figures, and a note
// here implying these are the quiet hours would be inventing a claim the rest of
// this page exists to avoid.
export function OffPeakNote({ spans }: { spans: [number, number][] }) {
  const first = spans[0];
  if (first === undefined) return null;
  const [opens, closes] = offPeakWindowFor(first[0]);
  return (
    <p className="mt-3 flex items-start gap-2 text-label text-muted">
      <span
        className="mt-[5px] inline-block h-3 w-4 shrink-0 rounded-sm bg-accent/25"
        aria-hidden="true"
      />
      <span>
        {/* Not "Shaded:", which is how CensoredNote opens — the two notes sit
            one above the other whenever both apply, and a reader would have
            only the swatch to tell which sentence belonged to which band. */}
        Off-peak: MiMo bills these hours at {OFFPEAK_COEFFICIENT}× — 20% fewer
        credits. That is 00:00–08:00 in Beijing, which lands at{" "}
        <span className="num">{formatTime(new Date(opens))}</span>–
        <span className="num">{formatTime(new Date(closes))}</span> here. It is
        a price, not a forecast: nothing is published about when the platform is
        busy.
      </span>
    </p>
  );
}

// OffPeakChip says whether the reduced rate is live right now, in the header.
//
// It names the BOUNDARY and never counts down to it. The page re-renders on the
// 5-minute probe cycle, so "in 3h 10m" would be a number that is wrong for most
// of the time it is on screen; "until 02:00" is simply true until it is not. The
// same cycle means the state itself can lag the boundary by one probe, which is
// nothing against an eight-hour window.
export function OffPeakChip({ now = Date.now() }: { now?: number }) {
  const { active, boundaryMs } = currentOffPeak(now);
  return (
    <span
      className={`num rounded-full border px-2 py-[2px] text-micro uppercase tracking-wider ${
        active
          ? "border-accent/50 bg-accent/15 text-accent-strong"
          : "border-border text-faint"
      }`}
    >
      {OFFPEAK_COEFFICIENT}× {active ? "until" : "from"}{" "}
      {formatTime(new Date(boundaryMs))}
    </span>
  );
}

export function Pill({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className="pill"
      aria-pressed={active}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

export function Card({
  title,
  subtitle,
  children,
  right,
}: {
  title: string;
  subtitle?: string;
  children: ReactNode;
  right?: ReactNode;
}) {
  return (
    <section className="card p-5">
      <header className="mb-4 flex items-start justify-between gap-4">
        <div>
          <h2 className="font-serif text-title font-normal text-ink">
            {title}
          </h2>
          {subtitle && (
            <p className="mt-1 max-w-[68ch] text-label text-muted">
              {subtitle}
            </p>
          )}
        </div>
        {right}
      </header>
      {children}
    </section>
  );
}

// Figure renders one number with its label, and renders SUPPRESSION honestly:
// below the sample threshold it says so in words rather than drawing a zero.
export function Figure({
  label,
  value,
  sufficient = true,
  n,
  state,
  hint,
}: {
  label: string;
  value: string;
  sufficient?: boolean;
  n?: number;
  state?: State;
  hint?: string;
}) {
  return (
    <div>
      <div className="flex items-center gap-2">
        <span className="text-micro uppercase tracking-wider text-faint">
          {label}
        </span>
        {state && <StateChip state={state} />}
      </div>
      {sufficient ? (
        <div className="num mt-1 text-display text-ink">{value}</div>
      ) : (
        <div
          className="mt-1 text-body text-faint italic font-serif"
          data-testid="insufficient"
        >
          insufficient data
          {n !== undefined && (
            <span className="num ml-1 text-micro not-italic"> ({n})</span>
          )}
        </div>
      )}
      {hint && <div className="mt-1 text-micro text-ghost">{hint}</div>}
    </div>
  );
}
