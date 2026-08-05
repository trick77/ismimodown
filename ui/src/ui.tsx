import type { ReactNode } from "react";
import type { State } from "./verdict";
import { formatTime } from "./format";
import { currentOffPeak, OFFPEAK_COEFFICIENT } from "./offpeak";

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

// LogScaleChip announces that a plot's y-axis is logarithmic.
//
// A log axis read as a linear one is worse than no chart, so the switch is
// always announced on the plot — on EVERY chart that can make it, not only the
// one that happened to render the badge first.
export function LogScaleChip() {
  return (
    <span className="num rounded-full border border-border px-2 py-[2px] text-micro uppercase tracking-wider text-faint">
      log scale
    </span>
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
        // Green, matching the band it refers to. The word beside it is what
        // separates this from a health chip wearing the same colour.
        active
          ? "border-online/50 bg-online/15 text-online"
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
        {/* "normal" is the resting state of every figure here, and repeating
            it beside each one says nothing the card's own header chip does not
            already say. Anything OTHER than normal — including no data — still
            gets its word, because that is the case worth spotting. */}
        {state && state !== "normal" && <StateChip state={state} />}
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
