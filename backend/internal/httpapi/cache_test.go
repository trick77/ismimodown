package httpapi

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Twenty concurrent misses on one key are one build. This is the shape the
// live site produced every cycle — the cache was dropped and every open tab
// refetched at once — and it measured 19 s per request against 2.8 s for one.
func TestConcurrentMissesShareOneBuild(t *testing.T) {
	c := newResponseCache(time.Minute)

	var builds atomic.Int32
	release := make(chan struct{})
	build := func() ([]byte, error) {
		builds.Add(1)
		<-release
		return []byte(`{"built":true}`), nil
	}

	const callers = 20
	var wg sync.WaitGroup
	results := make([][]byte, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, err := c.getOrBuild("k", build)
			if err != nil {
				t.Errorf("getOrBuild: %v", err)
			}
			results[i] = body
		}()
	}

	// Let every caller arrive while the first build is still running, then
	// let it finish.
	deadline := time.Now().Add(5 * time.Second)
	for builds.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := builds.Load(); got != 1 {
		t.Fatalf("builds = %d, want 1: concurrent misses each ran their own build", got)
	}
	for i, body := range results {
		if string(body) != `{"built":true}` {
			t.Errorf("caller %d got %q", i, body)
		}
	}
	if _, ok := c.get("k"); !ok {
		t.Error("the shared build was not cached")
	}
}

// A failed build is not cached and is reported to every caller that waited
// on it, so the next request tries again rather than serving an error for a
// TTL.
func TestFailedBuildIsNotCached(t *testing.T) {
	c := newResponseCache(time.Minute)
	boom := errors.New("boom")
	calls := 0
	build := func() ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return []byte("ok"), nil
	}

	if _, err := c.getOrBuild("k", build); !errors.Is(err, boom) {
		t.Fatalf("first call err = %v, want boom", err)
	}
	if _, ok := c.get("k"); ok {
		t.Fatal("an error was cached")
	}
	body, err := c.getOrBuild("k", build)
	if err != nil || string(body) != "ok" {
		t.Fatalf("second call = %q, %v", body, err)
	}
}

// Builds on DIFFERENT keys never overlap: the container has one CPU, and two
// concurrent 3-month sweeps finish later than one after the other would.
func TestBuildsOnDifferentKeysAreSerialised(t *testing.T) {
	c := newResponseCache(time.Minute)

	var running, maxRunning atomic.Int32
	build := func() ([]byte, error) {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		return []byte("x"), nil
	}

	var wg sync.WaitGroup
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.getOrBuild(key, build); err != nil {
				t.Errorf("getOrBuild(%s): %v", key, err)
			}
		}()
	}
	wg.Wait()

	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("max concurrent builds = %d, want 1", got)
	}
}

// A caller that queued for the build slot re-checks the cache before doing
// any work, so a warm-up that landed while it waited answers it for free.
func TestQueuedMissIsAnsweredByAWarmUp(t *testing.T) {
	c := newResponseCache(time.Minute)

	// Occupy the build slot by hand, as a warm-up does.
	c.build <- struct{}{}

	built := make(chan struct{})
	go func() {
		defer close(built)
		body, err := c.getOrBuild("k", func() ([]byte, error) {
			t.Error("build ran although the warm-up had filled the key")
			return nil, nil
		})
		if err != nil || string(body) != "warm" {
			t.Errorf("got %q, %v; want the warmed body", body, err)
		}
	}()

	// The caller is now queued on the slot. Fill its key, then release.
	time.Sleep(20 * time.Millisecond)
	c.put("k", []byte("warm"))
	<-c.build

	select {
	case <-built:
	case <-time.After(5 * time.Second):
		t.Fatal("queued caller never returned")
	}
}

// A build that panics must release the build slot and the flight, or the
// recovery middleware turns one 500 into a wedged site: every later miss
// blocks on the slot, and the next warm-up blocks the scheduler.
func TestAPanickingBuildReleasesTheSlotAndTheFlight(t *testing.T) {
	c := newResponseCache(time.Minute)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic did not propagate")
			}
		}()
		_, _ = c.getOrBuild("k", func() ([]byte, error) { panic("boom") })
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		body, err := c.getOrBuild("k", func() ([]byte, error) { return []byte("ok"), nil })
		if err != nil || string(body) != "ok" {
			t.Errorf("got %q, %v after the panic", body, err)
		}
		if _, err := c.getOrBuild("other", func() ([]byte, error) { return []byte("ok"), nil }); err != nil {
			t.Errorf("other key: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cache stayed wedged after a panicking build")
	}
}
