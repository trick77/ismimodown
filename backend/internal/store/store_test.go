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

// 0004 rebuilds net_probes to widen a CHECK, and a rebuild is the one migration
// shape that can silently lose data: DROP TABLE runs whether or not the
// INSERT ... SELECT above it copied every column.
//
// So this applies the schema as it stood BEFORE 0004, writes rows into it —
// including a 'ref_eu' row, the historical value the rebuild's own INSERT would
// choke on if the new CHECK had dropped it — and only then runs the migration.
// Asserting against a freshly-migrated database would prove nothing: there would
// be no pre-existing rows for the rebuild to preserve.
func TestMigration0004PreservesExistingNetProbes(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// The state a deployed database is actually in: everything up to 0004.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	for _, name := range []string{
		"0001_init.sql", "0002_drop_origin.sql", "0003_rename_probe_short.sql",
	} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}

	res, err := db.Exec(`INSERT INTO cycles (started_at) VALUES ('2026-08-04T06:00:00Z')`)
	if err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	cycleID, _ := res.LastInsertId()

	// Every column carries a distinct value, so a rebuild that copied the right
	// number of columns in the wrong ORDER fails here rather than passing.
	type row struct {
		target     string
		dnsMs      float64
		connectMs  float64
		ok         int
		errorClass any
	}
	want := map[string]row{
		"mimo_sgp": {"mimo_sgp", 3.5, 166.25, 1, nil},
		"ref_sgp":  {"ref_sgp", 4.5, 265.75, 1, nil},
		"ref_eu":   {"ref_eu", 5.5, 18.125, 0, "connect_timeout"},
	}
	for _, r := range want {
		if _, err := db.Exec(
			`INSERT INTO net_probes (cycle_id, target, dns_ms, connect_ms, ok, error_class)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			cycleID, r.target, r.dnsMs, r.connectMs, r.ok, r.errorClass,
		); err != nil {
			t.Fatalf("insert %s: %v", r.target, err)
		}
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := db.Query(
		`SELECT target, dns_ms, connect_ms, ok, error_class FROM net_probes WHERE cycle_id = ?`,
		cycleID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := map[string]row{}
	for rows.Next() {
		var r row
		var ec any
		if err := rows.Scan(&r.target, &r.dnsMs, &r.connectMs, &r.ok, &ec); err != nil {
			t.Fatalf("scan: %v", err)
		}
		r.errorClass = ec
		got[r.target] = r
	}
	if len(got) != len(want) {
		t.Fatalf("net_probes = %d rows after 0004, want %d — the rebuild lost data", len(got), len(want))
	}
	for target, w := range want {
		g, ok := got[target]
		if !ok {
			t.Errorf("%s did not survive the rebuild", target)
			continue
		}
		if g.dnsMs != w.dnsMs || g.connectMs != w.connectMs || g.ok != w.ok {
			t.Errorf("%s = %+v, want %+v", target, g, w)
		}
	}
	// The historical value has to remain READABLE, which is the whole reason it
	// stayed in the widened CHECK.
	if g := got["ref_eu"]; g.errorClass != "connect_timeout" {
		t.Errorf("ref_eu error_class = %v, want connect_timeout", g.errorClass)
	}

	// And the point of the migration: the new targets now insert.
	for _, target := range []string{"mimo_ams", "ref_ams"} {
		if _, err := db.Exec(
			`INSERT INTO net_probes (cycle_id, target, ok) VALUES (?, ?, 1)`,
			cycleID, target,
		); err != nil {
			t.Errorf("%s still rejected after 0004: %v", target, err)
		}
	}
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
		// Not 'mimo_ams': 0004 admitted that one. A region nobody probes.
		{"unknown ping target", `INSERT INTO net_probes (cycle_id, target, ok) VALUES (?, 'mimo_tokyo', 1)`},
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

// The other half of the constraint: every target the code writes must be
// accepted. A CHECK too narrow fails the whole cycle transaction on insert —
// taking the other region's rows and every inference probe down with it — so
// the enum is pinned from both sides.
//
// 'ref_eu' is here because stored rows still carry it, not because anything
// writes it.
func TestNetProbesAcceptsEveryLiveTarget(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO cycles (started_at) VALUES ('2026-08-04T06:00:00Z')`)
	if err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	id, _ := res.LastInsertId()

	for _, target := range []string{"mimo_sgp", "ref_sgp", "mimo_ams", "ref_ams", "ref_eu"} {
		t.Run(target, func(t *testing.T) {
			if _, err := db.Exec(
				`INSERT INTO net_probes (cycle_id, target, ok) VALUES (?, ?, 1)`, id, target,
			); err != nil {
				t.Errorf("target %q rejected: %v", target, err)
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
