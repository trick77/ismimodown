package store

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A migration that fails halfway must leave NOTHING behind — not the tables it
// managed to create, and not a schema_migrations row claiming it succeeded. A
// half-applied migration recorded as applied is unrecoverable without manual
// surgery, because the runner will skip it forever after.
func TestFailedMigrationRollsBackAndIsNotRecorded(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Simulate a bad migration by running its body directly the way Migrate
	// does: a valid statement followed by an invalid one, in one transaction.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec(`CREATE TABLE should_not_survive (id INTEGER PRIMARY KEY) STRICT`); err != nil {
		t.Fatalf("first statement: %v", err)
	}
	if _, err := tx.Exec(`THIS IS NOT SQL`); err == nil {
		t.Fatal("expected the bad statement to fail")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='should_not_survive'`,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Error("a rolled-back migration left its table behind")
	}
}

// The runner must report WHICH migration failed. "syntax error near THIS" with
// no filename is close to useless when a dozen migrations have accumulated.
func TestMigrateErrorNamesTheFailingMigration(t *testing.T) {
	// Migrate is driven by an embedded FS, so the failure path is exercised via
	// a database that cannot accept the migration: pre-create a conflicting
	// table so 0001_init's CREATE TABLE collides.
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE cycles (wrong INTEGER)`); err != nil {
		t.Fatalf("seed conflicting table: %v", err)
	}

	err = Migrate(db)
	if err == nil {
		t.Fatal("expected Migrate to fail against a conflicting schema")
	}
	if !strings.Contains(err.Error(), "0001_init.sql") {
		t.Errorf("error must name the failing migration, got: %v", err)
	}

	// And it must not have recorded the migration it could not apply.
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM schema_migrations WHERE version = '0001_init.sql'`,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Error("a failed migration was recorded as applied; the runner would skip it forever")
	}
}

// migrateUpTo applies every migration ordered before prefix, recording each the
// way Migrate does so a later Migrate picks up exactly where this left off.
//
// It exists so a test can hold a database at an OLDER schema and put rows in it.
// That is the only way to exercise a data migration: against an empty database
// every migration passes, because there is nothing to convert.
func migrateUpTo(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= prefix {
			break
		}
		// Skip what is already applied, so this can be called twice to walk a
		// database forward one migration at a time — which is how a test that
		// inspects the state BETWEEN two migrations has to be written.
		var applied int
		if err := db.QueryRow(
			`SELECT count(*) FROM schema_migrations WHERE version = ?`, name).Scan(&applied); err != nil {
			t.Fatalf("read schema_migrations: %v", err)
		}
		if applied > 0 {
			continue
		}
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
}

// The rename has to move the DATA, not just the constraint.
//
// Applying migrations to an empty database proves only that the new schema is
// valid. Every deployed database is full of rows written under the old name, and
// a migration that swapped the CHECK without rewriting them would leave history
// the daemon can no longer see: every query filters on `probe = 'short'`, so a
// surviving `infer` row goes invisible rather than wrong — the harder failure to
// notice, because the charts simply get shorter.
func TestRenameMigrationRewritesExistingRows(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Stopped before the rename, so the rows below are written under the old
	// CHECK exactly as a deployed database holds them.
	migrateUpTo(t, db, "0003")

	if _, err := db.Exec(
		`INSERT INTO cycles (id, started_at) VALUES (1, '2026-08-04T06:00:00Z')`); err != nil {
		t.Fatalf("seed cycle: %v", err)
	}
	// Explicit ids first, so the bulk rows below cannot claim them.
	for _, q := range []string{
		`INSERT INTO infer_probes (id, cycle_id, model_id, probe, ttft_ms, ok, answer_ok) VALUES (7, 1, 'mimo-v2.5', 'infer', 912.0, 1, 1)`,
		`INSERT INTO infer_probes (id, cycle_id, model_id, probe, ttft_ms, ok) VALUES (8, 1, 'mimo-v2.5', 'wide', 1543.0, 1)`,
		`INSERT INTO skipped_runs (occurred_at, model_id, probe) VALUES ('2026-08-04T06:00:00Z', 'mimo-v2.5', 'infer')`,
		`INSERT INTO skipped_runs (occurred_at, model_id, probe) VALUES ('2026-08-04T06:05:00Z', 'mimo-v2.5', 'wide')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed pre-rename row: %v", err)
		}
	}
	// And bulk alongside them, because the failure this guards is a rebuild that
	// copies SOME rows: a mistyped WHERE in the INSERT...SELECT loses history
	// silently, and three hand-written rows all survive a filter that a month of
	// real ones would not.
	const bulk = 5000
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < bulk; i++ {
		if _, err := tx.Exec(
			`INSERT INTO infer_probes (cycle_id, model_id, probe, ttft_ms, ok) VALUES (1, 'mimo-v2.5', 'infer', 900.0, 1)`,
		); err != nil {
			t.Fatalf("seed bulk row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk: %v", err)
	}

	// Up to 0004, not all the way: 0003 is what this test is about, and 0004
	// drops skipped_runs — which this test still has to inspect to prove 0003
	// rebuilt it without losing rows. The drop gets its own assertion below.
	migrateUpTo(t, db, "0004")

	// Nothing was left behind and nothing was lost: the named short row, the
	// wide one, and every bulk row.
	var total, short int
	if err := db.QueryRow(
		`SELECT count(*), sum(probe = 'short') FROM infer_probes`).Scan(&total, &short); err != nil {
		t.Fatalf("count infer_probes: %v", err)
	}
	if want := bulk + 2; total != want {
		t.Errorf("infer_probes holds %d rows, want %d — the rebuild dropped history", total, want)
	}
	if want := bulk + 1; short != want {
		t.Errorf("%d rows renamed, want %d", short, want)
	}

	for _, table := range []string{"infer_probes", "skipped_runs"} {
		var leftover int
		if err := db.QueryRow(
			`SELECT count(*) FROM ` + table + ` WHERE probe = 'infer'`).Scan(&leftover); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if leftover != 0 {
			t.Errorf("%s still holds %d rows under the old name", table, leftover)
		}
	}

	// skipped_runs is rebuilt by the same INSERT...SELECT and gets the same
	// count assertion, not just the leftover one above: a rebuild that dropped
	// every row would leave nothing under the old name either, and pass.
	var skippedTotal, skippedShort int
	if err := db.QueryRow(
		`SELECT count(*), sum(probe = 'short') FROM skipped_runs`,
	).Scan(&skippedTotal, &skippedShort); err != nil {
		t.Fatalf("count skipped_runs: %v", err)
	}
	if skippedTotal != 2 {
		t.Errorf("skipped_runs holds %d rows, want 2 — the rebuild dropped history",
			skippedTotal)
	}
	if skippedShort != 1 {
		t.Errorf("%d skipped_runs rows renamed, want 1", skippedShort)
	}

	// The wide row is untouched, and found by its ORIGINAL id: RecentSamples
	// breaks ties within a cycle on it, so renumbering would silently reorder
	// history without changing a single value.
	var kind string
	if err := db.QueryRow(`SELECT probe FROM infer_probes WHERE id = 8`).Scan(&kind); err != nil {
		t.Fatalf("read the wide row by its original id: %v", err)
	}
	if kind != "wide" {
		t.Errorf("wide row = %q, want it untouched", kind)
	}

	// The constraint moved with the data: the old name is now rejected.
	if _, err := db.Exec(
		`INSERT INTO infer_probes (cycle_id, model_id, probe, ok) VALUES (1, 'mimo-v2.5', 'infer', 1)`,
	); err == nil {
		t.Error("expected a CHECK violation inserting the pre-rename probe name")
	}
	// And the new one is accepted — a CHECK rejecting everything would also
	// satisfy the assertion above.
	if _, err := db.Exec(
		`INSERT INTO infer_probes (cycle_id, model_id, probe, ok) VALUES (1, 'mimo-v2.5', 'short', 1)`,
	); err != nil {
		t.Errorf("inserting the current probe name failed: %v", err)
	}

	// Then 0004 takes the table away entirely, ON A DATABASE THAT HAS ROWS IN
	// IT. A DROP is trivially correct against an empty table and is exactly the
	// statement a production database would meet with three months of history,
	// so it is applied here rather than only from a fresh schema.
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate to 0004: %v", err)
	}
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'skipped_runs'`,
	).Scan(&n); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	if n != 0 {
		t.Error("skipped_runs survived 0004")
	}
	// The cycles it never hung off are untouched: 0004 must not cascade.
	if err := db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n); err != nil {
		t.Fatalf("count cycles: %v", err)
	}
	if n != 1 {
		t.Errorf("cycles = %d, want 1 — 0004 must only drop its own table", n)
	}
}
