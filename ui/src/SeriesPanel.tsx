import type { ModelSeries } from "./api/types";
import { Card, CensoredNote, LogScaleChip, NoChart, TrendNote } from "./ui";
import { EChart } from "./charts/EChart";
import { CHART_HEIGHT, buildLineOption, colorForModel } from "./charts/options";

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
    bucketMs: series ? series.bucket_s * 1000 : undefined,
    // Only the model panels. The wire chart shares this builder and must not
    // grow a second line per host — see the `smoothed` option.
    smoothed: true,
  });

  return (
    <Card
      title={title}
      subtitle={subtitle}
      right={option.logScale ? <LogScaleChip /> : null}
    >
      {hasData ? (
        <EChart option={option} ariaLabel={`${title} over time, per model`} />
      ) : (
        <NoChart height={CHART_HEIGHT}>
          Not enough data yet — first samples within a few minutes.
        </NoChart>
      )}
      <Legend models={models} />
      <TrendNote smoothed={option.smoothed} spanMs={option.smoothSpanMs} />
      <CensoredNote bands={option.censoredBands} />
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
