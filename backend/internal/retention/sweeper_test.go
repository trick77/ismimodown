package retention

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/trick77/mimostats/internal/probe"
	"github.com/trick77/mimostats/internal/samples"
	"github.com/trick77/mimostats/internal/store"
)

func newTestStore(t *testing.T) *samples.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	return samples.New(db)
}

func saveCycleAt(t *testing.T, s *samples.Store, at time.Time) {
	t.Helper()
	if _, err := s.Save(context.Background(), samples.Cycle{
		StartedAt: at,
		Net: []probe.NetResult{
			{Target: probe.TargetMimoSGP, OK: true},
			{Target: probe.TargetRefSGP, OK: true},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestSweepOnceDeletesBeyondTheWindowAndKeepsWhatIsInside(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)

	saveCycleAt(t, s, now.Add(-100*24*time.Hour)) // outside a 3-month window
	saveCycleAt(t, s, now.Add(-24*time.Hour))     // inside

	sw := New(s, 2160*time.Hour) // 3 months
	sw.now = func() time.Time { return now }

	sw.SweepOnce(context.Background())

	n, err := s.CountCycles(context.Background())
	if err != nil {
		t.Fatalf("CountCycles: %v", err)
	}
	if n != 1 {
		t.Errorf("%d cycles survived, want 1", n)
	}
}

// The boundary must not delete data the API is still willing to serve: the
// window allow-list tops out at 3mo, so a sample at exactly the cutoff has to
// survive.
func TestSweepKeepsASampleExactlyAtTheCutoff(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
	retention := 2160 * time.Hour

	saveCycleAt(t, s, now.Add(-retention))

	sw := New(s, retention)
	sw.now = func() time.Time { return now }
	sw.SweepOnce(context.Background())

	n, _ := s.CountCycles(context.Background())
	if n != 1 {
		t.Error("a sample exactly at the cutoff was deleted; the 3mo window would show a hole")
	}
}

func TestSweepOnceIsANoOpOnAnEmptyDatabase(t *testing.T) {
	s := newTestStore(t)
	sw := New(s, 2160*time.Hour)

	// Must not panic or error-log its way into a crash loop.
	sw.SweepOnce(context.Background())

	n, _ := s.CountCycles(context.Background())
	if n != 0 {
		t.Errorf("cycles = %d, want 0", n)
	}
}

// Sweeps once at startup before the first tick. A process restarting daily
// would otherwise never reach a 24h tick, and the window would grow without
// bound — invisibly, since everything keeps working and only the disk fills.
func TestRunSweepsImmediatelyThenStops(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
	saveCycleAt(t, s, now.Add(-100*24*time.Hour))

	sw := New(s, 2160*time.Hour)
	sw.now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sw.Run(ctx) }()

	// Give the startup sweep a moment, then stop.
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, _ := s.CountCycles(context.Background())
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the startup sweep never ran; a daily-restarting process would never prune")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after its context was cancelled")
	}
}
