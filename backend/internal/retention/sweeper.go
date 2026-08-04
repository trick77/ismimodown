// Package retention deletes samples older than the retention window.
package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/trick77/mimostats/internal/samples"
	"github.com/trick77/mimostats/internal/sched"
)

// SweepInterval is how often the sweeper runs. Nightly rather than per-cycle:
// the window is three months, so nothing is gained by deleting more eagerly,
// and a DELETE competing with a probe write for the same SQLite writer is a
// needless source of busy-timeout contention.
const SweepInterval = 24 * time.Hour

// Sweeper prunes cycles beyond the retention window.
type Sweeper struct {
	store     *samples.Store
	retention time.Duration
	now       func() time.Time
}

// New builds a Sweeper.
func New(store *samples.Store, retention time.Duration) *Sweeper {
	return &Sweeper{store: store, retention: retention, now: time.Now}
}

// Run sweeps on an interval until ctx is cancelled.
//
// Sweeps once at startup before entering the loop: a process that restarts
// daily would otherwise never reach its first tick and the window would grow
// without bound — the failure being invisible, since everything keeps working
// and only the disk fills.
func (s *Sweeper) Run(ctx context.Context) {
	for {
		s.SweepOnce(ctx)
		if !sched.Sleep(ctx, SweepInterval) {
			return
		}
	}
}

// SweepOnce deletes everything older than the window.
func (s *Sweeper) SweepOnce(ctx context.Context) {
	cutoff := s.now().Add(-s.retention)
	n, err := s.store.Sweep(ctx, cutoff)
	if err != nil {
		slog.Error("retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("retention sweep", "cycles_deleted", n, "cutoff", cutoff.UTC())
	}
}
