// Package sse fans completed cycles out to connected dashboards.
package sse

import (
	"sync"
)

// MaxSubscribers caps concurrent streams.
//
// An SSE connection is held open indefinitely, so on a public endpoint the
// subscriber count is the resource a scraper can exhaust — the per-IP request
// limiter does not help, because one connection per IP across many IPs is
// perfectly polite and still unbounded. Rejecting past the cap keeps the site
// serving everyone else.
const MaxSubscribers = 100

// Broker fans one message out to every subscriber.
type Broker struct {
	mu     sync.RWMutex
	subs   map[int]chan []byte
	nextID int
}

// New builds a Broker.
func New() *Broker {
	return &Broker{subs: map[int]chan []byte{}}
}

// Subscribe registers a listener. The second return is false when the
// subscriber cap is reached; the caller must then answer 503 rather than
// holding a connection it will never feed.
//
// The returned cancel function must be called exactly once, by the handler,
// when the client goes away.
func (b *Broker) Subscribe() (<-chan []byte, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.subs) >= MaxSubscribers {
		return nil, nil, false
	}

	// Buffered, because Publish must never block on a slow reader. One slot is
	// enough: the payload is a cycle notification, and a client that has not
	// drained the previous one gains nothing from a queue of stale cycles.
	ch := make(chan []byte, 1)
	id := b.nextID
	b.nextID++
	b.subs[id] = ch

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if c, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(c)
			}
		})
	}
	return ch, cancel, true
}

// Publish sends msg to every subscriber, dropping it for any whose buffer is
// full.
//
// Dropping rather than blocking is the whole point: this is called from the
// probe loop, and a single stalled browser must never be able to delay a
// measurement cycle. A dropped notification costs that client one refresh of a
// dashboard that polls anyway.
func (b *Broker) Publish(msg []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Count reports the current subscriber count.
func (b *Broker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
