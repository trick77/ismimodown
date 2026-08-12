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
  ttft: Stats;
  itl: Stats;
  tps: Stats;
  attempts: number;
  succeeded: number;
  available_pct: number;
  // censored is how many attempts our own timeout ladder cut off. It is the
  // caveat the percentiles cannot carry: they are computed over runs that
  // FINISHED, so the runs counted here were removed from the top of the
  // distribution — and the figures improve as truncation worsens.
  censored: number;
  answered: number;
  correct: number;
  correct_pct: number | null;
  // Must stay 0. Non-zero means reasoning came back on and every latency figure
  // in the window is measuring something else.
  max_reasoning_tokens: number;
  // Must stay near 0, or the system prompt went missing and MiMo's own
  // injected one is being served from cache.
  max_cached_tokens: number;
};

export type NetSummary = {
  target: string;
  connect: Stats;
  attempts: number;
  succeeded: number;
  available_pct: number;
};

// One model's outcome in one cycle. Two fields, because a "how is it right
// now" reading acts on whether the run happened and whether it was right —
// never on its timings, which are scored from a window's percentiles.
export type RecentRun = { ok: boolean; answer_ok: boolean | null };

// One cycle with its stored attribution, newest first.
//
// This is the only part of the summary NOT scoped to the window. fault arrives
// RAW — including the historical "route" and the empty string a cycle with no
// attribution row carries — because deciding what those mean is verdict.ts's
// job, not the daemon's.
export type RecentCycle = {
  at: string;
  fault: string;
  models: Record<string, RecentRun>;
};

export type Summary = {
  window: string;
  cycles: number;
  models: ModelSummary[];
  net: NetSummary[];
  // NOT window-scoped, unlike everything above it: `cycles` and the model and
  // net blocks count what happened over the window, while this is what is
  // happening now, and the two answer different questions.
  recent: RecentCycle[];
  generated_at: string;
};

export type Point = {
  t: number;
  n: number;
  // censored is how many samples in this bucket were cut off by the timeout
  // ladder. A bucket can be censored and still carry a value — the line is then
  // drawn from the runs that finished — and a bucket with n = 0 and censored > 0
  // exists precisely so total truncation cannot be mistaken for a gap.
  censored: number;
  // null means the bucket had too few successful samples. It must render as a
  // GAP, never as zero.
  p50: number | null;
  p95: number | null;
};

export type ModelSeries = {
  window: string;
  bucket_s: number;
  metric: string;
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
  ttft_ms: number | null;
  total_ms: number | null;
  itl_p50_ms: number | null;
  output_tps: number | null;
  // What the run sent and what it generated. Null on a failure, like every
  // other measurement here — nothing was produced, which is not the same as
  // zero.
  prompt_tokens: number | null;
  output_tokens: number | null;
  ok: boolean;
  answer_ok: boolean | null;
  // Note what is absent: error_detail, and which question produced answer_ok.
  // The server never serves either.
  error_class: string | null;
};

// One inference call that went wrong, as the errors block serves it.
//
// Wrong in either of the two senses the probe records: the call failed, or the
// call succeeded and the answer was graded wrong. answer_ok is what tells them
// apart — see below.
//
// http_status is what this adds over the raw table: it tells a 429 apart from a
// 503 when both arrive as error_class "http_error". Null on a transport
// failure, which never got as far as a status — the card draws a dash for that
// rather than a 0, which would read as a status code.
//
// Note what is absent, here as everywhere else: error_detail. The upstream's
// own words can echo the request back, so they stay in the daemon's logs.
export type Failure = {
  at: string;
  model_id: string;
  error_class: string | null;
  http_status: number | null;
  // false when the call SUCCEEDED and the answer was then graded wrong — a
  // 200, a body, and the wrong element in it. null on everything else: a
  // failed run answered nothing to grade. The card reads this BEFORE it
  // reaches for the failure colour, because
  // the one thing a graded-wrong row cannot claim is that the endpoint was
  // down. Same shape and same reasoning as Cycle.answer_ok, which is what the
  // pulse strip paints amber on.
  answer_ok: boolean | null;
  // The cycle's stored attribution, so a row can say whose failure it was. Raw,
  // including the historical FAULT_ROUTE and the empty string a cycle with no
  // attribution carries — reading those is the client's job, as it is for
  // RecentCycle.fault. A failure on an unattributable cycle stays in the list
  // and gets labelled: dropping it would make the card claim a clean day during
  // an outage.
  fault: string;
};

export type SamplesResponse = {
  model_id: string;
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
  cycles: Cycle[];
};

// The five lines the page draws.
//
export type DashboardSeries = {
  ttft: ModelSeries;
  tps: ModelSeries;
  total: ModelSeries;
  network: NetSeries;
};

// Everything one render needs, in one response.
//
// `now` and `baseline` are the two fixed windows the verdict compares the
// selected one against — 24h against 7d — chosen by the server rather than
// asked for, because they are the page's question rather than a caller's.
//
// `pulse` and `samples` are both one group per model. They arrive as groups
// rather than pre-merged: mixing two models' TTFTs into one array is exactly
// what the per-model split exists to prevent, so the merge stays here, where
// the component that needs it can sort on the instant.
export type Dashboard = {
  window: string;
  generated_at: string;
  summary: Summary;
  now: Summary;
  baseline: Summary;
  series: DashboardSeries;
  cost: CostBreakdown;
  pulse: PulseResponse[];
  samples: SamplesResponse[];
  // The last few failed calls over a FIXED day, whichever window is selected.
  // The server pins that day deliberately: the current failures are a fact
  // about the endpoint, not about the chart selector.
  failures: Failure[];
};

// Two regions, each an edge paired with an independent reference.
//
// TARGET_MIMO keeps its unqualified name: it is the edge the site actually
// infers against, and every verdict on the page is about that one. The
// Amsterdam pair appears only in "The wire itself" — it is charted, never
// summarised, and never reaches a fault. See probe.AttributeFault for why.
export const TARGET_MIMO = "mimo_sgp";
export const TARGET_REF_SGP = "ref_sgp";
export const TARGET_MIMO_AMS = "mimo_ams";
export const TARGET_REF_AMS = "ref_ams";

export const FAULT_OK = "ok";
export const FAULT_EDGE = "edge";
// FAULT_ROUTE is no longer produced: telling a degraded route apart from a dead
// uplink needed a second reference host, and there is only one now. Stored
// cycles from before that change still carry it, so the dashboard still has to
// render it.
export const FAULT_ROUTE = "route";
export const FAULT_UPLINK = "uplink";

// Cost. What the probing itself bills, priced server-side from the usage MiMo
// reported on every run.
export type CostTokens = {
  // prompt is the WHOLE input count and cached is a subset of it, exactly as
  // MiMo reports them. Adding the two would count the cached part twice.
  prompt: number;
  cached: number;
  output: number;
};

export type CostGroup = {
  runs: number;
  tokens: CostTokens;
  // Nullable in the shape, non-null in practice: the server prices from a
  // constant table, so every group now carries a figure. The null stays in the
  // type because "not priced" and "$0.00" are different claims, and the
  // formatter that keeps them apart is worth more than the narrower type.
  usd: number | null;
  list_usd: number | null;
};

export type CostPoint = { t: number; usd: number | null; runs: number };

export type CostBreakdown = {
  window: string;
  currency: string;
  offpeak_coefficient: number;
  total: CostGroup;
  phases: ({ phase: "full" | "offpeak" } & CostGroup)[];
  series: CostPoint[];
  bucket_s: number;
  // unpriced_runs is how many runs carry no usage at all — cut off before the
  // usage chunk, and billed anyway. They are in no figure above, so the panel
  // has to say so rather than let the total quietly run low.
  unpriced_runs: number;
  // The reduced-rate spans inside this window, in unix SECONDS, clipped to it.
  // Server-derived: the client keeps no clock of its own.
  offpeak_spans: [number, number][];
  offpeak_until: number;
  offpeak_active: boolean;
  generated_at: string;
};
