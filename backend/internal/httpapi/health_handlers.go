package httpapi

import (
	"encoding/json"
	"net/http"
)

// handleHealthz is the unauthenticated liveness probe the container healthcheck
// and Traefik use.
//
// It pings the database rather than answering a bare "ok": mimostats' whole job
// is writing samples, and a process that is listening but cannot reach its
// SQLite file is not healthy in any sense that matters — it would keep
// answering 200 while silently recording nothing.
func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.deps.DB != nil {
		if err := s.deps.DB.PingContext(r.Context()); err != nil {
			serverError(w, r, err, "database unavailable")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": s.deps.Version,
	})
}
