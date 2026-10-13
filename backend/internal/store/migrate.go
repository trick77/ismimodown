package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/trick77/ship/kit/migrate"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any embedded migrations not yet recorded in
// schema_migrations, each in its own transaction, in version order (kit's
// migrate).
func Migrate(db *sql.DB) error {
	ctx := context.Background()
	if err := adoptLegacyTable(ctx, db); err != nil {
		return err
	}
	files, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	if _, err := migrate.Up(ctx, db, files, migrate.Options{Dialect: migrate.SQLite}); err != nil {
		return err
	}
	if err := keepLegacyColumn(ctx, db); err != nil {
		return err
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
	if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}

// adoptLegacyTable hands a database written by the runner before kit to kit.
//
// That runner keyed schema_migrations on a column named version; kit's is name.
// The values are the same filenames either way, so renaming the column is the
// whole adoption. Without it kit's CREATE TABLE IF NOT EXISTS leaves the old
// table standing, its SELECT name fails, and every deployed database refuses to
// start. A database already on kit's shape, or a fresh one, is left alone.
//
// pragma_table_info lists real columns only, so the generated version column
// keepLegacyColumn adds never reads as a table still to adopt.
func adoptLegacyTable(ctx context.Context, db *sql.DB) error {
	var legacy int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('schema_migrations') WHERE name = 'version'`,
	).Scan(&legacy); err != nil {
		return fmt.Errorf("inspect schema_migrations: %w", err)
	}
	if legacy == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`ALTER TABLE schema_migrations RENAME COLUMN version TO name`); err != nil {
		return fmt.Errorf("adopt schema_migrations: %w", err)
	}
	return nil
}

// keepLegacyColumn keeps a rollback to the image before kit startable.
//
// That image looks each migration up with WHERE version = ?. After the rename
// it would find no such column and refuse to start, so the one way back from a
// bad deploy would be closed. A generated column mirroring name answers those
// lookups and costs nothing: VIRTUAL, never stored, never written by kit.
func keepLegacyColumn(ctx context.Context, db *sql.DB) error {
	var present int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_xinfo('schema_migrations') WHERE name = 'version'`,
	).Scan(&present); err != nil {
		return fmt.Errorf("inspect schema_migrations: %w", err)
	}
	if present > 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		`ALTER TABLE schema_migrations ADD COLUMN version TEXT GENERATED ALWAYS AS (name) VIRTUAL`); err != nil {
		return fmt.Errorf("keep schema_migrations.version: %w", err)
	}
	return nil
}
