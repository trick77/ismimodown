import { useEffect, useState } from "react";
import type { Failure } from "./api/types";
import { FAULT_ROUTE } from "./api/types";
import { unattributableFault } from "./verdict";
import { Card } from "./ui";
import { formatAgo, formatDateTime } from "./format";

// The last few failures, in full.
//
// It sits above the raw table rather than inside it because it answers a
// different question. The table is the unaggregated record of what happened
// over the last three quarters of an hour, and on a healthy endpoint every row
// in it is a tick — the one row that failed yesterday is not in it at all. This
// block reaches back a fixed day and shows only the failures, so "what went
// wrong recently" stops depending on whether anything went wrong recently
// enough to still be on screen.
//
// It is also the only place the HTTP status surfaces, which is what separates a
// 429 from a 503 when both arrive under the class "http_error". What the
// endpoint SAID stays out: that text is the upstream's own bytes, it can echo
// the request back, and it belongs in the daemon's logs rather than on a public
// page. The class and the status are the daemon's own vocabulary, and between
// them they name the failure without quoting anyone.
//
// And it is the only public surface that neither excludes unattributable
// cycles nor aggregates them — so it is the only one that has to label them on
// the row. See attributionNote() below for why they are labelled rather than
// dropped.

// How often the ages are recomputed, matching SamplesTable: half a minute is
// under the resolution the column prints, so no row is visibly stale by a unit
// it could have shown.
const TICK_MS = 30_000;

// What each error class means, in the words a reader would use.
//
// The class itself is the wire vocabulary and stays visible — it is what
// matches a log line — but "ttft_timeout" does not say "the request was
// accepted and then queued", and that distinction is the whole reason the
// probe classifies at its granularity instead of calling everything "timeout".
//
// Keyed on the class the server sends. A class with no entry gets no gloss
// rather than a placeholder, which is the honest outcome for a failure mode
// this card has not heard of yet.
const CLASS_GLOSS: Record<string, string> = {
  connect_timeout: "the handshake never completed",
  dns_error: "the hostname did not resolve",
  header_timeout: "connected, then no response headers",
  ttft_timeout: "accepted, then no first token — queueing",
  stalled: "the stream started, then went silent",
  timeout: "the overall deadline, past every other bound",
  http_error: "a non-2xx response",
  rate_limited: "429 — throttled, not broken",
  auth_error: "401/403 — our credential, not their outage",
  connection_refused: "the port refused the connection",
  protocol_error: "the response was not a stream we can parse",
  canceled: "shutdown, not a fault",
};

// A failure nobody could pin on MiMo.
//
// Both classes travel together everywhere in this codebase — route is no longer
// produced, but stored cycles carry it, and handling only uplink would silently
// misread them. The predicate itself is unattributableFault() in verdict.ts, so
// the two surfaces cannot drift apart.
//
// The row stays in the list. The availability arithmetic drops these from its
// denominator because it is computing a claim ABOUT MiMo, and a failure during
// our own outage is not evidence for it. This card is not computing a claim —
// it is reporting what happened — and a monitor that goes quiet when the
// network dies would hide the very incident it exists to show. So the run is
// listed and the row says who it belongs to.
//
// ...and it says it in the vocabulary that layer already has on this page. The
// two classes are not the same sentence: uplink is "nothing in Singapore
// answered", route is the path between here and there, which is neither ours
// nor MiMo's. Naming a route cycle "our uplink" would claim a different outage
// than the one the attribution recorded, and it would draw it in the uplink
// colour while --color-fault-route exists precisely to tell them apart. The
// wording tracks the verdict banner's — see verdict.ts.
//
// null for a fault the row CAN hold MiMo to, which is what the ordinary gloss
// and the red then belong to.
function attributionNote(fault: string): { text: string; cls: string } | null {
  if (!unattributableFault(fault)) return null;
  return fault === FAULT_ROUTE
    ? {
        text: "the route to Singapore — not attributable to MiMo",
        cls: "text-fault-route",
      }
    : {
        text: "our uplink — not attributable to MiMo",
        cls: "text-fault-uplink",
      };
}

// null is NOT the empty list, and the difference is the whole card. An empty
// list is evidence — the server looked and found nothing — and the sentence it
// earns is a claim about the endpoint's health. null is the absence of
// evidence: the first load has not answered yet, or it failed. Collapsing the
// two would make the page assert "nothing failed in the last 24 hours" before
// it had asked anyone, on a page whose entire job is saying whether MiMo is
// down. Every other panel takes its no-data case as null for the same reason.
export function RecentErrors({ failures }: { failures: Failure[] | null }) {
  // The browser's clock, on the same timer and for the same reason as the raw
  // table: "ago" is a statement about the reader's own now.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(id);
  }, []);

  return (
    <Card
      title="Most recent errors"
      subtitle="Only the failed calls, from the last 24 hours whichever range is selected above — each with the status code the endpoint answered with, where it got that far. A run that failed while nothing in Singapore was reachable — or with no route to it — says so: it is listed, but it is not MiMo's."
    >
      {failures === null ? (
        // Nothing has answered yet. Neutral wording, matching the raw table
        // below: it says the page has no reading, not that the reading is
        // clean.
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within a few minutes.
        </p>
      ) : failures.length > 0 ? (
        <div className="overflow-x-auto">
          {/* Narrower than the raw table: five columns, and one of them is a
              class with a line of prose under it rather than a figure. */}
          <table className="w-full min-w-[520px] text-label">
            <thead>
              <tr className="text-micro uppercase tracking-wider text-ghost">
                <th className="py-2 pr-4 text-left font-medium">When</th>
                <th className="py-2 pr-4 text-left font-medium">Model</th>
                <th className="py-2 pr-4 text-left font-medium">Probe</th>
                <th className="py-2 pr-4 text-left font-medium">Error</th>
                <th className="py-2 text-right font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {failures.map((f, i) => {
                const note = attributionNote(f.fault);
                return (
                  <tr
                    key={`${f.at}-${f.model_id}-${f.probe}-${i}`}
                    className="border-t border-border-soft align-top text-muted"
                  >
                    <td className="num py-2 pr-4 whitespace-nowrap">
                      <time dateTime={f.at} title={formatDateTime(f.at)}>
                        {formatAgo(f.at, now)}
                      </time>
                    </td>
                    <td className="num py-2 pr-4">{f.model_id}</td>
                    <td className="num py-2 pr-4">{f.probe}</td>
                    <td className="py-2 pr-4">
                      {/* Not red when nothing in Singapore answered. The colour
                        on this column means "MiMo failed", and during our own
                        outage that is the one thing the row cannot claim — the
                        run genuinely failed, but the evidence stops at our
                        uplink. Muted, with the label below saying why. */}
                      <span
                        className={note ? "num text-muted" : "num text-danger"}
                      >
                        {f.error_class ?? "failed"}
                      </span>
                      {note ? (
                        <span
                          className={`mt-[2px] block text-micro ${note.cls}`}
                        >
                          {note.text}
                        </span>
                      ) : (
                        f.error_class &&
                        CLASS_GLOSS[f.error_class] && (
                          <span className="mt-[2px] block text-micro text-faint">
                            {CLASS_GLOSS[f.error_class]}
                          </span>
                        )
                      )}
                    </td>
                    <td className="num py-2 text-right">
                      {/* A transport failure never got a status, and a dash says
                        that. Printing 0 would read as a status code. */}
                      {f.http_status === null ? (
                        <span className="text-ghost">—</span>
                      ) : (
                        f.http_status
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        // A card that renders nothing when nothing failed is indistinguishable
        // from a card that broke, so the quiet state says so in words. This is
        // also the good news, and it is worth stating.
        <p className="font-serif italic text-faint">
          Nothing failed in the last 24 hours.
        </p>
      )}
    </Card>
  );
}
