import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ModelCards } from "./ModelCards";
import { AVAILABILITY_TARGET, MIN_FAILURES_FOR_STATE } from "./verdict";
import type { ModelSummary, RecentCycle, Summary } from "./api/types";
import { FAULT_OK } from "./api/types";

function model(over: Partial<ModelSummary> = {}): ModelSummary {
  return {
    model_id: "mimo-v2.5",
    ttft: { n: 288, sufficient: true, p50_ms: 916, p95_ms: 1400 },
    itl: { n: 288, sufficient: true, p50_ms: 24, p95_ms: 40 },
    tps: { n: 288, sufficient: true, p50_ms: 41, p95_ms: 60 },
    attempts: 288,
    succeeded: 288,
    available_pct: 100,
    censored: 0,
    answered: 288,
    correct: 288,
    correct_pct: 100,
    max_reasoning_tokens: 0,
    max_cached_tokens: 0,
    ...over,
  };
}

const summary = (
  models: ModelSummary[],
  recent: RecentCycle[] = [],
): Summary => ({
  window: "24h",
  cycles: 288,
  models,
  net: [],
  recent,
  generated_at: "2026-08-04T12:00:00Z",
});

// A block of clean cycles with a model failing on the ones named — the network
// reached MiMo, the inference run did not come back. Newest first, five minutes
// apart, like the daemon serves it.
const recentModelFailures = (
  failing: number[],
  modelId = "mimo-v2.5",
): RecentCycle[] =>
  Array.from({ length: 36 }, (_, i) => ({
    at: new Date(
      Date.parse("2026-08-04T12:00:00Z") - i * 300_000,
    ).toISOString(),
    fault: FAULT_OK,
    models: {
      [modelId]: {
        ok: !failing.includes(i),
        answer_ok: failing.includes(i) ? null : true,
      },
    },
  }));

describe("ModelCards", () => {
  // These cards sit above the fold with the whole page under them. Rendering
  // nothing until the fetch lands is what shoved that page down and made this
  // the largest single contributor to the dashboard's layout shift.
  it("holds a row of height before the models arrive", () => {
    render(<ModelCards summary={null} baseline={null} pending />);

    const pending = screen.getByTestId("model-cards-pending");
    // Two shapes, because the grid has two: one row side by side above the md
    // breakpoint, two stacked rows below it. Reserving only the desktop height
    // leaves the phone shifting by a whole card.
    expect(pending).toHaveClass("h-[500px]");
    expect(pending).toHaveClass("md:h-[242px]");
    // It carries no information, so it must not be an object a screen reader
    // stops on — the verdict banner above already says the page is loading.
    expect(pending).toHaveAttribute("aria-hidden", "true");
  });

  // A load that failed is empty too. Holding the ground then is not a
  // reservation — nothing is coming to fill it, and the error message above has
  // already said so.
  it("does not hold ground once the load has finished with no models", () => {
    const { container } = render(<ModelCards summary={null} baseline={null} />);

    expect(screen.queryByTestId("model-cards-pending")).toBeNull();
    // The empty grid it always rendered in this case — zero pixels tall, and
    // nothing left below it to push.
    expect(container.firstChild).toBeEmptyDOMElement();
  });

  it("renders one card per model", () => {
    render(
      <ModelCards
        summary={summary([model(), model({ model_id: "mimo-v2.5-pro" })])}
        baseline={null}
      />,
    );
    expect(screen.getByTestId("model-card-mimo-v2.5")).toBeInTheDocument();
    expect(screen.getByTestId("model-card-mimo-v2.5-pro")).toBeInTheDocument();
  });

  it("shows the headline figures", () => {
    render(<ModelCards summary={summary([model()])} baseline={null} />);
    expect(screen.getByText("916 ms")).toBeInTheDocument();
    expect(screen.getByText("41 tok/s")).toBeInTheDocument();
  });

  // A thin window must never render a number, because a P50 from three samples
  // is exactly what gets quoted out of context.
  it("suppresses a thin percentile", () => {
    render(
      <ModelCards
        summary={summary([
          model({
            ttft: { n: 3, sufficient: false, p50_ms: null, p95_ms: null },
          }),
        ])}
        baseline={null}
      />,
    );
    expect(screen.getAllByTestId("insufficient").length).toBeGreaterThan(0);
  });

  // Reasoning coming back on invalidates every latency figure on the card, so
  // it must be stated on the card rather than only in the banner.
  it("warns loudly when reasoning tokens appear", () => {
    render(
      <ModelCards
        summary={summary([model({ max_reasoning_tokens: 512 })])}
        baseline={null}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/not measuring what/i);
  });

  // The percentiles on this card are computed over runs that finished, so the
  // runs the ladder cut off are counted in one figure and absent from another.
  // Nothing in the numbers themselves can say that.
  it("says so when runs were cut off by the timeout limits", () => {
    render(
      <ModelCards
        summary={summary([
          model({ censored: 12, attempts: 288, succeeded: 276 }),
        ])}
        baseline={null}
      />,
    );
    const note = screen.getByTestId("censored-mimo-v2.5");
    expect(note).toHaveTextContent(/12 of 288/);
    // Both halves, because they are different claims and the old copy ran them
    // together: the runs ARE in the availability figure beside this, and they
    // are NOT in the percentiles above it.
    expect(note).toHaveTextContent(/count as failures in Availability/i);
    expect(note).toHaveTextContent(/p50s do not include them/i);
    // The note used to close by spelling out which way the omission cuts. It
    // was dropped on purpose; this keeps it from drifting back in.
    expect(note).not.toHaveTextContent(/faster this model looks/i);
  });

  it("stays quiet when nothing was cut off", () => {
    render(<ModelCards summary={summary([model()])} baseline={null} />);
    expect(screen.queryByTestId("censored-mimo-v2.5")).toBeNull();
  });

  // Without a floor the card paints an amber box about a single dropped run,
  // and at 288 cycles a day it does that essentially forever.
  //
  // The note and the Availability chip no longer share a threshold, and are not
  // meant to: the note reports that runs were cut off, the chip reports whether
  // that amounts to a missed target. See the test below for the case where they
  // deliberately disagree.
  it("stays quiet when too few runs were cut off to mean anything", () => {
    render(
      <ModelCards
        summary={summary([
          model({
            censored: MIN_FAILURES_FOR_STATE - 1,
            attempts: 221,
            // A censored run is a failed one, so the failure count can never be
            // below the censored count — a fixture that says otherwise would
            // test a state the backend cannot produce.
            succeeded: 219,
          }),
        ])}
        baseline={null}
      />,
    );
    expect(screen.queryByTestId("censored-mimo-v2.5")).toBeNull();
  });

  // The reported bug. Three runs cut off by the timeout ladder over 48 hours is
  // 99.49% available, which is not distinguishable from an endpoint meeting the
  // 99% target — and the card said ELEVATED anyway, in the 24h view and the 48h
  // view, off the same three runs.
  //
  // The note stays. Three cut-off runs is a fact about the p50s beside it and
  // is worth stating; what it is not is a verdict.
  it("notes three cut-off runs without calling them a missed target", () => {
    render(
      <ModelCards
        summary={summary([
          model({ censored: 3, attempts: 592, succeeded: 589 }),
        ])}
        baseline={null}
      />,
    );

    expect(screen.getByTestId("censored-mimo-v2.5")).toHaveTextContent(
      /3 of 592 runs were cut off/,
    );
    const card = screen.getByTestId("model-card-mimo-v2.5");
    expect(card.querySelector("[data-testid='state-chip']")).toHaveAttribute(
      "data-state",
      "normal",
    );
    expect(card.querySelectorAll("[data-state='elevated']")).toHaveLength(0);
  });

  // The presence half of the regression above, and the only assertion that
  // walks a genuinely-elevated availability through Figure.
  //
  // It is also the one test that can see the arguments going in backwards.
  // scoreAvailability takes (succeeded, attempts) and the card has both to
  // hand; swapped, p exceeds 1, the radical goes NaN, every comparison against
  // the bands is false and the card reads NORMAL forever. Nothing else in the
  // suite notices — the unit tests pass their own literals, and the 3-of-592
  // case asserts the absence of a chip, which a NaN gives it for free.
  it("chips the Availability figure when the target really is missed", () => {
    render(
      <ModelCards
        summary={summary([model({ attempts: 292, succeeded: 282 })])}
        baseline={null}
      />,
    );

    const hint = screen.getByText(/282\/292 runs/);
    const figure = hint.parentElement as HTMLElement;
    expect(figure.querySelector("[data-testid='state-chip']")).toHaveAttribute(
      "data-state",
      "elevated",
    );
  });

  // Publishing the target is what turns a sub-target percentage with no chip
  // into a contradiction, so the figure has to answer it itself. The sentence
  // used to live only in the censored note, which means three 502s — the same
  // figure, no note — left the card silently arguing with itself.
  it("says why a sub-target percentage carries no chip, with no censored runs", () => {
    render(
      <ModelCards
        summary={summary([
          // available_pct too: the hint keys off the DISPLAYED figure, which
          // is the one the reader sees disagreeing with the missing chip.
          model({
            attempts: 292,
            succeeded: 289,
            available_pct: 98.97,
            censored: 0,
          }),
        ])}
        baseline={null}
      />,
    );

    expect(screen.queryByTestId("censored-mimo-v2.5")).toBeNull();
    expect(screen.getByText(/289\/292 runs/)).toHaveTextContent(
      /under the 99% target, within what this many runs can tell apart/,
    );
  });

  // A window can fall under MIN_ATTEMPTS_FOR_STATE long after a cold start:
  // unattributable cycles are excluded from the denominator, so a local uplink
  // outage shrinks it — and modelTracks drops those same cycles from the recent
  // track, so the fold that normally saves the header chip is clean too. This
  // rendered 20.0% in green.
  it("refuses to call a window too small to hold evidence normal", () => {
    render(
      <ModelCards
        summary={summary([
          model({ attempts: 15, succeeded: 3, available_pct: 20 }),
        ])}
        baseline={null}
      />,
    );

    const figure = screen.getByText(/3\/15 runs/).parentElement as HTMLElement;
    expect(figure.querySelector("[data-testid='state-chip']")).toHaveAttribute(
      "data-state",
      "unknown",
    );
  });

  // The target is published, not implied. A bare percentage next to nothing
  // invites the reader to assume the goal is 100%, which is the assumption this
  // whole scoring change exists to retire.
  it("prints the availability target beside the counts", () => {
    render(<ModelCards summary={summary([model()])} baseline={null} />);
    expect(
      screen.getByText(`288/288 runs · target ${AVAILABILITY_TARGET}%`),
    ).toBeInTheDocument();
  });

  it("counts a single reasoning token in the singular", () => {
    render(
      <ModelCards
        summary={summary([model({ max_reasoning_tokens: 1 })])}
        baseline={null}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/returned 1 token\b/);
  });

  // The invariant this chip exists to hold: a card can never read greener than
  // the verdict banner above it. Two failed runs inside the hour is what the
  // banner calls elevated; over the SELECTED window those same two are nowhere
  // near enough evidence to claim the availability target was missed, so every
  // figure on the card is healthy and the header used to print NORMAL directly
  // under an ELEVATED banner about the same model.
  //
  // Load-bearing since the window score was loosened: this fold is the only
  // thing standing between a quieter card and a card that contradicts the
  // banner over it.
  it("never reads greener than the recent cycles", () => {
    render(
      <ModelCards
        summary={summary([model()], recentModelFailures([0, 2]))}
        baseline={null}
      />,
    );

    const card = screen.getByTestId("model-card-mimo-v2.5");
    expect(card.querySelector("[data-testid='state-chip']")).toHaveAttribute(
      "data-state",
      "elevated",
    );
    // The figures still describe the window, and the window is clean. The chip
    // is saying something they structurally cannot.
    expect(screen.getByText(/288\/288 runs/)).toBeInTheDocument();
  });

  // ...and one failed run is still nothing, on the card as on the banner.
  it("stays normal for a single failed run", () => {
    render(
      <ModelCards
        summary={summary([model()], recentModelFailures([0]))}
        baseline={null}
      />,
    );

    const card = screen.getByTestId("model-card-mimo-v2.5");
    expect(card.querySelector("[data-testid='state-chip']")).toHaveAttribute(
      "data-state",
      "normal",
    );
  });

  // The recent block can only ever make this chip WORSE. scoreModelRecent has
  // no "unknown" to return — a model absent from the block scores normal — so
  // folding it in flat would paint a card green for a model with no
  // measurements at all, which is the one thing the chip must not do.
  it("still says unknown for a model with nothing measured", () => {
    render(
      <ModelCards
        summary={summary([
          model({
            attempts: 0,
            succeeded: 0,
            // Not null: the field is non-nullable, and the card reads it as
            // null itself whenever attempts is 0.
            available_pct: 0,
            answered: 0,
            correct: 0,
            correct_pct: null,
            ttft: { n: 0, sufficient: false, p50_ms: null, p95_ms: null },
          }),
        ])}
        baseline={null}
      />,
    );

    const card = screen.getByTestId("model-card-mimo-v2.5");
    expect(card.querySelector("[data-testid='state-chip']")).toHaveAttribute(
      "data-state",
      "unknown",
    );
  });

  it("renders nothing but stays stable with no summary", () => {
    const { container } = render(<ModelCards summary={null} baseline={null} />);
    expect(container.querySelectorAll("section")).toHaveLength(0);
  });
});
