// Package httpapi builds ismimodown's HTTP handler: the public read-only JSON API
// plus the embedded SPA.
//
// Every route here is unauthenticated by design — this is a public status page —
// and that shapes the whole package: nothing caller-supplied reaches SQL, every
// window and metric comes from a server-side allow-list, responses are cached so
// the database is not the rate limit, and error_detail never leaves the process.
package httpapi

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/trick77/ismimodown/internal/ban"
	"github.com/trick77/ismimodown/internal/config"
	"github.com/trick77/ismimodown/internal/ratelimit"
	"github.com/trick77/ismimodown/internal/samples"
)

// cacheTTL is how long a rendered response is reused. Well under the 5-minute
// cycle, and invalidated outright when a cycle lands.
const cacheTTL = 30 * time.Second

// Broker is the SSE fan-out seam. *sse.Broker satisfies it.
//
// Subscribe takes the caller's identity (the client IP) because the stream is
// capped per caller as well as globally: without the key, one scraper takes
// every slot and the 503 lands on real visitors instead.
type Broker interface {
	Subscribe(key string) (<-chan []byte, func(), bool)
}

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
	// Samples answers every /api query. Optional in tests that only exercise
	// health or the static shell.
	Samples *samples.Store
	// Static serves the embedded SPA; may be nil in tests, in which case
	// unmatched paths 404 instead of panicking.
	Static http.Handler
	// Broker fans completed cycles out over /api/events. Optional: when nil,
	// that route reports 503 rather than panicking.
	Broker Broker
	// Limiter bounds /api/* per caller. Optional in tests.
	Limiter *ratelimit.Limiter
	// NotFoundLimiter bounds every route by the 404s a caller causes, which is
	// the only budget the non-API surface has — see notFoundPenalty. Optional:
	// nil means a caller may 404 forever.
	NotFoundLimiter *ratelimit.Limiter
	// Ban blocks callers caught asking for an exploit path outright, for as
	// long as the store's TTL — see banGate. Optional: nil means no such
	// caller is ever blocked, only metered by NotFoundLimiter.
	Ban *ban.Store
	// Shutdown is closed when the process begins shutting down. Only the SSE
	// handler watches it, and that is the point.
	//
	// http.Server.Shutdown waits for active connections but never cancels their
	// request contexts, so an /api/events handler blocked on r.Context() keeps a
	// connection alive until the shutdown timeout expires — one open dashboard
	// turns every restart into a 15s hang and a non-zero exit.
	//
	// The obvious fix is srv.BaseContext returning the signal context, but that
	// cancels EVERY in-flight request: a mid-flight /api/dashboard loses its DB
	// query and returns 500 on what should be a graceful restart. Signalling
	// only the long-lived stream fixes the hang, lets ordinary requests drain,
	// and — unlike BaseContext, which can only live in main() — sits where it
	// can be tested.
	Shutdown <-chan struct{}

	// The configured model set. Every /api route that serves per-model data
	// answers from this rather than from whatever ids happen to be in the
	// database, so a model that was dropped from the config stops being served.
	Models []string

	// Prices is the per-model list price table the cost panel multiplies tokens
	// by. Always config.DefaultPrices — see config.Config.Prices.
	Prices map[string]config.ModelPrice

	// ProbeUserAgent is deliberately NOT published: the request shape is
	// operator-only, because an endpoint that can recognise the probe by its
	// payload can serve it differently. It stays wired here so
	// TestRequestShapeIsNotServed can configure a distinctive value and assert
	// no public endpoint ever echoes it.
	ProbeUserAgent string

	// Now is a seam for tests.
	Now func() time.Time
}

type server struct {
	deps  Deps
	mux   *http.ServeMux
	cache *responseCache
}

// New builds the HTTP handler.
//
// The public surface is unauthenticated by design — this is a read-only status
// page — so the middleware chain carries no auth and every /api/* route assumes
// an anonymous caller.
func New(deps Deps) http.Handler {
	return NewServer(deps)
}

// Server is the handler plus the hooks the daemon needs.
type Server struct {
	http.Handler
	cache *responseCache
}

// NewServer builds the handler and exposes cache invalidation.
func NewServer(deps Deps) *Server {
	s := &server{deps: deps, mux: http.NewServeMux(), cache: newResponseCache(cacheTTL)}
	s.routes()
	// recovery outermost: a panic inside logging must still become a 500
	// rather than killing the process.
	//
	// securityHeaders inside logging but outside the mux, so the headers are on
	// EVERY response — the SPA, the JSON API, a 404, a 429 from the limiter and
	// the 500 that recovery writes alike. Placed outermost-but-one it would
	// miss nothing; placed inside the mux it would miss the limiter's rejection.
	//
	// notFoundPenalty innermost of the three, wrapping the mux: it must see the
	// status the mux produced — a 404 from the SPA handler or from an unmatched
	// /api path alike — and its own 429 must still be logged and still carry the
	// security headers.
	//
	// banGate between securityHeaders and notFoundPenalty. Inside logging, so
	// every ban and every blocked request is on the record; outside
	// notFoundPenalty, so a banned caller neither spends the 404 budget nor
	// reaches the mux — including on /api/events, which is registered on the
	// outer mux and so bypasses the request limiter entirely.
	return &Server{
		Handler: recovery(logging(securityHeaders(banGate(s.deps.Ban, notFoundPenalty(s.deps.NotFoundLimiter, s.mux))))),
		cache:   s.cache,
	}
}

// OnCycle drops the cached responses so a new measurement is visible
// immediately rather than up to a TTL later — which matters most during an
// incident, when the page is being reloaded.
func (s *Server) OnCycle() { s.cache.invalidate() }

func (s *server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	// /api/* is rate limited; /healthz and the static shell are not. The
	// container healthcheck must never be throttled, and the SPA is served from
	// memory.
	//
	// One data route, because the page is one page. It replaced six that the
	// SPA had to call fifteen times between them to draw itself once — and
	// still stays behind the limiter, being the heaviest thing the box serves.
	api := http.NewServeMux()
	api.HandleFunc("GET /api/dashboard", s.handleDashboard)

	var apiHandler http.Handler = api
	if s.deps.Limiter != nil {
		apiHandler = s.deps.Limiter.Middleware(api)
	}
	s.mux.Handle("/api/", apiHandler)

	// The event stream sits OUTSIDE the request rate limiter: it is one request
	// that lasts hours, so a per-request token bucket says nothing useful about
	// it. Its bound is the subscriber cap in the broker instead.
	s.mux.HandleFunc("GET /api/events", s.handleEvents)

	if s.deps.Static != nil {
		s.mux.Handle("/", s.deps.Static)
	}
}
