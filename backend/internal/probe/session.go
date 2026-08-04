package probe

import (
	"crypto/rand"
	"fmt"
	"sync/atomic"
	"time"
)

// Session ids mirror the shape the upstream issues, e.g.
//
//	ses_ 0367809bfffe ejtHKm95o6rU4mQ
//	│    └─12 hex────┘ └─14 base62───┘
//	│    timestamp+counter   random
//	prefix
//
// The 12 hex digits are the bitwise inversion of (millis << 12 | counter),
// truncated to 48 bits and written big-endian — a 12-bit per-process counter
// keeps ids minted in the same millisecond distinct, and the inversion is what
// gives upstream ids their characteristic trailing f's.
//
// Ported verbatim from loom, but used differently: loom caches one id per
// conversation so a thread pins to a single upstream node. mimostats mints a
// FRESH id per run, deliberately — session affinity would pin every probe to
// whichever node answered first, and the monitor would then report that one
// node's health as MiMo's.
const (
	sessionIDPrefix   = "ses_"
	sessionIDRandomLn = 14
	sessionIDAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

var sessionCounter atomic.Uint64

// NewSessionID mints "ses_" + 12 hex (timestamp+counter) + 14 base62 random.
func NewSessionID() string {
	millis := uint64(time.Now().UnixMilli())
	counter := sessionCounter.Add(1) & 0xFFF // 12 bits
	stamp := ^(millis<<12 | counter) & 0xFFFFFFFFFFFF
	return fmt.Sprintf("%s%012x%s", sessionIDPrefix, stamp, randomBase62(sessionIDRandomLn))
}

// randomBase62 draws n characters from the base62 alphabet. Rejection sampling
// keeps the draw unbiased; if the system entropy source fails the id degrades to
// the alphabet's first character rather than failing a probe run, since this is
// an opaque routing token and not a secret.
func randomBase62(n int) string {
	const limit = 256 - (256 % len(sessionIDAlphabet)) // largest unbiased byte range
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			for len(out) < n {
				out = append(out, sessionIDAlphabet[0])
			}
			break
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, sessionIDAlphabet[int(b)%len(sessionIDAlphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out)
}
