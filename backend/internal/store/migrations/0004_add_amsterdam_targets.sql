-- Admit the Amsterdam ping targets.
--
-- Xiaomi fronts MiMo from Amsterdam as well as Singapore
-- (token-plan-ams.xiaomimimo.com is a CNAME to mimo-pri-azams.alb.xiaomi.com),
-- and "The wire itself" now draws that edge beside its own independent
-- reference. Two new target values, one edge and one control, exactly mirroring
-- the Singapore pair.
--
-- Rebuilt rather than updated because SQLite cannot ALTER a CHECK constraint:
-- widening the enum in place is not expressible, and without the rebuild the
-- first `mimo_ams` insert fails the constraint — taking the whole cycle
-- transaction with it, including the Singapore rows and every inference probe
-- recorded alongside them.
--
-- Ordering is safe by construction: cmd/ismimodown/main.go runs store.Migrate
-- and returns its error before the scheduler is built, so no cycle can be
-- written against the old CHECK.
--
-- 'ref_eu' STAYS. It is historical — a nearby European PoP, removed, nothing
-- writes it — but rows carrying it are still stored and still readable, and
-- dropping it from the enum would make the rebuild's own INSERT fail on them.
-- 'ref_ams' is not its return: ref_eu fed fault attribution, and the Amsterdam
-- pair deliberately does not (see probe.AttributeFault).
--
-- Safe under the foreign_keys pragma the daemon opens with, and the migration
-- runner's transaction is enough: nothing REFERENCES net_probes — it is a child
-- of cycles, never a parent — so dropping it violates no constraint, and every
-- copied row carries the cycle_id it already had.
--
-- No index to recreate: (cycle_id, target) is the primary key and net_probes
-- carries no secondary index.

CREATE TABLE net_probes_new (
  cycle_id    INTEGER NOT NULL REFERENCES cycles (id) ON DELETE CASCADE,
  target      TEXT NOT NULL CHECK (target IN ('mimo_sgp', 'ref_sgp', 'mimo_ams', 'ref_ams', 'ref_eu')),
  dns_ms      REAL,
  connect_ms  REAL,
  ok          INTEGER NOT NULL CHECK (ok IN (0, 1)),
  error_class TEXT,
  PRIMARY KEY (cycle_id, target)
) STRICT;

INSERT INTO net_probes_new
SELECT cycle_id, target, dns_ms, connect_ms, ok, error_class FROM net_probes;

DROP TABLE net_probes;

ALTER TABLE net_probes_new RENAME TO net_probes;
