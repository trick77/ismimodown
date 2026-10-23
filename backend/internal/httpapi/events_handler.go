package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/sse"
)

// sseHeartbeat keeps idle connections alive.
//
// Traefik and most intermediaries drop a connection that has been silent too
// long, and cycles are five minutes apart — comfortably long enough to be
// mistaken for a dead connection. A comment frame costs nothing and is ignored
// by EventSource and by a fetch-based reader alike.
const sseHeartbeat = 25 * time.Second

// HubOptions are the caps of the /api/events hub.
//
// Each stream is held open for hours, outside any request rate limit, so the
// caps are the only bound there is. 100 bounds the process; 4 bounds one
// caller (clientKey), or a single scraper takes every slot from one address
// and hands the 503 to everyone else. Four rather than one: a reader may
// have several tabs open, a reload briefly overlaps the stream it replaces,
// and households and offices share one egress address. A one-deep buffer:
// the payload is a cycle notification, and a reader that has not drained the
// last one gains nothing from a queue of stale ones.
func HubOptions() sse.HubOptions {
	return sse.HubOptions{Buffer: 1, Max: 100, MaxPerKey: 4}
}

// handleEvents streams completed cycles.
//
// fetch + ReadableStream on the client rather than EventSource, which is why
// this sends plain SSE framing without relying on EventSource's reconnect
// semantics.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Broker == nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "events unavailable")
		return
	}

	// Keyed by caller identity, exactly as the request limiter is — an IPv4
	// address, or an IPv6 /64, since a per-address key bounds nobody over v6.
	// So one caller cannot take every stream slot and hand the 503 to everyone
	// else. This route sits outside that limiter — a request bucket says
	// nothing about a connection held for hours — so the cap in the hub is the
	// only bound there is.
	ch, cancel, ok := s.deps.Broker.Subscribe(clientKey(r))
	if !ok {
		// A cap is reached, global or per-caller, or the hub is closed for
		// shutdown. 503 rather than holding a connection that will never be
		// fed — a silent, permanently-idle stream is worse than a refusal the
		// client can retry.
		httpx.SetRetryAfter(w, 30*time.Second)
		httpx.WriteError(w, http.StatusServiceUnavailable, "too many subscribers")
		return
	}
	defer cancel()

	stream, err := sse.Open(w)
	if errors.Is(err, sse.ErrNoFlush) {
		httpx.ServerError(w, r, err, "streaming unsupported")
		return
	}
	if err != nil {
		return
	}
	defer stream.Close()
	stream.Heartbeat(sseHeartbeat)

	for {
		select {
		case <-r.Context().Done():
			// Client went away.
			return
		case <-stream.Done():
			// A write failed: the reader is gone even if the request was
			// never cancelled, as behind a proxy.
			return
		case msg, open := <-ch:
			// Closed by cancel or by the hub's Close on shutdown, so
			// http.Server.Shutdown is not left waiting on a connection that
			// will never close by itself.
			if !open {
				return
			}
			if stream.Send("cycle", string(msg)) != nil {
				return
			}
		}
	}
}
