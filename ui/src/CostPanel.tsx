import type { CostBreakdown, CostGroup } from "./api/types";
import { formatInt, formatTime, formatUSD, formatUSDPrecise } from "./format";
import { Card } from "./ui";
import { EChart } from "./charts/EChart";
import { buildCostOption } from "./charts/options";

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

// How often each probe kind runs, said once beside its per-run price — the one
// thing about a probe that no response carries.
//
// Keyed on the wire value, which is also what the label is built from: since
// 0003 the schema spells both kinds the way a reader should see them, so
// nothing translates and there is no second spelling to keep in step. A kind
// with no entry here simply gets no cadence, which is the honest outcome for a
// probe this card has never heard of.
const PROBE_HINTS: Record<string, string> = {
  short: "every 5 min",
  wide: "hourly",
};

export function CostPanel({ cost }: { cost: CostBreakdown | null }) {
  // No panel at all when there is no price table. A cost card showing tokens
  // and dashes answers a question nobody asked, and one showing $0.00 answers
  // it wrongly — see CostBreakdown.priced.
  if (cost === null || !cost.priced || cost.total.runs < MIN_RUNS) {
    return null;
  }

  const option = buildCostOption(cost.series, cost.offpeak_spans);
  const saved = savedUSD(cost.total);

  return (
    <Card
      title="What this dashboard costs to run"
      subtitle="Every probe mimostats sends, priced from the usage MiMo reported on it — both models, both probe kinds. The plan bills in credits, so these are list rates rather than an invoice."
      right={
        <span
          className={`num rounded-full border px-2 py-[2px] text-micro uppercase tracking-wider ${
            cost.offpeak_active
              ? "border-online/40 bg-online/10 text-online"
              : "border-border-soft text-muted"
          }`}
          data-testid="offpeak-chip"
        >
          {/* offpeak_until is the next boundary either way, so the preposition
              has to come from offpeak_active: "until" while the reduced rate is
              running, "from" while it is still ahead. Reading it as "until" in
              both states says the discount is live at 16:00 Zurich, which is
              two hours before it starts. */}
          {cost.offpeak_coefficient}× {cost.offpeak_active ? "until" : "from"}{" "}
          {formatTime(new Date(cost.offpeak_until * 1000))}
        </span>
      }
    >
      <div className="mb-5 grid grid-cols-2 gap-5 border-b border-border-soft pb-5 sm:grid-cols-4">
        <Money
          label={`At list, ${cost.window}`}
          value={formatUSD(cost.total.usd)}
          hint={`${formatInt(cost.total.runs)} runs · ${formatInt(
            cost.total.tokens.prompt + cost.total.tokens.output,
          )} tokens`}
        />
        {/* Per PROBE, never a single mean over both: a wide run carries a
            ~3.6k-token prompt against a short one's ~70, so their average
            describes no run that is ever sent. */}
        {cost.probes.map((p) => (
          <Money
            key={p.probe}
            label={`Per ${p.probe} run`}
            value={formatUSDPrecise(perRun(p))}
            hint={`${formatInt(p.runs)} runs · ${PROBE_HINTS[p.probe] ?? ""}`}
          />
        ))}
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
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
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
            {Math.round((1 - cost.offpeak_coefficient) * 100)}% fewer credits.
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
