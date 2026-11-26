package diameter

import (
	"net"
	"testing"
	"time"
)

func TestReachabilityAfterEachMove(t *testing.T) {
	initiator := NewInitiatorState(hss, []uint32{ApplicationIDCx})
	in := func(p Phase) State { s := initiator; s.Phase = p; return s }
	unreachable := Reachability{Unreachable: true, Reason: ReasonNoTransport}
	tests := []struct {
		name        string
		before      Reachability
		s           State
		ev          Event
		next        State
		want        Reachability
		wantChanged bool
	}{
		{"connect refused", Reachability{}, in(WaitConnAck), RcvConnNack{}, in(Closed),
			unreachable, true},
		{"CER unanswered", Reachability{}, in(WaitICEA), Timeout{}, in(Closed),
			Reachability{Unreachable: true, Reason: ReasonNoCapabilitiesExchange}, true},
		{"CEA opens the connection", unreachable, in(WaitICEA), RcvCEA{}, in(Open),
			Reachability{}, true},
		{"another failed attempt keeps the first reason", unreachable, in(WaitICEA), PeerDisc{}, in(Closed),
			unreachable, false},
		// Closed with Reconnect: the node tries again after Tc, so the
		// loss alone does not make the peer unreachable.
		{"open connection lost", Reachability{}, in(Open), PeerDisc{}, in(Closed),
			Reachability{}, false},
		{"watchdog suspects", Reachability{}, in(Open), Timeout{}, State{Role: Initiator, Phase: Open, Watchdog: WatchdogSuspect, Reconnect: true},
			Reachability{}, false},
		{"DPR with REBOOTING", Reachability{}, in(Open), RcvDPR{Cause: Rebooting}, in(Closing),
			Reachability{}, false},
		{"DPR with BUSY", Reachability{}, in(Open), RcvDPR{Cause: Busy}, State{Role: Initiator, Phase: Closing},
			Reachability{Unreachable: true, Reason: ReasonNoReconnect}, true},
		{"local stop while connecting", Reachability{}, in(WaitConnAck), Stop{}, State{Role: Initiator, Phase: Closed},
			Reachability{}, false},
		{"responder", Reachability{}, State{Role: Responder, Phase: Open}, PeerDisc{}, State{Role: Responder, Phase: Closed},
			Reachability{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := reachabilityAfter(tt.before, tt.s, tt.ev, tt.next)
			if got != tt.want || changed != tt.wantChanged {
				t.Errorf("got %+v, changed %v; want %+v, changed %v", got, changed, tt.want, tt.wantChanged)
			}
		})
	}
}

// watchReachability collects what WatchReachability reports of peer.
func watchReachability(peer *Peer) chan Reachability {
	seen := make(chan Reachability, 16)
	peer.WatchReachability(func(r Reachability) { seen <- r })
	return seen
}

func nextReachability(t *testing.T, seen chan Reachability, within time.Duration) Reachability {
	t.Helper()
	select {
	case r := <-seen:
		return r
	case <-time.After(within):
		t.Fatalf("no change of the reachability of the peer within %v", within)
		return Reachability{}
	}
}

// noReachabilityChange checks that seen holds no change. The peer tells
// its watchers of a change on its own goroutine, before it logs the next
// move, so a change that came before a line the test has read is in
// seen already.
func noReachabilityChange(t *testing.T, seen chan Reachability) {
	t.Helper()
	select {
	case r := <-seen:
		t.Fatalf("the reachability of the peer changed to %+v", r)
	default:
	}
}

// waitForLogs waits until logs hold n records that match want.
func waitForLogs(t *testing.T, logs *logBuffer, want fields, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for countLogs(logs.records(t), want) < n {
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d log records matching %v within 5 s; got:\n%v", n, want, logs.records(t))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// holdPort keeps a TCP connection to address until the test ends. The
// end of it at the server is bound to the port of address, and a port
// that a socket is bound to is given to no other socket that asks for a
// free one. So the port stays the server's while the server does not
// listen, and the server can listen on it again.
func holdPort(t *testing.T, address string) {
	t.Helper()
	c, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
}

// refusedAddress is an address that refuses connections, and on which
// no one can listen while the test runs: the local end of a connection
// that the test keeps open.
func refusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accepted.Close() })
	return c.LocalAddr().String()
}

// The HSS takes itself out of service the way a drain does: a DPR with
// REBOOTING and no listener until it is back. The node connects again
// after Tc, is refused, and only then is the HSS unreachable; the next
// attempt after the HSS is back opens the connection again.
func TestPeerIsUnreachableOnlyWhenAnAttemptFails(t *testing.T) {
	hssNode, _ := newTestNode(hss, testTimers)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	scscfNode, logs := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	defer peer.Stop()
	seen := watchReachability(peer)
	if r := nextReachability(t, seen, time.Second); r.Unreachable {
		t.Fatalf("a new peer reads %+v, want reachable", r)
	}
	opened := fields{"msg": "peer state", "from": "Wait-I-CEA", "to": "I-Open (OKAY)"}
	waitForLogs(t, logs, opened, 1)

	// A short reconnect: the HSS ends the connection but keeps
	// listening, so the attempt after Tc succeeds.
	server.DisconnectPeers()
	waitForLogs(t, logs, opened, 2)
	noReachabilityChange(t, seen)
	if !peer.Available() {
		t.Fatal("the peer opened its connection again but takes no requests")
	}

	holdPort(t, server.Addr())
	if err := server.SetAccepting("hss", false); err != nil {
		t.Fatal(err)
	}
	server.DisconnectPeers()
	r := nextReachability(t, seen, testTimers.Reconnect+2*time.Second)
	if want := (Reachability{Unreachable: true, Reason: ReasonNoTransport}); r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}
	waitForLog(t, logs, fields{"msg": "peer unreachable", "reason": ReasonNoTransport})
	// The attempts that fail after the first change nothing.
	refused := fields{"msg": "peer state", "event": "Rcv-Conn-Nack"}
	waitForLogs(t, logs, refused, countLogs(logs.records(t), refused)+2)
	noReachabilityChange(t, seen)

	if err := server.SetAccepting("hss", true); err != nil {
		t.Fatal(err)
	}
	if r := nextReachability(t, seen, testTimers.Reconnect+2*time.Second); r.Unreachable {
		t.Fatalf("got %+v once the HSS listens again, want reachable", r)
	}
	if !peer.Available() {
		t.Error("the peer is reachable again but takes no requests")
	}
	waitForLog(t, logs, fields{"msg": "peer reachable again"})
}

// A watcher added while the peer is unreachable learns so at once.
func TestWatchReachabilityStartsWithTheStateNow(t *testing.T) {
	scscfNode, logs := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: refusedAddress(t), OriginHost: hss})
	defer peer.Stop()
	waitForLog(t, logs, fields{"msg": "peer unreachable"})
	seen := watchReachability(peer)
	if r := nextReachability(t, seen, time.Second); !r.Unreachable || r.Reason != ReasonNoTransport {
		t.Errorf("got %+v, want unreachable for %q", r, ReasonNoTransport)
	}
}
