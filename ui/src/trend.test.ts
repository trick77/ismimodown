import { describe, expect, it } from "vitest";
import type { ModelTrend, Point, Trend } from "./api/types";
import {
  buildSpeedReading,
  TPS_FLOOR,
  TTFT_FLOOR,
  TTFT_FLOOR_BOTH,
} from "./trend";

// GENERATED_AT is what the module measures the two spans back from, so the
// buckets below have to sit on the real clock: which SIDE a censored bucket
// falls on is what the caveat is allowed to claim.
const GENERATED_AT = "2026-08-20T12:00:00Z";
const GENERATED_AT_S = Date.parse(GENERATED_AT) / 1000;
const HOUR = 3600;

// One bucket in the reference day, one inside the compared hours.
const points = (censoredRecent = 0, censoredBefore = 0): Point[] => [
  {
    t: GENERATED_AT_S - 10 * HOUR,
    n: 6,
    censored: censoredBefore,
    p50: 900,
    p95: 1200,
  },
  {
    t: GENERATED_AT_S - HOUR,
    n: 6,
    censored: censoredRecent,
    p50: 950,
    p95: 1300,
  },
];

function metric(
  recent: number | null,
  before: number | null,
  censored = 0,
  censoredBefore = 0,
) {
  return {
    recent: {
      n: recent === null ? 4 : 36,
      sufficient: recent !== null,
      p50_ms: recent,
      p95_ms: recent,
    },
    before: {
      n: before === null ? 4 : 288,
      sufficient: before !== null,
      p50_ms: before,
      p95_ms: before,
    },
    points: points(censored, censoredBefore),
  };
}

function trendOf(models: ModelTrend[]): Trend {
  return {
    recent_s: 3 * 3600,
    before_s: 24 * 3600,
    bucket_s: 1800,
    models,
    generated_at: GENERATED_AT,
  };
}

const model = (
  id: string,
  ttft: [number | null, number | null],
  tps: [number | null, number | null],
  // How many runs were cut off, and in which span. The recent one is the
  // default because that is the case the caveat was written for.
  censored = 0,
  censoredBefore = 0,
): ModelTrend => ({
  model_id: id,
  ttft: metric(ttft[0], ttft[1], censored, censoredBefore),
  tps: metric(tps[0], tps[1], censored, censoredBefore),
});

describe("buildSpeedReading", () => {
  // The floors are measured, not chosen — see the comment on TTFT_FLOOR. This
  // is the assertion that keeps someone from "tidying" them to a round 10%,
  // which on the measured spread puts the banner in its slower state on 70% of
  // readings.
  it("says nothing about a first token that moved less than the floor", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1200, 900], [70, 70])]),
    );
    expect(1200 / 900 - 1).toBeLessThan(TTFT_FLOOR);
    expect(reading.state).toBe("steady");
    expect(reading.lead).toBe("");
  });

  it("names a first token past the floor, with the numbers and the seconds", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [70, 70])]),
    );
    expect(reading.state).toBe("slower");
    expect(reading.lead).toContain("mimo-v2.5");
    expect(reading.lead).toContain("slow to start");
    expect(reading.line).toContain("1700 ms");
    expect(reading.line).toContain("900 ms");
    // Seconds, because a percentage on a metric is not something anyone feels.
    expect(reading.line).toContain("0.8 s");
    expect(reading.metric).toBe("ttft");
  });

  // Throughput is the quiet metric — measured spread 10-12% against first
  // token's 20-35% — so its floor is far lower, and this is the case a
  // first-token-only reading would have called normal.
  it("catches a throughput drop while the first token holds", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [900, 900], [49, 70])]),
    );
    expect(70 / 49 - 1).toBeGreaterThan(TPS_FLOOR);
    expect(reading.state).toBe("slower");
    expect(reading.metric).toBe("tps");
    expect(reading.lead).toContain("generating more slowly");
  });

  // Ranked by seconds added to the wait, never by per cent: a first-token jump
  // of 90% adds 0.8 s here and the throughput drop adds over one, so throughput
  // has to lead the sentence even though its percentage is smaller.
  it("leads with the move that costs the most seconds, not the largest percentage", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [40, 70])]),
    );
    expect(reading.state).toBe("slower");
    expect(reading.metric).toBe("tps");
    expect(reading.moves[0]!.metric).toBe("tps");
    expect(reading.moves[0]!.secondsAdded).toBeGreaterThan(
      reading.moves[1]!.secondsAdded,
    );
  });

  // "Both models" is a claim about ONE metric. When each model moved on a
  // different one, saying it asserts the lead's metric of both — false about
  // the model that only started slowly.
  it("does not claim both models moved when they moved on different metrics", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1700, 900], [70, 70]),
        model("mimo-v2.5-pro", [900, 900], [45, 70]),
      ]),
    );
    expect(reading.state).toBe("slower");
    expect(reading.lead).not.toContain("Both models");
    // The one it did not lead with is still named, once.
    expect(reading.line).toContain("mimo-v2.5");
    expect(reading.line.match(/mimo-v2\.5-pro/g)?.length ?? 0).toBeLessThan(2);
  });

  // A single-model payload can have both its metrics fire, which is not two
  // models by any reading.
  it("never says both models when the block carries one", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [45, 70])]),
    );
    expect(reading.lead).not.toContain("Both models");
    expect(reading.lead).toContain("mimo-v2.5");
  });

  // No request is answered by both models, so summing their penalties states a
  // wait nobody can experience — and it would grow with the fleet rather than
  // with the slowdown.
  it("costs the wait in the lead model's own seconds, not the sum across models", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1700, 900], [70, 70]),
        model("mimo-v2.5-pro", [1700, 900], [70, 70]),
      ]),
    );
    // 800 ms on one model, not 1.6 s across two.
    expect(reading.line).toContain("0.8 s");
    expect(reading.line).not.toContain("1.6 s");
  });

  // Both models moving together is rarer than either moving alone, and it is
  // the more meaningful event — so it fires at a lower floor.
  it("uses the lower floor when both models move together", () => {
    const both = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1350, 900], [70, 70]),
        model("mimo-v2.5-pro", [1350, 900], [70, 70]),
      ]),
    );
    const alone = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1350, 900], [70, 70]),
        model("mimo-v2.5-pro", [900, 900], [70, 70]),
      ]),
    );
    expect(1350 / 900 - 1).toBeGreaterThan(TTFT_FLOOR_BOTH);
    expect(1350 / 900 - 1).toBeLessThan(TTFT_FLOOR);
    expect(both.state).toBe("slower");
    expect(both.lead).toContain("Both models");
    expect(alone.state).toBe("steady");
  });

  // Both medians are computed over runs that FINISHED, so a period whose
  // slowest runs were cut off publishes a flattering figure. The sentence has
  // to say so rather than claiming nothing went wrong.
  it("says the change is understated when the compared span was censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [70, 70], 2)]),
    );
    expect(reading.line).toContain("at least this large");
    expect(reading.line).not.toContain("Nothing failed");
  });

  // Truncation cuts the SLOWEST runs, so a censored reference day publishes a
  // faster past than it had and the change measured against it comes out too
  // large. Claiming "at least this large" there says the opposite of the truth.
  it("says the change may be smaller when only the reference day was censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [70, 70], 0, 2)]),
    );
    expect(reading.line).toContain("may be smaller than this");
    expect(reading.line).not.toContain("at least this large");
  });

  // With both sides truncated the two errors pull against each other, and no
  // direction is left to claim.
  it("claims no direction when both periods were censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [70, 70], 2, 2)]),
    );
    expect(reading.line).toContain("either side of it");
    expect(reading.line).not.toContain("at least this large");
    expect(reading.line).not.toContain("may be smaller than this");
  });

  // Buckets are floored to bucket_s and the boundary is not on that grid, so one
  // bucket holds runs from both spans. Read by its START it counted entirely as
  // reference — and a run cut off minutes INTO the compared span then flipped
  // the caveat to the reassuring direction, which is the inversion the split
  // exists to prevent.
  it("claims no direction when the censored bucket straddles the boundary", () => {
    const straddling = model("mimo-v2.5", [1700, 900], [70, 70]);
    // Half a bucket before the boundary, so it carries runs from either side.
    straddling.ttft.points = [
      {
        t: GENERATED_AT_S - 3 * HOUR - 900,
        n: 6,
        censored: 2,
        p50: 900,
        p95: 1200,
      },
    ];
    const reading = buildSpeedReading(trendOf([straddling]));
    expect(reading.line).toContain("either side of it");
    expect(reading.line).not.toContain("may be smaller than this");
  });

  // "Fewer tokens per second" is a share of the rate the reader used to get, and
  // the ratio behind it is computed the other way round. Spending one as the
  // other published "100 % fewer" for a rate that had halved — with both figures
  // in the same sentence.
  it("states a throughput drop as a share of the rate it fell from", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [900, 900], [35, 70])]),
    );
    expect(reading.line).toContain("50 % fewer tokens per second");
    expect(reading.line).not.toContain("100 %");
    expect(reading.line).toContain("35.0 against 70.0");
  });

  // The same reading, the other way round: "sooner" is a share of the wait it
  // replaced. 900 ms falling to 500 is 44% off the wait, not 80%.
  it("states a recovered first token as a share of the wait it replaced", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [500, 900], [70, 70])]),
    );
    expect(reading.state).toBe("quicker");
    expect(reading.line).toContain("44 % sooner");
    expect(reading.line).not.toContain("80 %");
  });

  // "Longer" and "more tokens per second" were already shares of the old
  // reading, and must not be restated: this is the assertion that catches the
  // conversion being applied to all four sentences at once.
  it("leaves the two sentences that were already stated against the old figure", () => {
    const slower = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1620, 900], [70, 70])]),
    );
    expect(slower.line).toContain("80 % longer");
    const quicker = buildSpeedReading(
      trendOf([model("mimo-v2.5", [900, 900], [100, 70])]),
    );
    expect(quicker.line).toContain("43 % more tokens per second");
  });

  // Under "Both models", one pair of medians described a measurement only one of
  // them took — and the singular pronoun said so out loud.
  it("gives each model its own figures when both moved", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1800, 900], [70, 70]),
        model("mimo-v2.5-pro", [1400, 900], [70, 70]),
      ]),
    );
    expect(reading.lead).toContain("Both models");
    expect(reading.line).toContain(
      "mimo-v2.5's first token takes 100 % longer",
    );
    expect(reading.line).toContain("mimo-v2.5-pro's first token takes 56 %");
    expect(reading.line).toContain("1800 ms against 900 ms");
    expect(reading.line).toContain("1400 ms against 900 ms");
    expect(reading.line).not.toContain("Its first token");
  });

  // The wait is one model's own, so handing it to the other one with "each"
  // states a wait that model never adds.
  it("splits the extra wait per model when the two differ", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1800, 900], [70, 70]),
        model("mimo-v2.5-pro", [1400, 900], [70, 70]),
      ]),
    );
    // The unit is said once, so only the first figure carries the phrase.
    expect(reading.line).toContain(
      "0.9 s of extra waiting on a full-length answer for mimo-v2.5",
    );
    expect(reading.line).toContain("0.5 s for mimo-v2.5-pro");
    expect(reading.line).not.toContain("each");
  });

  // ...and keeps the shorter sentence when they really do cost the same.
  it("says each only when both models cost the same wait", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [1350, 900], [70, 70]),
        model("mimo-v2.5-pro", [1350, 900], [70, 70]),
      ]),
    );
    expect(reading.line).toContain(
      "0.5 s of extra waiting on a full-length answer, each.",
    );
  });

  // Words, never a zero: too few finished requests is not the same as nothing
  // having changed.
  it("says so in words when a span cannot produce a median", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [null, 900], [null, 70])]),
    );
    expect(reading.state).toBe("unknown");
    expect(reading.lead).toContain("Not enough answers");
  });

  it("reads a missing block as nothing to say rather than throwing", () => {
    expect(buildSpeedReading(null).state).toBe("unknown");
    expect(buildSpeedReading(undefined).lead).toBe("");
  });

  // Worth saying for one reason: it is how a reader knows a slowdown ended.
  it("reports a recovery too", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [500, 900], [70, 70])]),
    );
    expect(reading.state).toBe("quicker");
    expect(reading.lead).toContain("quicker to start");
  });
});
