// Package sse fans completed cycles out to connected dashboards.
package sse

import (
	"sync"
)

// MaxSubscribers caps concurrent streams across the whole process.
//
// An SSE connection is held open indefinitely, so on a public endpoint the
// subscriber count is the resource a scraper can exhaust — the per-IP REQUEST
// limiter does not help, because one connection per IP across many IPs is
// perfectly polite and still unbounded. Rejecting past the cap keeps the site
// serving everyone else.
const MaxSubscribers = 100

// MaxSubscribersPerClient caps concurrent streams from one caller.
//
// The global cap alone is not a bound on ANY one caller: a single scraper can
// open all 100 slots from one address and every real visitor then gets a 503,
// with the request rate limiter no help at all — 100 opens is well inside its
// burst, and the connections cost nothing to hold afterwards. The global cap
// bounds the process; this one bounds a client, and both are needed.
//
// Four rather than one: a browser can legitimately hold a stream in several
// tabs, and a reload can briefly overlap the connection it is replacing. Whole
// households and offices also share one egress address, so a tight cap here
// would break real visitors before it inconvenienced anyone.
const MaxSubscribersPerClient = 4

// Broker fans one message out to every subscriber.
type Broker struct {
	mu     sync.RWMutex
	subs   map[int]subscriber
	perKey map[string]int
	nextID int
}

type subscriber struct {
	ch  chan []byte
	key string
}

// New builds a Broker.
func New() *Broker {
	return &Broker{subs: map[int]subscriber{}, perKey: map[string]int{}}
}

// Subscribe registers a listener from key, which identifies the caller (the
// client IP). The second return is false when either the global cap or the
// per-caller cap is reached; the caller must then answer 503 rather than
// holding a connection it will never feed.
//
// The returned cancel function must be called exactly once, by the handler,
// when the client goes away — it is what releases both slots.
func (b *Broker) Subscribe(key string) (<-chan []byte, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.subs) >= MaxSubscribers {
		return nil, nil, false
	}
	if b.perKey[key] >= MaxSubscribersPerClient {
		return nil, nil, false
	}

	// Buffered, because Publish must never block on a slow reader. One slot is
	// enough: the payload is a cycle notification, and a client that has not
	// drained the previous one gains nothing from a queue of stale cycles.
	ch := make(chan []byte, 1)
	id := b.nextID
	b.nextID++
	b.subs[id] = subscriber{ch: ch, key: key}
	b.perKey[key]++

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			s, ok := b.subs[id]
			if !ok {
				return
			}
			delete(b.subs, id)
			close(s.ch)
			// Deleting at zero rather than leaving a 0 entry: perKey is keyed
			// by client IP, so a counter left behind per address seen would be
			// the same unbounded caller-controlled map the request limiter
			// needs a sweeper for.
			if b.perKey[s.key]--; b.perKey[s.key] <= 0 {
				delete(b.perKey, s.key)
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

	for _, s := range b.subs {
		select {
		case s.ch <- msg:
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
