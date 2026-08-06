import { useEffect, useState } from "react";
import type { Sample } from "./api/types";
import { Card } from "./ui";
import {
  formatAgo,
  formatDateTime,
  formatInt,
  formatMs,
  formatTps,
} from "./format";

// How many runs the table renders.
//
// The card's job is "what happened just now", and a wall of numbers nobody
// scrolls to the end of does not answer it better than a screenful does — the
// pulse strip above is what covers the whole day.
//
// A screenful, and deliberately still 20 now that the table draws every model
// rather than one.
//
// The cap counts ROWS, but what a reader takes from it is a stretch of TIME,
// and those two only track each other while the row rate is fixed. Two models
// at 12 short runs an hour each, plus a wide run an hour each, is ~26 rows an
// hour, so 20 rows is ~45 minutes with one wide run in it — where the same cap
// covered ~90 minutes when the table held a single model. That halving is the
// price of showing every call, and it is the one being paid: the card answers
// "what happened just now", and the pulse strip above is what covers the day.
//
// The daemon supplies dashboardSampleLimit rows per model and probe, which is
// four times this in total, so the slice below does real work rather than being
// the no-op it was when the table fetched one series.
//
// Keep the two numbers equal. A supply below this cap would not error — it
// would quietly shorten how far back the table reaches, which is the one thing
// the paragraph above exists to keep visible.
const ROWS = 20;

// newestFirst merges one array of samples per model and probe kind into the
// single ordering the table draws.
//
// Ordered on the parsed instant, never on the timestamp as text. The daemon
// stamps cycles with Go's RFC3339Nano, which DROPS trailing zeros from the
// fraction, so "12:00:00Z" and "12:00:00.5Z" sort the wrong way round as
// strings — "." precedes "Z". Cycles are five minutes apart so the difference
// lands in an earlier field today and the bug cannot fire; the cadence is not
// something this function should have to know.
//
// The tie-break is not cosmetic, and it grew with the table. Every run in a
// cycle is stamped with that CYCLE'S instant rather than its own, so an ordinary
// cycle puts two rows on the same timestamp and a wide cycle puts three. That
// holds however the daemon dispatches them — the runs are serialised now, and
// the rows still tie, because the stamp was never the dispatch time. Probe
// alone stopped being a total order the moment the second model arrived — the
// two short runs would tie and swap places between renders, on a table a reader
// is scanning down. Model, then probe: both sorts are stable and total, so a run
// sits in the same place on every load.
export function newestFirst(perGroup: Sample[][]): Sample[] {
  // Parsed once per sample rather than twice per comparison: the merge runs on
  // every stream event, and Date.parse is the expensive part of the sort.
  return perGroup
    .flat()
    .map((s) => ({ s, t: Date.parse(s.at) }))
    .sort((a, b) => {
      if (b.t !== a.t) return b.t - a.t;
      const byModel = a.s.model_id.localeCompare(b.s.model_id);
      return byModel !== 0 ? byModel : a.s.probe.localeCompare(b.s.probe);
    })
    .map(({ s }) => s);
}

// Raw cycles, nothing aggregated away — the one place on the page a screen
// reader gets numbers rather than a canvas with a summary label.
//
// It used to claim to be the accessible alternative to every chart above. It
// never quite was, and the reason has now been fixed rather than restated: the
// charts run to 3 months and this holds a couple of hours. The span is what
// still separates them, not the coverage. The charts' own aria-labels are what
// carry them, and closing that gap properly means giving each chart its own
// tabular alternative, not making this table longer than anyone reads.
//
// EVERY inference call inside that span, though — both models, both probes.
// The table's whole claim is that nothing is aggregated away, and it was
// quietly dropping three quarters of the runs: the hourly wide probe was never
// requested, and every request named the first model, so the page's only raw
// record showed one probe of one model on a page about two of each.
//
// Three consequences are shown rather than hidden. Every run in a cycle carries
// the cycle's instant, so When repeats down the column and Model and Probe
// beside it are what tell the rows apart. Wide has no single assertable answer,
// so it is never graded and its Answer cell is a dash. And In jumps ~200x
// between the probes — that step IS the difference between them, not an anomaly.
//
// Tokens sits between Total and Throughput because that is the order the four
// columns explain each other in: how long the run took, what went in, what came
// out, and the rate the last two imply. Throughput on its own is the number
// that misleads — tok/s is measured over the decode window, so a 40-token reply
// and a 400-token one can post the same rate while taking wildly different
// times, and only the counts say which happened. Cached and reasoning tokens
// stay out: both are invariants pinned at zero rather than per-run
// measurements, and the model cards are where a breach of either surfaces.

// How often the ages are recomputed.
//
// The rows themselves only change when a cycle completes, five minutes apart,
// and a column of distances that only moves when new data arrives would sit
// there reading "3 min ago" for the whole gap. Half a minute is under the
// resolution the column prints, so no row is ever visibly stale by a unit it
// could have shown.
const TICK_MS = 30_000;

export function SamplesTable({ perGroup }: { perGroup: Sample[][] }) {
  const rows = newestFirst(perGroup).slice(0, ROWS);
  // The clock the ages are measured against, re-read on the timer above.
  //
  // The browser's, not the daemon's generated_at, and this is the one place
  // that choice goes the other way from isStale in verdict.ts. That function
  // refuses the client clock on purpose — a skewed browser must not be able to
  // manufacture an outage — but "ago" is a statement about the reader's own
  // now, and measuring it from a payload instant would freeze every row at the
  // age it had when the page loaded. Skew costs a minute of accuracy in a
  // column whose smallest unit is a minute; the alternative costs the column
  // its meaning.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(id);
  }, []);
  // No count at all, so there is nothing to hedge.
  //
  // It used to name ROWS and qualify it with "at most" — the qualifier was
  // load-bearing, because a fresh database holds two cycles and a subtitle
  // claiming twenty while showing two is wrong in exactly the situation where
  // the reader is least sure what they are looking at. But it read like a typo,
  // and the number was never the thing worth saying: the cap is this card's own
  // housekeeping, while what a reader needs to know is which runs are here.
  // Dropping it removes the hedge and the thing being hedged together.
  const subtitle =
    "The most recent inference calls — every model, short and wide, unaggregated. Failed runs show their error class.";
  return (
    <Card title="Raw cycles" subtitle={subtitle}>
      {rows.length > 0 ? (
        <div className="overflow-x-auto">
          {/* The min-width went 640 → 700 with the Tokens column, and 700 → 860
              with Model and the In/Out split. It is what stops the columns
              being squeezed to the point of wrapping on a phone; the wrapper
              scrolls instead. */}
          <table className="w-full min-w-[860px] text-label">
            <thead>
              <tr className="text-micro uppercase tracking-wider text-ghost">
                {/* "When", not "Time": the column stopped being a clock
                    reading and a header promising one would be the only thing
                    on the card still saying so. */}
                <th className="py-2 pr-4 text-left font-medium">When</th>
                <th className="py-2 pr-4 text-left font-medium">Model</th>
                <th className="py-2 pr-4 text-left font-medium">Probe</th>
                <th className="py-2 pr-4 text-right font-medium">TTFT</th>
                <th className="py-2 pr-4 text-right font-medium">Total</th>
                <th className="py-2 pr-4 text-right font-medium">In</th>
                <th className="py-2 pr-4 text-right font-medium">Out</th>
                <th className="py-2 pr-4 text-right font-medium">Throughput</th>
                <th className="py-2 pr-4 text-left font-medium">Answer</th>
                <th className="py-2 text-left font-medium">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((s, i) => (
                <tr
                  key={`${s.at}-${s.model_id}-${s.probe}-${i}`}
                  className="border-t border-border-soft text-muted"
                >
                  <td className="num py-2 pr-4">
                    {/* dateTime carries the instant machine-readably; title
                        carries it for a reader who wants the clock reading the
                        cell no longer prints. Both are the raw stamp's job —
                        the visible text is a distance, and a distance cannot
                        be matched against a log line somewhere else. */}
                    <time dateTime={s.at} title={formatDateTime(s.at)}>
                      {formatAgo(s.at, now)}
                    </time>
                  </td>
                  <td className="num py-2 pr-4">{s.model_id}</td>
                  <td className="num py-2 pr-4">{s.probe}</td>
                  <td className="num py-2 pr-4 text-right">
                    {formatMs(s.ttft_ms)}
                  </td>
                  <td className="num py-2 pr-4 text-right">
                    {formatMs(s.total_ms)}
                  </td>
                  <td className="num py-2 pr-4 text-right">
                    {formatInt(s.prompt_tokens)}
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
          Not enough data yet — first samples within a few minutes.
        </p>
      )}
    </Card>
  );
}
