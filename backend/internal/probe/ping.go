package probe

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"time"
)

// Ping targets. These names are the stored `target` enum in net_probes.
//
// Two regions, each an edge paired with an independent reference: Singapore is
// where the inference base URL points, Amsterdam is Xiaomi's other front for the
// same service. The pairing is what makes either readable — an edge on its own
// cannot tell a provider problem from a path problem.
//
// The Amsterdam pair is RECORDED AND CHARTED ONLY. AttributeFault never sees it,
// and Summary.Net does not carry it; see the comment on AttributeFault for why
// that is a deliberate limit rather than an oversight.
//
// `ref_eu` was a third target — a nearby European PoP — and is gone. It is
// still accepted by the net_probes CHECK constraint because rows written before
// its removal are still in the database and still readable; nothing writes it
// any more. `ref_ams` is NOT its return: that probe fed attribution, this one
// does not.
const (
	TargetMimoSGP = "mimo_sgp"
	TargetRefSGP  = "ref_sgp"
	TargetMimoAMS = "mimo_ams"
	TargetRefAMS  = "ref_ams"
)

// pingPort is fixed at 443 and deliberately not configurable: the point of the
// measurement is that it traverses the same path, through the same firewalls,
// to the same port that inference actually uses. A ping to any other port would
// answer a different question while looking like the same number.
const pingPort = "443"

// pingNetwork pins the handshake to IPv4, at both the resolve and the dial step.
//
// The four targets are read AGAINST EACH OTHER — that comparison is the entire
// point of drawing them on one chart — and a number is only comparable if it
// measured the same kind of path. Both Xiaomi edges publish A records and no
// AAAA, while both references are dual-stack, so an unpinned probe would time a
// v6 route to the references against a v4 route to the edges and present the
// difference as edge latency.
//
// It also removes a failure mode the address walk below only partly covers: on a
// box with present-but-broken IPv6, RFC 6724 puts the v6 address first, and every
// reference probe pays a dead-path timeout before falling back.
const pingNetwork = "tcp4"

// NetResult is one network-layer reading.
//
// DNSMs and ConnectMs are separate because they fail for different reasons and
// at different layers: a resolver outage is ours, a connect failure is the
// path's or the endpoint's. Collapsing them would hide which.
type NetResult struct {
	Target     string
	DNSMs      float64
	ConnectMs  float64
	OK         bool
	ErrorClass string
	// ErrorDetail is operator-only and never served publicly.
	ErrorDetail string
}

// Pinger measures TCP handshake latency.
//
// TCP, never ICMP: ICMP echo is dropped or deprioritised by routers and cloud
// networks as routine policy, so a timeout would carry no information; it needs
// CAP_NET_RAW, which forces a privileged container; and Xiaomi may not answer it
// at all. A TCP handshake to 443 is the same path and a failure genuinely means
// the endpoint is unreachable.
//
// No TLS, no HTTP, no auth, no tokens — the handshake completes and the socket
// closes. That is what makes this layer free and independently interpretable.
type Pinger struct {
	timeout  time.Duration
	resolver *net.Resolver
	// dial is a seam for tests; nil uses net.Dialer.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// lookup is a seam for tests; nil uses the resolver above. It exists so a
	// test can state how many addresses a host has rather than inheriting the
	// answer from the machine's /etc/hosts — the address walk below is the
	// behaviour under test, and it used to be exercised by `localhost` happening
	// to carry both 127.0.0.1 and ::1, which pinning to IPv4 took away.
	lookup func(ctx context.Context, host string) ([]net.IP, error)
}

// NewPinger builds a Pinger with the given per-target timeout.
func NewPinger(timeout time.Duration) *Pinger {
	return &Pinger{timeout: timeout, resolver: net.DefaultResolver}
}

// Ping resolves host and measures the time to complete a TCP handshake to
// port 443.
//
// A failure is a RESULT, never an error return: the caller records every
// outcome as a sample, and a dropped sample would make the availability strip
// lie by omission. The error is carried in ErrorClass/ErrorDetail instead.
func (p *Pinger) Ping(ctx context.Context, target, host string) NetResult {
	res := NetResult{Target: target}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Resolve explicitly rather than letting Dial do it, so DNS time is
	// attributable on its own. An IP literal resolves trivially and reports
	// ~0 ms, which is correct: there was no lookup.
	// LookupIP with "ip4", not LookupHost: the lookup has to be pinned to the
	// same family as the dial (see pingNetwork), or a dual-stack host spends DNS
	// time resolving AAAA records nothing will ever connect to, and an
	// IPv6-only host reports addresses and then fails at every dial.
	lookup := p.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return p.resolver.LookupIP(ctx, "ip4", host)
		}
	}

	dnsStart := time.Now()
	ips, err := lookup(ctx, host)
	res.DNSMs = msSince(dnsStart)
	if err != nil {
		res.ErrorClass = classifyNetErr(ctx, err, ErrClassDNS)
		res.ErrorDetail = err.Error()
		return res
	}
	if len(ips) == 0 {
		res.ErrorClass = ErrClassDNS
		res.ErrorDetail = "no IPv4 addresses returned for " + host
		return res
	}

	dial := p.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}

	// Try every resolved address, not just the first.
	//
	// This is a credibility question, not a robustness nicety. MiMo's Singapore
	// edge resolves to EIGHT addresses and the Amsterdam one to two, and the
	// resolver's ordering is not guaranteed. Pinning to ips[0] turns "one
	// address in the rotation is bad" into a published provider outage.
	//
	// A real client does not behave that way: net.Dial with a hostname walks
	// the list. The dial here is per-address only so the handshake can be timed
	// in isolation, and that must not cost the resilience the question
	// ("is this endpoint reachable?") actually implies.
	//
	// The v6-first hazard this also used to cover is now handled a step earlier,
	// by resolving and dialling IPv4 only — see pingNetwork.
	//
	// connect_ms is the SUCCESSFUL handshake alone, never the sum of failed
	// attempts before it — otherwise a slow first address would masquerade as
	// edge latency. The resolve above is likewise not folded in.
	var lastErr error
	for _, ip := range ips {
		connectStart := time.Now()
		conn, err := dial(ctx, pingNetwork, net.JoinHostPort(ip.String(), pingPort))
		elapsed := msSince(connectStart)
		if err == nil {
			_ = conn.Close()
			res.ConnectMs = elapsed
			res.OK = true
			return res
		}
		lastErr = err
		// A cancelled or expired context will not be helped by the next
		// address, and walking the rest would blow past the ping timeout.
		if ctx.Err() != nil {
			res.ConnectMs = elapsed
			break
		}
	}

	res.ErrorClass = classifyNetErr(ctx, lastErr, ErrClassConnectTimeout)
	res.ErrorDetail = lastErr.Error()
	return res
}

// classifyNetErr maps a transport error onto the public error vocabulary.
//
// fallback is what an otherwise-unrecognised failure is called at this layer,
// so a DNS lookup failure does not get reported as a connect timeout.
func classifyNetErr(ctx context.Context, err error, fallback string) string {
	if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
		// Shutdown, not a fault. Never counted against availability.
		return ErrClassCanceled
	}

	// A resolver failure is ours (or the resolver's), never the endpoint's —
	// timeout or not, which is why both cases land on the same class.
	//
	// Checked BEFORE the deadline and net.Error tests, not after: a lookup that
	// runs out of context comes back as a *net.DNSError that ALSO satisfies
	// errors.Is(err, context.DeadlineExceeded) (net.newDNSError wraps the
	// context error verbatim), and a resolver's own timeout satisfies
	// netErr.Timeout(). Either ordering mistake reports a DNS outage as a
	// connect timeout — blaming the endpoint for a failure that never reached it.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrClassDNS
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		// The layer that timed out, not a hardcoded class: at the resolve step
		// this is dns_error, at the dial step connect_timeout.
		return fallback
	case errors.Is(err, syscall.ECONNREFUSED):
		return ErrClassRefused
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fallback
	}
	// Some platforms surface a refused connection only in the message.
	if strings.Contains(err.Error(), "connection refused") {
		return ErrClassRefused
	}
	return fallback
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Nanoseconds()) / 1e6
}

// AttributeFault derives the cycle verdict from the two SINGAPORE readings.
//
// This is the whole fault-attribution rule, in one place, with no heuristics —
// the layers make it fall out. It is stored per cycle rather than recomputed
// per query so the availability strip and the availability arithmetic can never
// disagree.
//
// The Amsterdam pair is deliberately not an argument. A working Amsterdam
// reference does prove the uplink is alive, which is exactly what the removed
// `ref_eu` probe once proved, and restoring FaultRoute from it is the obvious
// next move. Do NOT make it here in passing: the uplink/route classes are what
// netSummary excludes from mimo_sgp's availability denominator, so reviving the
// distinction MOVES availability figures already published on the page. That is
// its own change, with its own reasoning about the numbers it shifts.
//
// Until then Amsterdam is a chart the reader interprets, not an input to a
// verdict the page states.
//
//	mimo ok                -> ok
//	mimo fail, ref_sgp ok  -> edge    (MiMo's edge; the path is fine)
//	both fail              -> uplink  (unattributable; window excluded)
//
// Precedence runs outward from us: each layer makes the ones beyond it
// unreadable, so the outermost failure wins.
//
// The last line is the one that changed when the European reference was
// removed. Three probes could separate "our own connection is down" from
// "the route to Singapore is degraded", because a working European PoP proved
// we still had an uplink. Two cannot: if neither MiMo nor an unrelated
// Singapore host answers, the cause may be our uplink, our ISP, or the route,
// and nothing here can tell them apart.
//
// So it resolves to FaultUplink — not because the uplink is proven down, but
// because that class is the one EXCLUDED from provider availability. When the
// measurement cannot attribute a failure, the only honest thing it can do is
// decline to attribute it, and the only unsafe direction is publishing our own
// outage as MiMo's. FaultRoute is no longer emitted; it remains defined, and
// accepted by the CHECK constraint, because cycles recorded under the old rule
// still carry it.
func AttributeFault(mimoOK, refSGPOK bool) string {
	switch {
	case mimoOK:
		return FaultOK
	case refSGPOK:
		return FaultEdge
	default:
		return FaultUplink
	}
}

// Cycle fault verdicts, matching the cycle_fault CHECK constraint.
//
// FaultRoute is historical: it needed a third probe to be distinguishable and
// is no longer produced. It stays here, and in the CHECK, because stored cycles
// carry it and the dashboard must keep rendering them.
const (
	FaultOK     = "ok"
	FaultEdge   = "edge"
	FaultRoute  = "route"
	FaultUplink = "uplink"
)
