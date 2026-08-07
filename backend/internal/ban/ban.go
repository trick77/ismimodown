// Package ban holds an in-memory list of callers that are blocked outright.
//
// This is deliberately NOT a rate limiter. ratelimit.Limiter answers "has this
// caller asked too often lately", refills continuously, and forgives within
// minutes — the right shape for a scraper, the wrong one for a caller that
// asked for /wp-admin/install.php. There is no honest volume of such requests,
// so there is nothing to meter: the caller is simply out, for a fixed span.
//
// Expressing that as a token bucket would mean a rate of 1/172800 per second,
// which reads as nothing at all, and would put the entries under the limiter's
// 30-minute idle sweep — which would forgive a 48-hour ban after half an hour.
//
// State is process-local and never persisted, which is the requirement rather
// than a shortcut: a restart is the intended escape hatch, and the only one.
package ban

import (
	"log/slog"
	"sync"
	"time"
)

// logInterval is the minimum gap between two log lines about the same caller.
//
// A ban is worth a line; a banned caller's next thousand requests are not. The
// callers this catches do not stop knocking when they are refused — that is
// what makes them worth banning — so an unthrottled line per refusal turns a
// bounded map into an unbounded log, and buries the ban that started it.
const logInterval = time.Minute

// Outcome describes what a call to Ban did, so the caller can log it without
// the store having to know what the request was for.
type Outcome int

const (
	// OutcomeNew is a caller that was not banned and now is.
	OutcomeNew Outcome = iota
	// OutcomeExtended is a caller that was already banned and whose block has
	// been pushed out to a full TTL from now.
	OutcomeExtended
)

// entry is one banned caller.
type entry struct {
	// expires is when the block lifts.
	expires time.Time
	// lastLogged throttles this caller's log lines — see logInterval.
	lastLogged time.Time
}

// Store is a set of banned keys, each with an expiry.
//
// The zero value is not usable; call New.
type Store struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]*entry

	// lastEvictionLog throttles the map-full warning, which fires once per ban
	// once the map is full — i.e. exactly when a flood is in progress.
	lastEvictionLog time.Time

	// now is a seam for tests: a 48-hour expiry is otherwise only testable by
	// waiting 48 hours.
	now func() time.Time
}

// New returns a store whose bans last ttl and which holds at most max keys.
//
// max bounds memory against the thing this package exists to handle. The keys
// are caller-supplied addresses and each lives for ttl, so an unbounded map is
// a slow leak with a scanner holding the pen — and the scanners this catches
// are exactly the ones that rotate addresses.
func New(ttl time.Duration, max int) *Store {
	if max < 1 {
		max = 1
	}
	return &Store{
		ttl:     ttl,
		max:     max,
		entries: make(map[string]*entry),
		now:     time.Now,
	}
}

// Ban blocks key for a full TTL starting now, whether or not it was already
// blocked, and reports what happened and whether it is worth logging.
//
// Re-banning always restarts the clock rather than leaving the original expiry.
// A caller that comes back has not stopped being what got it banned, so its 48
// hours start again from the last time it tried — the alternative lets a
// persistent scanner sit out a fixed window and resume.
//
// shouldLog is false when this caller was logged within the last logInterval.
// The caller does the logging because only it knows the request; the store
// owns the throttle because only it knows the history.
func (s *Store) Ban(key string) (outcome Outcome, shouldLog bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()

	e, existed := s.entries[key]
	// An entry whose ban already lapsed is a new ban, not an extension: the
	// caller served its term and came back.
	if existed && !e.expires.After(now) {
		existed = false
	}

	if !existed {
		if _, present := s.entries[key]; !present && len(s.entries) >= s.max {
			s.makeRoom(now)
		}
		// lastLogged stays zero rather than being set to now, so the FIRST
		// return visit is always logged however quickly it arrives. That
		// transition — banned, and back already — is the informative one; it
		// is the thousandth repeat that is noise, and the throttle catches
		// that from here on.
		s.entries[key] = &entry{expires: now.Add(s.ttl)}
		return OutcomeNew, true
	}

	e.expires = now.Add(s.ttl)
	if now.Sub(e.lastLogged) < logInterval {
		return OutcomeExtended, false
	}
	e.lastLogged = now
	return OutcomeExtended, true
}

// makeRoom frees a slot in a full map. Caller holds the lock.
func (s *Store) makeRoom(now time.Time) {
	// Expired entries first: they are free, and under any normal load this is
	// the only branch that ever runs.
	s.sweepLocked(now)
	if len(s.entries) < s.max {
		return
	}

	// Still full, so evict the entry closest to expiring anyway.
	//
	// Evicting rather than refusing to insert, because refusing inverts the
	// feature: one burst fills the map and every scanner arriving afterwards is
	// then permanently un-bannable. Evicting means the newest ban always lands,
	// and what is lost is the ban with the least remaining life.
	var oldestKey string
	var oldestAt time.Time
	found := false
	for k, e := range s.entries {
		if !found || e.expires.Before(oldestAt) {
			oldestKey, oldestAt, found = k, e.expires, true
		}
	}
	if !found {
		return
	}
	delete(s.entries, oldestKey)

	if now.Sub(s.lastEvictionLog) >= logInterval {
		s.lastEvictionLog = now
		slog.Warn("ban store full, evicting oldest entry",
			"max", s.max,
			"evicted", oldestKey,
		)
	}
}

// Banned reports whether key is currently blocked. An expired entry is not.
func (s *Store) Banned(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[key]
	// After, not !Before: a ban whose expiry is exactly now has served its term.
	return ok && e.expires.After(s.now())
}

// Expires is when key's ban lifts, and whether it is banned at all. Used to log
// the new expiry when a ban is extended.
func (s *Store) Expires(key string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[key]
	if !ok || !e.expires.After(s.now()) {
		return time.Time{}, false
	}
	return e.expires, true
}

// Sweep drops entries whose ban has expired. Called periodically by the daemon
// so a map that grew during a scan shrinks again once the bans lapse, rather
// than waiting for the next Ban to notice.
func (s *Store) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
}

// sweepLocked drops expired entries. Caller holds the lock.
func (s *Store) sweepLocked(now time.Time) {
	for k, e := range s.entries {
		if !e.expires.After(now) {
			delete(s.entries, k)
		}
	}
}

// Len is the number of entries held, expired ones included. For tests and for
// asserting the cap.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
