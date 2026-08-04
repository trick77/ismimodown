import type { Sample } from "./api/types";
import { Card } from "./ui";
import { formatMs, formatTime, formatTps } from "./format";

// Raw cycles, nothing aggregated away. Also the accessible alternative to every
// chart above: a screen reader gets the numbers, not a canvas.
export function SamplesTable({ samples }: { samples: Sample[] }) {
  return (
    <Card
      title="Raw cycles"
      subtitle="The most recent runs, unaggregated. Failed runs show their error class."
    >
      {samples.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[560px] text-label">
            <thead>
              <tr className="text-micro uppercase tracking-wider text-ghost">
                <th className="py-2 pr-4 text-left font-medium">Time</th>
                <th className="py-2 pr-4 text-right font-medium">TTFT</th>
                <th className="py-2 pr-4 text-right font-medium">Total</th>
                <th className="py-2 pr-4 text-right font-medium">Throughput</th>
                <th className="py-2 pr-4 text-left font-medium">Answer</th>
                <th className="py-2 text-left font-medium">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {samples.map((s, i) => (
                <tr
                  key={`${s.at}-${i}`}
                  className="border-t border-border-soft text-muted"
                >
                  <td className="num py-2 pr-4">{formatTime(s.at)}</td>
                  <td className="num py-2 pr-4 text-right">
                    {formatMs(s.ttft_ms)}
                  </td>
                  <td className="num py-2 pr-4 text-right">
                    {formatMs(s.total_ms)}
                  </td>
                  <td className="num py-2 pr-4 text-right">
                    {formatTps(s.output_tps)}
                  </td>
                  <td className="py-2 pr-4">
                    {s.answer_ok === null ? (
                      <span className="text-ghost">—</span>
                    ) : s.answer_ok ? (
                      <span className="text-online">correct</span>
                    ) : (
                      <span className="text-danger">wrong</span>
                    )}
                  </td>
                  <td className="py-2">
                    {s.ok ? (
                      <span className="text-online">ok</span>
                    ) : (
                      <span className="num text-danger">
                        {s.error_class ?? "failed"}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
      )}
    </Card>
  );
}
