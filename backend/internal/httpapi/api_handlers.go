package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	probeKind, ok := s.probeKind(w, r)
	if !ok {
		return
	}

	model := r.URL.Query().Get("model")
	if model == "" && len(s.deps.Models) > 0 {
		model = s.deps.Models[0]
	}
	if !s.knownModel(model) {
		writeJSONError(w, http.StatusBadRequest, "unknown model")
		return
	}

	// Clamped server-side; the samples package clamps again at its own limit.
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
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

// handleMethodology publishes what is actually being measured, including the
// parts that are unflattering.
//
// This endpoint is the reason the site can be trusted: every caveat the plan
// insists on lives here in machine-readable form, so the UI cannot quietly drop
// one during a redesign.
func (s *server) handleMethodology(w http.ResponseWriter, r *http.Request) {
	// Built inside the closure: this map is large and entirely static, so a
	// cache hit should not pay to reconstruct it.
	s.writeJSON(w, r, "methodology", func() (any, error) {
		return map[string]any{
			// The model names come from the configured set rather than a
			// literal, because BACKEND_MODELS can change them. A hardcoded
			// pair would let this endpoint — the one the site's credibility
			// rests on — name models nothing is actually probing.
			"scope": "Latency of " + strings.Join(s.deps.Models, " and ") + ", measured from one host, every five minutes. Each model is its own series.",
			// The vantage point is still disclosed — it is a real limit on every
			// figure here — but it is prose, not a labelled identifier. There is
			// one probe host and no second one planned, so a machine-readable id
			// bought nothing and named the site.
			"vantage": "Single egress. All figures are from one probe host, over one network path.",
			// Sanitised, never s.deps.BaseURL raw: this is the one place a
			// configured value is echoed to the public, and a base URL is
			// exactly the kind of string an operator pastes a credential into.
			// config.Load already refuses such a value at boot; this is the
			// second gate, in the same spirit as the metric allow-lists.
			"endpoint": publicBaseURL(s.deps.BaseURL),
			"cadence":  "One aligned cycle every 5 minutes, jittered symmetrically by up to 30s.",
			"layers": map[string]any{
				"network": "A bare TCP handshake to port 443 (SYN -> SYN-ACK). No TLS, no HTTP, no auth, no tokens. TCP rather than ICMP because ICMP is dropped or deprioritised as routine policy, so a timeout would carry no information.",
				"infer":   "One short streamed completion per model per cycle, ~34 prompt tokens.",
				"wide":    "One ~3800-token prompt hourly, to expose prefill scaling and sustained decode that the short probe structurally cannot see.",
			},
			"residual_naming": map[string]any{
				"term": "server-side time",
				"why":  "The TCP handshake terminates at the TLS edge. Xiaomi runs no European GPUs, so whatever backhaul exists between that edge and the actual compute sits INSIDE the residual, along with queueing, prefill and scheduling. Calling it 'model time' would be a claim the measurement cannot support.",
			},
			"references": map[string]any{
				"sgp": map[string]any{
					"host":  s.deps.RefSGPHost,
					"why":   "Answers whether ANY path from this egress to Singapore is healthy, so a route problem is not blamed on MiMo.",
					"limit": "Not the same carrier as the probe host, so an operator-specific backbone fault on MiMo's path may not show here. Green here with MiMo red narrows the fault to MiMo's specific path OR its edge; separating those would need a traceroute.",
				},
				"only_one": "There is a single reference host, so when neither it nor MiMo answers, this measurement cannot say whether the cause was our own connection or the route to Singapore. Those cycles are attributed 'uplink' and excluded from availability rather than guessed at: declining to attribute is the only reading the data supports, and the alternative would publish our own outage as MiMo's.",
			},
			"client_identity": "Requests present as a real coding agent rather than a neutral client, because the endpoint is a coding-agent product and neutral traffic would not measure what production traffic experiences.",
			"reasoning":       "Thinking is disabled on every request, through both of the switches the API offers for it. The check that matters is published on every sample: reasoning_tokens must be 0, and a non-zero value invalidates every latency figure for that window.",
			"cache_defeat":    "The wide prompt is unique per run, varied at the front because prompt caches key on the leading prefix. The short probe needs no such treatment but does carry a system message of its own — with none, the endpoint supplies one (~250 tokens, most of it cache-served), which would inflate the token budget and turn measured prefill into a cache lookup. cached_tokens is recorded every run and must stay near zero.",
			"exclusions": map[string]any{
				"percentiles": "Failed runs are excluded from every latency percentile and counted in availability instead. Otherwise a 240 000 ms timeout lands in the P50 and an outage reads as catastrophic latency.",
				// The unflattering consequence of the rule directly above it, and
				// the reason this endpoint exists: the exclusion is correct AND it
				// truncates the distribution, and a reader who is told only the
				// first half has been told the flattering half.
				"censoring":   "The runs that exclusion removes are the slowest ones, so every latency percentile here is a percentile of the runs that FINISHED — and it improves as truncation worsens. A probe is cut off when it produces no response headers, no first token, or no further chunk within the configured limits, or when it passes the overall deadline. Every summary carries a 'censored' count of how many attempts were cut off that way, and every chart bucket carries its own; a bucket where everything was cut off is published with no value and a censored count rather than omitted. Connection failures are not counted as censoring: nothing was measured, so no tail was truncated.",
				"suppression": "Fewer than " + strconv.Itoa(samples.MinSamplesForPercentile) + " successful samples in a window returns insufficient_data rather than a number.",
				"uplink_down": "Cycles where neither MiMo nor the reference host answered are attributed 'uplink' — historically split into 'uplink' and 'route' while a second reference host existed — and failures on them are excluded from any provider availability figure. The cause may be our uplink or the route; with one reference host the two are indistinguishable, and neither is MiMo's to answer for. A run that succeeded anyway is still counted: it is evidence MiMo answered.",
			},
			"throughput_caveat": "itl_p50_ms is the median gap between STREAM CHUNKS, not between tokens. The endpoint batches tokens into chunks and delivers them in bursts, so on a healthy run the median can collapse toward zero (measured 0.0075 ms against 70 tok/s). output_tps over the decode window is the robust figure; both are published.",
			"retention":         "Raw samples are kept for 3 months and swept nightly. No rollups, so no window longer than that is offered.",
			"error_detail":      "Provider error bodies are recorded for operators and never served publicly. The error class is served, and it is the part that names the failure.",
		}, nil
	})
}

// publicBaseURL renders the configured endpoint for publication, without
// userinfo and without a query string.
//
// Both are places a credential lands when someone pastes a full URL, and this
// value is served to anonymous callers on /api/methodology. config.Load rejects
// either at boot, so in a correctly configured deployment this function changes
// nothing — it exists so that a Deps built by some other caller (a test, a
// future embedding) cannot turn the one echoed config value into an exfiltration
// path.
//
// An unparseable URL is returned as-is: it cannot contain a credential in a form
// this could safely strip, and blanking the field would hide a misconfiguration
// on the endpoint whose job is disclosure.
func publicBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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
