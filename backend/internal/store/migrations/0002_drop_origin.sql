-- Drop the origin label.
--
-- 0001 carried `origin` on cycles and skipped_runs so a second probe location
-- could be added without a migration. There is one probe host, there is no
-- second one planned, and nothing ever filtered or grouped by the column —
-- Summarize took it as an argument and echoed it straight back into the
-- response. It was a constant wearing a dimension's clothes, and the value it
-- carried named the site the box sits in.
--
-- DROP COLUMN rather than leaving it NOT NULL and unused: a STRICT table with a
-- mandatory column nothing writes is a trap for the next INSERT.

DROP INDEX idx_cycles_origin_started_at;

ALTER TABLE cycles DROP COLUMN origin;

ALTER TABLE skipped_runs DROP COLUMN origin;
