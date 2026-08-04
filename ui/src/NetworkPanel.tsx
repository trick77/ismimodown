import type { NetSeries } from "./api/types";
import { TARGET_MIMO, TARGET_REF_SGP } from "./api/types";
import { Card } from "./ui";
import { EChart } from "./charts/EChart";
import { buildLineOption, WIRE_COLOR } from "./charts/options";

const LABELS: Record<string, string> = {
  [TARGET_MIMO]: "MiMo edge (Singapore)",
  [TARGET_REF_SGP]: "Reference (Singapore)",
};

// Reference lines are faint; MiMo's own edge is the one being read. All three
// are drawn in neutral ink rather than series colours, because none of them is
// a model.
const COLORS: Record<string, string> = {
  [TARGET_MIMO]: "#faf9f5",
  [TARGET_REF_SGP]: WIRE_COLOR,
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

  return (
    <Card
      title="The wire itself"
      subtitle="Time to complete the TCP handshake on port 443 — no TLS, no HTTP, no auth, no tokens. The two reference hosts are what keep a route problem, or an outage of our own, from being published as a MiMo outage."
    >
      {order.length > 0 ? (
        <EChart
          option={buildLineOption({
            series: relabelled,
            order,
            colorOf: (name) => {
              const key = Object.keys(LABELS).find((k) => LABELS[k] === name);
              return key ? COLORS[key]! : WIRE_COLOR;
            },
            unit: "ms",
          })}
          ariaLabel="TCP handshake time to MiMo's edge and the two reference hosts"
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
