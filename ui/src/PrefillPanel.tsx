import type { ModelSeries, Point } from "./api/types";
import { Card } from "./ui";
import { EChart } from "./charts/EChart";
import { buildLineOption, colorForModel } from "./charts/options";

// The prefill panel plots the SHORT probe's TTFT against the WIDE probe's, per
// model. The gap between the two lines is the prefill cost, and a widening gap
// is what catches a batching change or a requantisation.
//
// The two probes are never merged into one series: mixing a 34-token request
// with a 3800-token one produces an average that describes neither.
export function PrefillPanel({
  infer,
  wide,
  models,
}: {
  infer: ModelSeries | null;
  wide: ModelSeries | null;
  models: string[];
}) {
  const series: Record<string, Point[]> = {};
  const order: string[] = [];
  for (const m of models) {
    const short = infer?.models[m];
    const long = wide?.models[m];
    if (short && short.length) {
      series[`${m} · 34 tok`] = short;
      order.push(`${m} · 34 tok`);
    }
    if (long && long.length) {
      series[`${m} · 3800 tok`] = long;
      order.push(`${m} · 3800 tok`);
    }
  }

  const hasWide = Object.keys(series).some((k) => k.includes("3800"));

  return (
    <Card
      title="Prefill cost"
      subtitle="TTFT at ~3800 input tokens against TTFT at ~34, per model. The gap between the lines is what prefill actually costs; a widening gap is the signal that catches a batching change or a requantisation."
    >
      {Object.keys(series).length > 0 ? (
        <EChart
          option={buildLineOption({
            series,
            order,
            colorOf: (name) => colorForModel(name.split(" · ")[0]!, models),
            unit: "ms",
          })}
          ariaLabel="Time to first token for the short and wide probes, per model"
        />
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
      )}
      {!hasWide && (
        <p className="mt-3 text-label text-muted">
          The wide probe runs hourly, so it takes an hour before this gap is
          readable.
        </p>
      )}
    </Card>
  );
}
