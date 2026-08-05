import type { NetSeries } from "./api/types";
import { TARGET_MIMO, TARGET_REF_SGP } from "./api/types";
import { Card, LogScaleChip } from "./ui";
import { EChart } from "./charts/EChart";
import { buildLineOption, REFERENCE_COLOR, WIRE_COLOR } from "./charts/options";

const LABELS: Record<string, string> = {
  [TARGET_MIMO]: "MiMo edge (Singapore)",
  [TARGET_REF_SGP]: "Reference (Singapore)",
};

// The reference line is faint; MiMo's own edge is the one being read. Both are
// drawn in neutral ink rather than series colours, because neither of them is
// a model — and the reference gets the darker of the two neutrals, so the two
// lines separate on sight rather than on the legend.
const COLORS: Record<string, string> = {
  [TARGET_MIMO]: "#faf9f5",
  [TARGET_REF_SGP]: REFERENCE_COLOR,
};

export function NetworkPanel({ series }: { series: NetSeries | null }) {
  const targets = series?.targets ?? {};
  const relabelled: Record<string, (typeof targets)[string]> = {};
  const order: string[] = [];
  for (const key of [TARGET_MIMO, TARGET_REF_SGP]) {
    const points = targets[key];
    if (points && points.length) {
      relabelled[LABELS[key]!] = points;
      order.push(LABELS[key]!);
    }
  }

  // Built unconditionally, like SeriesPanel does, so the header can say
  // whether the axis went logarithmic before the chart itself is rendered.
  const option = buildLineOption({
    series: relabelled,
    order,
    colorOf: (name) => {
      const key = Object.keys(LABELS).find((k) => LABELS[k] === name);
      return key ? COLORS[key]! : WIRE_COLOR;
    },
    unit: "ms",
  });

  return (
    <Card
      title="The wire itself"
      subtitle="Time to complete the TCP handshake on port 443 — no TLS, no HTTP, no auth, no tokens. The reference host is what keeps a route problem, or an outage of our own, from being published as a MiMo outage. Lower is better."
      right={option.logScale ? <LogScaleChip /> : null}
    >
      {order.length > 0 ? (
        <EChart
          option={option}
          ariaLabel="TCP handshake time to MiMo's edge and the reference host"
        />
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
      )}
      <ul className="mt-3 flex flex-wrap gap-4">
        {order.map((name) => {
          const key = Object.keys(LABELS).find((k) => LABELS[k] === name)!;
          return (
            <li
              key={name}
              className="flex items-center gap-2 text-label text-muted"
            >
              <span
                className="inline-block h-2 w-4 rounded-sm"
                style={{ background: COLORS[key] }}
                aria-hidden="true"
              />
              <span>{name}</span>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}
