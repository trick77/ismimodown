import { describe, expect, it } from "vitest";
import type { ModelTrend, Point, Trend } from "./api/types";
import {
  buildSpeedReading,
  SLOW_TPS,
  SLOW_TTFT_MS,
  TAIL_S,
  TPS_FLOOR,
  TTFT_FLOOR,
  TTFT_FLOOR_BOTH,
  type SpeedMetric,
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

function trendOf(models: ModelTrend[], bucketS = 1800): Trend {
  return {
    recent_s: 3 * 3600,
    before_s: 24 * 3600,
    bucket_s: bucketS,
    models,
    generated_at: GENERATED_AT,
  };
}

// The quarter-hour buckets the tail gate reads, filling the last hour. Three
// successful runs apiece is the rate the daemon actually probes at, so a whole
// hour is twelve — comfortably past TAIL_MIN_SAMPLES, and the reason a thinner
// hour has to be spelled out to be tested.
const QUARTER = 900;
const tailPoints = (values: number[], n = 3): Point[] =>
  values.map((v, i) => ({
    t: GENERATED_AT_S - (values.length - i) * QUARTER,
    n,
    censored: 0,
    p50: v,
    p95: v,
  }));

// One bucket back in the reference day, so the series is not made entirely of
// the hour under test.
const REFERENCE_BUCKET: Point = {
  t: GENERATED_AT_S - 10 * HOUR,
  n: 6,
  censored: 0,
  p50: 900,
  p95: 1200,
};

function withTail(
  m: ModelTrend,
  metric: SpeedMetric,
  values: number[],
  n = 3,
): ModelTrend {
  const block = {
    ...m[metric],
    points: [REFERENCE_BUCKET, ...tailPoints(values, n)],
  };
  return metric === "ttft" ? { ...m, ttft: block } : { ...m, tps: block };
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
      trendOf([model("mimo-v2.5", [3400, 1800], [70, 70])]),
    );
    expect(reading.state).toBe("slower");
    expect(reading.lead).toContain("mimo-v2.5");
    expect(reading.lead).toContain("slow to start");
    expect(reading.line).toContain("3400 ms");
    expect(reading.line).toContain("1800 ms");
    // Seconds, because a percentage on a metric is not something anyone feels.
    expect(reading.line).toContain("1.6 s");
    expect(reading.metric).toBe("ttft");
  });

  // Throughput is the quiet metric — measured spread 10-12% against first
  // token's 20-35% — so its floor is far lower, and this is the case a
  // first-token-only reading would have called normal.
  it("catches a throughput drop while the first token holds", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [900, 900], [39, 70])]),
    );
    expect(70 / 39 - 1).toBeGreaterThan(TPS_FLOOR);
    expect(reading.state).toBe("slower");
    expect(reading.metric).toBe("tps");
    expect(reading.lead).toContain("generating more slowly");
  });

  // Ranked by seconds added to the wait, never by per cent: a first-token jump
  // of 90% adds 0.8 s here and the throughput drop adds over one, so throughput
  // has to lead the sentence even though its percentage is smaller.
  it("leads with the move that costs the most seconds, not the largest percentage", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [3400, 1800], [40, 70])]),
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
        model("mimo-v2.5", [3400, 1800], [70, 70]),
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
      trendOf([model("mimo-v2.5", [3400, 1800], [45, 70])]),
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
        model("mimo-v2.5", [3400, 1800], [70, 70]),
        model("mimo-v2.5-pro", [3400, 1800], [70, 70]),
      ]),
    );
    // 1.6 s on one model, not 3.2 s across two.
    expect(reading.line).toContain("1.6 s");
    expect(reading.line).not.toContain("3.2 s");
  });

  // Both models moving together is rarer than either moving alone, and it is
  // the more meaningful event — so it fires at a lower floor.
  it("uses the lower floor when both models move together", () => {
    const both = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [3000, 2000], [70, 70]),
        model("mimo-v2.5-pro", [3000, 2000], [70, 70]),
      ]),
    );
    const alone = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [3000, 2000], [70, 70]),
        model("mimo-v2.5-pro", [900, 900], [70, 70]),
      ]),
    );
    expect(3000 / 2000 - 1).toBeGreaterThan(TTFT_FLOOR_BOTH);
    expect(3000 / 2000 - 1).toBeLessThan(TTFT_FLOOR);
    expect(both.state).toBe("slower");
    expect(both.lead).toContain("Both models");
    expect(alone.state).toBe("steady");
  });

  // Both medians are computed over runs that FINISHED, so a period whose
  // slowest runs were cut off publishes a flattering figure. The sentence has
  // to say so rather than claiming nothing went wrong.
  it("says the change is understated when the compared span was censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [3400, 1800], [70, 70], 2)]),
    );
    expect(reading.line).toContain("at least this large");
    expect(reading.line).not.toContain("Nothing failed");
  });

  // Truncation cuts the SLOWEST runs, so a censored reference day publishes a
  // faster past than it had and the change measured against it comes out too
  // large. Claiming "at least this large" there says the opposite of the truth.
  it("says the change may be smaller when only the reference day was censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [3400, 1800], [70, 70], 0, 2)]),
    );
    expect(reading.line).toContain("may be smaller than this");
    expect(reading.line).not.toContain("at least this large");
  });

  // With both sides truncated the two errors pull against each other, and no
  // direction is left to claim.
  it("claims no direction when both periods were censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [3400, 1800], [70, 70], 2, 2)]),
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
    const straddling = model("mimo-v2.5", [3400, 1800], [70, 70]);
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

  // The same reading the other way round is measured and never printed: a
  // recovery carries no lead, no line, no plot.
  it("keeps a recovered first token off the page entirely", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [500, 900], [70, 70])]),
    );
    expect(reading.state).toBe("quicker");
    expect(reading.lead).toBe("");
    expect(reading.line).toBe("");
    expect(reading.moves).toEqual([]);
    expect(reading.metric).toBeNull();
  });

  // "Longer" was already a share of the old reading, and must not be restated:
  // this is the assertion that catches the conversion being applied to every
  // sentence at once.
  it("leaves the sentence that was already stated against the old figure", () => {
    const slower = buildSpeedReading(
      trendOf([model("mimo-v2.5", [3240, 1800], [70, 70])]),
    );
    expect(slower.line).toContain("80 % longer");
  });

  // Under "Both models", one pair of medians described a measurement only one of
  // them took — and the singular pronoun said so out loud.
  it("gives each model its own figures when both moved", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [3600, 1800], [70, 70]),
        model("mimo-v2.5-pro", [2800, 1800], [70, 70]),
      ]),
    );
    expect(reading.lead).toContain("Both models");
    expect(reading.line).toContain(
      "mimo-v2.5's first token takes 100 % longer",
    );
    expect(reading.line).toContain("mimo-v2.5-pro's first token takes 56 %");
    expect(reading.line).toContain("3600 ms against 1800 ms");
    expect(reading.line).toContain("2800 ms against 1800 ms");
    expect(reading.line).not.toContain("Its first token");
  });

  // The wait is one model's own, so handing it to the other one with "each"
  // states a wait that model never adds.
  it("splits the extra wait per model when the two differ", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [3600, 1800], [70, 70]),
        model("mimo-v2.5-pro", [2800, 1800], [70, 70]),
      ]),
    );
    // The unit is said once, so only the first figure carries the phrase.
    expect(reading.line).toContain(
      "1.8 s of extra waiting on a full-length answer for mimo-v2.5",
    );
    expect(reading.line).toContain("1.0 s for mimo-v2.5-pro");
    expect(reading.line).not.toContain("each");
  });

  // ...and keeps the shorter sentence when they really do cost the same.
  it("says each only when both models cost the same wait", () => {
    const reading = buildSpeedReading(
      trendOf([
        model("mimo-v2.5", [3000, 2000], [70, 70]),
        model("mimo-v2.5-pro", [3000, 2000], [70, 70]),
      ]),
    );
    expect(reading.line).toContain(
      "1.0 s of extra waiting on a full-length answer, each.",
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

  // The compared median cannot follow the edge of its own window: a spike that
  // ended an hour ago still owns it, and the page went on announcing a
  // slowdown over a plot that had been flat for an hour.
  it("withdraws the reading when the last hour is back on the reference level", () => {
    const reading = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [2016, 954], [70, 70]),
            "ttft",
            [950, 940, 960, 950],
          ),
        ],
        QUARTER,
      ),
    );
    expect(2016 / 954 - 1).toBeGreaterThan(TTFT_FLOOR);
    expect(reading.state).toBe("recovered");
    // No badge, no headline, no plot — and the figure that is still elevated is
    // printed with the hour that undid it, never on its own.
    expect(reading.lead).toBe("");
    expect(reading.moves).toEqual([]);
    expect(reading.metric).toBeNull();
    expect(reading.line).toContain("was slower earlier in the last 3 hours");
    expect(reading.line).toContain("2016 ms against 954 ms");
    expect(reading.line).toContain("back to normal for the last hour");
  });

  // ...and only when it really is back. The clear is the floor halved, not the
  // floor, so a reading sitting on its threshold cannot flicker between the two
  // states as single buckets roll off the hour.
  it("keeps the reading while the last hour is still elevated", () => {
    const still = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [4032, 1908], [70, 70]),
            "ttft",
            [4000, 3800, 4200, 4000],
          ),
        ],
        QUARTER,
      ),
    );
    expect(still.state).toBe("slower");

    // Halfway down is not down: 1.5x the reference is under the 0.7 floor and
    // still well over half of it.
    const halfway = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [4032, 1908], [70, 70]),
            "ttft",
            [2860, 2860, 2860, 2860],
          ),
        ],
        QUARTER,
      ),
    );
    expect(halfway.state).toBe("slower");
  });

  // Too thin an hour changes NOTHING in either direction. Withdrawing a claim
  // on two samples is the same error as raising one on two samples.
  it("withdraws nothing when the last hour is too thin to read", () => {
    const reading = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [4032, 1908], [70, 70]),
            "ttft",
            [1900, 1880],
            2,
          ),
        ],
        QUARTER,
      ),
    );
    expect(reading.state).toBe("slower");
  });

  // The mirror of the recovery lag: a slowdown twenty minutes old moves a
  // three-hour median by almost nothing, and the banner was as late to fire as
  // it was to clear.
  it("fires off the last hour when the compared median has not moved yet", () => {
    const reading = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [900, 900], [70, 70]),
            "ttft",
            [3400, 3500, 3400, 3600],
          ),
        ],
        QUARTER,
      ),
    );
    expect(reading.state).toBe("slower");
    // The TAIL's figures, never the compared median — which is 900 here and
    // supports none of this sentence.
    expect(reading.line).toContain("3400 ms against 900 ms");
    expect(reading.line).toContain("over the last hour");
    expect(reading.moves[0]!.spanS).toBe(TAIL_S);
  });

  // An AND across the whole hour, because this is a ~12-sample reading: three
  // samples must not be able to turn the page amber on their own.
  it("needs every quarter-hour to agree before the last hour may fire", () => {
    const reading = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [900, 900], [70, 70]),
            "ttft",
            [1700, 900, 1750, 1700],
          ),
        ],
        QUARTER,
      ),
    );
    expect(reading.state).toBe("steady");
  });

  // Throughput recovers on its own words, and on its own floor — a fifth of the
  // first token's.
  it("says a throughput drop is over in the throughput's own words", () => {
    const reading = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [900, 900], [35, 70]),
            "tps",
            [70, 69, 71, 70],
          ),
        ],
        QUARTER,
      ),
    );
    expect(reading.state).toBe("recovered");
    expect(reading.line).toContain("was generating more slowly earlier");
    expect(reading.line).toContain("35.0 against 70.0");
  });

  // Each model with its own figures here too — one pair of medians standing in
  // for the fleet is the bug the present-tense sentence already fixed.
  it("gives each recovered model its own clause", () => {
    const reading = buildSpeedReading(
      trendOf(
        [
          withTail(
            model("mimo-v2.5", [3600, 1800], [70, 70]),
            "ttft",
            [1800, 1810, 1790, 1800],
          ),
          withTail(
            model("mimo-v2.5-pro", [2800, 1800], [70, 70]),
            "ttft",
            [1800, 1790, 1810, 1800],
          ),
        ],
        QUARTER,
      ),
    );
    expect(reading.state).toBe("recovered");
    expect(reading.line).toContain("mimo-v2.5's first token was slower");
    expect(reading.line).toContain("mimo-v2.5-pro's first token was slower");
    expect(reading.line).toContain("3600 ms against 1800 ms");
    expect(reading.line).toContain("2800 ms against 1800 ms");
  });

  // The two spans can now differ inside one reading: one model fired off its
  // last hour and the other off the compared ones. A caveat worded from the
  // lead then said "in the last hour" about runs cut off two hours before it —
  // a sentence about a span that does not contain what it describes.
  it("names the widest censored span, not the lead's", () => {
    const hot = withTail(
      model("mimo-v2.5", [900, 900], [70, 70]),
      "ttft",
      [3400, 3400, 3400, 3400],
    );
    const censoredEarlier = model("mimo-v2.5-pro", [1700, 900], [70, 70]);
    censoredEarlier.ttft = {
      ...censoredEarlier.ttft,
      points: [
        REFERENCE_BUCKET,
        {
          t: GENERATED_AT_S - 2 * HOUR,
          n: 6,
          censored: 2,
          p50: 1700,
          p95: 2000,
        },
      ],
    };
    const reading = buildSpeedReading(trendOf([hot, censoredEarlier], QUARTER));
    expect(reading.state).toBe("slower");
    // The lead is the hour-old move, and the cut-off runs are not in it.
    expect(reading.moves[0]!.spanS).toBe(TAIL_S);
    expect(reading.line).toContain("Some requests in the last 3 hours");
    expect(reading.line).not.toContain("Some requests in the last hour");
  });

  // Faster is not what this page is asked about, so it says nothing — and in
  // particular not the steady sentence, which would claim a reading that moved
  // past its floor sits inside the ordinary spread.
  it("says nothing at all when a model got quicker", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [900, 900], [100, 70])]),
    );
    expect(reading.state).toBe("quicker");
    expect(reading.lead).toBe("");
    expect(reading.line).toBe("");
  });
  // A doubling is a real move and a two-second first token is not a headline.
  // The page published exactly this — "mimo-v2.5 is slow to start right now"
  // over 2016 ms — while the other model, three and a half seconds to first
  // token, sat underneath it with a chip on its card.
  it("keeps a doubled first token off the headline while the wait is still short", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [2016, 954], [70, 70])]),
    );
    expect(2016 / 954 - 1).toBeGreaterThan(TTFT_FLOOR);
    expect(2016).toBeLessThan(SLOW_TTFT_MS);
    expect(reading.state).toBe("minor");
    // Stated, not announced: no headline, no plot, and the figures and the
    // cost still printed.
    expect(reading.lead).toBe("");
    expect(reading.moves).toEqual([]);
    expect(reading.metric).toBeNull();
    expect(reading.line).toContain("2016 ms against 954 ms");
    expect(reading.line).toContain("1.1 s");
  });

  // The same gate in the throughput's own units: text arriving at 45 tokens a
  // second is faster than anyone reads, however fast it arrived yesterday.
  it("keeps a throughput drop off the headline while the rate is still quick", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [900, 900], [45, 70])]),
    );
    expect(70 / 45 - 1).toBeGreaterThan(TPS_FLOOR);
    expect(45).toBeGreaterThan(SLOW_TPS);
    expect(reading.state).toBe("minor");
    expect(reading.lead).toBe("");
    expect(reading.line).toContain("45.0 against 70.0");
  });

  // ...and the lead is what the gate reads, so a small move riding along with
  // a big one does not have to pass it on its own.
  it("leads once the reading itself is slow", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [3400, 1800], [45, 70])]),
    );
    expect(3400).toBeGreaterThanOrEqual(SLOW_TTFT_MS);
    expect(reading.state).toBe("slower");
    expect(reading.lead).toContain("slow to start");
  });
});
