package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// openTestDB opens a real SQLite file in t.TempDir() and migrates it. A real
// file rather than :memory: because WAL mode, busy_timeout and foreign_keys are
// exactly the behaviours worth exercising, and an in-memory DB does not model
// them the same way.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestOpenAppliesPragmas(t *testing.T) {
	db := openTestDB(t)

	var journal string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Error("foreign_keys must be on; cascade deletes from cycles depend on it")
	}
}

func TestOpenRejectsUnusablePath(t *testing.T) {
	// A directory is not a database file.
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("expected an error opening a directory as a database")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)

	// Already migrated by openTestDB; a second run must be a no-op rather than
	// re-applying CREATE TABLE and failing.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// Counted from the embedded files rather than hardcoded: a literal here
	// turns every future migration into a spurious test failure, which trains
	// people to edit the number instead of reading what broke.
	files, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if n != len(files) {
		t.Errorf("schema_migrations has %d rows, want %d", n, len(files))
	}
}

func TestSchemaTablesExistAndAreStrict(t *testing.T) {
	db := openTestDB(t)

	want := []string{"cycles", "net_probes", "infer_probes", "cycle_fault", "skipped_runs"}
	for _, table := range want {
		var name, sqlText string
		err := db.QueryRow(
			`SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name, &sqlText)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
		// STRICT is the guard against a parsing bug storing text in a REAL
		// column, where it would survive until a percentile query met it.
		if !strings.Contains(strings.ToUpper(sqlText), "STRICT") {
			t.Errorf("table %s is not STRICT", table)
		}
	}
}

// The network-vs-inference subtraction is a join on cycle_id. If an inference
// row could exist without a cycle, "server-side time" would silently become an
// unbacked number.
func TestInferProbeRequiresACycle(t *testing.T) {
	db := openTestDB(t)

	_, err := db.Exec(
		`INSERT INTO infer_probes (cycle_id, model_id, probe, ok) VALUES (999, 'mimo-v2.5', 'short', 1)`)
	if err == nil {
		t.Fatal("expected a foreign-key violation inserting an infer_probe for a nonexistent cycle")
	}
}

func TestDeletingACycleCascades(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO cycles (started_at) VALUES ('2026-08-04T06:00:00Z')`)
	if err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO net_probes (cycle_id, target, dns_ms, connect_ms, ok) VALUES (?, 'mimo_sgp', 3.4, 166.3, 1)`, []any{id}},
		{`INSERT INTO infer_probes (cycle_id, model_id, probe, ttft_ms, ok, answer_ok) VALUES (?, 'mimo-v2.5', 'short', 912.0, 1, 1)`, []any{id}},
		{`INSERT INTO cycle_fault (cycle_id, fault) VALUES (?, 'ok')`, []any{id}},
	} {
		if _, err := db.Exec(stmt.q, stmt.args...); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Retention sweeps whole cycles; the children must go with them rather than
	// accumulating as orphans that percentile queries would still find.
	if _, err := db.Exec(`DELETE FROM cycles WHERE id = ?`, id); err != nil {
		t.Fatalf("delete cycle: %v", err)
	}
	for _, table := range []string{"net_probes", "infer_probes", "cycle_fault"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows after its cycle was deleted", table, n)
		}
	}
}

func TestCheckConstraintsRejectUnknownEnums(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO cycles (started_at) VALUES ('2026-08-04T06:00:00Z')`)
	if err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	id, _ := res.LastInsertId()

	cases := []struct {
		name string
		q    string
	}{
		{"unknown ping target", `INSERT INTO net_probes (cycle_id, target, ok) VALUES (?, 'mimo_ams', 1)`},
		{"unknown probe kind", `INSERT INTO infer_probes (cycle_id, model_id, probe, ok) VALUES (?, 'mimo-v2.5', 'narrow', 1)`},
		{"unknown fault", `INSERT INTO cycle_fault (cycle_id, fault) VALUES (?, 'gremlins')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(tc.q, id); err == nil {
				t.Error("expected a CHECK constraint violation")
			}
		})
	}
}

// One network reading per target per cycle. A duplicate would double-count a
// target in the fault attribution.
func TestNetProbeIsUniquePerCycleAndTarget(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO cycles (started_at) VALUES ('2026-08-04T06:00:00Z')`)
	if err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	id, _ := res.LastInsertId()

	q := `INSERT INTO net_probes (cycle_id, target, ok) VALUES (?, 'mimo_sgp', 1)`
	if _, err := db.Exec(q, id); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.Exec(q, id); err == nil {
		t.Error("expected a primary-key violation on a duplicate (cycle_id, target)")
	}
}
