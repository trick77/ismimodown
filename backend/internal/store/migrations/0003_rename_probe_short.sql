-- Rename the short probe's kind from `infer` to `short`.
--
-- Both probes run an inference. What differs is the prompt — ~20 tokens against
-- ~4 000 — so naming one of them after the operation they share said nothing,
-- and invited the reader to think `wide` was some other sort of thing. The UI
-- stopped saying `infer` when the Raw cycles table grew a Probe column; this is
-- the wire and the schema catching up.
--
-- The TABLE keeps its name. `infer_probes` holds both kinds and means
-- "inference probes", which is what they are — the same word doing honest work
-- at a different level. Only the VALUE was ever wrong.
--
-- Rebuilt rather than updated because SQLite cannot ALTER a CHECK constraint:
-- an UPDATE alone would leave every new row still measured against
-- CHECK (probe IN ('infer', 'wide')) and fail on the first `short` insert.
--
-- Safe under the foreign_keys pragma the daemon opens with, and the migration
-- runner's transaction is enough: nothing REFERENCES infer_probes — it is a
-- child of cycles, never a parent — so dropping it violates no constraint, and
-- every copied row carries the cycle_id it already had.
--
-- `id` is preserved on both tables. RecentSamples orders by (started_at, id) to
-- break ties within a cycle, so renumbering here would silently reorder history.

CREATE TABLE infer_probes_new (
  id       INTEGER PRIMARY KEY,
  cycle_id INTEGER NOT NULL REFERENCES cycles (id) ON DELETE CASCADE,
  model_id TEXT NOT NULL,
  probe    TEXT NOT NULL CHECK (probe IN ('short', 'wide')),

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
  -- Must stay near zero. On `wide` a rise means the cache-defeat nonce stopped
  -- working and the prefill numbers have quietly become cache lookups; on
  -- `short` it means the system prompt went missing (see
  -- config.DefaultSystemPrompt).
  cached_tokens    INTEGER,
  -- Hard gate: must be 0. Proves reasoning is actually disabled.
  reasoning_tokens INTEGER,

  question_id TEXT,
  ok          INTEGER NOT NULL CHECK (ok IN (0, 1)),
  -- The correctness canary: did the answer contain what it had to contain.
  -- NULL when the run failed before an answer existed.
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
  CASE probe WHEN 'infer' THEN 'short' ELSE probe END,
  ttft_ms, ttfat_ms, total_ms, itl_p50_ms, itl_p95_ms, output_tps,
  prompt_tokens, output_tokens, cached_tokens, reasoning_tokens,
  question_id, ok, answer_ok, http_status, error_class, error_detail
FROM infer_probes;

DROP TABLE infer_probes;

ALTER TABLE infer_probes_new RENAME TO infer_probes;

CREATE INDEX idx_infer_probes_cycle ON infer_probes (cycle_id);
CREATE INDEX idx_infer_probes_model_probe ON infer_probes (model_id, probe, cycle_id);

-- skipped_runs carries the same CHECK, and is rebuilt in its post-0002 shape:
-- `origin` was dropped there and must not come back.

CREATE TABLE skipped_runs_new (
  id          INTEGER PRIMARY KEY,
  occurred_at TEXT NOT NULL,
  model_id    TEXT NOT NULL,
  probe       TEXT NOT NULL CHECK (probe IN ('short', 'wide'))
) STRICT;

INSERT INTO skipped_runs_new
SELECT
  id, occurred_at, model_id,
  CASE probe WHEN 'infer' THEN 'short' ELSE probe END
FROM skipped_runs;

DROP TABLE skipped_runs;

ALTER TABLE skipped_runs_new RENAME TO skipped_runs;

CREATE INDEX idx_skipped_runs_occurred_at ON skipped_runs (occurred_at);
