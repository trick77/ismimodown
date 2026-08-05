import type { Sample } from "./api/types";
import { Card } from "./ui";
import {
  formatInt,
  formatMs,
  formatTime,
  formatTps,
  probeName,
} from "./format";

// How many cycles the table renders.
//
// The card's job is "what happened just now", and a wall of numbers nobody
// scrolls to the end of does not answer it better than a screenful does — the
// pulse strip above is what covers the whole day.
//
// App asks the server for exactly this many, so the slice below is normally a
// no-op. It stays because the cap is the table's own rule: whatever it is
// handed, this is what it draws.
const ROWS = 20;

// newestFirst merges one array of samples per probe kind into the single
// ordering the table draws.
//
// Ordered on the parsed instant, never on the timestamp as text. The daemon
// stamps cycles with Go's RFC3339Nano, which DROPS trailing zeros from the
// fraction, so "12:00:00Z" and "12:00:00.5Z" sort the wrong way round as
// strings — "." precedes "Z". Cycles are five minutes apart so the difference
// lands in an earlier field today and the bug cannot fire; the cadence is not
// something this function should have to know.
//
// A stable tie-break matters here in a way it did not when the table held one
// probe: a wide run and the short run beside it share their cycle's timestamp
// exactly, so without one the pair would swap places between renders. Probe
// name, so the order is the same on every load.
export function newestFirst(perProbe: Sample[][]): Sample[] {
  // Parsed once per sample rather than twice per comparison: the merge runs on
  // every stream event, and Date.parse is the expensive part of the sort.
  return perProbe
    .flat()
    .map((s) => ({ s, t: Date.parse(s.at) }))
    .sort((a, b) =>
      b.t !== a.t ? b.t - a.t : a.s.probe.localeCompare(b.s.probe),
    )
    .map(({ s }) => s);
}

// Raw cycles, nothing aggregated away — the one place on the page a screen
// reader gets numbers rather than a canvas with a summary label.
//
// It used to claim to be the accessible alternative to every chart above. It
// never quite was: the charts run to 3 months across both models, and this
// holds one. Now that it stops after a couple of hours of cycles the claim is
// plainly false, so it is not made. The charts' own aria-labels are what carry
// them, and closing that gap properly means giving each chart its own tabular
// alternative, not making this table longer than anyone reads.
//
// Both probes, though. The hourly wide run is stored against the same cycle as
// the short one and was simply never asked for, which left the page's only raw
// record quietly incomplete — the one table that promises nothing is aggregated
// away was aggregating away a whole probe. Two consequences are shown rather
// than hidden: the pair shares a timestamp, so Time repeats and the Probe
// column beside it is what tells the rows apart; and wide has no single
// assertable answer, so it is never graded and its Answer cell is a dash.
//
// Tokens sits between Total and Throughput because that is the order the three
// explain each other in: how long the run took, how much it produced, and the
// rate those two imply. Throughput on its own is the number that misleads —
// tok/s is measured over the decode window, so a 40-token reply and a
// 400-token one can post the same rate while taking wildly different times,
// and only the count says which happened. It is the generated tokens, not the
// prompt or cached ones: those are what the run cost, and the cost panel
// already carries them.
export function SamplesTable({ perProbe }: { perProbe: Sample[][] }) {
  const rows = newestFirst(perProbe).slice(0, ROWS);
  // "At most" rather than a flat count: a fresh database has two cycles, and a
  // subtitle claiming twenty while showing two is wrong in exactly the
  // situation where the reader is least sure what they are looking at.
  const subtitle = `The last ${ROWS} runs at most, short and wide, unaggregated. Failed runs show their error class.`;
  return (
    <Card title="Raw cycles" subtitle={subtitle}>
      {rows.length > 0 ? (
        <div className="overflow-x-auto">
          {/* The min-width went 640 → 700 with the Tokens column. It is what
              stops the columns being squeezed to the point of wrapping on a
              phone; the wrapper scrolls instead. */}
          <table className="w-full min-w-[700px] text-label">
            <thead>
              <tr className="text-micro uppercase tracking-wider text-ghost">
                <th className="py-2 pr-4 text-left font-medium">Time</th>
                <th className="py-2 pr-4 text-left font-medium">Probe</th>
                <th className="py-2 pr-4 text-right font-medium">TTFT</th>
                <th className="py-2 pr-4 text-right font-medium">Total</th>
                <th className="py-2 pr-4 text-right font-medium">Tokens</th>
                <th className="py-2 pr-4 text-right font-medium">Throughput</th>
                <th className="py-2 pr-4 text-left font-medium">Answer</th>
                <th className="py-2 text-left font-medium">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((s, i) => (
                <tr
                  key={`${s.at}-${s.probe}-${i}`}
                  className="border-t border-border-soft text-muted"
                >
                  <td className="num py-2 pr-4">{formatTime(s.at)}</td>
                  <td className="num py-2 pr-4">{probeName(s.probe)}</td>
                  <td className="num py-2 pr-4 text-right">
                    {formatMs(s.ttft_ms)}
                  </td>
                  <td className="num py-2 pr-4 text-right">
                    {formatMs(s.total_ms)}
                  </td>
                  <td className="num py-2 pr-4 text-right">
                    {formatInt(s.output_tokens)}
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
