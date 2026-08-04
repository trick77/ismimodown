package sse

import (
	"sync"
	"testing"
	"time"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	b := New()

	a, cancelA, ok := b.Subscribe()
	if !ok {
		t.Fatal("first subscribe failed")
	}
	defer cancelA()
	c, cancelC, ok := b.Subscribe()
	if !ok {
		t.Fatal("second subscribe failed")
	}
	defer cancelC()

	b.Publish([]byte(`{"cycle_id":1}`))

	for i, ch := range []<-chan []byte{a, c} {
		select {
		case msg := <-ch:
			if string(msg) != `{"cycle_id":1}` {
				t.Errorf("subscriber %d got %q", i, msg)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d received nothing", i)
		}
	}
}

// An SSE connection is held open indefinitely, so on a public endpoint the
// subscriber count is the resource a scraper can exhaust — the per-IP request
// limiter does not help, because one connection per IP across many IPs is
// polite and still unbounded.
func TestSubscriberCapIsEnforced(t *testing.T) {
	b := New()
	var cancels []func()
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	for i := 0; i < MaxSubscribers; i++ {
		_, cancel, ok := b.Subscribe()
		if !ok {
			t.Fatalf("subscribe %d failed below the cap", i)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, ok := b.Subscribe(); ok {
		t.Error("subscribing past the cap must fail so the handler can answer 503")
	}

	// Freeing one slot must let the next caller in.
	cancels[0]()
	cancels = cancels[1:]
	ch, cancel, ok := b.Subscribe()
	if !ok {
		t.Fatal("a freed slot must be reusable")
	}
	_ = ch
	cancels = append(cancels, cancel)
}

// Publish is called from the probe loop. A single stalled browser must never be
// able to delay a measurement cycle, so a full buffer drops the message rather
// than blocking.
func TestPublishDropsRatherThanBlocksOnASlowSubscriber(t *testing.T) {
	b := New()
	_, cancel, _ := b.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The subscriber never reads. Buffer is 1, so the first fills it and
		// every later one must be dropped, not queued or blocked on.
		for i := 0; i < 100; i++ {
			b.Publish([]byte("x"))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that is not reading; a stalled browser would stall the probe loop")
	}
}

// The handler calls cancel in a defer, and a panic-and-retry path could call it
// twice. Closing a closed channel panics, so this must be idempotent.
func TestCancelIsIdempotent(t *testing.T) {
	b := New()
	_, cancel, _ := b.Subscribe()

	cancel()
	cancel() // must not panic

	if b.Count() != 0 {
		t.Errorf("count = %d after cancel, want 0", b.Count())
	}
}

func TestCancelClosesTheChannel(t *testing.T) {
	b := New()
	ch, cancel, _ := b.Subscribe()

	cancel()

	select {
	case _, open := <-ch:
		if open {
			t.Error("channel should be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Error("cancel must close the channel so the handler's read loop exits")
	}
}

func TestConcurrentSubscribeAndPublishIsSafe(t *testing.T) {
	b := New()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel, ok := b.Subscribe()
			if !ok {
				return
			}
			defer cancel()
			select {
			case <-ch:
			case <-time.After(100 * time.Millisecond):
			}
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Publish([]byte("tick"))
		}()
	}
	wg.Wait()
}
