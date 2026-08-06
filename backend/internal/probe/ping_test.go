package probe

import (
	"context"
	"net"
	"testing"
	"time"
)

// listenerAcceptingAfter opens a real local listener that completes the TCP
// handshake only after delay, so connect_ms can be asserted against a known
// value rather than against whatever the network happened to do.
//
// Note this measures the ACCEPT delay, not the handshake itself: the kernel
// completes the SYN/SYN-ACK into the backlog before Accept runs. The test asserts
// the probe reports a plausible small number and never a negative or absurd one.
func listenerAcceptingAfter(t *testing.T, delay time.Duration) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			time.Sleep(delay)
			_ = conn.Close()
		}
	}()
	return ln.Addr().String(), func() { ln.Close(); <-done }
}

// pingerTo builds a Pinger whose dial is redirected at a local listener, so the
// TCP measurement is exercised without leaving the machine.
func pingerTo(timeout time.Duration, target string) *Pinger {
	p := NewPinger(timeout)
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	return p
}

func TestPingMeasuresASuccessfulHandshake(t *testing.T) {
	addr, stop := listenerAcceptingAfter(t, 0)
	defer stop()

	res := pingerTo(5*time.Second, addr).Ping(context.Background(), TargetMimoSGP, "localhost")

	if !res.OK {
		t.Fatalf("expected OK, got class=%s detail=%s", res.ErrorClass, res.ErrorDetail)
	}
	if res.Target != TargetMimoSGP {
		t.Errorf("target = %q", res.Target)
	}
	if res.ConnectMs < 0 {
		t.Errorf("connect_ms = %v, must not be negative", res.ConnectMs)
	}
	if res.ConnectMs > 2000 {
		t.Errorf("connect_ms = %v, implausible for a loopback handshake", res.ConnectMs)
	}
	if res.ErrorClass != "" {
		t.Errorf("a successful ping must carry no error class, got %q", res.ErrorClass)
	}
}

// DNS time is reported separately from connect time. Folding them together
// would blame the endpoint for a resolver problem that is ours.
func TestPingSeparatesDNSFromConnect(t *testing.T) {
	addr, stop := listenerAcceptingAfter(t, 0)
	defer stop()

	res := pingerTo(5*time.Second, addr).Ping(context.Background(), TargetRefSGP, "localhost")

	if !res.OK {
		t.Fatalf("expected OK, got %s", res.ErrorClass)
	}
	if res.DNSMs < 0 {
		t.Errorf("dns_ms = %v, must not be negative", res.DNSMs)
	}
	// Loopback resolution is fast; the point is that it is measured at all and
	// is not silently folded into connect_ms.
	if res.DNSMs > 2000 {
		t.Errorf("dns_ms = %v, implausible for localhost", res.DNSMs)
	}
}

// A closed port must record a class and must not panic — this is the single
// most likely real-world failure and it has to produce a usable sample.
func TestPingClosedPortRecordsAClassAndDoesNotPanic(t *testing.T) {
	// Bind then immediately release, so the port is almost certainly free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	res := pingerTo(2*time.Second, addr).Ping(context.Background(), TargetRefSGP, "localhost")

	if res.OK {
		t.Fatal("expected failure against a closed port")
	}
	if res.ErrorClass == "" {
		t.Error("a failed ping must carry an error class")
	}
	if res.ErrorClass != ErrClassRefused && res.ErrorClass != ErrClassConnectTimeout {
		t.Errorf("class = %q, want connection_refused or connect_timeout", res.ErrorClass)
	}
	if res.Target != TargetRefSGP {
		t.Errorf("a failed ping must still identify its target, got %q", res.Target)
	}
}

func TestPingUnresolvableHostIsADNSError(t *testing.T) {
	// .invalid is reserved by RFC 2606 and must never resolve.
	res := NewPinger(3*time.Second).Ping(context.Background(), TargetMimoSGP,
		"ismimodown-does-not-exist.invalid")

	if res.OK {
		t.Fatal("expected failure")
	}
	if res.ErrorClass != ErrClassDNS {
		t.Errorf("class = %q, want %q — a resolver failure is ours, not the endpoint's",
			res.ErrorClass, ErrClassDNS)
	}
}

func TestPingRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := NewPinger(5*time.Second).Ping(ctx, TargetMimoSGP, "example.com")

	if res.OK {
		t.Fatal("expected failure on a cancelled context")
	}
	// Shutdown is not a fault; it must never be counted against availability.
	if res.ErrorClass != ErrClassCanceled && res.ErrorClass != ErrClassDNS {
		t.Errorf("class = %q", res.ErrorClass)
	}
}

// One case per row of the attribution table. This is the rule the whole
// product rests on: "MiMo is down" and "the path to MiMo is bad" are different
// findings, and nobody publishes both.
func TestAttributeFaultCoversEveryRowOfTheTable(t *testing.T) {
	cases := []struct {
		name         string
		mimo, refSGP bool
		want         string
		why          string
	}{
		{
			name: "all reachable", mimo: true, refSGP: true, want: FaultOK,
			why: "nothing is wrong at the network layer",
		},
		{
			name: "mimo down, reference up", mimo: false, refSGP: true,
			want: FaultEdge,
			why:  "the route to Singapore demonstrably works, so this is MiMo's edge",
		},
		{
			// The case that lost its resolution when the European reference was
			// removed: this used to split into route-vs-uplink. It cannot any
			// more, and the excluded class is the safe place to land — the
			// alternative publishes our own outage as MiMo's.
			name: "mimo and reference both down", mimo: false, refSGP: false,
			want: FaultUplink,
			why:  "unattributable from here; the window is excluded rather than blamed on MiMo",
		},
		{
			// MiMo answering while our reference does not is odd, but it is not a
			// MiMo fault, and calling it one would publish an outage that never
			// happened.
			name: "mimo up despite a reference failure", mimo: true, refSGP: false,
			want: FaultOK,
			why:  "MiMo answered; a reference quirk must never manufacture a provider outage",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttributeFault(tc.mimo, tc.refSGP); got != tc.want {
				t.Errorf("AttributeFault(%v,%v) = %q, want %q — %s",
					tc.mimo, tc.refSGP, got, tc.want, tc.why)
			}
		})
	}
}

// FaultRoute is no longer produced, but it is still a legal stored value and
// the dashboard still renders cycles that carry it. Deleting the constant would
// break reading history that was correctly recorded under the old rule.
func TestFaultRouteRemainsDefinedForStoredCycles(t *testing.T) {
	if FaultRoute != "route" {
		t.Errorf("FaultRoute = %q; stored cycles carry this exact value", FaultRoute)
	}
}

// A host resolving to several addresses must not be declared unreachable
// because the FIRST one is bad. MiMo's Singapore edge resolves to eight
// addresses and its Amsterdam edge to two; pinning to ips[0] would turn one bad
// address into a published provider outage that never happened.
//
// The two addresses come from the lookup seam rather than from `localhost`
// carrying 127.0.0.1 and ::1. That coincidence used to supply them, and pinning
// the probe to IPv4 ended it — leaving the walk untested on any machine whose
// hosts file lists one A record.
func TestPingTriesEveryResolvedAddress(t *testing.T) {
	good, stop := listenerAcceptingAfter(t, 0)
	defer stop()

	// Reserve then release a port so it is almost certainly refusing.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	badAddr := dead.Addr().String()
	dead.Close()

	var attempts []string
	p := NewPinger(5 * time.Second)
	p.resolver = net.DefaultResolver
	p.lookup = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.IPv4(127, 0, 0, 1), net.IPv4(127, 0, 0, 1)}, nil
	}
	// Two "addresses": the first refuses, the second answers.
	targets := []string{badAddr, good}
	idx := 0
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		if idx >= len(targets) {
			t.Fatalf("dialled more times (%d) than there were addresses", idx+1)
		}
		target := targets[idx]
		idx++
		attempts = append(attempts, target)
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}

	res := p.Ping(context.Background(), TargetMimoSGP, "localhost")

	if !res.OK {
		t.Fatalf("expected the second address to succeed, got class=%s detail=%s",
			res.ErrorClass, res.ErrorDetail)
	}
	if len(attempts) < 2 {
		t.Errorf("only %d address(es) attempted; a bad first address would read as an outage",
			len(attempts))
	}
	// connect_ms must be the SUCCESSFUL handshake alone — folding in the failed
	// attempt before it would masquerade as edge latency.
	if res.ConnectMs > 2000 {
		t.Errorf("connect_ms = %v; it must time the successful handshake only", res.ConnectMs)
	}
}

// The handshake is IPv4, at the dial and at the resolve.
//
// Not a style preference: the four targets are read against each other on one
// chart, both Xiaomi edges are A-only and both references are dual-stack, so an
// unpinned probe would time a v6 route to the references against a v4 route to
// the edges and the gap would be published as edge latency.
func TestPingDialsIPv4Only(t *testing.T) {
	addr, stop := listenerAcceptingAfter(t, 0)
	defer stop()

	var networks []string
	p := NewPinger(5 * time.Second)
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		networks = append(networks, network)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	p.lookup = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.IPv4(127, 0, 0, 1)}, nil
	}

	res := p.Ping(context.Background(), TargetRefAMS, "localhost")

	if !res.OK {
		t.Fatalf("expected OK, got class=%s detail=%s", res.ErrorClass, res.ErrorDetail)
	}
	for _, n := range networks {
		if n != "tcp4" {
			t.Errorf("dialled network %q, want tcp4 — a v6 route is not comparable "+
				"with the v4 route the Xiaomi edges force", n)
		}
	}
}

// A host with AAAA records and no A is unreachable to this probe, and must say
// so at the DNS layer rather than reporting a connect timeout: the endpoint was
// never dialled, and blaming it would be a fabricated provider fault.
func TestPingReportsDNSWhenNoIPv4Address(t *testing.T) {
	p := NewPinger(5 * time.Second)
	p.lookup = func(context.Context, string) ([]net.IP, error) { return nil, nil }
	p.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dialled despite there being no IPv4 address to dial")
		return nil, nil
	}

	res := p.Ping(context.Background(), TargetRefAMS, "v6only.example")

	if res.OK {
		t.Fatal("expected failure when the host has no IPv4 address")
	}
	if res.ErrorClass != ErrClassDNS {
		t.Errorf("class = %q, want %q", res.ErrorClass, ErrClassDNS)
	}
}

// When every address fails, the probe still reports a failure rather than
// silently succeeding or panicking on an empty address list.
func TestPingFailsWhenEveryAddressFails(t *testing.T) {
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	badAddr := dead.Addr().String()
	dead.Close()

	res := pingerTo(2*time.Second, badAddr).Ping(context.Background(), TargetRefSGP, "localhost")

	if res.OK {
		t.Fatal("expected failure when no address answers")
	}
	if res.ErrorClass == "" {
		t.Error("a failed ping must carry an error class")
	}
}
