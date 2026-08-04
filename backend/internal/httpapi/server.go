// Package httpapi builds llmstats' HTTP handler: the public read-only JSON API
// plus the embedded SPA. Phase 1 wires health and the static shell; the
// summary/series/samples/methodology endpoints and the SSE stream land in
// phase 4.
package httpapi

import (
	"database/sql"
	"net/http"
)

// Deps are the dependencies needed to build the server.
//
// Everything the server needs arrives through this struct rather than through
// package state, so a test can construct a server with a real temp-file DB and
// a nil Static and exercise the routes without a container or a config file.
type Deps struct {
	// Version is the running build version, surfaced on /healthz.
	Version string
	// DB is the sample store. /healthz pings it, so a server whose database
	// has gone away reports unhealthy rather than serving stale cached JSON.
	DB *sql.DB
	// Static serves the embedded SPA; may be nil in tests, in which case
	// unmatched paths 404 instead of panicking.
	Static http.Handler
}

type server struct {
	deps Deps
	mux  *http.ServeMux
}

// New builds the HTTP handler.
//
// The public surface is unauthenticated by design — this is a read-only status
// page — so the middleware chain carries no auth and every future /api/* route
// must assume an anonymous caller.
func New(deps Deps) http.Handler {
	s := &server{deps: deps, mux: http.NewServeMux()}
	s.routes()
	// recovery outermost: a panic inside logging must still become a 500
	// rather than killing the process.
	return recovery(logging(s.mux))
}

func (s *server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	if s.deps.Static != nil {
		s.mux.Handle("/", s.deps.Static)
	}
}
