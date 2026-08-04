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
const (
	TargetMimoSGP = "mimo_sgp"
	TargetRefSGP  = "ref_sgp"
	TargetRefEU   = "ref_eu"
)

// pingPort is fixed at 443 and deliberately not configurable: the point of the
// measurement is that it traverses the same path, through the same firewalls,
// to the same port that inference actually uses. A ping to any other port would
// answer a different question while looking like the same number.
const pingPort = "443"

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
	dnsStart := time.Now()
	addrs, err := p.resolver.LookupHost(ctx, host)
	res.DNSMs = msSince(dnsStart)
	if err != nil {
		res.ErrorClass = classifyNetErr(ctx, err, ErrClassDNS)
		res.ErrorDetail = err.Error()
		return res
	}
	if len(addrs) == 0 {
		res.ErrorClass = ErrClassDNS
		res.ErrorDetail = "no addresses returned for " + host
		return res
	}

	dial := p.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}

	// Measure the handshake alone — the resolve above is already accounted for
	// and must not be double-counted into connect_ms.
	connectStart := time.Now()
	conn, err := dial(ctx, "tcp", net.JoinHostPort(addrs[0], pingPort))
	res.ConnectMs = msSince(connectStart)
	if err != nil {
		res.ErrorClass = classifyNetErr(ctx, err, ErrClassConnectTimeout)
		res.ErrorDetail = err.Error()
		return res
	}
	_ = conn.Close()

	res.OK = true
	return res
}

// classifyNetErr maps a transport error onto the public error vocabulary.
//
// fallback is what an otherwise-unrecognised failure is called at this layer,
// so a DNS lookup failure does not get reported as a connect timeout.
func classifyNetErr(ctx context.Context, err error, fallback string) string {
	switch {
	case errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled:
		// Shutdown, not a fault. Never counted against availability.
		return ErrClassCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrClassConnectTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ErrClassRefused
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return ErrClassDNS
		}
		return ErrClassDNS
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

// AttributeFault derives the cycle verdict from the three network readings.
//
// This is the whole fault-attribution rule, in one place, with no heuristics —
// the layers make it fall out. It is stored per cycle rather than recomputed
// per query so the availability strip and the availability arithmetic can never
// disagree.
//
//	mimo ok                          -> ok
//	mimo fail, ref_sgp ok            -> edge    (MiMo's edge; the route is fine)
//	mimo + ref_sgp fail, ref_eu ok   -> route   (Roubaix->Singapore degraded)
//	all three fail                   -> uplink  (ours; window excluded)
//
// Precedence runs outward from us: each layer makes the ones beyond it
// unreadable, so our own uplink is checked last but wins when it is down.
func AttributeFault(mimoOK, refSGPOK, refEUOK bool) string {
	switch {
	case mimoOK:
		return FaultOK
	case refSGPOK:
		return FaultEdge
	case refEUOK:
		return FaultRoute
	default:
		return FaultUplink
	}
}

// Cycle fault verdicts, matching the cycle_fault CHECK constraint.
const (
	FaultOK     = "ok"
	FaultEdge   = "edge"
	FaultRoute  = "route"
	FaultUplink = "uplink"
)
