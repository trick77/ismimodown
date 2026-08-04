// Package store opens the SQLite database (pure-Go ncruces driver, so the
// binary stays CGO_ENABLED=0) and applies embedded migrations.
package store

import (
	"database/sql"
	"fmt"
	"net/url"

	// Registers the "sqlite3" database/sql driver. Pure-Go (WASM), so the binary
	// stays CGO_ENABLED=0 and the runtime image stays distroless.
	//
	// No companion /embed import: since v0.34 the driver carries the WASM build
	// itself and importing embed alongside it makes the library print
	// "you're unnecessarily importing github.com/ncruces/go-sqlite3/embed" on
	// every boot.
	//
	// peeq pins this driver at v0.23.3 because sqlite-vec's Go bindings are
	// welded to that older API and host-function ABI. llmstats has no
	// sqlite-vec — percentiles are plain SQL over a rolling window — so it
	// carries no such pin and tracks the current release.
	_ "github.com/ncruces/go-sqlite3/driver"
)

// Open opens (creating if needed) the SQLite database at path and applies the
// PRAGMAs for safe concurrent use. Callers must run Migrate separately.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(on)",
		url.PathEscape(path),
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}
