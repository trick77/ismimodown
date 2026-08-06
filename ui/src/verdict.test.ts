import { describe, expect, it } from "vitest";
import type { ModelSummary, RecentCycle, Summary } from "./api/types";
import { FAULT_EDGE, FAULT_OK, FAULT_ROUTE, FAULT_UPLINK } from "./api/types";
import {
  buildVerdict,
  RECENT_CYCLES,
  scoreAvailability,
  scoreCorrectness,
  scoreRatio,
  worst,
} from "./verdict";

const MODEL = "mimo-v2.5";
const GENERATED_AT = "2026-08-04T12:00:00Z";
const CYCLE_MS = 5 * 60 * 1000;

function model(over: Partial<ModelSummary> = {}): ModelSummary {
  return {
    model_id: MODEL,
    probe: "short",
    ttft: { n: 100, sufficient: true, p50_ms: 900, p95_ms: 1200 },
    itl: { n: 100, sufficient: true, p50_ms: 24, p95_ms: 40 },
    tps: { n: 100, sufficient: true, p50_ms: 41, p95_ms: 60 },
    attempts: 100,
    succeeded: 100,
    available_pct: 100,
    censored: 0,
    answered: 100,
    correct: 100,
    correct_pct: 100,
    max_reasoning_tokens: 0,
    max_cached_tokens: 0,
    ...over,
  };
}

// A block of cycles, newest first, five minutes apart — the shape the daemon
// serves. `faults` describes the newest cycles in order; the rest are clean.
function recent(
  faults: string[] = [],
  over: { modelIds?: string[]; count?: number; newestAt?: string } = {},
): RecentCycle[] {
  const ids = over.modelIds ?? [MODEL];
  const count = over.count ?? 36;
  const newest = Date.parse(over.newestAt ?? GENERATED_AT);
  return Array.from({ length: count }, (_, i) => {
    const fault = faults[i] ?? FAULT_OK;
    const models: Record<string, { ok: boolean; answer_ok: boolean | null }> =
      {};
    for (const id of ids) {
      // An unattributed cycle is not a failed one: the run happened, the
      // network layer just recorded nothing about it.
      const reachable = fault === FAULT_OK || fault === "";
      models[id] = { ok: reachable, answer_ok: reachable ? true : null };
    }
    return {
      at: new Date(newest - i * CYCLE_MS).toISOString(),
      fault,
      models,
    };
  });
}

// The same block, but with a model failing on cycles the network reached fine —
// MiMo answered the handshake and then the inference run failed.
function recentModelFailures(
  failing: number[],
  over: { wrong?: number[] } = {},
): RecentCycle[] {
  return recent().map((cycle, i) => ({
    ...cycle,
    models: {
      [MODEL]: {
        ok: !failing.includes(i),
        answer_ok: over.wrong?.includes(i)
          ? false
          : failing.includes(i)
            ? null
            : true,
      },
    },
  }));
}

function summary(over: Partial<Summary> = {}): Summary {
  return {
    window: "24h",
    cycles: 288,
    models: [model()],
    net: [],
    faults: { ok: 288 },
    recent: recent(),
    skipped_runs: 0,
    generated_at: GENERATED_AT,
    ...over,
  };
}

describe("scoreRatio", () => {
  // Deliberately generous: latency is noisy, and a dashboard that cries wolf at
  // +20% gets ignored, which is worse than no dashboard.
  it("bands against the rolling baseline", () => {
    expect(scoreRatio(900, 900)).toBe("normal");
    expect(scoreRatio(1050, 900)).toBe("normal");
    expect(scoreRatio(1400, 900)).toBe("elevated");
    expect(scoreRatio(2500, 900)).toBe("degraded");
  });

  it("is unknown without a usable baseline", () => {
    expect(scoreRatio(900, null)).toBe("unknown");
    expect(scoreRatio(null, 900)).toBe("unknown");
    expect(scoreRatio(900, 0)).toBe("unknown");
  });
});

describe("lower-is-worse metrics", () => {
  // Scored against ABSOLUTE expectations, not a rolling baseline: a model that
  // has been 97% available all week has not made 97% acceptable.
  it("scores availability absolutely", () => {
    expect(scoreAvailability(100, 0)).toBe("normal");
    expect(scoreAvailability(99, 10)).toBe("elevated");
    expect(scoreAvailability(96.8, 32)).toBe("degraded");
    expect(scoreAvailability(null, 0)).toBe("unknown");
  });

  it("scores correctness absolutely", () => {
    expect(scoreCorrectness(100, 0)).toBe("normal");
    expect(scoreCorrectness(97, 30)).toBe("elevated");
    expect(scoreCorrectness(90, 100)).toBe("degraded");
    expect(scoreCorrectness(null, 0)).toBe("unknown");
  });

  // The floor. Over a day of cycles a single dropped connection is 99.65%
  // available — under the band — and painting a state on that is how the chip
  // came to mean nothing.
  it("refuses to make a state out of one or two failures", () => {
    expect(scoreAvailability(99.65, 1)).toBe("normal");
    expect(scoreAvailability(99.3, 2)).toBe("normal");
    expect(scoreAvailability(99.0, 3)).toBe("elevated");
    expect(scoreCorrectness(98, 1)).toBe("normal");
    expect(scoreCorrectness(94, 3)).toBe("degraded");
  });
});

describe("worst", () => {
  it("picks the most severe state", () => {
    expect(worst("normal", "degraded", "elevated")).toBe("degraded");
    expect(worst("normal", "elevated")).toBe("elevated");
    expect(worst("unknown", "normal")).toBe("normal");
    expect(worst("unknown")).toBe("unknown");
  });
});

describe("buildVerdict", () => {
  it("says everything is normal when it is", () => {
    const v = buildVerdict(summary(), summary());
    expect(v.state).toBe("normal");
    expect(v.headline).toMatch(/normal/i);
  });

  it("shows the empty state before any data", () => {
    const v = buildVerdict(summary({ cycles: 0, recent: [] }), null);
    expect(v.state).toBe("unknown");
    expect(v.headline).toMatch(/collecting data/i);
  });

  // The reported bug. One failed cycle is an anecdote: it is indistinguishable
  // from one retransmit storm on one connection from one vantage point.
  it("does not call a single failed cycle degraded", () => {
    const v = buildVerdict(
      summary({ recent: recent([FAULT_EDGE]) }),
      summary(),
    );
    expect(v.state).toBe("elevated");
    expect(v.detail.join(" ")).toMatch(/not yet a pattern/i);
  });

  it("calls two consecutive failures what they are", () => {
    const v = buildVerdict(
      summary({ recent: recent([FAULT_EDGE, FAULT_EDGE]) }),
      summary(),
    );
    expect(v.state).toBe("degraded");
    expect(v.headline).toMatch(/edge is unreachable/i);
    expect(v.detail.join(" ")).toMatch(/last 2 cycles in a row/i);
  });

  // A flapping endpoint never builds a streak and is exactly as broken as one
  // that fails twice in a row.
  it("calls three scattered failures inside the hour degraded", () => {
    const faults = Array<string>(RECENT_CYCLES).fill(FAULT_OK);
    faults[0] = FAULT_EDGE;
    faults[4] = FAULT_EDGE;
    faults[9] = FAULT_EDGE;
    const v = buildVerdict(summary({ recent: recent(faults) }), summary());
    expect(v.state).toBe("degraded");
    expect(v.detail.join(" ")).toMatch(/3 of the last 12 cycles failed/i);
  });

  // The other half of the reported bug: the banner stayed red long after the
  // situation resolved, because it was reading counts over the whole window.
  it("forgets a fault the hour has moved past, and says what it forgot", () => {
    const faults = Array<string>(24).fill(FAULT_OK);
    faults[18] = FAULT_EDGE;
    faults[19] = FAULT_EDGE;
    const v = buildVerdict(
      summary({ faults: { ok: 286, edge: 2 }, recent: recent(faults) }),
      summary(),
    );
    expect(v.state).toBe("normal");
    expect(v.detail.join(" ")).toMatch(/last failed cycle was/i);
  });

  // The window's fault counts must no longer be able to fire the banner at all.
  it("ignores window fault counts when the recent cycles are clean", () => {
    const v = buildVerdict(
      summary({ faults: { ok: 200, edge: 40, route: 30, uplink: 18 } }),
      summary(),
    );
    expect(v.state).toBe("normal");
  });

  // Precedence runs uplink -> route -> edge, because each layer makes the ones
  // beyond it unreadable.
  it("declines to attribute before it blames anything else", () => {
    const v = buildVerdict(
      summary({
        recent: recent([FAULT_UPLINK, FAULT_UPLINK, FAULT_EDGE]),
        models: [model({ available_pct: 40, succeeded: 40 })],
      }),
      summary(),
    );
    expect(v.headline).toMatch(/nothing at the far end was reachable/i);
    // It may NAME MiMo, but only to disclaim it. Blaming the provider for our
    // own outage is the credibility-ending failure this whole layer prevents.
    expect(v.headline).toMatch(/says? nothing about MiMo/i);
    expect(v.headline).not.toMatch(
      /MiMo('s)? (is |edge )?(down|unreachable|broken)/i,
    );
  });

  // A tie breaks outward for the same reason the precedence runs outward: an
  // unattributable cycle cannot support a claim about MiMo's edge.
  it("breaks a mixed run outward and discloses the mix", () => {
    const v = buildVerdict(
      summary({ recent: recent([FAULT_EDGE, FAULT_UPLINK]) }),
      summary(),
    );
    expect(v.state).toBe("degraded");
    expect(v.headline).toMatch(/nothing at the far end was reachable/i);
    expect(v.detail.join(" ")).toMatch(/1 of them did reach the reference/i);
  });

  it("names the unattributable cycles inside an edge run", () => {
    const v = buildVerdict(
      summary({ recent: recent([FAULT_EDGE, FAULT_EDGE, FAULT_UPLINK]) }),
      summary(),
    );
    expect(v.headline).toMatch(/edge is unreachable/i);
    expect(v.detail.join(" ")).toMatch(/cannot be attributed to MiMo/i);
  });

  // Historical fault class: no longer produced, but stored cycles carry it and
  // must still read correctly rather than falling through to a model verdict.
  it("still reads stored route cycles, ahead of MiMo's edge", () => {
    const v = buildVerdict(
      summary({ recent: recent([FAULT_ROUTE, FAULT_ROUTE]) }),
      summary(),
    );
    expect(v.headline).toMatch(/route to the far end/i);
    // Never "the endpoint": that noun is MiMo's API everywhere else on the
    // page, and a route fault is explicitly not MiMo's.
    expect(v.headline).not.toMatch(/route to the endpoint/i);
  });

  // A daemon that died mid-incident leaves its last red cycles on record. Every
  // rule below this one would keep publishing an outage nobody is measuring.
  it("says so when the newest measurement is not current", () => {
    const v = buildVerdict(
      summary({
        recent: recent([FAULT_EDGE, FAULT_EDGE], {
          newestAt: "2026-08-04T11:20:00Z",
        }),
      }),
      summary(),
    );
    expect(v.state).toBe("unknown");
    expect(v.headline).toMatch(/probe itself may be down/i);
    expect(v.detail.join(" ")).toMatch(/40 minutes ago/);
  });

  // `cycles` counts the fixed window; `recent` does not. A daemon dead longer
  // than that window has cycles = 0 and a full block of stale cycles, and the
  // empty-state guard must not swallow the stale banner that owns this case.
  it("calls a daemon dead longer than the window stale, not empty", () => {
    const v = buildVerdict(
      summary({
        cycles: 0,
        recent: recent([], { newestAt: "2026-08-03T09:00:00Z" }),
      }),
      summary(),
    );
    expect(v.headline).toMatch(/probe itself may be down/i);
    expect(v.headline).not.toMatch(/collecting data/i);
  });

  // max_reasoning_tokens is a `max` over the whole fixed window, so it stays
  // true for a day after one bad run. Above the network branch it would swallow
  // an outage happening right now under a caveat about yesterday.
  it("lets a live fault speak ahead of a day-old reasoning caveat", () => {
    const v = buildVerdict(
      summary({
        recent: recent([FAULT_UPLINK, FAULT_UPLINK]),
        models: [model({ max_reasoning_tokens: 512 })],
      }),
      summary(),
    );
    expect(v.headline).toMatch(/nothing at the far end was reachable/i);
  });

  // The headline follows the severity here for the same reason it does in the
  // model branch: one cycle cannot support a present-tense absolute claim.
  it("does not headline a single failed cycle as an outage", () => {
    const v = buildVerdict(
      summary({ recent: recent([FAULT_EDGE]) }),
      summary(),
    );
    expect(v.state).toBe("elevated");
    expect(v.headline).not.toMatch(/edge is unreachable/i);
    expect(v.headline).toMatch(/missed a cycle/i);
  });

  // The index is a count of cycles, not a clock: a dropped slot leaves no cycle
  // behind — the scheduler records those as skipped_runs — so multiplying the
  // index out understates how long ago the failure was, in the flattering
  // direction.
  it("dates the last failure from the timestamps, not the cycle index", () => {
    // The three reds sit at indices 2-4, but everything from index 2 back is a
    // further two hours old: the slots between them were dropped.
    const cycles = recent([]).map((c, i) =>
      i < 2
        ? c
        : {
            ...c,
            fault: i < 5 ? FAULT_EDGE : c.fault,
            at: new Date(Date.parse(c.at) - 2 * 60 * 60 * 1000).toISOString(),
          },
    );
    const v = buildVerdict(summary({ recent: cycles }), summary());
    expect(v.state).toBe("degraded");
    // Two hours and ten minutes back, not the ten minutes the index implies.
    expect(v.detail.join(" ")).toMatch(/2 hours ago/);
    expect(v.detail.join(" ")).not.toMatch(/10 minutes ago/);
  });

  // A cycle with no stored attribution is not a failure. Absence of evidence is
  // not evidence, and a monitor must never round in that direction.
  it("treats an unattributed cycle as quiet, not as red", () => {
    const v = buildVerdict(summary({ recent: recent(["", ""]) }), summary());
    expect(v.state).toBe("normal");
  });

  // Reasoning coming back on invalidates every latency figure in the window, so
  // it outranks any latency verdict rather than sitting beside it.
  it("reports reasoning being switched on above any latency verdict", () => {
    const v = buildVerdict(
      summary({ models: [model({ max_reasoning_tokens: 512 })] }),
      summary(),
    );
    expect(v.state).toBe("degraded");
    expect(v.headline).toMatch(/reasoning is switched on/i);
  });

  // The same sentence the model card carries, and it counts in the singular for
  // the same reason: one leaked token is still the whole finding.
  it("counts a single reasoning token in the singular", () => {
    const v = buildVerdict(
      summary({ models: [model({ max_reasoning_tokens: 1 })] }),
      summary(),
    );
    expect(v.detail.join(" ")).toMatch(/up to 1 reasoning token\b/);
  });

  // A cold start: the daemon has served exactly one cycle and it failed. The
  // horizon is then 1, and "1 of the last 1 cycles" reads as a typo in the one
  // sentence that is supposed to be carefully hedged.
  it("says one cycle in the singular on a one-cycle horizon", () => {
    const v = buildVerdict(
      summary({ cycles: 1, recent: recent([FAULT_EDGE], { count: 1 }) }),
      summary(),
    );
    expect(v.detail.join(" ")).toMatch(/1 of the last 1 cycle /);
  });

  it("names the struggling model and quantifies the regression", () => {
    const v = buildVerdict(
      summary({
        recent: recentModelFailures([0, 1, 2]),
        models: [
          model({
            ttft: { n: 100, sufficient: true, p50_ms: 1540, p95_ms: 3000 },
          }),
        ],
      }),
      summary(),
    );
    expect(v.state).toBe("degraded");
    expect(v.headline).toMatch(/mimo-v2\.5 is having problems/i);
    expect(v.detail.join(" ")).toMatch(/failed 3 of the last 12 runs/i);
    expect(v.detail.join(" ")).toMatch(/71% longer/);
  });

  // One failed run is an anecdote whichever layer produced it, so the model
  // branch has to say so in the same voice the network branch does.
  it("softens a single model failure the way it softens a single cycle", () => {
    const v = buildVerdict(
      summary({ recent: recentModelFailures([0]) }),
      summary(),
    );
    expect(v.state).toBe("elevated");
    expect(v.headline).toMatch(/showing the odd failure/i);
    expect(v.headline).not.toMatch(/having problems/i);
    expect(v.detail.join(" ")).toMatch(/not yet a pattern/i);
  });

  // The run failed on connect, before it ever reached MiMo. Counting it against
  // the model manufactures provider downtime out of our own outage.
  it("does not charge a model for cycles nothing in Singapore answered", () => {
    const cycles = recent([FAULT_UPLINK, FAULT_UPLINK, FAULT_UPLINK]).map(
      (c, i) =>
        i < 3
          ? { ...c, models: { [MODEL]: { ok: false, answer_ok: null } } }
          : c,
    );
    const v = buildVerdict(summary({ recent: cycles }), summary());
    // The uplink verdict speaks, and it speaks about the uplink.
    expect(v.headline).toMatch(/nothing at the far end was reachable/i);
    expect(v.detail.join(" ")).not.toMatch(/mimo-v2\.5 failed/i);
  });

  // The canary: a silent reroute to a smaller model shows up here before it
  // shows up in any timing.
  it("reports wrong answers from a model that is answering", () => {
    const v = buildVerdict(
      summary({ recent: recentModelFailures([], { wrong: [0, 1, 3] }) }),
      summary(),
    );
    expect(v.state).toBe("degraded");
    expect(v.detail.join(" ")).toMatch(/3 of the last 12 questions wrongly/i);
  });
});
