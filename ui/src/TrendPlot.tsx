import type { Trend } from "./api/types";
import type { SpeedMetric, SpeedMove } from "./trend";
import { EChart } from "./charts/EChart";
import {
  buildTrendOption,
  colorForModel,
  TREND_HEIGHT,
} from "./charts/options";
import { formatMs, formatTps } from "./format";

// The plot under the speed sentence: the whole span the reading compares, with
// the recent side shaded.
//
// Its legend sits ABOVE the plot rather than inside it. A label drawn in the
// plotting area lands on the very stretch the reader is trying to look at —
// the last few hours, at the right edge, which is exactly where a series label
// wants to go.
export function TrendPlot({
  trend,
  metric,
  moves,
}: {
  trend: Trend;
  metric: SpeedMetric;
  moves: SpeedMove[];
}) {
  const ids = trend.models.map((m) => m.model_id);
  const series: Record<
    string,
    (typeof trend.models)[number]["ttft"]["points"]
  > = {};
  for (const model of trend.models) {
    series[model.model_id] = model[metric].points;
  }
  const unit = metric === "ttft" ? "ms" : "tok/s";
  const format = metric === "ttft" ? formatMs : formatTps;

  // Where the compared span begins, and the level it is compared against. Both
  // come off the payload rather than off a constant here: the daemon owns the
  // spans, and a plot that shaded a different three hours than the sentence
  // described would be worse than no plot.
  const generatedAt = Date.parse(trend.generated_at);
  const recentFromMs = generatedAt - trend.recent_s * 1000;
  // The reference line is drawn for the model the sentence leads with, so the
  // dashes and the numbers in the sentence are the same measurement.
  const lead = moves.find((m) => m.metric === metric);
  const leadModel = trend.models.find((m) => m.model_id === lead?.modelID);
  const referenceLevel = leadModel?.[metric].before.p50_ms ?? null;

  return (
    <div className="mt-4" data-testid="trend-plot">
      <div className="mb-1 flex flex-wrap items-center gap-x-5 gap-y-1">
        {trend.models.map((model) => {
          // Matched on the metric as well as the model. The plot draws ONE
          // metric, so a move on the other one must not label this line: a
          // model whose first token regressed would otherwise have its
          // throughput value tagged "slower" while that line sat flat.
          const move = moves.find(
            (m) => m.modelID === model.model_id && m.metric === metric,
          );
          const value = model[metric].recent.p50_ms;
          return (
            <span
              key={model.model_id}
              className="flex items-center gap-2 text-label"
            >
              <span
                className="inline-block h-[3px] w-4 rounded-full"
                style={{ background: colorForModel(model.model_id, ids) }}
                aria-hidden="true"
              />
              <span className="num text-ink-dim">{model.model_id}</span>
              <span className="num text-ink">{format(value)}</span>
              {/* The direction in WORDS, never a colour or an arrow alone —
                  the same rule every state chip on this page follows. */}
              {move && (
                <span className="num text-muted">
                  {move.worseBy > 0 ? "slower" : "quicker"}
                </span>
              )}
            </span>
          );
        })}
        <span className="num text-micro text-ghost">
          shaded: the compared span
        </span>
      </div>
      <EChart
        height={TREND_HEIGHT}
        ariaLabel={`${metric === "ttft" ? "Time to first token" : "Throughput"} over the span the comparison covers, with the recent hours shaded`}
        option={buildTrendOption({
          series,
          order: ids,
          colorOf: (name) => colorForModel(name, ids),
          unit,
          recentFromMs,
          referenceLevel,
        })}
      />
    </div>
  );
}
