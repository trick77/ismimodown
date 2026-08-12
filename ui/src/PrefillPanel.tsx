import type { ModelSeries, Point } from "./api/types";
import { Card, CensoredNote, LogScaleChip, NoChart } from "./ui";
import { EChart } from "./charts/EChart";
import {
  CHART_HEIGHT,
  REFERENCE_HEIGHT,
  buildLineOption,
  colorForModel,
  timeExtent,
} from "./charts/options";
import { prefillDelta } from "./charts/prefill";

// The prefill panel charts the DIFFERENCE between the two probes' TTFT, per
// model: what a ~3800-token prompt adds over a ~34-token one. A rise in it is
// what catches a batching change or a requantisation.
//
// It used to plot the two probes against each other and leave the difference to
// be read off the whitespace between them. Three things were wrong with that,
// and all three are why the delta is now the chart rather than an addition to
// it. The gap had to be estimated by eye, so a 200 ms widening and a 600 ms one
// looked alike. The axis switches to log above 20x spread, and on a log axis a
// constant gap visually NARROWS as the levels rise — the panel's own escape
// hatch flattening the one thing the panel is for. And two models each with two
// probes is four lines on two hues, told apart by a dash convention that has to
// be read off a legend before the chart means anything at all.
//
// The two probes are still plotted, below, small. Not decoration: a widening is
// ambiguous on its own, because a gap grows identically whether the wide probe
// got slower — a real prefill regression — or the short baseline got faster on a
// quieter queue, which is not one. The strip is where that gets settled.
//
// What is never done is merging the probes into one series: a 34-token request
// averaged with a 3800-token one produces a figure describing neither.
export function PrefillPanel({
  short,
  wide,
  models,
}: {
  short: ModelSeries | null;
  wide: ModelSeries | null;
  models: string[];
}) {
  // The delta, keyed by bare model name. No " · N tok" suffix here: there is
  // one line per model, and a suffix would name a probe this series is not.
  const delta: Record<string, Point[]> = {};
  const deltaOrder: string[] = [];
  // The two probes as they were, for the reference strip below.
  const probes: Record<string, Point[]> = {};
  const probeOrder: string[] = [];
  for (const m of models) {
    const shortSeries = short?.models[m];
    const long = wide?.models[m];
    if (shortSeries && shortSeries.length) {
      probes[`${m} · 34 tok`] = shortSeries;
      probeOrder.push(`${m} · 34 tok`);
    }
    if (long && long.length) {
      probes[`${m} · 3800 tok`] = long;
      probeOrder.push(`${m} · 3800 tok`);
    }
    const d = prefillDelta(shortSeries, long);
    if (d.length) {
      delta[m] = d;
      deltaOrder.push(m);
    }
  }

  const hasProbes = probeOrder.length > 0;
  const hasWide = probeOrder.some((k) => k.includes("3800"));
  const bucketS = short?.bucket_s ?? wide?.bucket_s;
  const bucketMs = bucketS !== undefined ? bucketS * 1000 : undefined;
  const colorOf = (name: string) =>
    colorForModel(name.split(" · ")[0]!, models);

  // Counted rather than taken from deltaOrder.length, which is the number of
  // SERIES and not the number of readings. prefillDelta emits a point for every
  // wide bucket whether or not there was anything to subtract — deliberately,
  // so censoring survives — so a series can be full-length and entirely null,
  // and a line needs two values to be a line at all. Gating on the array length
  // drew an empty grid with axes and no explanation, in the two cases that
  // actually happen: the first hour after a deploy, and a stretch where every
  // wide probe timed out.
  const readings = Object.values(delta)
    .flat()
    .filter((p) => p.p50 !== null).length;
  const hasDelta = readings >= 2;

  // Both plots are pinned to the probes' extent — the wider of the two, since
  // the short probe runs every cycle — so a vertical through the pair means one
  // instant. Without it the delta's hourly cadence leaves its axis up to an hour
  // short of the strip's, and the strip stops describing the chart above it.
  //
  // Widened when everything sits in one bucket, which is a real state on a
  // fresh database: a min equal to its max is a zero-width axis, and pinning
  // one is worse than not pinning at all — ECharts pads a lone point itself.
  const extent = timeExtent(probes);
  const xRange: [number, number] | undefined =
    extent && extent[0] === extent[1]
      ? [extent[0], extent[1] + (bucketMs ?? 1)]
      : (extent ?? undefined);

  const deltaOption = buildLineOption({
    series: delta,
    order: deltaOrder,
    colorOf,
    unit: "ms",
    // NOT pinned linear, though the first cut of this was.
    //
    // The log axis is what flattened the gap on the old chart, so forcing this
    // one linear looked like the fix. It is not: on a window holding a 45 s
    // prefill spike, a linear axis squashes an entire day's drift into the
    // bottom two pixels just as thoroughly. The distortion being escaped was
    // never the log axis itself — it was reading a DISTANCE BETWEEN two lines
    // on one, which compresses as the levels rise. A single line's own height
    // reads correctly on a log axis, which is why every other chart on this
    // page is allowed one. buildLineOption keeps this chart off it when the
    // delta reaches zero, where log cannot go.
    // Zero is where the reading changes meaning, not the bottom of the plot.
    zeroLine: true,
    bucketMs,
    xRange,
  });

  const probeOption = buildLineOption({
    series: probes,
    order: probeOrder,
    colorOf,
    // Colour follows the model, so the two probes of one model share a hue;
    // the wide series is dashed so they stay distinguishable.
    dashed: (name) => name.includes("3800"),
    // The short probe is the baseline, not the subject.
    muted: (name) => !name.includes("3800"),
    unit: "ms",
    bucketMs,
    xRange,
    // Drawn at REFERENCE_HEIGHT, where the default five gridlines land a few
    // pixels apart and the labels smear into a column.
    compact: true,
  });

  return (
    <Card
      title="Prefill cost"
      subtitle="What a ~3800-token prompt adds to time to first token over a ~34-token one, per model. That difference is what prefill actually costs, and a rise in it is the signal that catches a batching change or a requantisation. Lower is better."
      // The card's chip belongs to the chart the card leads with. The strip
      // below carries its own, because the two axes decide independently and a
      // single chip could not say which plot it meant.
      right={deltaOption.logScale ? <LogScaleChip /> : null}
    >
      {hasDelta ? (
        <EChart
          option={deltaOption}
          ariaLabel="Prefill cost per model: time to first token at 3800 input tokens minus time to first token at 34"
        />
      ) : (
        // Three states, not two. Blaming the wide probe's hourly cadence on a
        // fresh install — where NOTHING has been collected — names the wrong
        // reason and sends the reader away for an hour over something the next
        // cycle would have produced.
        <NoChart height={CHART_HEIGHT}>
          {!hasProbes
            ? "Not enough data yet — first samples within a few minutes."
            : !hasWide
              ? "The wide probe runs hourly, so it takes an hour before there is a cost to plot."
              : "First cost readings are in; the line starts once a second wide probe has run."}
        </NoChart>
      )}
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
        <li className="text-label text-muted">
          One point per wide probe, so hourly — sparser than the strip below. At
          or under zero, the short baseline moved rather than prefill.
        </li>
      </ul>
      {/* Only alongside a chart. The note explains shading, and shading with no
          plot under it to carry it is a caption for nothing. */}
      {hasDelta && <CensoredNote bands={deltaOption.censoredBands} />}

      {hasProbes && (
        <div className="mt-5 border-t border-border pt-4">
          {/* The strip is what makes a rise above attributable. A gap widens
              the same whether the wide probe got slower or the short one got
              faster, and only one of those is a prefill regression.
              The two plots each carry their own log chip because their axes
              decide independently — the strip goes log on the spread between
              two probes an order of magnitude apart, the chart above on the
              spread of the difference alone, and either can go without the
              other. */}
          <div className="mb-2 flex flex-col items-start gap-2 sm:flex-row sm:justify-between sm:gap-4">
            <p className="text-label text-muted">
              For reference, the two probes the cost above is measured from —
              which of them moved is what tells a prefill regression from a
              quieter queue.
            </p>
            {probeOption.logScale ? <LogScaleChip /> : null}
          </div>
          <EChart
            option={probeOption}
            height={REFERENCE_HEIGHT}
            ariaLabel="Time to first token for the short and wide probes, per model"
          />
          <p className="mt-2 flex items-center gap-2 text-label text-muted">
            <span
              className="inline-block h-0 w-4 shrink-0 border-t-2 border-dashed border-muted"
              aria-hidden="true"
            />
            <span>
              dashed = wide probe (~3800 tok); faint = the short probe it is
              measured against
            </span>
          </p>
          {/* The strip paints its own censoring bands, so it needs its own
              note. The delta's count does not cover them: the delta is driven
              off the wide side, so a bucket where only the short probe was cut
              off is not in it at all — and that bucket still shades here. */}
          <CensoredNote bands={probeOption.censoredBands} />
        </div>
      )}
    </Card>
  );
}
