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
	// Without them the planner drives every windowed query from the model
	// index and walks the model's WHOLE history to test each row's started_at
	// — the 24h summary reads 90 days. With sqlite_stat1 populated it drives
	// the short windows from idx_cycles_started_at instead: measured on 90
	// days of synthetic cycles, the 24h build fell from 180 ms to 25 ms and
	// 7d from 277 ms to 142 ms, for 47 ms of ANALYZE. The 3mo window has to
	// read everything either way and does not move.
	//
	// Once per start rather than per cycle: the shape of the data does not
	// change between deploys, and the statistics persist in the file.
	if _, err := db.Exec(`ANALYZE`); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}
