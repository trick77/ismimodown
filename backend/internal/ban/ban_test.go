package ban

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// newAt returns a store with a clock the test drives, so a 48-hour TTL costs
// nothing to exercise.
func newAt(ttl time.Duration, max int, clock *time.Time) *Store {
	s := New(ttl, max)
	s.now = func() time.Time { return *clock }
	return s
}

func TestUnknownKeyIsNotBanned(t *testing.T) {
	s := New(time.Hour, 10)
	if s.Banned("203.0.113.7") {
		t.Fatal("a caller that has done nothing is banned")
	}
}

func TestBanBlocksTheKey(t *testing.T) {
	s := New(time.Hour, 10)
	s.Ban("203.0.113.7")
	if !s.Banned("203.0.113.7") {
		t.Fatal("banned key reports as not banned")
	}
}

func TestBanDoesNotAffectOtherKeys(t *testing.T) {
	s := New(time.Hour, 10)
	s.Ban("203.0.113.7")
	if s.Banned("203.0.113.8") {
		t.Fatal("banning one caller banned another")
	}
}

func TestBanExpiresAfterTheTTL(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(48*time.Hour, 10, &now)

	s.Ban("203.0.113.7")

	now = now.Add(47 * time.Hour)
	if !s.Banned("203.0.113.7") {
		t.Fatal("ban lapsed an hour before its TTL")
	}

	// Exactly at the expiry the term is served, so the ban is already over.
	now = now.Add(time.Hour)
	if s.Banned("203.0.113.7") {
		t.Fatal("ban outlived its TTL")
	}
}

// A caller that comes back gets a FULL fresh term from that moment — not a
// top-up of what was left, and not the original expiry left standing. So a
// scanner has to actually stop for the whole TTL to get back in.
func TestRebanningRestartsTheFullTerm(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(48*time.Hour, 10, &now)

	s.Ban("203.0.113.7")
	now = now.Add(47 * time.Hour)
	s.Ban("203.0.113.7")

	now = now.Add(2 * time.Hour) // past the FIRST expiry
	if !s.Banned("203.0.113.7") {
		t.Fatal("re-banning did not restart the block")
	}

	// Exactly 48h from the SECOND ban, not 48h + whatever was left over.
	expires, ok := s.Expires("203.0.113.7")
	if !ok {
		t.Fatal("Expires reports no ban")
	}
	want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC).Add(47 * time.Hour).Add(48 * time.Hour)
	if !expires.Equal(want) {
		t.Errorf("expires = %v, want %v — the term must be reset, not accumulated", expires, want)
	}
}

func TestRebanningReportsAnExtension(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(48*time.Hour, 10, &now)

	if outcome, shouldLog := s.Ban("203.0.113.7"); outcome != OutcomeNew || !shouldLog {
		t.Fatalf("first ban = (%v, %v), want (OutcomeNew, true)", outcome, shouldLog)
	}

	now = now.Add(2 * logInterval)
	if outcome, shouldLog := s.Ban("203.0.113.7"); outcome != OutcomeExtended || !shouldLog {
		t.Errorf("second ban = (%v, %v), want (OutcomeExtended, true)", outcome, shouldLog)
	}
}

// A banned caller that keeps knocking must not write a log line per request:
// the callers this catches are exactly the ones that do not stop.
func TestRepeatBansAreLoggedAtMostOncePerInterval(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(48*time.Hour, 10, &now)

	logged := 0
	if _, ok := s.Ban("203.0.113.7"); ok {
		logged++
	}
	for range 1000 {
		now = now.Add(time.Millisecond)
		if _, ok := s.Ban("203.0.113.7"); ok {
			logged++
		}
	}
	// Two: the ban itself, and the first return visit however fast it came
	// back. The other 999 are the noise this throttle exists to drop.
	if logged != 2 {
		t.Errorf("logged %d times for 1001 bans in under an interval, want 2", logged)
	}

	// Past the interval, one more line is allowed.
	now = now.Add(logInterval)
	if _, ok := s.Ban("203.0.113.7"); !ok {
		t.Error("no line allowed after the interval elapsed")
	}
}

// The throttle only bounds a caller asking FASTER than the interval; one
// polling just slower is logged every time. That makes the interval the cap on
// lines per banned caller across a whole term, so it has to be big enough that
// a persistent slow scanner writes a record rather than a transcript.
func TestASlowPersistentScannerIsBoundedAcrossAFullTerm(t *testing.T) {
	const ttl = 48 * time.Hour
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(ttl, 10, &now)

	logged := 0
	if _, ok := s.Ban("203.0.113.7"); ok {
		logged++
	}
	// Knocking just slower than the throttle, which is its worst case, for the
	// length of a full ban.
	for elapsed := time.Duration(0); elapsed < ttl; elapsed += logInterval + time.Second {
		now = now.Add(logInterval + time.Second)
		if _, ok := s.Ban("203.0.113.7"); ok {
			logged++
		}
	}

	if logged > 60 {
		t.Errorf("logged %d lines for one caller over 48h; the interval is too short", logged)
	}
}

// A caller that served its term and came back is newly banned, not extended —
// and gets a line of its own regardless of when it was last logged.
func TestBanAfterExpiryIsANewBanNotAnExtension(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(time.Hour, 10, &now)

	s.Ban("203.0.113.7")
	now = now.Add(2 * time.Hour) // the ban lapses

	outcome, shouldLog := s.Ban("203.0.113.7")
	if outcome != OutcomeNew {
		t.Errorf("outcome = %v, want OutcomeNew after the term was served", outcome)
	}
	if !shouldLog {
		t.Error("a fresh ban was suppressed by the log throttle")
	}
}

func TestExpiresReportsNothingForAnUnbannedCaller(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(time.Hour, 10, &now)

	if _, ok := s.Expires("203.0.113.7"); ok {
		t.Error("Expires reports a ban for a caller that has none")
	}

	s.Ban("203.0.113.7")
	now = now.Add(2 * time.Hour)
	if _, ok := s.Expires("203.0.113.7"); ok {
		t.Error("Expires reports a ban that has already lapsed")
	}
}

func TestSweepDropsOnlyExpiredEntries(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(time.Hour, 10, &now)

	s.Ban("expired")
	now = now.Add(30 * time.Minute)
	s.Ban("live")

	now = now.Add(31 * time.Minute) // first is out, second has 29 minutes left
	s.Sweep()

	if got := s.Len(); got != 1 {
		t.Fatalf("Len after sweep = %d, want 1", got)
	}
	if !s.Banned("live") {
		t.Fatal("sweep dropped a ban that had not expired")
	}
}

func TestSweepKeepsEverythingWhenNothingHasExpired(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(time.Hour, 10, &now)

	s.Ban("a")
	s.Ban("b")
	s.Sweep()

	if got := s.Len(); got != 2 {
		t.Fatalf("Len after sweep = %d, want 2", got)
	}
}

func TestLenNeverExceedsMax(t *testing.T) {
	s := New(time.Hour, 5)
	for i := range 100 {
		s.Ban(fmt.Sprintf("203.0.113.%d", i))
	}
	if got := s.Len(); got > 5 {
		t.Fatalf("Len = %d, want at most the cap of 5", got)
	}
}

func TestFullStoreReusesExpiredSlotsBeforeEvicting(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(time.Hour, 2, &now)

	s.Ban("old-a")
	s.Ban("old-b")
	now = now.Add(2 * time.Hour) // both lapse

	s.Ban("fresh")

	// The two dead entries should have been reclaimed, not one of them evicted
	// while the other lingered.
	if got := s.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1 — expired entries were not reclaimed", got)
	}
	if !s.Banned("fresh") {
		t.Fatal("the newly banned caller is not banned")
	}
}

func TestFullStoreEvictsTheSoonestToExpire(t *testing.T) {
	// Refusing to insert into a full map would let one burst make every later
	// scanner un-bannable, so the newest ban must always land.
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	s := newAt(48*time.Hour, 2, &now)

	s.Ban("oldest") // expires first
	now = now.Add(time.Hour)
	s.Ban("middle")
	now = now.Add(time.Hour)

	s.Ban("newest")

	if s.Banned("oldest") {
		t.Fatal("the soonest-to-expire entry was not the one evicted")
	}
	if !s.Banned("middle") {
		t.Fatal("evicted the wrong entry")
	}
	if !s.Banned("newest") {
		t.Fatal("the newest ban did not land")
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
}

func TestMaxBelowOneIsClampedRatherThanDisablingTheStore(t *testing.T) {
	// A zero cap must not mean "hold nothing", which would silently turn the
	// feature off.
	s := New(time.Hour, 0)
	s.Ban("203.0.113.7")
	if !s.Banned("203.0.113.7") {
		t.Fatal("a store built with max=0 bans nobody")
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	// Run with -race; the point is the detector, not the assertion.
	s := New(time.Hour, 50)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("203.0.113.%d", i)
			for range 50 {
				s.Ban(key)
				s.Banned(key)
				s.Sweep()
				s.Len()
			}
		}()
	}
	wg.Wait()

	if got := s.Len(); got > 50 {
		t.Fatalf("Len = %d, want at most the cap of 50", got)
	}
}
