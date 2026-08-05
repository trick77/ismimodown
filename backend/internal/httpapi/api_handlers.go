package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/trick77/ismimodown/internal/samples"
)

// cacheMaxAge is what public caches and browsers are told.
//
// Short relative to the 5-minute cycle: a stale dashboard during an incident is
// worse than an extra request, and 30s bounds how wrong the page can be while
// still absorbing a burst.
const cacheMaxAge = 30

func (s *server) writeJSON(w http.ResponseWriter, r *http.Request, cacheKey string, build func() (any, error)) {
	if cacheKey != "" {
		if body, ok := s.cache.get(cacheKey); ok {
			s.sendJSON(w, body)
			return
		}
	}

	v, err := build()
	if err != nil {
		serverError(w, r, err, "could not build response")
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		serverError(w, r, err, "could not encode response")
		return
	}
	if cacheKey != "" {
		s.cache.put(cacheKey, body)
	}
	s.sendJSON(w, body)
}

func (s *server) sendJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(cacheMaxAge))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// metricColumns maps the public metric name onto a storage column.
//
// The indirection is deliberate: the wire name is a stable contract, while the
// column is an implementation detail that a migration may rename. It is also
// the second gate in front of the SQL allow-list in the samples package.
//
// It outlived the ?metric= parameter it once validated. The names are now
// chosen in dashboard_handler.go rather than by a caller, which makes this a
// mapping rather than a gate — but the mapping is the part that was load
// bearing, and a column name interpolated into SQL keeps the second gate worth
// having.
var metricColumns = map[string]string{
	"ttft":  "ttft_ms",
	"ttfat": "ttfat_ms",
	"total": "total_ms",
	"itl":   "itl_p50_ms",
	"tps":   "output_tps",
}

// window resolves and validates the window parameter.
//
// The only caller-controlled input left on the API. Everything else the page
// used to ask for — the metric, the model, the probe kind, the row limit — was
// the page's own choice arriving the long way round, and is now made where it
// belongs.
func (s *server) window(w http.ResponseWriter, r *http.Request) (samples.Window, bool) {
	key := r.URL.Query().Get("window")
	if key == "" {
		key = "24h"
	}
	window, ok := samples.LookupWindow(key)
	if !ok {
		// Rejected rather than defaulted: silently charting a different range
		// than the caller asked for is worse than an error.
		writeJSONError(w, http.StatusBadRequest, "unknown window")
		return samples.Window{}, false
	}
	return window, true
}

func (s *server) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}
