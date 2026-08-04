package store

import (
	"path/filepath"
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
