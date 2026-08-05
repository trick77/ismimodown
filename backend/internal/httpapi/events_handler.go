package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/trick77/ismimodown/internal/ratelimit"
)

// sseHeartbeat keeps idle connections alive.
//
// Traefik and most intermediaries drop a connection that has been silent too
// long, and cycles are five minutes apart — comfortably long enough to be
// mistaken for a dead connection. A comment frame costs nothing and is ignored
// by EventSource and by a fetch-based reader alike.
const sseHeartbeat = 25 * time.Second

// handleEvents streams completed cycles.
//
// fetch + ReadableStream on the client rather than EventSource, which is why
// this sends plain SSE framing without relying on EventSource's reconnect
// semantics.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Broker == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "events unavailable")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without flushing, every event sits in the buffer and the stream is a
		// slow way of delivering nothing.
		serverError(w, r, fmt.Errorf("response writer is not an http.Flusher"), "streaming unsupported")
		return
	}

	// Keyed by caller identity, exactly as the request limiter is — an IPv4
	// address, or an IPv6 /64, since a per-address key bounds nobody over v6.
	// So one caller cannot take every stream slot and hand the 503 to everyone
	// else. This route sits outside that limiter — a request bucket says
	// nothing about a connection held for hours — so the cap in the broker is
	// the only bound there is.
	ch, cancel, ok := s.deps.Broker.Subscribe(ratelimit.ClientIP(r))
	if !ok {
		// A cap is reached, global or per-caller. 503 rather than holding a
		// connection that will never be fed — a silent, permanently-idle stream
		// is worse than a refusal the client can retry.
		w.Header().Set("Retry-After", "30")
		writeJSONError(w, http.StatusServiceUnavailable, "too many subscribers")
		return
	}
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Defeats proxy buffering, which would otherwise batch events and defeat
	// the point of streaming them.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// A nil channel blocks forever in a select, which is exactly the right
	// behaviour when no shutdown signal was wired.
	shutdown := s.deps.Shutdown

	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client went away.
			return
		case <-shutdown:
			// The process is stopping. Return now, so http.Server.Shutdown is
			// not left waiting on a connection that will never close by itself.
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: cycle\ndata: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
