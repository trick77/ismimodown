// Package sched holds the scheduling primitives the probe loop needs: a
// cancellable sleep, a jittered repeat interval, and the pseudo-random source
// that feeds the jitter.
//
// Ported from peeq, which grew it after three background loops each evolved
// their own copy and a fix to the jitter maths had to be found three times.
package sched

import (
	"context"
	"math/rand"
	"time"
)

// Sleep waits d unless ctx is cancelled first. It returns false if ctx was
// cancelled (the caller should stop), true if the full wait elapsed.
//
// A non-positive d is not a wait at all, but it still reports cancellation
// honestly: a loop that computed a zero delay must not keep going round after
// its context is done.
func Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// JitteredInterval returns base plus a SYMMETRIC random jitter in
// [-jitter, +jitter), never less than min.
//
// Symmetric is the property that matters here and the reason peeq's version
// ports unchanged: the mean stays exactly base, so a 5-minute cadence really
// does produce 288 cycles a day. A one-sided jitter (base + rand*jitter) would
// drift the mean to base + jitter/2, quietly shortening the day's sample count
// and inflating the token bill.
//
// The clamp is the safety half: rand is a seam callers can replace, and a
// pathological source — or a jitter configured larger than the base — must not
// be able to turn a 5-minute schedule into a tight loop hammering the endpoint.
// It is a floor on the schedule, not a correction to the caller's arithmetic.
func JitteredInterval(base, jitter, min time.Duration, rand func() float64) time.Duration {
	d := base + time.Duration(rand()*float64(2*jitter)) - jitter
	if d < min {
		d = min
	}
	return d
}

// AlignedNext returns the next instant strictly after `after` that sits on a
// multiple of period counted from the Unix epoch.
//
// The epoch anchor is what makes a cycle mean the same instant everywhere: a
// 5-minute period puts ticks at :00, :05, :10 and so on, regardless of when the
// process started. That matters because ismimodown compares a network reading and
// an inference reading from the SAME cycle, and because a restart must not
// shift the whole series sideways.
//
// A non-positive period has no ticks to land on; the anchor is returned
// unchanged rather than dividing by zero.
func AlignedNext(after time.Time, period time.Duration) time.Time {
	if period <= 0 {
		return after
	}
	p := int64(period)
	base := after.UnixNano()
	// Distance back to the last tick at or before the anchor. The double modulo
	// keeps this correct for anchors before the epoch, where Go's % yields a
	// negative remainder.
	behind := ((base % p) + p) % p
	return time.Unix(0, base-behind+p).UTC()
}

// PseudoRand returns a float64-in-[0,1) source seeded from the wall clock. It
// exists so callers hold an injectable seam rather than reaching for the global
// source; the scheduling jitter it feeds needs no cryptographic quality.
//
// The returned closure is NOT safe for concurrent use — the probe loop is a
// single goroutine and holds its own.
func PseudoRand() func() float64 {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Float64
}
