import { describe, expect, it } from "vitest";
import type { ModelTrend, Point, Trend } from "./api/types";
import {
  buildSpeedReading,
  figureDelta,
  TPS_FLOOR,
  TTFT_FLOOR,
  TTFT_FLOOR_BOTH,
} from "./trend";

const points = (censored = 0): Point[] => [
  { t: 1, n: 6, censored, p50: 900, p95: 1200 },
  { t: 2, n: 6, censored: 0, p50: 950, p95: 1300 },
];

function metric(recent: number | null, before: number | null, censored = 0) {
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
    points: points(censored),
  };
}

function trendOf(models: ModelTrend[]): Trend {
  return {
    recent_s: 3 * 3600,
    before_s: 24 * 3600,
    bucket_s: 1800,
    models,
    generated_at: "2026-08-20T12:00:00Z",
  };
}

const model = (
  id: string,
  ttft: [number | null, number | null],
  tps: [number | null, number | null],
  censored = 0,
): ModelTrend => ({
  model_id: id,
  ttft: metric(ttft[0], ttft[1], censored),
  tps: metric(tps[0], tps[1], censored),
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
  it("says the change is understated when either period was censored", () => {
    const reading = buildSpeedReading(
      trendOf([model("mimo-v2.5", [1700, 900], [70, 70], 2)]),
    );
    expect(reading.line).toContain("at least this large");
    expect(reading.line).not.toContain("Nothing failed");
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

describe("figureDelta", () => {
  it("prints ordinary movement quietly and a cleared floor loudly", () => {
    const t = trendOf([model("mimo-v2.5", [1100, 900], [70, 70])]);
    const quiet = figureDelta(t, "mimo-v2.5", "ttft");
    expect(quiet?.words).toContain("slower");
    expect(quiet?.past).toBe(false);

    const loud = figureDelta(
      trendOf([model("mimo-v2.5", [1700, 900], [70, 70])]),
      "mimo-v2.5",
      "ttft",
    );
    expect(loud?.past).toBe(true);
  });

  it("calls a small change the same rather than printing a number for noise", () => {
    const t = trendOf([model("mimo-v2.5", [930, 900], [70, 70])]);
    expect(figureDelta(t, "mimo-v2.5", "ttft")?.words).toContain(
      "about the same",
    );
  });

  it("names its own period, since the card's window does not apply to it", () => {
    const t = trendOf([model("mimo-v2.5", [1700, 900], [70, 70])]);
    expect(figureDelta(t, "mimo-v2.5", "ttft")?.words).toContain("24 hours");
  });

  it("is absent for a model the block does not carry", () => {
    const t = trendOf([model("mimo-v2.5", [900, 900], [70, 70])]);
    expect(figureDelta(t, "mimo-v2.5-pro", "ttft")).toBeNull();
    expect(figureDelta(null, "mimo-v2.5", "ttft")).toBeNull();
  });
});
