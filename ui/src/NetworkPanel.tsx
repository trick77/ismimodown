import type { NetSeries } from "./api/types";
import { TARGET_MIMO, TARGET_REF_SGP } from "./api/types";
import { Card, LogScaleChip } from "./ui";
import { EChart } from "./charts/EChart";
import {
  buildLineOption,
  MIMO_EDGE_COLOR,
  REFERENCE_COLOR,
  WIRE_COLOR,
} from "./charts/options";

const LABELS: Record<string, string> = {
  [TARGET_MIMO]: "MiMo edge (Singapore)",
  [TARGET_REF_SGP]: "Reference (Singapore)",
};

// The reference line is faint; MiMo's own edge is the one being read. The edge
// carries a hue and the reference stays neutral, so the two separate on sight
// rather than on the legend — see MIMO_EDGE_COLOR for why a green is acceptable
// on a chart that measures no health, and REFERENCE_COLOR for the contrast
// floor that stops the reference going darker still.
const COLORS: Record<string, string> = {
  [TARGET_MIMO]: MIMO_EDGE_COLOR,
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
          Not enough data yet — first samples within a few minutes.
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
