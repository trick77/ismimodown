// Package store opens the SQLite database (pure-Go ncruces driver, so the
// binary stays CGO_ENABLED=0) and applies embedded migrations.
package store

import (
	"database/sql"
	"fmt"

	// Registers the "sqlite3" database/sql driver. Pure-Go (WASM), so the binary
	// stays CGO_ENABLED=0 and the runtime image stays distroless.
	//
	// No companion /embed import: since v0.34 the driver carries the WASM build
	// itself and importing embed alongside it makes the library print
	// "you're unnecessarily importing github.com/ncruces/go-sqlite3/embed" on
	// every boot.
	//
	// peeq pins this driver at v0.23.3 because sqlite-vec's Go bindings are
	// welded to that older API and host-function ABI. ismimodown has no
	// sqlite-vec — percentiles are plain SQL over a rolling window — so it
	// carries no such pin and tracks the current release.
	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/trick77/ship/kit/sqlitex"
)

// Open opens (creating if needed) the SQLite database at path and applies the
// PRAGMAs for safe concurrent use. Callers must run Migrate separately.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", sqlitex.DSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Each pooled connection is its own wasm SQLite instance with its own
	// memory, and database/sql opens one per concurrent caller with no ceiling.
	// The daemon needs few: the scheduler's write, the healthcheck's ping, the
	// one dashboard build the response cache admits at a time, and a spare.
	// Everything past that queues here rather than multiplying instances on a
	// container capped at one CPU and 512 MB.
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}
