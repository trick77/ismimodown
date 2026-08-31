import type { NetSeries } from "./api/types";
import {
  TARGET_MIMO,
  TARGET_REF_SGP,
  TARGET_MIMO_AMS,
  TARGET_REF_AMS,
} from "./api/types";
import { Card, LogScaleChip, NoChart } from "./ui";
import { EChart } from "./charts/EChart";
import {
  buildLineOption,
  CHART_HEIGHT,
  MIMO_EDGE_COLOR,
  MIMO_EDGE_AMS_COLOR,
  REFERENCE_COLOR,
  REFERENCE_AMS_COLOR,
  WIRE_COLOR,
} from "./charts/options";

// Region named in every label, on both halves of every pair. "MiMo edge" and
// "Reference" unqualified would be two ambiguous labels on a chart whose whole
// point is the comparison between regions.
const LABELS: Record<string, string> = {
  [TARGET_MIMO]: "MiMo edge (Singapore)",
  [TARGET_REF_SGP]: "Reference (Singapore)",
  [TARGET_MIMO_AMS]: "MiMo edge (Amsterdam)",
  [TARGET_REF_AMS]: "Reference (Amsterdam)",
};

// Role is hue-versus-ink, region is which one: an edge carries a colour and a
// reference is grey, so a reader can tell a measurement from its control
// without the legend, and the violet/teal and dark/light-grey split then says
// which region each belongs to. See MIMO_EDGE_AMS_COLOR for the measured
// separations, MIMO_EDGE_COLOR for why the accent family could not supply these
// hues, and REFERENCE_COLOR for the contrast floor that forced Amsterdam's grey
// to be the lighter of the two rather than the darker.
const COLORS: Record<string, string> = {
  [TARGET_MIMO]: MIMO_EDGE_COLOR,
  [TARGET_REF_SGP]: REFERENCE_COLOR,
  [TARGET_MIMO_AMS]: MIMO_EDGE_AMS_COLOR,
  [TARGET_REF_AMS]: REFERENCE_AMS_COLOR,
};

export function NetworkPanel({ series }: { series: NetSeries | null }) {
  const targets = series?.targets ?? {};
  const relabelled: Record<string, (typeof targets)[string]> = {};
  const order: string[] = [];
  // Edge before its reference, Singapore before Amsterdam: Singapore is where
  // inference actually goes, so it leads, and each edge sits next to the control
  // it is read against rather than the two edges being grouped together.
  for (const key of [
    TARGET_MIMO,
    TARGET_REF_SGP,
    TARGET_MIMO_AMS,
    TARGET_REF_AMS,
  ]) {
    const points = targets[key];
    if (points && points.length) {
      relabelled[LABELS[key]!] = points;
      order.push(LABELS[key]!);
    }
  }

  // Built unconditionally, like SeriesPanel does, so the header can say
  // whether the axis went logarithmic before the chart itself is rendered.
  //
  // This panel now sits on a LOG axis nearly always, and that is expected
  // rather than a symptom. shouldUseLogScale trips at a 20x dynamic range
  // across every series at once, and probing two regions from one European box
  // clears that on ordinary data: Singapore lands around 170-270 ms and
  // Amsterdam in the low tens, a ratio in the twenties or thirties before
  // anything has gone wrong.
  //
  // Left on deliberately. Forcing linear is the tempting fix and it is the
  // wrong one — a linear axis spanning 0 to 280 ms puts the two Amsterdam lines
  // about four pixels apart, which deletes that pair's reading entirely, while
  // log keeps both pairs legible (roughly 30 px and 20 px on a 240 px plot).
  //
  // What it does cost: LOG_SCALE_THRESHOLD was chosen to catch an ANOMALY — a
  // normal reading going flat against a spike — so a permanently-lit chip says
  // "unusual" about a chart whose spread is now structural. The chip is honest
  // about the axis either way, which is the property that actually matters. If
  // that reads as noise, the fix is a per-panel threshold or a split axis, not
  // forceLinear.
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
      subtitle="Time to complete the TCP handshake on port 443 — no TLS, no HTTP, no auth, no tokens. Each Xiaomi MiMo edge is paired with an independent reference host in the same city, so a route problem, or an outage on our side, shows up as its own problem and not as MiMo's. Only Singapore serves the inference this page measures; Amsterdam is the same service from another region, for comparison. Lower is better."
      right={option.logScale ? <LogScaleChip /> : null}
    >
      {order.length > 0 ? (
        <EChart
          option={option}
          ariaLabel="TCP handshake time to MiMo's Singapore and Amsterdam edges and their reference hosts"
        />
      ) : (
        <NoChart height={CHART_HEIGHT}>
          Not enough data yet — first samples within a few minutes.
        </NoChart>
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
