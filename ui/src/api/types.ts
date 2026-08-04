// Wire types, mirroring the Go structs in internal/samples.

export type Stats = {
  n: number;
  // sufficient is the suppression flag. When false, p50_ms/p95_ms are null and
  // must render as "insufficient data" — never as 0, which would draw a floor
  // that does not exist.
  sufficient: boolean;
  p50_ms: number | null;
  p95_ms: number | null;
};

export type ModelSummary = {
  model_id: string;
  probe: string;
  ttft: Stats;
  itl: Stats;
  tps: Stats;
  attempts: number;
  succeeded: number;
  available_pct: number;
  answered: number;
  correct: number;
  correct_pct: number | null;
  // Must stay 0. Non-zero means reasoning came back on and every latency figure
  // in the window is measuring something else.
  max_reasoning_tokens: number;
  // Must stay near 0 on wide, or the prefill numbers are cache lookups.
  max_cached_tokens: number;
};

export type NetSummary = {
  target: string;
  connect: Stats;
  attempts: number;
  succeeded: number;
  available_pct: number;
};

export type Summary = {
  window: string;
  cycles: number;
  models: ModelSummary[];
  net: NetSummary[];
  faults: Record<string, number>;
  skipped_runs: number;
  generated_at: string;
};

export type Point = {
  t: number;
  n: number;
  // null means the bucket had too few successful samples. It must render as a
  // GAP, never as zero.
  p50: number | null;
  p95: number | null;
};

export type ModelSeries = {
  window: string;
  bucket_s: number;
  metric: string;
  probe: string;
  models: Record<string, Point[]>;
};

export type NetSeries = {
  window: string;
  bucket_s: number;
  metric: string;
  targets: Record<string, Point[]>;
};

export type Sample = {
  at: string;
  model_id: string;
  probe: string;
  ttft_ms: number | null;
  total_ms: number | null;
  itl_p50_ms: number | null;
  output_tps: number | null;
  ok: boolean;
  answer_ok: boolean | null;
  // Note what is absent: error_detail, and which question produced answer_ok.
  // The server never serves either.
  error_class: string | null;
};

export type SamplesResponse = {
  model_id: string;
  probe: string;
  samples: Sample[];
};

// One bar of the pulse strip. A Sample with every column the strip does not
// draw left on the server — a day of these is a shape, not a detail series.
export type Cycle = {
  at: string;
  ttft_ms: number | null;
  ok: boolean;
  answer_ok: boolean | null;
  error_class: string | null;
};

export type PulseResponse = {
  model_id: string;
  probe: string;
  cycles: Cycle[];
};

export type ModelInfo = { id: string; note: string };

export type ModelsResponse = {
  models: ModelInfo[];
  probes: string[];
  windows: string[];
};

export const TARGET_MIMO = "mimo_sgp";
export const TARGET_REF_SGP = "ref_sgp";

export const FAULT_OK = "ok";
export const FAULT_EDGE = "edge";
// FAULT_ROUTE is no longer produced: telling a degraded route apart from a dead
// uplink needed a second reference host, and there is only one now. Stored
// cycles from before that change still carry it, so the dashboard still has to
// render it.
export const FAULT_ROUTE = "route";
export const FAULT_UPLINK = "uplink";
