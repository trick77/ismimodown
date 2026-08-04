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

	res := pingerTo(5*time.Second, addr).Ping(context.Background(), TargetRefEU, "localhost")

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
		"llmstats-does-not-exist.invalid")

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
		name                string
		mimo, refSGP, refEU bool
		want                string
		why                 string
	}{
		{
			name: "all reachable", mimo: true, refSGP: true, refEU: true, want: FaultOK,
			why: "nothing is wrong at the network layer",
		},
		{
			name: "mimo down, both references up", mimo: false, refSGP: true, refEU: true,
			want: FaultEdge,
			why:  "the route to Singapore demonstrably works, so this is MiMo's edge",
		},
		{
			name: "mimo and sgp reference down, europe up", mimo: false, refSGP: false, refEU: true,
			want: FaultRoute,
			why:  "a second Singapore host is also unreachable — not MiMo, not us",
		},
		{
			name: "everything down", mimo: false, refSGP: false, refEU: false, want: FaultUplink,
			why: "our own uplink; the window is excluded from everyone's availability",
		},
		{
			// MiMo answering while our own anycast reference does not is odd, but
			// it is not a MiMo fault, and calling it one would publish an outage
			// that never happened.
			name: "mimo up despite reference failures", mimo: true, refSGP: false, refEU: false,
			want: FaultOK,
			why:  "MiMo answered; a reference quirk must never manufacture a provider outage",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AttributeFault(tc.mimo, tc.refSGP, tc.refEU); got != tc.want {
				t.Errorf("AttributeFault(%v,%v,%v) = %q, want %q — %s",
					tc.mimo, tc.refSGP, tc.refEU, got, tc.want, tc.why)
			}
		})
	}
}
