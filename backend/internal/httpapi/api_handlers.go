package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/trick77/mimostats/internal/probe"
	"github.com/trick77/mimostats/internal/samples"
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

// handleModels lists what is being probed: the models, the probe kinds and the
// windows the API will accept.
func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	// Built inside the closure, so a cache hit costs a map lookup rather than
	// rebuilding the payload on every request.
	s.writeJSON(w, r, "models", func() (any, error) {
		type model struct {
			ID   string `json:"id"`
			Note string `json:"note"`
		}
		notes := map[string]string{
			"mimo-v2.5":     "omnimodal model",
			"mimo-v2.5-pro": "1T/42B-active text flagship",
		}
		out := struct {
			Models  []model  `json:"models"`
			Probes  []string `json:"probes"`
			Windows []string `json:"windows"`
		}{
			Probes: []string{probe.ProbeInfer, probe.ProbeWide},
		}
		for _, id := range s.deps.Models {
			out.Models = append(out.Models, model{ID: id, Note: notes[id]})
		}
		for _, wd := range samples.Windows {
			out.Windows = append(out.Windows, wd.Key)
		}
		return out, nil
	})
}

func (s *server) handleSummary(w http.ResponseWriter, r *http.Request) {
	window, ok := s.window(w, r)
	if !ok {
		return
	}
	probeKind, ok := s.probeKind(w, r)
	if !ok {
		return
	}

	key := "summary|" + window.Key + "|" + probeKind
	s.writeJSON(w, r, key, func() (any, error) {
		return s.deps.Samples.Summarize(
			r.Context(), window, s.deps.Models, probeKind, s.now())
	})
}

func (s *server) handleSeries(w http.ResponseWriter, r *http.Request) {
	window, ok := s.window(w, r)
	if !ok {
		return
	}
	probeKind, ok := s.probeKind(w, r)
	if !ok {
		return
	}

	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "ttft"
	}

	// The network series is per-target, not per-model, so it is its own branch
	// rather than a metric on a model — the network is not a model, and
	// pretending otherwise is how it ends up drawn in a model's colour.
	if metric == "network" {
		key := "series|network|" + window.Key
		s.writeJSON(w, r, key, func() (any, error) {
			out := map[string][]samples.Point{}
			for _, target := range []string{probe.TargetMimoSGP, probe.TargetRefSGP} {
				pts, err := s.deps.Samples.NetSeries(r.Context(), target, window, s.now())
				if err != nil {
					return nil, err
				}
				// An empty series must marshal as [] rather than null: a client
				// mapping over it would throw on a fresh or swept database.
				if pts == nil {
					pts = []samples.Point{}
				}
				out[target] = pts
			}
			return map[string]any{
				"window": window.Key, "bucket_s": int(window.Bucket / time.Second),
				"metric": "network", "targets": out,
			}, nil
		})
		return
	}

	column, ok := metricColumns[metric]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown metric")
		return
	}

	key := "series|" + metric + "|" + probeKind + "|" + window.Key
	s.writeJSON(w, r, key, func() (any, error) {
		out := map[string][]samples.Point{}
		for _, model := range s.deps.Models {
			pts, err := s.deps.Samples.Series(r.Context(), column, model, probeKind, window, s.now())
			if err != nil {
				return nil, err
			}
			// Same as the network branch: [] not null, so the client can map
			// over a model with no data yet.
			if pts == nil {
				pts = []samples.Point{}
			}
			out[model] = pts
		}
		return map[string]any{
			"window": window.Key, "bucket_s": int(window.Bucket / time.Second),
			"metric": metric, "probe": probeKind, "models": out,
		}, nil
	})
}

// metricColumns maps the public metric name onto a storage column.
//
// The indirection is deliberate: the wire name is a stable contract, while the
// column is an implementation detail that a migration may rename. It is also
// the second gate in front of the SQL allow-list in the samples package.
var metricColumns = map[string]string{
	"ttft":  "ttft_ms",
	"ttfat": "ttfat_ms",
	"total": "total_ms",
	"itl":   "itl_p50_ms",
	"tps":   "output_tps",
}

func (s *server) handleSamples(w http.ResponseWriter, r *http.Request) {
	model, probeKind, limit, ok := s.sampleQuery(w, r)
	if !ok {
		return
	}

	key := "samples|" + model + "|" + probeKind + "|" + strconv.Itoa(limit)
	s.writeJSON(w, r, key, func() (any, error) {
		rows, err := s.deps.Samples.RecentSamples(r.Context(), model, probeKind, limit)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []samples.Sample{}
		}
		return map[string]any{"model_id": model, "probe": probeKind, "samples": rows}, nil
	})
}

// handlePulse serves the same cycles as /api/samples with every column the
// pulse strip does not draw removed.
//
// It is a separate endpoint rather than a ?fields= flag on /api/samples because
// the projection is not a caller preference — it is the whole point. A flag
// would leave the wide shape one query parameter away, which is the same as not
// having narrowed anything. Here the wide payload is reachable only at the
// small limits a table asks for.
func (s *server) handlePulse(w http.ResponseWriter, r *http.Request) {
	model, probeKind, limit, ok := s.sampleQuery(w, r)
	if !ok {
		return
	}

	key := "pulse|" + model + "|" + probeKind + "|" + strconv.Itoa(limit)
	s.writeJSON(w, r, key, func() (any, error) {
		rows, err := s.deps.Samples.RecentPulse(r.Context(), model, probeKind, limit)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []samples.Pulse{}
		}
		return map[string]any{"model_id": model, "probe": probeKind, "cycles": rows}, nil
	})
}

// sampleQuery validates the model/probe/limit trio the two raw-cycle endpoints
// share. It writes the error response itself and reports false when it has.
func (s *server) sampleQuery(w http.ResponseWriter, r *http.Request) (model, probeKind string, limit int, ok bool) {
	probeKind, ok = s.probeKind(w, r)
	if !ok {
		return "", "", 0, false
	}

	model = r.URL.Query().Get("model")
	if model == "" && len(s.deps.Models) > 0 {
		model = s.deps.Models[0]
	}
	if !s.knownModel(model) {
		writeJSONError(w, http.StatusBadRequest, "unknown model")
		return "", "", 0, false
	}

	// Clamped server-side; the samples package clamps again at its own limit.
	limit = 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return "", "", 0, false
		}
		limit = n
	}
	// Clamped BEFORE the cache key is built, not just inside the samples
	// package: the key would otherwise be caller-controlled, and every distinct
	// limit an arbitrary scraper invents would mint a permanent cache entry —
	// the response cache is a plain map with no eviction on read.
	if limit > samples.MaxSampleLimit {
		limit = samples.MaxSampleLimit
	}
	return model, probeKind, limit, true
}

// window resolves and validates the window parameter.
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

// probeKind resolves the probe filter.
//
// Always a filter, never an aggregation: the gap between the short and wide
// probes' TTFTs IS the prefill signal, so mixing them into one series would
// destroy the only thing wide exists to measure.
func (s *server) probeKind(w http.ResponseWriter, r *http.Request) (string, bool) {
	kind := r.URL.Query().Get("probe")
	if kind == "" {
		kind = probe.ProbeInfer
	}
	if kind != probe.ProbeInfer && kind != probe.ProbeWide {
		writeJSONError(w, http.StatusBadRequest, "unknown probe")
		return "", false
	}
	return kind, true
}

func (s *server) knownModel(id string) bool {
	for _, m := range s.deps.Models {
		if m == id {
			return true
		}
	}
	return false
}

func (s *server) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}
