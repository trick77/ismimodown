import type { Summary } from "./api/types";
import { FAULT_EDGE, FAULT_OK, FAULT_ROUTE, FAULT_UPLINK } from "./api/types";
import { Card } from "./ui";
import { FAULT_COLORS } from "./charts/options";
import { formatInt, formatPct } from "./format";

const FAULT_LABELS: Record<string, string> = {
  [FAULT_OK]: "fine",
  [FAULT_EDGE]: "MiMo's edge unreachable",
  // Historical: produced only while a second reference host existed.
  // "the far end", not "the endpoint": that noun is MiMo's API elsewhere on
  // the page, and this row sits directly under one that says "MiMo's edge
  // unreachable" — the pair has to name two different things. Same words as
  // the verdict banner's route/uplink headlines.
  [FAULT_ROUTE]: "route to the far end degraded",
  [FAULT_UPLINK]: "nothing at the far end reachable",
};

// The attribution table, rendered. "MiMo is down" and "the path to MiMo is bad"
// are different findings, and nobody else publishes both.
export function AvailabilityStrip({ summary }: { summary: Summary | null }) {
  const faults = summary?.faults ?? {};
  const total = Object.values(faults).reduce((a, b) => a + b, 0);
  const order = [FAULT_OK, FAULT_EDGE, FAULT_ROUTE, FAULT_UPLINK];

  return (
    <Card
      title="What broke, and whose fault it was"
      subtitle="Every cycle is attributed from two independent TCP probes, with no heuristics: if an unrelated host beside MiMo's edge answers while MiMo does not, the path is fine and the fault is MiMo's."
      right={
        summary && summary.skipped_runs > 0 ? (
          <span
            className="num rounded-full border border-fault-edge/40 bg-fault-edge/10 px-2 py-[2px] text-micro uppercase tracking-wider text-fault-edge"
            // The old wording named the in-flight guard, which cannot fire while
            // cycles run one at a time — so the number it described was always
            // zero. What actually feeds this is a cycle overrunning its slot:
            // the probe then samples less often, and it does so precisely when
            // the endpoint is slow enough to cause the overrun.
            title="Scheduled runs that did not happen because the previous cycle was still running"
          >
            {summary.skipped_runs} skipped
          </span>
        ) : null
      }
    >
      {total > 0 ? (
        <>
          <div
            className="flex h-3 w-full overflow-hidden rounded-full"
            role="img"
            aria-label={`Cycle outcomes: ${order
              .filter((f) => faults[f])
              .map((f) => `${faults[f]} ${FAULT_LABELS[f]}`)
              .join(", ")}`}
          >
            {order.map((f) =>
              faults[f] ? (
                <span
                  key={f}
                  style={{
                    width: `${(faults[f]! / total) * 100}%`,
                    background: FAULT_COLORS[f],
                  }}
                />
              ) : null,
            )}
          </div>
          <ul className="mt-4 grid gap-2 sm:grid-cols-2">
            {order.map((f) =>
              faults[f] ? (
                <li key={f} className="flex items-center gap-2 text-label">
                  <span
                    className="inline-block h-2.5 w-2.5 rounded-sm"
                    style={{ background: FAULT_COLORS[f] }}
                    aria-hidden="true"
                  />
                  <span className="text-ink">{FAULT_LABELS[f]}</span>
                  <span className="num ml-auto text-muted">
                    {formatInt(faults[f])} ·{" "}
                    {formatPct((faults[f]! / total) * 100)}
                  </span>
                </li>
              ) : null,
            )}
          </ul>
          {faults[FAULT_UPLINK] ? (
            <p className="mt-4 text-label text-muted">
              Cycles where neither MiMo nor the reference host answered are
              excluded from every model's availability. From one vantage point
              those are indistinguishable from our own connection being down,
              and publishing them as provider outages would risk reporting our
              fault as theirs.
            </p>
          ) : null}
        </>
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within a few minutes.
        </p>
      )}
    </Card>
  );
}
