package sched

import (
	"context"
	"testing"
	"time"
)

func TestSleepReturnsTrueWhenTheWaitElapses(t *testing.T) {
	if !Sleep(context.Background(), time.Millisecond) {
		t.Error("Sleep must return true when the full wait elapsed")
	}
}

func TestSleepReturnsFalseWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if Sleep(ctx, time.Hour) {
		t.Error("Sleep must return false when its context is cancelled")
	}
}

// A zero delay is not a wait, but it must still report cancellation honestly —
// a loop that computed a zero delay must not keep going round after its
// context is done.
func TestSleepReportsCancellationEvenWithZeroDelay(t *testing.T) {
	if !Sleep(context.Background(), 0) {
		t.Error("zero delay on a live context must return true")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Sleep(ctx, 0) {
		t.Error("zero delay on a cancelled context must return false")
	}
}

// The property the cadence depends on: the jitter is symmetric, so the MEAN
// stays exactly base. A one-sided jitter would drift the mean to
// base + jitter/2, quietly shortening the day's sample count and inflating the
// token bill.
func TestJitteredIntervalIsSymmetricAroundBase(t *testing.T) {
	base := 5 * time.Minute
	jitter := 30 * time.Second

	// rand() = 0 -> the bottom of the range; 0.5 -> exactly base; ~1 -> the top.
	if got := JitteredInterval(base, jitter, time.Minute, func() float64 { return 0 }); got != base-jitter {
		t.Errorf("rand=0 gave %v, want %v", got, base-jitter)
	}
	if got := JitteredInterval(base, jitter, time.Minute, func() float64 { return 0.5 }); got != base {
		t.Errorf("rand=0.5 gave %v, want exactly base %v", got, base)
	}
	top := JitteredInterval(base, jitter, time.Minute, func() float64 { return 0.999999 })
	if top <= base || top > base+jitter {
		t.Errorf("rand~1 gave %v, want just under %v", top, base+jitter)
	}
}

// A day must really produce 288 cycles. Averaged over many draws the interval
// must sit on base, not above it.
func TestJitteredIntervalMeanIsBase(t *testing.T) {
	base := 5 * time.Minute
	jitter := 30 * time.Second
	rand := PseudoRand()

	var total time.Duration
	const n = 20000
	for i := 0; i < n; i++ {
		total += JitteredInterval(base, jitter, time.Minute, rand)
	}
	mean := total / n

	// Within a second of base over 20k draws.
	drift := mean - base
	if drift < -time.Second || drift > time.Second {
		t.Errorf("mean interval %v drifts %v from base %v; the daily cycle count would be wrong",
			mean, drift, base)
	}
}

// The clamp is the safety half: a pathological rand source, or a jitter larger
// than the base, must never turn the schedule into a tight loop against a
// billed endpoint.
func TestJitteredIntervalNeverGoesBelowTheFloor(t *testing.T) {
	// Jitter larger than base: without the floor this would go negative.
	got := JitteredInterval(time.Minute, 5*time.Minute, 30*time.Second, func() float64 { return 0 })
	if got < 30*time.Second {
		t.Errorf("interval %v fell below the floor", got)
	}

	// A source returning nonsense.
	got = JitteredInterval(5*time.Minute, 30*time.Second, time.Minute, func() float64 { return -100 })
	if got < time.Minute {
		t.Errorf("a pathological rand source produced %v, below the floor", got)
	}
}

// Ticks are anchored to the epoch, so a restart does not shift the series
// sideways and a 5-minute period really lands on :00, :05, :10.
func TestAlignedNextLandsOnEpochMultiples(t *testing.T) {
	period := 5 * time.Minute

	at := time.Date(2026, 8, 4, 6, 3, 17, 0, time.UTC)
	next := AlignedNext(at, period)

	if next.UnixNano()%int64(period) != 0 {
		t.Errorf("%v is not a multiple of %v from the epoch", next, period)
	}
	if !next.After(at) {
		t.Errorf("next %v must be strictly after %v", next, at)
	}
	if want := time.Date(2026, 8, 4, 6, 5, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

// Strictly after, not at-or-after: an anchor already sitting exactly on a tick
// must move a whole period, or a reschedule would be an immediate re-run.
func TestAlignedNextMovesAWholePeriodFromAnExactTick(t *testing.T) {
	period := 5 * time.Minute
	at := time.Date(2026, 8, 4, 6, 5, 0, 0, time.UTC)

	next := AlignedNext(at, period)

	if want := time.Date(2026, 8, 4, 6, 10, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestAlignedNextHandlesNonPositivePeriod(t *testing.T) {
	at := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
	if got := AlignedNext(at, 0); !got.Equal(at) {
		t.Errorf("a zero period must return the anchor unchanged, got %v", got)
	}
}

func TestPseudoRandStaysInRange(t *testing.T) {
	r := PseudoRand()
	for i := 0; i < 1000; i++ {
		v := r()
		if v < 0 || v >= 1 {
			t.Fatalf("PseudoRand returned %v, outside [0,1)", v)
		}
	}
}
