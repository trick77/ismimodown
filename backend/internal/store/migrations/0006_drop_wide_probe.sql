-- Drop the wide probe: its rows, and the column that told the two kinds apart.
--
-- The wide probe sent a ~3800-token summarisation hourly per model, and it
-- existed for one measurement: prefill cost, the difference between its
-- time-to-first-token and the short probe's. Fourteen days of production said
-- the endpoint cannot support that measurement.
--
-- Paired the way the dashboard paired it — the two probes' bucketed percentiles
-- against each other — the median difference came out NEGATIVE for mimo-v2.5
-- (-147 ms, with 55% of hours below zero), because a wide run and a short run
-- from different cycles are minutes apart and differ by queueing rather than by
-- prefill. Paired within the cycle, which is what this schema was built for, it
-- is real: +245 ms and +305 ms. But the per-run spread is ~1600 ms against a
-- ~250 ms effect, so a single reading carries nothing and only a weekly
-- aggregate resolves it — into a number that does not move. A panel drawing
-- that was drawing the endpoint's queue and calling it prefill.
--
-- So the measurement is not being fixed, it is being stopped, and with it goes
-- every trace of there having been two kinds of prompt. The `probe` column is
-- the last one.
--
-- Rebuilt rather than altered because SQLite cannot drop a column that appears
-- in a CHECK constraint, and `probe TEXT NOT NULL CHECK (probe IN ('short',
-- 'wide'))` is exactly that. 0003 and 0004 are the precedents; 0002 used a
-- plain ALTER because `origin` carried no constraint.
--
-- Rows are DISCARDED, not migrated, and this is the destructive half. A wide
-- row cannot be copied into a table with no column to say what it is, and it
-- would be a lie if it were: its ttft_ms is the timing of a 3800-token prompt,
-- and every reader of this table now assumes ~20. Averaged in, it would drag
-- every published percentile up by an amount nothing in the schema explains.
-- Retention would have deleted them within three months anyway; this is the
-- same deletion, taken deliberately and at once. Take a backup first — see
-- DEPLOY.md — because nothing here is reversible.
--
-- `id` is preserved, as in 0003. RecentSamples orders by (started_at, id) to
-- break ties within a cycle, so renumbering here would silently reorder
-- history.
--
-- Safe under the foreign_keys pragma the daemon opens with, and the migration
-- runner's transaction is enough: nothing REFERENCES infer_probes — it is a
-- child of cycles, never a parent — so dropping it violates no constraint, and
-- every copied row keeps the cycle_id it already had.

CREATE TABLE infer_probes_new (
  id       INTEGER PRIMARY KEY,
  cycle_id INTEGER NOT NULL REFERENCES cycles (id) ON DELETE CASCADE,
  model_id TEXT NOT NULL,

  -- ttft_ms is the first chunk carrying ANY delta; ttfat_ms the first chunk
  -- carrying actual content. They differ by one chunk in the healthy case
  -- (MiMo opens with an empty-content role chunk). Divergence beyond that is
  -- the alarm that thinking has silently come back on.
  ttft_ms    REAL,
  ttfat_ms   REAL,
  total_ms   REAL,
  itl_p50_ms REAL,
  itl_p95_ms REAL,
  output_tps REAL,

  prompt_tokens    INTEGER,
  output_tokens    INTEGER,
  -- Must stay near zero. A rise means the system prompt went missing (see
  -- config.DefaultSystemPrompt) and MiMo's own injected prompt is being served
  -- from cache. It used to carry a second reading on the wide probe, where a
  -- rise meant the cache-defeat nonce had broken; there is no wide probe now.
  cached_tokens    INTEGER,
  -- Hard gate: must be 0. Proves reasoning is actually disabled.
  reasoning_tokens INTEGER,

  -- Non-NULL on every row from here on. It was NULL on wide runs, which were
  -- ungraded — the ~3800-token summarisation had no single assertable answer —
  -- and that was the only source of an ungraded row.
  question_id TEXT,
  ok          INTEGER NOT NULL CHECK (ok IN (0, 1)),
  -- The correctness canary: did the answer contain what it had to contain.
  -- NULL when the run failed before an answer existed — and now ONLY then,
  -- since every run that succeeds is graded.
  answer_ok   INTEGER CHECK (answer_ok IN (0, 1)),
  http_status INTEGER,
  error_class TEXT,
  -- OPERATOR-ONLY. A provider error body can echo request fragments, so this
  -- column is never served by any public endpoint — asserted by a test.
  error_detail TEXT
) STRICT;

INSERT INTO infer_probes_new
SELECT
  id, cycle_id, model_id,
  ttft_ms, ttfat_ms, total_ms, itl_p50_ms, itl_p95_ms, output_tps,
  prompt_tokens, output_tokens, cached_tokens, reasoning_tokens,
  question_id, ok, answer_ok, http_status, error_class, error_detail
FROM infer_probes
WHERE probe = 'short';

DROP TABLE infer_probes;

ALTER TABLE infer_probes_new RENAME TO infer_probes;

CREATE INDEX idx_infer_probes_cycle ON infer_probes (cycle_id);
-- Was idx_infer_probes_model_probe (model_id, probe, cycle_id). Same index
-- minus the middle column: every query that filtered on probe has stopped.
CREATE INDEX idx_infer_probes_model ON infer_probes (model_id, cycle_id);
