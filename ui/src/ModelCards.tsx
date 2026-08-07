import type { Summary } from "./api/types";
import { Figure, StateChip } from "./ui";
import { formatMs, formatPct, formatTps, plural } from "./format";
import type { State } from "./verdict";
import {
  MIN_FAILURES_FOR_STATE,
  scoreAvailability,
  scoreCorrectness,
  scoreModelRecent,
  scoreRatio,
  worst,
} from "./verdict";
import { colorForModel } from "./charts/options";

// chipState folds the recent cycles into the three window figures WITHOUT
// letting them answer a question the data cannot.
//
// Not a fourth argument to worst(). scoreModelRecent never returns "unknown" —
// a model absent from the recent block, or a payload with no recent block at
// all, both score normal — and worst() takes normal over unknown, so passing it
// in flat would paint a card green for a model that has no measurements at all.
// A window with no attempts and no baseline is exactly that card, and it read
// "unknown" before this. Absence of evidence is not evidence of health any more
// than it is evidence of failure.
//
// So the recent block can only ever make the chip WORSE, which is the whole
// point of folding it in.
function chipState(
  availability: State,
  correctness: State,
  ttft: State,
  recent: State,
): State {
  const window = worst(availability, correctness, ttft);
  return recent === "normal" ? window : worst(window, recent);
}

// One card per model. Two models, not three.
export function ModelCards({
  summary,
  baseline,
  pending = false,
}: {
  summary: Summary | null;
  baseline: Summary | null;
  // Whether a first response is still on its way. Only then is holding ground
  // for cards a promise the page can keep — see the placeholder below.
  pending?: boolean;
}) {
  const models = summary?.models ?? [];
  const ids = models.map((m) => m.model_id);

  // One empty card holding the height the real ones will need, until there are
  // models to put in them.
  //
  // This grid used to be zero pixels tall and then abruptly full height, above
  // the fold with the entire page below it — the single largest contributor to
  // the layout shift this page scored. Reserving the height is the fix; the
  // surface is so that the reservation reads as a card on its way rather than
  // as a hole in the page.
  //
  // TWO numbers because the grid has two shapes and a card is 242 px tall in
  // both: side by side above the sm breakpoint, one row; stacked below it, two
  // rows and a gap-4 between them. A single 242 was measured on a desktop
  // viewport and left the phone shifting by a whole card — which is the layout
  // PageSpeed grades on mobile.
  //
  // Both numbers assume the two models this probes, and they assume it
  // IDENTICALLY: one row of a two-column grid is the same assumption as two
  // stacked rows. Nothing here can know the count — it is the first thing the
  // response says and this renders before the response — so a third model
  // would leave one row unreserved at either width, rather than one shape
  // being right and the other wrong.
  //
  // Gated on pending rather than on being empty. A load that failed is empty
  // too, and holding 500 px of blank ground for a response that is never coming
  // is not a reservation — it is a hole with a border, sitting under an error
  // message that already said so.
  if (models.length === 0 && pending) {
    return (
      <div
        className="card h-[500px] p-5 sm:h-[242px]"
        aria-hidden="true"
        data-testid="model-cards-pending"
      />
    );
  }

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      {models.map((m) => {
        const base = baseline?.models.find((b) => b.model_id === m.model_id);
        // The counts, not just the percentages. A card describes the selected
        // window, and over a day of cycles one dropped connection is 99.65%
        // available — under the band, and painted as a state. The band decides
        // how bad it is; the count decides whether anything happened at all.
        const availability = scoreAvailability(
          m.attempts > 0 ? m.available_pct : null,
          m.attempts - m.succeeded,
        );
        const correctness = scoreCorrectness(
          m.correct_pct,
          m.answered - m.correct,
        );
        const ttft = scoreRatio(m.ttft.p50_ms, base?.ttft.p50_ms ?? null);
        const recentState = scoreModelRecent(m.model_id, summary?.recent ?? []);

        return (
          <section
            key={m.model_id}
            className="card p-5"
            data-testid={`model-card-${m.model_id}`}
          >
            <header className="mb-4 flex items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <span
                  className="inline-block h-2.5 w-2.5 rounded-sm"
                  style={{ background: colorForModel(m.model_id, ids) }}
                  aria-hidden="true"
                />
                {/* h2, not h3. These cards are the first panels on the page
                    and every Card below them titles itself h2, so an h3 here
                    made the document read h1 → h3 → h2 — a level skipped, and
                    a screen reader's outline that claims each model card is
                    subordinate to nothing. The size is set by the class, not
                    by the tag. */}
                <h2 className="num text-ui text-ink">{m.model_id}</h2>
              </div>
              {/* The recent block joins the three window figures, so a
                  DISCRETE failure — a dropped run, a wrong answer — can never
                  leave this chip greener than the verdict banner above it: the
                  figures describe the SELECTED window behind a floor of
                  MIN_FAILURES_FOR_STATE, which a fault an hour old clears
                  neither. See scoreModelRecent.

                  Latency is not covered by that, and deliberately so. The
                  banner scores TTFT on the fixed `now` window while this scores
                  it on the selected one (App.tsx), so an hour-long spike can
                  still show ELEVATED above a normal card. Pulling `now` in here
                  would fix the chip and break the card: the TTFT figure below
                  it is the selected window's, and a chip contradicting the
                  number it sits over is worse than one contradicting a banner
                  that says "right now" in its own headline.

                  summary.recent, not a new prop: Recent is the one part of a
                  Summary that is NOT window-scoped — the daemon fills it
                  outside the window predicate (backend/internal/samples/
                  queries.go, Summarize) — so this is the identical block the
                  banner reads off `now`, whichever window is selected. */}
              <StateChip
                state={chipState(availability, correctness, ttft, recentState)}
              />
            </header>

            <div className="grid grid-cols-2 gap-4">
              <Figure
                label="TTFT p50"
                value={formatMs(m.ttft.p50_ms)}
                sufficient={m.ttft.sufficient}
                n={m.ttft.n}
                state={ttft}
              />
              <Figure
                label="Throughput p50"
                value={formatTps(m.tps.p50_ms)}
                sufficient={m.tps.sufficient}
                n={m.tps.n}
              />
              <Figure
                label="Availability"
                value={formatPct(m.attempts > 0 ? m.available_pct : null)}
                state={availability}
                hint={`${m.succeeded}/${m.attempts} runs`}
              />
              <Figure
                label="Correctness"
                value={formatPct(m.correct_pct)}
                sufficient={m.correct_pct !== null}
                n={m.answered}
                state={correctness}
                hint="answers containing the expected fact"
              />
            </div>

            {m.censored >= MIN_FAILURES_FOR_STATE && (
              // Not a StateChip, and deliberately not folded into the
              // availability figure: these runs ARE counted there. This says
              // something the numbers above structurally cannot — that the
              // slowest runs in the window are missing from the percentiles
              // beside it, and that the worse the endpoint gets the more
              // flattering those percentiles become.
              //
              // Gated on the same floor the chips use, for the same reason: a
              // single cut-off run is a rounding error, not a finding, and an
              // amber box about it sits on the card every day forever. The
              // floor is also what keeps the two halves of this card from
              // contradicting each other — a censored run is a SUBSET of a
              // failed one, so censored >= 3 implies at least 3 failures, and
              // this banner can never appear while the Availability chip
              // beside it is still suppressed by its own floor.
              <p
                className="mt-4 rounded-ui border border-fault-edge/40 bg-fault-edge/10 px-3 py-2 text-label text-fault-edge"
                data-testid={`censored-${m.model_id}`}
              >
                {m.censored} of {m.attempts} runs were cut off by the timeout
                limits. They count as failures in Availability above. The p50s
                do not include them — those are medians of the runs that
                finished, so the more runs get cut off, the faster this model
                looks here.
              </p>
            )}

            {m.max_reasoning_tokens > 0 && (
              <p
                className="mt-4 rounded-ui border border-danger/40 bg-danger/10 px-3 py-2 text-label text-danger"
                role="alert"
              >
                Reasoning returned {m.max_reasoning_tokens}{" "}
                {plural(m.max_reasoning_tokens, "token")} despite being disabled
                — these latency figures are not measuring what they claim.
              </p>
            )}
          </section>
        );
      })}
    </div>
  );
}
