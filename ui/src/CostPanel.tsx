import type { CostBreakdown, CostGroup } from "./api/types";
import { formatInt, formatTime, formatUSD, formatUSDPrecise } from "./format";
import { Card, NoChart } from "./ui";
import { EChart } from "./charts/EChart";
import { CHART_HEIGHT, buildCostOption } from "./charts/options";

// What the probing costs, in money.
//
// The figures and the chart are one card and one request: the row answers "what
// did the selected window cost" and the line answers "when", and splitting them
// across two panels would let the two describe different instants.
//
// This is also the only place the off-peak band survives. On the latency charts
// it shaded a quantity the rate does not govern; here it shades the one it does.

// Nothing is rendered below this many priced runs. A window with three runs in it
// produces a total that is technically correct and reads as the daily bill.
const MIN_RUNS = 10;

// How often the probe runs, said once beside its per-run price — the one thing
// about it that no response carries.
const PROBE_CADENCE = "every few minutes";

export function CostPanel({ cost }: { cost: CostBreakdown | null }) {
  // Still no panel below MIN_RUNS: a total over three runs is technically
  // correct and reads as the daily bill. The "no price table configured" case
  // this also used to guard is gone — prices are a constant now.
  if (cost === null || cost.total.runs < MIN_RUNS) {
    return null;
  }

  const option = buildCostOption(cost.series, cost.offpeak_spans);
  const saved = savedUSD(cost.total);

  return (
    <Card
      title="What this dashboard costs to run"
      subtitle="Every probe this page sends, priced from the usage MiMo reported on it — both models."
      right={
        <span
          className={`num whitespace-nowrap rounded-full border px-2 py-[2px] text-micro uppercase tracking-wider ${
            cost.offpeak_active
              ? "border-online/40 bg-online/10 text-online"
              : "border-border-soft text-muted"
          }`}
          data-testid="offpeak-chip"
        >
          {/* offpeak_until is the next boundary either way, so the preposition
              has to come from offpeak_active: "until" while the reduced rate is
              running, "from" while it is still ahead. Reading it as "until" in
              both states announces the discount as live right up to the hour it
              actually starts.

              The hour itself is the reader's local clock, like every time on
              the page — the boundary is an instant, and the footer names the
              zone it is being shown in. The Beijing hours in the note below the
              chart are the other half of that: they are the window's fixed
              definition, not a second clock competing with this one. */}
          {cost.offpeak_coefficient}× {cost.offpeak_active ? "until" : "from"}{" "}
          {formatTime(new Date(cost.offpeak_until * 1000))}
        </span>
      }
    >
      {/* Four up from md rather than sm, for the reason the model cards move
          on the same breakpoint: four columns inside a 600px card leaves each
          figure ~120px, and "$0.000141" needs more than that. Two by two until
          there is room for one row. */}
      <div className="mb-5 grid grid-cols-2 gap-5 border-b border-border-soft pb-5 md:grid-cols-4">
        <Money
          label={`Last ${cost.window}`}
          value={formatUSD(cost.total.usd)}
          hint={`${formatInt(cost.total.runs)} runs · ${formatInt(
            cost.total.tokens.prompt + cost.total.tokens.output,
          )} tokens`}
        />
        {/* One mean over every run, which is honest now that every run sends
            the same prompt. It used to be split per probe kind: a ~3.6k-token
            prompt averaged with a ~70-token one describes no run that is ever
            sent. There is one kind. */}
        <Money
          label="Per run"
          value={formatUSDPrecise(perRun(cost.total))}
          hint={`${formatInt(cost.total.runs)} runs · ${PROBE_CADENCE}`}
        />
        <Money
          label="Saved off-peak"
          value={saved === null ? "—" : formatUSDPrecise(saved)}
          hint={offPeakHint(cost)}
          tone="online"
        />
      </div>

      {cost.series.length > 0 ? (
        <EChart option={option} ariaLabel="Inference cost over time" />
      ) : (
        <NoChart height={CHART_HEIGHT}>
          Not enough data yet — first samples within a few minutes.
        </NoChart>
      )}

      {option.banded && (
        <p className="mt-3 flex items-start gap-2 text-label text-muted">
          <span
            className="mt-[5px] inline-block h-3 w-4 shrink-0 rounded-sm bg-online/30"
            aria-hidden="true"
          />
          <span>
            Off-peak: MiMo bills these hours — 00:00–08:00 in Beijing — at{" "}
            {cost.offpeak_coefficient}×, or{" "}
            {Math.round((1 - cost.offpeak_coefficient) * 100)}% off the
            per-token rate.
          </span>
        </p>
      )}

      <UnpricedNote runs={cost.unpriced_runs} />
    </Card>
  );
}

// UnpricedNote names the runs that are in no figure above.
//
// The usage chunk arrives last, so a run cut off by the timeout ladder reports
// no tokens and cannot be priced — while having been billed for whatever
// happened before the cut. Without this line the total is quietly low exactly
// when the endpoint is at its worst, which is the same failure the censoring
// note exists to prevent one chart up.
export function UnpricedNote({ runs }: { runs: number }) {
  if (runs < 1) return null;
  return (
    <p className="mt-3 text-label text-muted" data-testid="unpriced-note">
      <span className="num">{formatInt(runs)}</span>{" "}
      {runs === 1 ? "run is" : "runs are"} not in this total: they were cut off
      before reporting usage. MiMo billed whatever they had done by then, and
      nothing here estimates it.
    </p>
  );
}

function Money({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: string;
  hint?: string;
  tone?: "online";
}) {
  return (
    <div>
      <span className="text-micro uppercase tracking-wider text-faint">
        {label}
      </span>
      <div
        className={`num mt-1 text-display ${tone === "online" ? "text-online" : "text-ink"}`}
      >
        {value}
      </div>
      {hint && <div className="mt-1 text-micro text-ghost">{hint}</div>}
    </div>
  );
}

// perRun is a group's mean cost per inference. Null rather than zero when the
// group could not be priced or holds no runs — a division that cannot be done is
// not a figure of nothing.
export function perRun(g: CostGroup): number | null {
  if (g.usd === null || g.runs < 1) return null;
  return g.usd / g.runs;
}

// savedUSD is what the coefficient took off: list price minus what was billed.
export function savedUSD(g: CostGroup): number | null {
  if (g.usd === null || g.list_usd === null) return null;
  return g.list_usd - g.usd;
}

function offPeakHint(cost: CostBreakdown): string {
  const off = cost.phases.find((p) => p.phase === "offpeak");
  if (off === undefined) return "no runs at the reduced rate";
  return `${formatInt(off.runs)} of ${formatInt(cost.total.runs)} runs at ${cost.offpeak_coefficient}×`;
}
