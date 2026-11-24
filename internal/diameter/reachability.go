package diameter

// Reachability is whether this node can reach a peer that it connects
// to, as an alarm about the peer needs it. It is not Available: a
// connection that closes takes no requests, but the node connects again
// after Tc by itself (RFC 6733 2.1), and a peer that is back by then
// was never unreachable. A peer is unreachable only once the recovery
// of the Diameter layer has failed: an attempt to open the connection
// failed, or the peer ended the connection and asked not to be
// connected to again (RFC 6733 5.4.3).
//
// The watchdog alone never makes a peer unreachable. A connection that
// it suspects (RFC 3539 3.4.1 [3]) is closed one Tw later and opened
// again after Tc, and the peer is unreachable only if that fails. A
// peer that answers late would otherwise turn SUSPECT and back again
// every few Tw, since a reconnected peer here goes straight back into
// service without the three DWAs of REOPEN (fsm.go).
type Reachability struct {
	// Unreachable is set from the failure until a connection to the peer
	// opens again, with its CEA.
	Unreachable bool
	// Reason says what failed, while Unreachable. It is one of the
	// phrases below, never an error of the moment, so that what an
	// operator reads stays the same for one outage.
	Reason string
}

// The reasons of an unreachable peer.
const (
	// ReasonNoTransport: the TCP connection could not be opened (the
	// event Rcv-Conn-Nack of RFC 6733 5.6).
	ReasonNoTransport = "no transport connection to the peer"
	// ReasonNoCapabilitiesExchange: the connection opened, but no CEA
	// with DIAMETER_SUCCESS came back for the CER (RFC 6733 5.3).
	ReasonNoCapabilitiesExchange = "no capabilities exchange with the peer"
	// ReasonNoReconnect: the peer sent a DPR whose Disconnect-Cause is
	// BUSY or DO_NOT_WANT_TO_TALK_TO_YOU (RFC 6733 5.4.3), so the node
	// does not connect again.
	ReasonNoReconnect = "the peer disconnected and asked not to be connected to again"
)

// reachabilityAfter is the reachability of a peer after the state
// machine moved from s to next on ev, and whether it changed from r.
// Only an initiator has one: a peer that connected to this node is
// one that this node does not reach out to.
func reachabilityAfter(r Reachability, s State, ev Event, next State) (Reachability, bool) {
	if s.Role != Initiator {
		return r, false
	}
	// A Stop ends every attempt without making the peer unreachable. A
	// node that stops while it is connecting may still see the attempt
	// fail first, when the failure and the Stop reach the loop together,
	// and then tells its watchers once that the peer is unreachable just
	// before the peer finishes. Nothing reads that alarm afterwards: the
	// alarm list goes with the process.
	var after Reachability
	_, stop := ev.(Stop)
	_, dpr := ev.(RcvDPR)
	switch {
	case next.Phase == Open && s.Phase != Open:
		after = Reachability{}
	case !stop && next.Phase == Closed && s.Phase == WaitConnAck:
		after = Reachability{Unreachable: true, Reason: ReasonNoTransport}
	case !stop && next.Phase == Closed && s.Phase == WaitICEA:
		after = Reachability{Unreachable: true, Reason: ReasonNoCapabilitiesExchange}
	case dpr && s.Reconnect && !next.Reconnect:
		after = Reachability{Unreachable: true, Reason: ReasonNoReconnect}
	default:
		return r, false
	}
	// A failed attempt while the peer is unreachable already keeps the
	// reason of the first one.
	if after.Unreachable == r.Unreachable {
		return r, false
	}
	return after, true
}

// WatchReachability calls f with the reachability of the peer now, and
// again each time it changes, in the order of the changes. f runs on
// the goroutine of the peer, so it must not block and must not call
// WatchReachability. Watchers stay until the peer stops.
func (p *Peer) WatchReachability(f func(Reachability)) {
	p.reachMu.Lock()
	defer p.reachMu.Unlock()
	p.watchers = append(p.watchers, f)
	f(p.reach)
}

// setReachability tells the watchers when the move from s to next on
// ev changed the reachability of the peer.
func (p *Peer) setReachability(s State, ev Event, next State) {
	p.reachMu.Lock()
	defer p.reachMu.Unlock()
	r, changed := reachabilityAfter(p.reach, s, ev, next)
	if !changed {
		return
	}
	p.reach = r
	if r.Unreachable {
		p.log.Warn("peer unreachable", "reason", r.Reason)
	} else {
		p.log.Info("peer reachable again")
	}
	for _, f := range p.watchers {
		f(r)
	}
}
