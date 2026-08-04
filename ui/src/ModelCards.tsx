import type { Summary } from "./api/types";
import { Figure, StateChip } from "./ui";
import { formatMs, formatPct, formatTps } from "./format";
import {
  scoreAvailability,
  scoreCorrectness,
  scoreRatio,
  worst,
} from "./verdict";
import { colorForModel } from "./charts/options";

// One card per model. Two models, not three.
export function ModelCards({
  summary,
  baseline,
}: {
  summary: Summary | null;
  baseline: Summary | null;
}) {
  const models = summary?.models ?? [];
  const ids = models.map((m) => m.model_id);

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      {models.map((m) => {
        const base = baseline?.models.find((b) => b.model_id === m.model_id);
        const availability = scoreAvailability(
          m.attempts > 0 ? m.available_pct : null,
        );
        const correctness = scoreCorrectness(m.correct_pct);
        const ttft = scoreRatio(m.ttft.p50_ms, base?.ttft.p50_ms ?? null);

        return (
          <section
            key={m.model_id}
            className="card p-5"
            data-testid={`model-card-${m.model_id}`}
          >
            <header className="mb-4 flex items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <span
                  className="inline-block h-2.5 w-2.5 rounded-sm"
                  style={{ background: colorForModel(m.model_id, ids) }}
                  aria-hidden="true"
                />
                <h3 className="num text-ui text-ink">{m.model_id}</h3>
              </div>
              <StateChip state={worst(availability, correctness, ttft)} />
            </header>

            <div className="grid grid-cols-2 gap-4">
              <Figure
                label="TTFT p50"
                value={formatMs(m.ttft.p50_ms)}
                sufficient={m.ttft.sufficient}
                n={m.ttft.n}
                state={ttft}
              />
              <Figure
                label="Throughput p50"
                value={formatTps(m.tps.p50_ms)}
                sufficient={m.tps.sufficient}
                n={m.tps.n}
              />
              <Figure
                label="Availability"
                value={formatPct(m.attempts > 0 ? m.available_pct : null)}
                state={availability}
                hint={`${m.succeeded}/${m.attempts} runs`}
              />
              <Figure
                label="Correctness"
                value={formatPct(m.correct_pct)}
                sufficient={m.correct_pct !== null}
                n={m.answered}
                state={correctness}
                hint="answers containing the expected fact"
              />
            </div>

            {m.censored > 0 && (
              // Not a StateChip, and deliberately not folded into the
              // availability figure: these runs ARE counted there. This says
              // something the numbers above structurally cannot — that the
              // slowest runs in the window are missing from the percentiles
              // beside it, and that the worse the endpoint gets the more
              // flattering those percentiles become.
              <p
                className="mt-4 rounded-ui border border-fault-edge/40 bg-fault-edge/10 px-3 py-2 text-label text-fault-edge"
                data-testid={`censored-${m.model_id}`}
              >
                {m.censored} of {m.attempts} runs were cut off by the timeout
                limits. The percentiles above cover the {m.succeeded} that
                finished, so the slowest runs in this window are not in them.
              </p>
            )}

            {m.max_reasoning_tokens > 0 && (
              <p
                className="mt-4 rounded-ui border border-danger/40 bg-danger/10 px-3 py-2 text-label text-danger"
                role="alert"
              >
                Reasoning returned {m.max_reasoning_tokens} tokens despite being
                disabled — these latency figures are not measuring what they
                claim.
              </p>
            )}
          </section>
        );
      })}
    </div>
  );
}
