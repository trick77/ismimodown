// Scoring. "Is 996 ms good?" is unanswerable in the abstract, so no figure is
// published without the context needed to read it.
import type { ModelSummary, Summary } from "./api/types";
import { FAULT_EDGE, FAULT_ROUTE, FAULT_UPLINK } from "./api/types";

export type State = "normal" | "elevated" | "degraded" | "unknown";

// Bands for higher-is-worse metrics, scored against the metric's own rolling
// baseline rather than an absolute number.
//
// Deliberately generous. Latency is noisy, and a dashboard that cries wolf at
// +20% gets ignored — which is worse than no dashboard. A rolling baseline also
// means the page keeps working if MiMo gets permanently faster or slower.
export const ELEVATED_RATIO = 1.5;
export const DEGRADED_RATIO = 2.5;

export function scoreRatio(
  current: number | null,
  baseline: number | null,
): State {
  if (
    current === null ||
    baseline === null ||
    !Number.isFinite(current) ||
    !Number.isFinite(baseline) ||
    baseline <= 0
  ) {
    return "unknown";
  }
  const ratio = current / baseline;
  if (ratio >= DEGRADED_RATIO) return "degraded";
  if (ratio >= ELEVATED_RATIO) return "elevated";
  return "normal";
}

// Lower-is-worse metrics are scored against ABSOLUTE expectations, not a
// rolling baseline: a model that has been 97% available all week has not made
// 97% acceptable, and a drifting baseline would quietly normalise a fault.
export const AVAILABILITY_ELEVATED = 99.5;
export const AVAILABILITY_DEGRADED = 98;
export const CORRECTNESS_ELEVATED = 98;
export const CORRECTNESS_DEGRADED = 95;

export function scoreAvailability(pct: number | null): State {
  if (pct === null || !Number.isFinite(pct)) return "unknown";
  if (pct < AVAILABILITY_DEGRADED) return "degraded";
  if (pct < AVAILABILITY_ELEVATED) return "elevated";
  return "normal";
}

export function scoreCorrectness(pct: number | null): State {
  if (pct === null || !Number.isFinite(pct)) return "unknown";
  if (pct < CORRECTNESS_DEGRADED) return "degraded";
  if (pct < CORRECTNESS_ELEVATED) return "elevated";
  return "normal";
}

export function worst(...states: State[]): State {
  if (states.includes("degraded")) return "degraded";
  if (states.includes("elevated")) return "elevated";
  if (states.includes("normal")) return "normal";
  return "unknown";
}

export type Verdict = {
  state: State;
  headline: string;
  detail: string[];
};

// buildVerdict states the situation in plain English before any number appears.
//
// Precedence runs uplink -> route -> edge -> model, because each layer makes
// the ones beyond it unreadable: if our own uplink was down we cannot say
// anything about MiMo, and saying it anyway is how a monitor publishes an
// outage that never happened.
export function buildVerdict(
  summary: Summary | null,
  baseline: Summary | null,
): Verdict {
  if (!summary || summary.cycles === 0) {
    return {
      state: "unknown",
      headline: "Collecting data — first samples within 5 minutes",
      detail: [],
    };
  }

  const faults = summary.faults ?? {};
  if (faults[FAULT_UPLINK]) {
    return {
      state: "degraded",
      headline:
        "Our own uplink was down — these windows say nothing about MiMo",
      detail: [
        `${faults[FAULT_UPLINK]} of ${summary.cycles} cycles could not reach any reference host, so they are excluded from availability.`,
      ],
    };
  }
  if (faults[FAULT_ROUTE]) {
    return {
      state: "degraded",
      headline: "The route to Singapore is degraded — not MiMo, and not us",
      detail: [
        `${faults[FAULT_ROUTE]} of ${summary.cycles} cycles could not reach MiMo's edge OR an unrelated Singapore host.`,
      ],
    };
  }
  if (faults[FAULT_EDGE]) {
    return {
      state: "degraded",
      headline: "MiMo's edge is unreachable — the route to Singapore is fine",
      detail: [
        `${faults[FAULT_EDGE]} of ${summary.cycles} cycles failed to reach MiMo while a second Singapore host answered.`,
      ],
    };
  }

  const detail: string[] = [];
  let state: State = "normal";

  for (const model of summary.models) {
    const base = baseline?.models.find((m) => m.model_id === model.model_id);
    const modelState = scoreModel(model, base ?? null);
    state = worst(state, modelState.state);
    detail.push(...modelState.detail);
  }

  // A non-zero reasoning count invalidates every latency figure in the window,
  // so it outranks any latency verdict rather than sitting beside it.
  const reasoning = summary.models.filter((m) => m.max_reasoning_tokens > 0);
  if (reasoning.length > 0) {
    return {
      state: "degraded",
      headline:
        "Reasoning is switched on — the latency figures are not measuring what they claim",
      detail: reasoning.map(
        (m) =>
          `${m.model_id} returned up to ${m.max_reasoning_tokens} reasoning tokens despite thinking being disabled.`,
      ),
    };
  }

  if (state === "normal") {
    return {
      state,
      headline: "Everything looks normal right now",
      detail,
    };
  }
  const struggling = summary.models
    .filter(
      (m) =>
        scoreModel(
          m,
          baseline?.models.find((b) => b.model_id === m.model_id) ?? null,
        ).state !== "normal",
    )
    .map((m) => m.model_id);
  return {
    state,
    headline: `${struggling.join(" and ")} ${struggling.length > 1 ? "are" : "is"} having problems right now`,
    detail,
  };
}

function scoreModel(
  model: ModelSummary,
  baseline: ModelSummary | null,
): { state: State; detail: string[] } {
  const detail: string[] = [];

  const availability = scoreAvailability(
    model.attempts > 0 ? model.available_pct : null,
  );
  if (availability !== "normal" && availability !== "unknown") {
    detail.push(
      `${model.model_id} availability has dropped to ${model.available_pct.toFixed(1)}%.`,
    );
  }

  const correctness = scoreCorrectness(model.correct_pct);
  if (correctness !== "normal" && correctness !== "unknown") {
    detail.push(
      `${model.model_id} answered ${model.correct_pct?.toFixed(1)}% of questions correctly.`,
    );
  }

  const ttft = scoreRatio(model.ttft.p50_ms, baseline?.ttft.p50_ms ?? null);
  if (ttft !== "normal" && ttft !== "unknown") {
    const pct = Math.round(
      ((model.ttft.p50_ms ?? 0) / (baseline?.ttft.p50_ms ?? 1) - 1) * 100,
    );
    detail.push(
      `${model.model_id} first tokens are taking ${pct}% longer than its 7-day normal.`,
    );
  }

  return { state: worst(availability, correctness, ttft), detail };
}
