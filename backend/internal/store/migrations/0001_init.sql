-- The aligned probe cycle, modelled explicitly.
--
-- The whole point of mimostats is subtracting the network layer from the
-- inference layer, and that subtraction is a JOIN on cycle_id — never a
-- nearest-timestamp guess. Every net_probes and infer_probes row therefore
-- hangs off a cycle, and a cycle with no network reading cannot silently
-- produce an inference sample whose "server-side time" is unbacked.
--
-- Every table is STRICT so a parsing bug cannot quietly store text in a REAL
-- column and reappear months later as an un-averageable percentile.

CREATE TABLE cycles (
  id         INTEGER PRIMARY KEY,
  -- RFC3339 UTC. Text rather than a Unix integer so the raw DB stays readable
  -- with sqlite3 on the server during the manual smoke check.
  started_at TEXT NOT NULL,
  origin     TEXT NOT NULL
) STRICT;

CREATE INDEX idx_cycles_started_at ON cycles (started_at);
CREATE INDEX idx_cycles_origin_started_at ON cycles (origin, started_at);

-- The network layer: a bare TCP handshake to port 443. No TLS, no HTTP, no
-- auth, no tokens.
--
-- 'ref_eu' is historical — that probe was removed and nothing writes it any
-- more. It stays in the CHECK because rows written before the removal are still
-- stored and still readable.
CREATE TABLE net_probes (
  cycle_id    INTEGER NOT NULL REFERENCES cycles (id) ON DELETE CASCADE,
  target      TEXT NOT NULL CHECK (target IN ('mimo_sgp', 'ref_sgp', 'ref_eu')),
  dns_ms      REAL,
  connect_ms  REAL,
  ok          INTEGER NOT NULL CHECK (ok IN (0, 1)),
  error_class TEXT,
  PRIMARY KEY (cycle_id, target)
) STRICT;

-- The inference layer. `probe` separates the ~40-token infer run from the
-- ~4000-token hourly wide run: the gap between their TTFTs IS the prefill
-- signal, so the two are always filtered apart and never aggregated together.
CREATE TABLE infer_probes (
  id       INTEGER PRIMARY KEY,
  cycle_id INTEGER NOT NULL REFERENCES cycles (id) ON DELETE CASCADE,
  model_id TEXT NOT NULL,
  probe    TEXT NOT NULL CHECK (probe IN ('infer', 'wide')),

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
  -- `infer` it means the system prompt went missing (see config.DefaultSystemPrompt).
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

CREATE INDEX idx_infer_probes_cycle ON infer_probes (cycle_id);
CREATE INDEX idx_infer_probes_model_probe ON infer_probes (model_id, probe, cycle_id);

-- Fault attribution, derived per cycle from the three net_probes rows and
-- stored rather than recomputed, so the availability strip and the availability
-- arithmetic can never disagree and the rule is testable in exactly one place.
--
--   mimo ok                       -> 'ok'
--   mimo fail, ref_sgp ok         -> 'edge'   (MiMo's edge; the route is fine)
--   mimo + ref_sgp fail           -> 'uplink' (unattributable; window excluded
--                                              from availability)
--
-- 'route' is historical: it needed a third probe (ref_eu, a European PoP) to be
-- distinguishable from 'uplink' and is no longer produced. The old rule was
--
--   mimo + ref_sgp fail, ref_eu ok-> 'route'  (Europe->Singapore degraded)
--   all three fail                -> 'uplink' (ours)
--
-- and it stays in the CHECK, as does 'ref_eu' in net_probes above, because
-- cycles recorded under it are still stored and must still read correctly.
CREATE TABLE cycle_fault (
  cycle_id INTEGER PRIMARY KEY REFERENCES cycles (id) ON DELETE CASCADE,
  fault    TEXT NOT NULL CHECK (fault IN ('ok', 'edge', 'route', 'uplink'))
) STRICT;

-- The overrun guard's counter. A skipped run means the previous run was still
-- in flight when the timer fired; silently skipping would make the availability
-- strip lie by omission, so the count is surfaced rather than swallowed.
CREATE TABLE skipped_runs (
  id          INTEGER PRIMARY KEY,
  occurred_at TEXT NOT NULL,
  origin      TEXT NOT NULL,
  model_id    TEXT NOT NULL,
  probe       TEXT NOT NULL CHECK (probe IN ('infer', 'wide'))
) STRICT;

CREATE INDEX idx_skipped_runs_occurred_at ON skipped_runs (occurred_at);
