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
		`INSERT INTO infer_probes (id, cycle_id, model_id, probe, ttft_ms, ok, answer_ok) VALUES (7, 1, 'mimo-v2.6-flash', 'infer', 912.0, 1, 1)`,
		`INSERT INTO infer_probes (id, cycle_id, model_id, probe, ttft_ms, ok) VALUES (8, 1, 'mimo-v2.6-flash', 'wide', 1543.0, 1)`,
		`INSERT INTO skipped_runs (occurred_at, model_id, probe) VALUES ('2026-08-04T06:00:00Z', 'mimo-v2.6-flash', 'infer')`,
		`INSERT INTO skipped_runs (occurred_at, model_id, probe) VALUES ('2026-08-04T06:05:00Z', 'mimo-v2.6-flash', 'wide')`,
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
			`INSERT INTO infer_probes (cycle_id, model_id, probe, ttft_ms, ok) VALUES (1, 'mimo-v2.6-flash', 'infer', 900.0, 1)`,
		); err != nil {
			t.Fatalf("seed bulk row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk: %v", err)
	}

	// Up to 0005, not all the way: 0003 is what this test is about, and 0005
	// drops skipped_runs — which this test still has to inspect to prove 0003
	// rebuilt it without losing rows. The drop gets its own assertion below.
	migrateUpTo(t, db, "0005")

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
		`INSERT INTO infer_probes (cycle_id, model_id, probe, ok) VALUES (1, 'mimo-v2.6-flash', 'infer', 1)`,
	); err == nil {
		t.Error("expected a CHECK violation inserting the pre-rename probe name")
	}
	// And the new one is accepted — a CHECK rejecting everything would also
	// satisfy the assertion above.
	if _, err := db.Exec(
		`INSERT INTO infer_probes (cycle_id, model_id, probe, ok) VALUES (1, 'mimo-v2.6-flash', 'short', 1)`,
	); err != nil {
		t.Errorf("inserting the current probe name failed: %v", err)
	}

	// Then 0005 takes the table away entirely, ON A DATABASE THAT HAS ROWS IN
	// IT. A DROP is trivially correct against an empty table and is exactly the
	// statement a production database would meet with three months of history,
	// so it is applied here rather than only from a fresh schema.
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate to 0005: %v", err)
	}
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'skipped_runs'`,
	).Scan(&n); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	if n != 0 {
		t.Error("skipped_runs survived 0005")
	}
	// The cycles it never hung off are untouched: 0005 must not cascade.
	if err := db.QueryRow(`SELECT count(*) FROM cycles`).Scan(&n); err != nil {
		t.Fatalf("count cycles: %v", err)
	}
	if n != 1 {
		t.Errorf("cycles = %d, want 1 — 0005 must only drop its own table", n)
	}
}

// 0006 both drops the column and DELETES the wide rows, and the second half is
// what needs proving: a rebuild that keeps everything would leave 3800-token
// timings averaged into percentiles that now assume ~20, with nothing in the
// schema left to say which rows those are.
//
// Bulk rows alongside the explicit ids for the same reason 0003's test seeds
// them: a mistyped WHERE in the INSERT...SELECT loses history silently, and a
// handful of hand-written rows all survive a filter that a month of real ones
// would not.
func TestDropWideMigrationKeepsShortRowsAndDiscardsWide(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	migrateUpTo(t, db, "0006")

	if _, err := db.Exec(
		`INSERT INTO cycles (id, started_at) VALUES (1, '2026-08-04T06:00:00Z')`); err != nil {
		t.Fatalf("seed cycle: %v", err)
	}
	// Explicit ids first, so the bulk rows below cannot claim them. The short
	// row carries a value in every column the rebuild has to carry across.
	for _, q := range []string{
		`INSERT INTO infer_probes
		   (id, cycle_id, model_id, probe, ttft_ms, ttfat_ms, total_ms, itl_p50_ms,
		    itl_p95_ms, output_tps, prompt_tokens, output_tokens, cached_tokens,
		    reasoning_tokens, question_id, ok, answer_ok, http_status, error_class,
		    error_detail)
		 VALUES (7, 1, 'mimo-v2.6-flash', 'short', 912.0, 913.0, 1700.0, 24.0, 30.0, 41.0,
		         34, 59, 0, 0, 'capital-france', 1, 1, 200, NULL, NULL)`,
		`INSERT INTO infer_probes (id, cycle_id, model_id, probe, ttft_ms, ok)
		 VALUES (8, 1, 'mimo-v2.6-flash', 'wide', 2939.0, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed pre-drop row: %v", err)
		}
	}
	const shortBulk, wideBulk = 4000, 1000
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < shortBulk; i++ {
		if _, err := tx.Exec(
			`INSERT INTO infer_probes (cycle_id, model_id, probe, ttft_ms, ok)
			 VALUES (1, 'mimo-v2.6-flash', 'short', 900.0, 1)`); err != nil {
			t.Fatalf("seed bulk short %d: %v", i, err)
		}
	}
	for i := 0; i < wideBulk; i++ {
		if _, err := tx.Exec(
			`INSERT INTO infer_probes (cycle_id, model_id, probe, ttft_ms, ok)
			 VALUES (1, 'mimo-v2.6-flash', 'wide', 2900.0, 1)`); err != nil {
			t.Fatalf("seed bulk wide %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM infer_probes`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if want := shortBulk + 1; n != want {
		t.Errorf("rows = %d, want %d — every short row survives and no wide one does", n, want)
	}

	// The column is gone, not merely unused. A surviving column would let a
	// later query filter on a value nothing writes.
	var scrap any
	if err := db.QueryRow(`SELECT probe FROM infer_probes LIMIT 1`).Scan(&scrap); err == nil {
		t.Error("probe column still exists")
	}

	// Ids preserved: RecentSamples breaks ties within a cycle on (started_at,
	// id), so renumbering would silently reorder history.
	var ttft float64
	var questionID string
	var answerOK int
	if err := db.QueryRow(
		`SELECT ttft_ms, question_id, answer_ok FROM infer_probes WHERE id = 7`,
	).Scan(&ttft, &questionID, &answerOK); err != nil {
		t.Fatalf("id 7 did not survive: %v", err)
	}
	if ttft != 912.0 || questionID != "capital-france" || answerOK != 1 {
		t.Errorf("row 7 = (%v, %q, %d), want (912, capital-france, 1) — columns shifted in the rebuild",
			ttft, questionID, answerOK)
	}
	if err := db.QueryRow(`SELECT ttft_ms FROM infer_probes WHERE id = 8`).Scan(&ttft); err == nil {
		t.Error("the wide row at id 8 survived")
	}

	// Both indexes, or every model-scoped query falls back to a scan.
	for _, name := range []string{"idx_infer_probes_cycle", "idx_infer_probes_model"} {
		var got string
		if err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
		).Scan(&got); err != nil {
			t.Errorf("index %s is missing after the rebuild: %v", name, err)
		}
	}
}

// Two cycles, because the migration deletes by CYCLE and the interesting case
// is that it takes the whole pairing with it: the network readings a retired
// cycle carried are gone too, while a cycle measured against the configured
// pair keeps all of its rows. Bulk rows for the same reason as the tests above
// — a mistyped WHERE deletes either nothing or everything, and a handful of
// hand-written rows can survive a filter that a month of real ones would not.
func TestDropRenamedModelRowsTakesTheWholeRetiredCycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	migrateUpTo(t, db, "0007")

	// Cycle 1 is the retired generation, cycle 2 the configured one.
	for _, q := range []string{
		`INSERT INTO cycles (id, started_at) VALUES (1, '2026-09-19T06:00:00Z')`,
		`INSERT INTO cycles (id, started_at) VALUES (2, '2026-09-21T06:00:00Z')`,
		`INSERT INTO net_probes (cycle_id, target, connect_ms, ok)
		 VALUES (1, 'mimo_sgp', 166.0, 1)`,
		`INSERT INTO net_probes (cycle_id, target, connect_ms, ok)
		 VALUES (1, 'ref_sgp', 265.0, 1)`,
		`INSERT INTO net_probes (cycle_id, target, connect_ms, ok)
		 VALUES (2, 'mimo_sgp', 164.0, 1)`,
		`INSERT INTO net_probes (cycle_id, target, connect_ms, ok)
		 VALUES (2, 'ref_sgp', 263.0, 1)`,
		`INSERT INTO cycle_fault (cycle_id, fault) VALUES (1, 'ok')`,
		`INSERT INTO cycle_fault (cycle_id, fault) VALUES (2, 'ok')`,
		// The retired short ID is a PREFIX of the retired long one, which is
		// what the LIKE has to cover without reaching the pair that replaced
		// them.
		`INSERT INTO infer_probes (id, cycle_id, model_id, ttft_ms, ok)
		 VALUES (11, 1, 'mimo-v2.5', 900.0, 1)`,
		`INSERT INTO infer_probes (id, cycle_id, model_id, ttft_ms, ok)
		 VALUES (12, 1, 'mimo-v2.5-pro', 1600.0, 1)`,
		`INSERT INTO infer_probes (id, cycle_id, model_id, ttft_ms, ok)
		 VALUES (13, 2, 'mimo-v2.6-flash', 910.0, 1)`,
		`INSERT INTO infer_probes (id, cycle_id, model_id, ttft_ms, ok)
		 VALUES (14, 2, 'mimo-v2.6-pro', 1580.0, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	const retiredBulk, currentBulk = 3000, 2000
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < retiredBulk; i++ {
		if _, err := tx.Exec(
			`INSERT INTO infer_probes (cycle_id, model_id, ttft_ms, ok)
			 VALUES (1, 'mimo-v2.5', 900.0, 1)`); err != nil {
			t.Fatalf("seed bulk retired %d: %v", i, err)
		}
	}
	for i := 0; i < currentBulk; i++ {
		if _, err := tx.Exec(
			`INSERT INTO infer_probes (cycle_id, model_id, ttft_ms, ok)
			 VALUES (2, 'mimo-v2.6-flash', 910.0, 1)`); err != nil {
			t.Fatalf("seed bulk current %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit bulk: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// The retired cycle is gone, and so is everything that hung off it. Counted
	// per table: the cascade is what carries net_probes and cycle_fault, and a
	// pragma that was off would leave them orphaned rather than deleted.
	for _, q := range []struct {
		what  string
		query string
	}{
		{"cycles", `SELECT count(*) FROM cycles WHERE id = 1`},
		{"net_probes", `SELECT count(*) FROM net_probes WHERE cycle_id = 1`},
		{"cycle_fault", `SELECT count(*) FROM cycle_fault WHERE cycle_id = 1`},
		{"infer_probes", `SELECT count(*) FROM infer_probes WHERE cycle_id = 1`},
		{"retired ids", `SELECT count(*) FROM infer_probes WHERE model_id LIKE 'mimo-v2.5%'`},
	} {
		var got int
		if err := db.QueryRow(q.query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", q.what, err)
		}
		if got != 0 {
			t.Errorf("%s left on the retired cycle = %d, want 0", q.what, got)
		}
	}

	// The configured cycle keeps ALL of its rows, network readings included.
	for _, want := range []struct {
		what  string
		query string
		n     int
	}{
		{"cycles", `SELECT count(*) FROM cycles WHERE id = 2`, 1},
		{"net_probes", `SELECT count(*) FROM net_probes WHERE cycle_id = 2`, 2},
		{"cycle_fault", `SELECT count(*) FROM cycle_fault WHERE cycle_id = 2`, 1},
		// Counted per ID: a LIKE that reached one of the configured models
		// would still leave the other behind and pass a bare total.
		{"flash", `SELECT count(*) FROM infer_probes WHERE model_id = 'mimo-v2.6-flash'`, currentBulk + 1},
		{"pro", `SELECT count(*) FROM infer_probes WHERE model_id = 'mimo-v2.6-pro'`, 1},
	} {
		var got int
		if err := db.QueryRow(want.query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", want.what, err)
		}
		if got != want.n {
			t.Errorf("%s on the configured cycle = %d, want %d", want.what, got, want.n)
		}
	}
}
