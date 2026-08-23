import type { Trend } from "./api/types";
import type { SpeedMetric, SpeedMove } from "./trend";
import { spanWords } from "./trend";
import { EChart } from "./charts/EChart";
import {
  buildTrendOption,
  colorForModel,
  TREND_HEIGHT,
} from "./charts/options";
import { formatMs, formatTps } from "./format";
import { LogScaleChip } from "./ui";

// The plot under the speed sentence: the hours it measured, shaded, with as
// much again in front of them for contrast.
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
  // Only the models the sentence is about. A second line for a model that did
  // not move is a shape the reader has to rule out before they can read the one
  // that did — and the two are on one axis, so the steady line also sets the
  // scale the moved one is drawn against.
  //
  // Matched on the metric too: the plot draws ONE of them, and a model that only
  // started slowly has nothing to say on the throughput plot.
  const drawn = trend.models.filter((m) =>
    moves.some((move) => move.modelID === m.model_id && move.metric === metric),
  );
  // Colour follows the MODEL, so it is picked from the whole fleet — a model
  // keeps its hue whether or not the other one is on the plot.
  const ids = trend.models.map((m) => m.model_id);
  const unit = metric === "ttft" ? "ms" : "tok/s";
  const format = metric === "ttft" ? formatMs : formatTps;

  // Where the compared span begins, and the level it is compared against. Both
  // come off the payload rather than off a constant here: the daemon owns the
  // spans, and a plot that shaded a different three hours than the sentence
  // described would be worse than no plot.
  const generatedAt = Date.parse(trend.generated_at);
  const recentFromMs = generatedAt - trend.recent_s * 1000;
  // The compared span, and as much again in front of it — never the whole 27
  // hours the payload carries. The reference day is a LEVEL in this reading, not
  // a shape: it reaches the plot as the dashed line, and drawing its buckets as
  // well bought nothing while costing everything. One timeout bucket sixteen
  // hours back set the y-range, and a first token that had doubled since lunch
  // drew as a flat line under a sentence saying it had doubled.
  //
  // The lead-in is what makes the shading legible: a plot that started exactly
  // where the shading starts would be shaded end to end, and the reader could
  // not see the level the compared hours departed from.
  const fromMs = generatedAt - 2 * trend.recent_s * 1000;
  const series: Record<
    string,
    (typeof trend.models)[number]["ttft"]["points"]
  > = {};
  for (const model of drawn) {
    series[model.model_id] = model[metric].points.filter(
      (p) => p.t * 1000 >= fromMs,
    );
  }
  // The reference line is drawn for the model the sentence leads with, so the
  // dashes and the numbers in the sentence are the same measurement.
  const lead = moves.find((m) => m.metric === metric);
  const leadModel = trend.models.find((m) => m.model_id === lead?.modelID);
  const referenceLevel = leadModel?.[metric].before.p50_ms ?? null;

  const option = buildTrendOption({
    series,
    order: ids,
    colorOf: (name) => colorForModel(name, ids),
    unit,
    recentFromMs,
    referenceLevel,
  });

  return (
    <div className="mt-4" data-testid="trend-plot">
      <div className="mb-1 flex flex-wrap items-center gap-x-5 gap-y-1">
        {drawn.map((model) => {
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
        {/* Both marks named, because neither carries a number: the y-axis has
            no labels, so an unexplained dashed line is just a line. The words
            come off the payload's own spans, like the sentence's do. */}
        <span className="num text-micro text-ghost">
          shaded: the compared span · dashed: the {spanWords(trend.before_s)}{" "}
          before
        </span>
        {option.logScale && <LogScaleChip />}
      </div>
      <EChart
        height={TREND_HEIGHT}
        ariaLabel={`${metric === "ttft" ? "Time to first token" : "Throughput"} over the compared hours and as much again before them, with the compared span shaded and both medians drawn`}
        option={option}
      />
    </div>
  );
}
