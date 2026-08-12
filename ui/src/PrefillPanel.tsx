import type {
  DashboardPrefill,
  ModelSeries,
  Point,
  PrefillCost,
} from "./api/types";
import { Card, Figure, LogScaleChip, NoChart } from "./ui";
import { EChart } from "./charts/EChart";
import {
  REFERENCE_HEIGHT,
  buildLineOption,
  colorForModel,
  timeExtent,
} from "./charts/options";
import { formatMs } from "./format";

// What a long prompt costs: the median difference between the wide probe's TTFT
// and the short probe's, measured WITHIN the same cycle.
//
// This panel was a chart twice, and the data says it cannot be one. The two
// probes were plotted against each other and the gap read off the whitespace;
// then the difference itself was plotted. Fourteen days of production says the
// per-run spread on that difference is ~1600 ms against a real cost of ~250 —
// so a single point carries no reading, and a line of single points is a
// picture of the endpoint's queue, not of prefill. Only the window aggregate
// resolves it. See backend/internal/samples/prefill.go.
//
// What is left is more useful to a reader anyway. The figure answers the
// question someone actually arrives with — if I send a big prompt, what does it
// cost me — and the share answers the one they ask next: ~250 ms inside a
// ~2600 ms wait means prompt size is not what makes this endpoint slow, the
// queue is.
export function PrefillPanel({
  prefill,
  short,
  wide,
  models,
}: {
  prefill: DashboardPrefill | null;
  short: ModelSeries | null;
  wide: ModelSeries | null;
  models: string[];
}) {
  // The server's window, NOT the page's. Prefill is fixed at 7d whatever the
  // reader selected — see dashboardPrefillWindow — so the panel has to name it
  // rather than let the selector above imply it applies here.
  const windowKey = prefill?.window ?? "7d";
  // The reference strip: both probes' TTFT over time, unchanged.
  //
  // Kept while the delta chart went, because it is a real chart with real
  // sampling — the short probe runs every cycle — and because the wide probe's
  // ABSOLUTE level appears nowhere else on the page. It answers the reader's
  // other question directly: how long until the first token on a big prompt.
  const probes: Record<string, Point[]> = {};
  const probeOrder: string[] = [];
  for (const m of models) {
    const s = short?.models[m];
    const w = wide?.models[m];
    if (s?.length) {
      probes[`${m} · 34 tok`] = s;
      probeOrder.push(`${m} · 34 tok`);
    }
    if (w?.length) {
      probes[`${m} · 3800 tok`] = w;
      probeOrder.push(`${m} · 3800 tok`);
    }
  }
  const hasProbes = probeOrder.length > 0;
  const bucketS = short?.bucket_s ?? wide?.bucket_s;
  const bucketMs = bucketS !== undefined ? bucketS * 1000 : undefined;
  const extent = timeExtent(probes);
  const pad = (bucketMs ?? 0) / 2 || 1;

  const probeOption = buildLineOption({
    series: probes,
    order: probeOrder,
    colorOf: (name) => colorForModel(name.split(" · ")[0]!, models),
    dashed: (name) => name.includes("3800"),
    muted: (name) => !name.includes("3800"),
    unit: "ms",
    bucketMs,
    xRange: extent ? [extent[0] - pad, extent[1] + pad] : undefined,
    compact: true,
  });

  const byModel = new Map(prefill?.current.map((c) => [c.model_id, c]) ?? []);
  const prevByModel = new Map(
    prefill?.previous.map((c) => [c.model_id, c]) ?? [],
  );
  const censored = (prefill?.current ?? []).reduce((n, c) => n + c.censored, 0);

  return (
    <Card
      title="What a long prompt costs"
      subtitle={`The extra time to first token a ~3800-token prompt costs over a ~34-token one, per model, over the last ${windowKey}. Both are measured in the SAME probe cycle, seconds apart, so the endpoint's queueing cancels out instead of drowning the difference. Lower is better.`}
    >
      <div className="grid gap-5 sm:grid-cols-2">
        {models.map((m) => {
          const c = byModel.get(m);
          const prev = prevByModel.get(m);
          return (
            <Figure
              key={m}
              label={m}
              value={
                c?.p50_ms !== null && c?.p50_ms !== undefined
                  ? `+${formatMs(c.p50_ms)}`
                  : "—"
              }
              sufficient={c?.sufficient ?? false}
              n={c?.pairs ?? 0}
              hint={hintFor(c, prev, windowKey, prefill?.min_pairs)}
            />
          );
        })}
      </div>

      {/* Not the chart CensoredNote: there is no shading here to explain. Same
          doctrine though — the pairs this counts were cut off at the SLOW end,
          so the figure above improves as truncation worsens, and a reader who
          cannot see the count cannot tell a fast window from a truncated one. */}
      {censored > 0 && (
        <p className="mt-4 text-label text-muted">
          {censored} {censored === 1 ? "pair was" : "pairs were"} cut off by the
          timeout limits and left out of the figures above. Those are the slow
          ones, so the numbers read better than the window was.
        </p>
      )}

      {hasProbes ? (
        <div className="mt-5 border-t border-border pt-4">
          <div className="mb-2 flex flex-col items-start gap-2 sm:flex-row sm:justify-between sm:gap-4">
            <p className="text-label text-muted">
              The two probes themselves, over time — the upper line is the wait
              on a long prompt, which is what the figures above are a slice of.
            </p>
            {probeOption.logScale ? <LogScaleChip /> : null}
          </div>
          <EChart
            option={probeOption}
            height={REFERENCE_HEIGHT}
            ariaLabel="Time to first token for the short and wide probes, per model"
          />
          {/* The swatches belong to the strip, which is the only thing on this
              card drawn in model colour now that the delta chart is gone. The
              figures above are labelled in words, so without this row a reader
              has no way to tell which line is which model. */}
          <ul className="mt-2 flex flex-wrap gap-4">
            {models.map((m) => (
              <li
                key={m}
                className="flex items-center gap-2 text-label text-muted"
              >
                <span
                  className="inline-block h-2 w-4 rounded-sm"
                  style={{ background: colorForModel(m, models) }}
                  aria-hidden="true"
                />
                <span className="num">{m}</span>
              </li>
            ))}
            <li className="flex items-center gap-2 text-label text-muted">
              <span
                className="inline-block h-0 w-4 shrink-0 border-t-2 border-dashed border-muted"
                aria-hidden="true"
              />
              <span>
                dashed = wide probe (~3800 tok); faint = the short probe it is
                measured against
              </span>
            </li>
          </ul>
        </div>
      ) : (
        <NoChart height={REFERENCE_HEIGHT}>
          Not enough data yet — first samples within a few minutes.
        </NoChart>
      )}
    </Card>
  );
}

// hintFor is the line under the figure: what it is a share of, and whether it
// moved.
export function hintFor(
  c: PrefillCost | undefined,
  prev: PrefillCost | undefined,
  windowKey: string,
  minPairs: number | undefined,
): string {
  if (!c) return "";
  // Suppressed. The Figure already prints "insufficient data (n)", so this says
  // WHY that many is not enough — which is a fact about the probe cadence the
  // reader can act on, where a bare blank is not. The wide probe runs hourly,
  // so a 24h window structurally holds 24 pairs however long the site has been
  // up; only a longer window fixes it.
  if (!c.sufficient) {
    return minPairs !== undefined
      ? `needs ${minPairs} paired runs; the wide probe runs hourly, so try a longer window`
      : "needs more paired runs";
  }

  const parts: string[] = [];
  if (c.lo_ms !== null && c.hi_ms !== null) {
    parts.push(`${formatMs(c.lo_ms)} to ${formatMs(c.hi_ms)}`);
  }
  // The share. This is the sentence the panel exists for: a cost with no total
  // to sit inside is a number nobody can act on.
  if (c.p50_ms !== null && c.wide_p50_ms) {
    parts.push(
      `${Math.round((c.p50_ms / c.wide_p50_ms) * 100)}% of a ${formatMs(c.wide_p50_ms)} wait`,
    );
  }
  parts.push(`n=${c.pairs}`);
  parts.push(describeChange(c, prev, windowKey));
  return parts.join(" · ");
}

// describeChange compares two periods and refuses to claim a move it cannot
// support.
//
// The intervals have to MISS each other before this says anything changed. On a
// spread this wide the point estimates drift by a hundred milliseconds between
// periods with nothing having happened, and a panel that reports those as
// movement is the same mistake the line chart was: a resolution the data does
// not have, stated confidently. Gate the prose, never the data — the figures
// and their intervals are served either way, and a reader who wants to compare
// them can.
export function describeChange(
  c: PrefillCost,
  prev: PrefillCost | undefined,
  windowKey: string,
): string {
  if (
    !prev?.sufficient ||
    prev.lo_ms === null ||
    prev.hi_ms === null ||
    c.lo_ms === null ||
    c.hi_ms === null ||
    c.p50_ms === null ||
    prev.p50_ms === null
  ) {
    return `no comparable previous ${windowKey}`;
  }
  const overlap = c.lo_ms <= prev.hi_ms && prev.lo_ms <= c.hi_ms;
  if (overlap) {
    return `unchanged from the previous ${windowKey}`;
  }
  const move = c.p50_ms - prev.p50_ms;
  return `${move > 0 ? "up" : "down"} ${formatMs(Math.abs(move))} from the previous ${windowKey}`;
}
