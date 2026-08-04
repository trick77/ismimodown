import type { ReactNode } from "react";
import type { State } from "./verdict";

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
