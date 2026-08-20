import type { Summary, Trend } from "./api/types";
import { Figure, StateChip } from "./ui";
import { formatMs, formatPct, formatTps, plural } from "./format";
import type { State } from "./verdict";
import {
  AVAILABILITY_TARGET,
  MIN_FAILURES_FOR_STATE,
  scoreAvailability,
  scoreCorrectness,
  scoreModelRecent,
  scoreRatio,
  worst,
} from "./verdict";
import { colorForModel } from "./charts/options";
import { figureDelta } from "./trend";

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
  trend = null,
  pending = false,
}: {
  summary: Summary | null;
  baseline: Summary | null;
  // The recent-past comparison, per figure. A NUMBER under each value and never
  // a verdict: the banner has already made the page's one claim, and a second
  // opinion down here is how the page ends up disagreeing with itself.
  //
  // Not window-scoped, unlike every figure it sits under — it is the same fixed
  // reading whichever pill is selected, which is why the line names its own
  // period ("than the 24 hours before") rather than leaning on the card's.
  trend?: Trend | null;
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
  // TWO numbers because the grid has two shapes: side by side above the md
  // breakpoint, one row; stacked below it, two rows and a gap-4 between them. A
  // single 242 was measured on a desktop viewport and left the phone shifting
  // by a whole card — which is the layout PageSpeed grades on mobile.
  //
  // The breakpoint is md rather than sm because the cards themselves moved —
  // see the grid below. Two numbers still, though the card now has three sizes
  // under them, and both are deliberately the SLACK side of every band they
  // cover: 500 against a stacked pair measuring 488 on a phone and 470 between
  // sm and md (a full-width card wraps less prose), 242 against 244 at
  // 768–895, where the display step is one size down (index.css). Reserving
  // slightly long settles the page UP by a few pixels when the cards arrive;
  // reserving short drops everything below them down, which is the shift this
  // whole placeholder exists to prevent. The one band that reserves short is
  // 896–1023 at 259 — measured, pre-existing, and left alone rather than
  // fixed with a third number nothing else on the page is split by.
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
        className="card h-[500px] p-4 sm:p-5 md:h-[242px]"
        aria-hidden="true"
        data-testid="model-cards-pending"
      />
    );
  }

  return (
    // Two up from md, not from sm. A card holds four figures in a 2x2 grid,
    // so two cards side by side is four columns of numbers across the page —
    // and between 640 and 768 that column is ~110px against a longest figure
    // of 138. Every throughput value wrapped. Stacked, each card gets the full
    // width and the figures are the ones the desktop shows.
    <div className="grid gap-4 md:grid-cols-2">
      {models.map((m) => {
        const base = baseline?.models.find((b) => b.model_id === m.model_id);
        // The counts, not the percentage beside them. A card describes the
        // selected window, and over a day of cycles three cut-off runs are
        // 98.97% — under the target, and indistinguishable from an endpoint
        // that meets it. scoreAvailability needs both integers to tell those
        // apart; available_pct is derived from the same two and adds nothing.
        const availability = scoreAvailability(m.succeeded, m.attempts);
        // A percentage under the target with no chip beside it reads as a
        // contradiction, and printing the target is what made it one — before
        // it there was nothing on the card for the number to disagree with.
        //
        // The sentence that reconciles them existed already, but only inside
        // the censored note below, so it appeared only when the failures came
        // from the timeout ladder. Three 502s produce the identical figure and
        // no note at all. It belongs on the figure that raises the question.
        const unexplainedMiss =
          availability === "normal" &&
          m.attempts > 0 &&
          m.available_pct < AVAILABILITY_TARGET;
        const correctness = scoreCorrectness(
          m.correct_pct,
          m.answered - m.correct,
        );
        const ttft = scoreRatio(m.ttft.p50_ms, base?.ttft.p50_ms ?? null);
        const recentState = scoreModelRecent(m.model_id, summary?.recent ?? []);

        return (
          <section
            key={m.model_id}
            className="card p-4 sm:p-5"
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
                  figures describe the SELECTED window behind floors of their
                  own, which a fault an hour old clears neither. See
                  scoreModelRecent — and note this fold got MORE load-bearing
                  when availability moved to a confidence bound, because a
                  quieter window figure is exactly what would have opened the
                  gap.

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
                delta={figureDelta(trend, m.model_id, "ttft")}
              />
              <Figure
                label="Throughput p50"
                value={formatTps(m.tps.p50_ms)}
                sufficient={m.tps.sufficient}
                n={m.tps.n}
                delta={figureDelta(trend, m.model_id, "tps")}
              />
              <Figure
                label="Availability"
                value={formatPct(m.attempts > 0 ? m.available_pct : null)}
                state={availability}
                // The target rides along with the counts because this is the
                // one figure on the card that HAS one, and a percentage with
                // nothing to be measured against invites the reader to assume
                // the goal is 100%. It is not. See AVAILABILITY_TARGET.
                hint={
                  unexplainedMiss
                    ? `${m.succeeded}/${m.attempts} runs · under the ${AVAILABILITY_TARGET}% target, within what this many runs can tell apart from meeting it`
                    : `${m.succeeded}/${m.attempts} runs · target ${AVAILABILITY_TARGET}%`
                }
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
              // beside it.
              //
              // Gated on MIN_FAILURES_FOR_STATE for the reason that constant
              // exists: a single cut-off run is a rounding error, not a
              // finding, and an amber box about it sits on the card every day
              // forever.
              //
              // This note can now appear beside a NORMAL Availability chip, and
              // that is not the two halves of the card contradicting each
              // other. It used to be impossible, back when three failures was
              // also the threshold for the chip — which was the bug: three
              // cut-off runs in two days is a fact worth stating and is not a
              // missed target. The note reports what happened; the chip reports
              // whether it amounts to anything. The sentence below says which
              // is which.
              <p
                className="mt-4 rounded-ui border border-fault-edge/40 bg-fault-edge/10 px-3 py-2 text-label text-fault-edge"
                data-testid={`censored-${m.model_id}`}
              >
                {m.censored} of {m.attempts} runs were cut off by the timeout
                limits. They count as failures in Availability above, which says
                for itself whether that missed the target. The p50s do not
                include them — those are medians of the runs that finished.
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
