import { describe, expect, it } from "vitest";
import type { ModelSummary, Summary } from "./api/types";
import {
  buildVerdict,
  scoreAvailability,
  scoreCorrectness,
  scoreRatio,
  worst,
} from "./verdict";

function model(over: Partial<ModelSummary> = {}): ModelSummary {
  return {
    model_id: "mimo-v2.5",
    probe: "infer",
    ttft: { n: 100, sufficient: true, p50_ms: 900, p95_ms: 1200 },
    itl: { n: 100, sufficient: true, p50_ms: 24, p95_ms: 40 },
    tps: { n: 100, sufficient: true, p50_ms: 41, p95_ms: 60 },
    attempts: 100,
    succeeded: 100,
    available_pct: 100,
    answered: 100,
    correct: 100,
    correct_pct: 100,
    max_reasoning_tokens: 0,
    max_cached_tokens: 0,
    ...over,
  };
}

function summary(over: Partial<Summary> = {}): Summary {
  return {
    window: "24h",
    origin: "rbx",
    cycles: 288,
    models: [model()],
    net: [],
    faults: { ok: 288 },
    skipped_runs: 0,
    generated_at: "2026-08-04T12:00:00Z",
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
    expect(scoreAvailability(100)).toBe("normal");
    expect(scoreAvailability(99)).toBe("elevated");
    expect(scoreAvailability(96.8)).toBe("degraded");
    expect(scoreAvailability(null)).toBe("unknown");
  });

  it("scores correctness absolutely", () => {
    expect(scoreCorrectness(100)).toBe("normal");
    expect(scoreCorrectness(97)).toBe("elevated");
    expect(scoreCorrectness(90)).toBe("degraded");
    expect(scoreCorrectness(null)).toBe("unknown");
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
    const v = buildVerdict(summary({ cycles: 0 }), null);
    expect(v.state).toBe("unknown");
    expect(v.headline).toMatch(/collecting data/i);
  });

  // Precedence runs uplink -> route -> edge -> model, because each layer makes
  // the ones beyond it unreadable.
  it("blames our own uplink before anything else", () => {
    const v = buildVerdict(
      summary({
        faults: { ok: 200, edge: 40, route: 30, uplink: 18 },
        models: [model({ available_pct: 40, succeeded: 40 })],
      }),
      summary(),
    );
    expect(v.headline).toMatch(/our own uplink/i);
    // It may NAME MiMo, but only to disclaim it. Blaming the provider for our
    // own outage is the credibility-ending failure this whole layer prevents.
    expect(v.headline).toMatch(/say nothing about MiMo/i);
    expect(v.headline).not.toMatch(
      /MiMo('s)? (is |edge )?(down|unreachable|broken)/i,
    );
  });

  it("blames the route before MiMo's edge", () => {
    const v = buildVerdict(
      summary({ faults: { ok: 200, edge: 40, route: 30 } }),
      summary(),
    );
    expect(v.headline).toMatch(/route to singapore/i);
  });

  it("blames MiMo's edge when the route is demonstrably fine", () => {
    const v = buildVerdict(
      summary({ faults: { ok: 200, edge: 40 } }),
      summary(),
    );
    expect(v.headline).toMatch(/edge is unreachable/i);
    expect(v.detail.join(" ")).toMatch(/second Singapore host answered/i);
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

  it("names the struggling model and quantifies the regression", () => {
    const v = buildVerdict(
      summary({
        models: [
          model({
            model_id: "mimo-v2.5-pro",
            available_pct: 96.8,
            succeeded: 968,
            attempts: 1000,
            ttft: { n: 100, sufficient: true, p50_ms: 1540, p95_ms: 3000 },
          }),
        ],
      }),
      summary({
        models: [
          model({
            model_id: "mimo-v2.5-pro",
            ttft: { n: 100, sufficient: true, p50_ms: 900, p95_ms: 1200 },
          }),
        ],
      }),
    );
    expect(v.state).toBe("degraded");
    expect(v.headline).toMatch(/mimo-v2\.5-pro is having problems/i);
    expect(v.detail.join(" ")).toMatch(/96\.8%/);
    expect(v.detail.join(" ")).toMatch(/71% longer/);
  });
});
