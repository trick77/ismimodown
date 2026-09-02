import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { VerdictBanner } from "./VerdictBanner";
import type { ModelTrend, Trend } from "./api/types";
import type { Verdict } from "./verdict";
import { colorForModel } from "./charts/options";

const MODELS = ["mimo-v2.5-pro", "mimo-v2.5"];

// The plot renders to a canvas jsdom cannot exercise, so the wrapper is mocked
// here as it is in every other panel test — what this file asserts is the
// SENTENCE and which chip carries it.
vi.mock("./charts/EChart", () => ({
  EChart: ({ ariaLabel }: { ariaLabel: string }) => (
    <div data-testid="echart" aria-label={ariaLabel} />
  ),
}));

const stats = (p50: number | null) => ({
  n: p50 === null ? 4 : 36,
  sufficient: p50 !== null,
  p50_ms: p50,
  p95_ms: p50,
});

const model = (id: string, ttft: [number, number]): ModelTrend => ({
  model_id: id,
  ttft: {
    recent: stats(ttft[0]),
    before: stats(ttft[1]),
    points: [{ t: 1, n: 6, censored: 0, p50: ttft[0], p95: ttft[0] }],
  },
  tps: {
    recent: stats(70),
    before: stats(70),
    points: [{ t: 1, n: 6, censored: 0, p50: 70, p95: 70 }],
  },
});

const AT_S = Date.parse("2026-08-20T12:00:00Z") / 1000;

const trend = (models: ModelTrend[], bucketS = 1800): Trend => ({
  recent_s: 3 * 3600,
  before_s: 24 * 3600,
  bucket_s: bucketS,
  models,
  generated_at: "2026-08-20T12:00:00Z",
});

// A model whose compared median is still elevated and whose last hour is back
// on the reference level — the shape the banner used to announce as current.
const recoveredModel = (id: string, ttft: [number, number]): ModelTrend => ({
  ...model(id, ttft),
  ttft: {
    recent: stats(ttft[0]),
    before: stats(ttft[1]),
    points: [900, 900, 900, 900].map((p50, i) => ({
      t: AT_S - (4 - i) * 900,
      n: 3,
      censored: 0,
      p50,
      p95: p50,
    })),
  },
});

const normal: Verdict = {
  state: "normal",
  headline: "Xiaomi MiMo is answering",
  detail: [],
};

describe("VerdictBanner", () => {
  // The contradiction this whole arrangement exists to make unrepresentable: a
  // green "everything looks normal" with "slower than usual" underneath it does
  // not answer the page's own question. One chip, one sentence, one claim.
  it("never says normal and slower at the same time", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [3400, 1800])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "slower",
    );
    expect(screen.queryAllByTestId("state-chip")).toHaveLength(1);
    expect(screen.queryByText(/Xiaomi MiMo is answering/)).toBeNull();
    expect(screen.getByText(/slow to start right now/)).toBeInTheDocument();
  });

  // A fault outranks a slowdown and takes the banner outright. Hedging an
  // outage with a note about latency reads as neither.
  it("says nothing about speed while something is failing", () => {
    render(
      <VerdictBanner
        verdict={{
          state: "degraded",
          headline: "mimo-v2.5 is failing requests right now",
          detail: ["Nine of the last twenty came back as errors."],
        }}
        trend={trend([model("mimo-v2.5", [3400, 1800])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "degraded",
    );
    expect(screen.queryByText(/slow to start/)).toBeNull();
    expect(screen.queryByTestId("trend-plot")).toBeNull();
  });

  // The plot appearing is itself part of the signal, so a quiet day must not
  // draw one.
  it("plots the slowdown, and only when there is one", () => {
    const { rerender } = render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [3400, 1800])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("trend-plot")).toBeInTheDocument();

    rerender(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [900, 900])])}
        loading={false}
      />,
    );
    expect(screen.queryByTestId("trend-plot")).toBeNull();
    // The headline is split across elements now — the state word carries the
    // chip's colour — so it is read off the paragraph rather than matched whole.
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      "Xiaomi MiMo is answering",
    );
  });

  // Faster is not the question this page answers, so a recovery gets the plain
  // normal banner: no headline about it, no sentence, no plot — and not the
  // steady sentence either, which would claim an ordinary spread this reading
  // is outside of.
  it("says nothing when a model got quicker", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [500, 900])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      "Xiaomi MiMo is answering",
    );
    expect(screen.queryByTestId("trend-plot")).toBeNull();
    expect(
      screen.queryByText(/quicker|faster|sooner|ordinary spread/),
    ).toBeNull();
  });

  // The spike is inside the compared median and over. Announcing it in the
  // present tense over a plot that has been flat for an hour is what the tail
  // gate exists to stop — but silence would drop a reading the page was shouting
  // about an hour ago, so it is demoted, not deleted.
  it("reports a slowdown the last hour has undone in the past tense", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([recoveredModel("mimo-v2.5", [2016, 954])], 900)}
        loading={false}
      />,
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "normal",
    );
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      "Xiaomi MiMo is answering",
    );
    expect(screen.queryByTestId("trend-plot")).toBeNull();
    expect(
      screen.getByText(/back to normal for the last hour/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/slow to start right now/)).toBeNull();
  });

  // The plot exists to show ONE shape. A line for the model that held steady is
  // a second shape the reader has to rule out first — and on a shared axis it
  // also sets the scale the moved line is drawn against.
  it("plots only the models the sentence is about", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([
          model("mimo-v2.5", [900, 900]),
          model("mimo-v2.5-pro", [3400, 1800]),
        ])}
        loading={false}
      />,
    );
    const plot = screen.getByTestId("trend-plot");
    expect(plot).toHaveTextContent("mimo-v2.5-pro");
    // The steady model keeps its own legend entry off the plot. Matched on the
    // whole label, since its id is a prefix of the one that did move.
    expect(
      screen.queryByText("mimo-v2.5", { selector: "span" }),
    ).not.toBeInTheDocument();
  });

  // Forgetting is only acceptable if the page says what it forgot: the resting
  // banner claims the readings sit where they usually do, which is what makes
  // its silence in the other states readable. In the HEADLINE, as a clause —
  // printed underneath as well it said the same thing twice, the second time
  // as a statistic nobody asked for.
  it("says both models are behaving as usual on a quiet day", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([
          model("mimo-v2.5", [900, 900]),
          model("mimo-v2.5-pro", [4000, 4000]),
        ])}
        models={["mimo-v2.5-pro", "mimo-v2.5"]}
        loading={false}
      />,
    );
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      /Xiaomi MiMo is answering, and both models are behaving as usual/,
    );
    // No figures anywhere: the cards below carry every number.
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(/\d/);
  });

  // ...and the claim is dropped the moment it stops being true. A "minor"
  // reading crossed a measured floor — that is why it was noticed — so the
  // headline falls back to the bare verdict rather than calling it usual.
  it("does not call a reading usual once it has passed a floor", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [2016, 954])])}
        models={["mimo-v2.5"]}
        loading={false}
      />,
    );
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      /Xiaomi MiMo is answering/,
    );
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /behaving as usual/,
    );
  });

  // The pill and the sentence are one statement, so the word the pill carries
  // is painted the pill's colour rather than left as ink.
  it("paints the state word in the headline the colour of its chip", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [900, 900])])}
        loading={false}
      />,
    );
    const word = screen.getByTestId("headline-state");
    expect(word).toHaveTextContent("answering");
    expect(word).toHaveClass("text-online");
  });

  // ...but only when the chip says it. A headline that mentions the word while
  // the page is degraded must not borrow the green and claim a state the page
  // is not in.
  it("leaves the word alone when the chip disagrees with it", () => {
    render(
      <VerdictBanner
        verdict={{
          state: "degraded",
          headline: "Reasoning is switched on — nothing here is normal",
          detail: [],
        }}
        loading={false}
      />,
    );
    expect(screen.queryByTestId("headline-state")).toBeNull();
  });

  // A payload from an older daemon has no trend block at all, and the banner is
  // the one thing on the page that must never fail to render.
  it("renders the plain verdict when the block is missing", () => {
    render(<VerdictBanner verdict={normal} loading={false} />);
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      "Xiaomi MiMo is answering",
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "normal",
    );
  });
  // A move that cleared its floor and left a wait nobody would call slow says
  // nothing at all: no chip, no plot, and no line either. Six tenths of a
  // second is not something to hand a visitor in the box they read first.
  it("says nothing at all about a small slowdown", () => {
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [2016, 954])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("state-chip")).toHaveAttribute(
      "data-state",
      "normal",
    );
    expect(screen.queryByText(/slow to start right now/)).toBeNull();
    expect(screen.queryByTestId("trend-plot")).toBeNull();
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /2016 ms against 954 ms/,
    );
    // ...and the steady sentence must not speak for it either: the reading
    // moved past its floor, so "inside the ordinary spread" would be false.
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /ordinary spread/,
    );
  });
  // A fault is the news: the banner takes it alone, with no speed clause and
  // no reassurance riding along. Asserted on the clause that EXISTS — the old
  // wording checked for a sentence the source no longer contains, so it could
  // not fail.
  it("says nothing about speed beside a fault", () => {
    render(
      <VerdictBanner
        verdict={{
          state: "degraded",
          headline: "mimo-v2.5 is having problems right now",
          detail: [],
        }}
        trend={trend([model("mimo-v2.5", [1030, 1030])])}
        loading={false}
      />,
    );
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /behaving as usual/,
    );
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /slow to start|generating more slowly/,
    );
  });

  // The same tie the masthead subline makes: a model ID carries its series
  // colour wherever the page names it, so the reader finds the same orange on
  // the card, the chart line and the legend below.
  it("colours the model names it says, longest ID first", () => {
    render(
      <VerdictBanner
        verdict={{
          state: "degraded",
          headline: "mimo-v2.5-pro is having problems right now",
          detail: ["mimo-v2.5's first token is fine."],
        }}
        models={["mimo-v2.5-pro", "mimo-v2.5"]}
        loading={false}
      />,
    );
    const pro = screen.getByText("mimo-v2.5-pro");
    const fast = screen.getByText("mimo-v2.5");
    // Painted, and two different hues — the prefix must not swallow the suffix.
    expect(pro).toHaveStyle({ color: colorForModel("mimo-v2.5-pro", MODELS) });
    expect(fast).toHaveStyle({ color: colorForModel("mimo-v2.5", MODELS) });
    // The possessive stays prose: the match ends at the ID.
    expect(fast).toHaveTextContent(/^mimo-v2\.5$/);
  });
  // The clause is a claim about the run record too, not only about speed. A
  // verdict can be normal and still carry a line — one failed run inside the
  // hour is reported in the past tense rather than painted — and the headline
  // then congratulated the endpoint directly above the run it had just lost.
  it("does not call it usual over a verdict that still has something to say", () => {
    render(
      <VerdictBanner
        verdict={{
          state: "normal",
          headline: "Xiaomi MiMo is answering",
          detail: [
            "mimo-v2.5 failed 1 of the last 12 runs, 8 minutes ago. One run is not yet a pattern.",
          ],
        }}
        trend={trend([
          model("mimo-v2.5", [900, 900]),
          model("mimo-v2.5-pro", [4000, 4000]),
        ])}
        models={["mimo-v2.5-pro", "mimo-v2.5"]}
        loading={false}
      />,
    );
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /behaving as usual/,
    );
    expect(screen.getByTestId("verdict")).toHaveTextContent(
      /failed 1 of the last 12 runs/,
    );
  });

  // "Both models" may only speak for models that were actually measured: a
  // model whose spans are too thin produces no reading, and the other one was
  // left vouching for it.
  it("does not speak for a model the block could not measure", () => {
    const unread = model("mimo-v2.5-pro", [4000, 4000]);
    unread.ttft.recent = {
      n: 4,
      sufficient: false,
      p50_ms: null,
      p95_ms: null,
    };
    render(
      <VerdictBanner
        verdict={normal}
        trend={trend([model("mimo-v2.5", [900, 900]), unread])}
        models={["mimo-v2.5-pro", "mimo-v2.5"]}
        loading={false}
      />,
    );
    expect(screen.getByTestId("verdict")).not.toHaveTextContent(
      /behaving as usual/,
    );
  });
});
