import type { Sample } from "./api/types";
import { Card } from "./ui";
import { formatMs, formatTime, formatTps } from "./format";

// How many cycles the table renders.
//
// The card's job is "what happened just now", and a wall of numbers nobody
// scrolls to the end of does not answer it better than ten rows do — the pulse
// strip above is what covers the whole day.
//
// App asks the server for exactly this many, so the slice below is normally a
// no-op. It stays because the cap is the table's own rule: whatever it is
// handed, this is what it draws.
const ROWS = 10;

// Raw cycles, nothing aggregated away — the one place on the page a screen
// reader gets numbers rather than a canvas with a summary label.
//
// It used to claim to be the accessible alternative to every chart above. It
// never quite was: the charts run to 3 months across both models, and this has
// only ever held the infer probe for one. Now that it stops at ten rows the
// claim is plainly false, so it is not made. The charts' own aria-labels are
// what carry them, and closing that gap properly means giving each chart its
// own tabular alternative, not making this table longer than anyone reads.
export function SamplesTable({ samples }: { samples: Sample[] }) {
  // Samples arrive newest-first from the API, so the head is the recent end.
  const rows = samples.slice(0, ROWS);
  // "At most" rather than a flat count: a fresh database has two cycles, and a
  // subtitle claiming ten while showing two is wrong in exactly the situation
  // where the reader is least sure what they are looking at.
  const subtitle = `The last ${ROWS} runs at most, unaggregated. Failed runs show their error class.`;
  return (
    <Card title="Raw cycles" subtitle={subtitle}>
      {rows.length > 0 ? (
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
              {rows.map((s, i) => (
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
                      // A tick reads faster than the word down a column of
                      // rows, but a bare glyph is not a word to a screen
                      // reader — so it keeps saying "ok".
                      <span className="text-online">
                        <span aria-hidden="true">✓</span>
                        <span className="sr-only">ok</span>
                      </span>
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
