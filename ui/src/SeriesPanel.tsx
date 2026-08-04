import type { ModelSeries } from "./api/types";
import { Card } from "./ui";
import { EChart } from "./charts/EChart";
import { buildLineOption, colorForModel } from "./charts/options";

export function SeriesPanel({
  title,
  subtitle,
  series,
  models,
  unit,
  forceLinear = false,
}: {
  title: string;
  subtitle: string;
  series: ModelSeries | null;
  models: string[];
  unit: string;
  forceLinear?: boolean;
}) {
  const data = series?.models ?? {};
  const hasData = Object.values(data).some((points) => points.length > 0);
  const option = buildLineOption({
    series: data,
    order: models,
    colorOf: (name) => colorForModel(name, models),
    unit,
    forceLinear,
  });

  return (
    <Card
      title={title}
      subtitle={subtitle}
      right={
        // A log axis read as a linear one is worse than no chart, so the switch
        // is always announced on the plot.
        option.logScale ? (
          <span className="num rounded-full border border-border px-2 py-[2px] text-micro uppercase tracking-wider text-faint">
            log scale
          </span>
        ) : null
      }
    >
      {hasData ? (
        <EChart option={option} ariaLabel={`${title} over time, per model`} />
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
      )}
      <Legend models={models} />
    </Card>
  );
}

export function Legend({ models }: { models: string[] }) {
  return (
    <ul className="mt-3 flex flex-wrap gap-4">
      {models.map((m) => (
        <li key={m} className="flex items-center gap-2 text-label text-muted">
          <span
            className="inline-block h-2 w-4 rounded-sm"
            style={{ background: colorForModel(m, models) }}
            aria-hidden="true"
          />
          <span className="num">{m}</span>
        </li>
      ))}
    </ul>
  );
}
