import { useEffect, useMemo, useState } from "react";
import type { Failure } from "./api/types";
import { FAULT_ROUTE } from "./api/types";
import { colorForModel } from "./charts/options";
import { unattributableFault } from "./verdict";
import { Card } from "./ui";
import { formatAgo, formatDateTime } from "./format";

// The last few runs that went wrong, in full.
//
// Wrong in both senses the probe records, and that pairing is the point. A call
// that never completed and a call that came back with the wrong answer are the
// two ways MiMo fails to do its job, and the pulse strip above already draws
// them on ONE timeline — red for the first, amber for the second. This card is
// where a reader goes when a bar catches their eye, so it has to answer for
// both bars. It used to answer only for red: the query filtered on ok = 0, a
// graded-wrong run carries ok = 1, and every amber bar on the strip therefore
// led here to nothing.
//
// The two kinds are told apart on the row rather than by omission — amber
// against red, and a line of prose saying which happened. See wrongAnswer()
// below.
//
// It sits above the raw table rather than inside it because it answers a
// different question. The table is the unaggregated record of what happened
// over the last three quarters of an hour, and on a healthy endpoint every row
// in it is a tick — the one row that failed yesterday is not in it at all. This
// block reaches back a fixed day and shows only what went wrong, so "what went
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

// What a graded-wrong run is called in the Error column.
//
// Not in CLASS_GLOSS, and not sent by the server either: that map is keyed on
// error_class, and a graded-wrong run has no error_class because nothing about
// the CALL went wrong. Minting a synthetic class server-side would put a word
// on the wire that no log line and no error_class column ever contains, and the
// whole value of that column is that it matches a log line. So the name is the
// card's, made here, in the same snake_case the real classes use — the column
// reads as one vocabulary, and the prose lives on the line beneath as it does
// for every other row.
const WRONG_ANSWER_CLASS = "wrong_answer";
const WRONG_ANSWER_GLOSS = "the call succeeded — the answer did not";

// A run that came back and was graded wrong.
//
// answer_ok is only ever non-null on a run that SUCCEEDED — the probe grades
// what it received, and a failed call received nothing — so false here means
// exactly one thing: 200, a body, and the wrong content in it. That is the
// amber bar on the strip, and this is the predicate that makes the row match
// it.
function wrongAnswer(f: Failure): boolean {
  return f.answer_ok === false;
}

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
// two classes are not the same sentence: uplink is "nothing at the far end
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
        text: "the route to the far end — not attributable to MiMo",
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
// two would make the page assert "nothing went wrong in the last 24 hours" before
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
  const modelIds = useMemo(
    () => (failures ? [...new Set(failures.map((f) => f.model_id))] : []),
    [failures],
  );

  return (
    <Card
      title="Most recent errors"
      subtitle="Only the runs that went wrong, from the last 24 hours whichever range is selected above — the calls that failed, and the calls that came back with the wrong answer, which are marked as such. Each carries the status code the endpoint answered with, where it got that far. A run that failed while nothing at the far end was reachable — or with no route to it — says so: it is listed, but it is not MiMo's."
    >
      {failures === null ? (
        // Nothing has answered yet. Neutral wording, matching the raw table
        // below: it says the page has no reading, not that the reading is
        // clean.
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within a few minutes.
        </p>
      ) : failures.length > 0 ? (
        // `relative` for the same reason as the raw table's wrapper: anything
        // absolutely positioned inside a scroller escapes it unless the
        // scroller is the containing block, and then holds the page open at the
        // table's full width. Nothing here is positioned today — this keeps it
        // that way for the next cell that needs a visually-hidden label.
        // Bled to the card's edges below sm, so a row that continues past the
        // screen is cut off AT the screen rather than at a margin — on a phone
        // the margin read as the end of the table. The padding is put back
        // inside the scroller, so the first column still lines up with the
        // prose above it and the last one is not flush against the edge.
        <div className="relative -mx-4 overflow-x-auto px-4 sm:mx-0 sm:px-0">
          {/* Narrower than the raw table: five columns, and one of them is a
              class with a line of prose under it rather than a figure. */}
          <table className="w-full min-w-[520px] text-label">
            <thead>
              <tr className="text-micro uppercase tracking-wider text-ghost">
                <th className="py-2 pr-4 text-left font-medium">When</th>
                <th className="py-2 pr-4 text-left font-medium">Model</th>
                <th className="py-2 pr-4 text-left font-medium">Error</th>
                <th className="py-2 text-right font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {failures.map((f, i) => {
                const note = attributionNote(f.fault);
                const wrong = wrongAnswer(f);
                // Attribution outranks the grader for the COLOUR and the note,
                // and for nothing else. The two can co-occur: the fault is the
                // cycle's, recorded from the net probes, and a cycle whose two
                // Singapore probes went dark can still have carried an HTTPS
                // call that completed with 200 and a wrong answer in it. Such a
                // row must say whose outage it was — that is the claim the
                // reader needs first — but it must not go on to call a run that
                // demonstrably returned 200 a failure. So the amber is
                // surrendered and the WORD is not.
                const mutedByFault = note !== null;
                return (
                  <tr
                    key={`${f.at}-${f.model_id}-${i}`}
                    className="border-t border-border-soft align-top text-muted"
                  >
                    <td className="num py-2 pr-4 whitespace-nowrap">
                      <time dateTime={f.at} title={formatDateTime(f.at)}>
                        {formatAgo(f.at, now)}
                      </time>
                    </td>
                    <td className="num py-2 pr-4">
                      <span style={{ color: colorForModel(f.model_id, modelIds) }}>
                        {f.model_id}
                      </span>
                    </td>
                    <td className="py-2 pr-4">
                      {/* Three colours, three claims.

                        Red means "MiMo failed", and it is the only one of the
                        three that says the endpoint was down.

                        Amber is the graded-wrong run, and it is the SAME token
                        the pulse strip paints that cycle with
                        (--color-fault-edge) so the bar and the row read as one
                        event. Red here would claim an outage that demonstrably
                        did not happen — the call returned 200.

                        Muted is the run nobody could pin on MiMo: during our
                        own uplink outage the run genuinely failed, but the
                        evidence stops at our own edge, with the label below
                        saying why. It outranks the amber — but only the
                        colour. The WORD still says what happened, because
                        "failed" on a run that returned 200 would assert a
                        transport failure that did not occur. */}
                      <span
                        className={
                          mutedByFault
                            ? "num text-muted"
                            : wrong
                              ? "num text-fault-edge"
                              : "num text-danger"
                        }
                      >
                        {wrong
                          ? WRONG_ANSWER_CLASS
                          : (f.error_class ?? "failed")}
                      </span>
                      {note ? (
                        <span
                          className={`mt-[2px] block text-micro ${note.cls}`}
                        >
                          {note.text}
                        </span>
                      ) : wrong ? (
                        <span className="mt-[2px] block text-micro text-faint">
                          {WRONG_ANSWER_GLOSS}
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
                        that. Printing 0 would read as a status code.

                        A graded-wrong run is the opposite case and prints its
                        real status, which is normally 200 — and that is the
                        whole row in one cell: it worked, and it was still
                        wrong. */}
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
          Nothing failed and nothing was answered wrong in the last 24 hours.
        </p>
      )}
    </Card>
  );
}
