import type { ModelSeries } from "./api/types";
import { Card, CensoredNote, LogScaleChip, OffPeakChip } from "./ui";
import { EChart } from "./charts/EChart";
import { buildLineOption, colorForModel } from "./charts/options";

export function SeriesPanel({
  title,
  subtitle,
  series,
  models,
  unit,
  forceLinear = false,
  offPeak = false,
  offPeakChip = true,
}: {
  title: string;
  subtitle: string;
  series: ModelSeries | null;
  models: string[];
  unit: string;
  forceLinear?: boolean;
  // offPeak shades MiMo's reduced-rate billing hours behind the plot. Opt-in
  // per panel rather than automatic: it belongs on the chart a reader consults
  // before deciding when to send work, and on every other chart it would be one
  // more band competing with the measurement.
  offPeak?: boolean;
  // offPeakChip is the header pill, separate from the band. The topmost chart
  // opts out: the band is already on it, and the rate is stated again on the
  // charts below.
  offPeakChip?: boolean;
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
    offPeak,
  });

  return (
    <Card
      title={title}
      subtitle={subtitle}
      right={
        <div className="flex items-center gap-2">
          {/* Tied to the band actually being drawn, not to the prop. On 7d and
              wider the band is dropped, and a chip promising a rate the plot
              does not show is worse than no chip. */}
          {offPeakChip && option.offPeakSpans.length > 0 && <OffPeakChip />}
          {option.logScale && <LogScaleChip />}
        </div>
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
