package httpapi

import (
	"fmt"
	"net/http"
	"time"
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

	ch, cancel, ok := s.deps.Broker.Subscribe()
	if !ok {
		// The cap is reached. 503 rather than holding a connection that will
		// never be fed — a silent, permanently-idle stream is worse than a
		// refusal the client can retry.
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

	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client went away, or the server is shutting down.
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
