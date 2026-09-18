package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any embedded migrations not yet recorded in
// schema_migrations, each in its own transaction, in lexicographic filename
// order.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var dummy int
		err := db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ?`, name).Scan(&dummy)
		if err == nil {
			continue // already applied
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	// Refresh the planner's statistics once per start.
	//
	// Measured on 90 days of synthetic cycles, two models: the 24h dashboard
	// window fell from 180 ms to 25 ms, 48h from 196 ms to 45 ms, 7d from
	// 277 ms to 142 ms, and the window-independent half of the build from
	// 456 ms to 202 ms, for 47 ms of ANALYZE. The 3mo window reads every row
	// either way and did not move. The windowed queries filter on
	// cycles.started_at through a join, and without sqlite_stat1 the planner
	// has nothing to tell it how selective that range is.
	//
	// Once per start rather than per cycle: the shape of the data does not
	// change between deploys, and the statistics persist in the file.
	if _, err := db.Exec(`ANALYZE`); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}
