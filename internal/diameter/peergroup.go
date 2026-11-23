package diameter

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// PeerGroup is a group of peers that can each serve the same requests,
// such as the instances of an HSS server farm: every instance holds
// every subscriber, so any of them can answer a Cx or Sh request (TS
// 29.228 6.4). The requests carry no Destination-Host, so nothing in
// them names one instance.
type PeerGroup struct {
	peers []*Peer
	// role is the value of the role label of what the group counts, the
	// name of the role in the configuration file.
	role     string
	measured *measurements
}

// NewPeerGroup groups peers, in the order of the configuration. They
// belong to one node, whose Answer timer bounds a request to the group.
//
// role is the role that sends through the group, one of the roles that
// count Diameter messages (measurements.go), and reg the registry of
// its node, in which the group counts the requests and the answers of
// that role. Nil gives it a registry of its own, which nothing serves.
func NewPeerGroup(reg *metrics.Registry, role string, peers ...*Peer) (*PeerGroup, error) {
	if !slices.Contains(countingRoles.Values, role) {
		return nil, fmt.Errorf("diameter: %q is not a role that counts Diameter messages (%s)",
			role, strings.Join(countingRoles.Values, ", "))
	}
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	measured, err := newMeasurements(reg)
	if err != nil {
		return nil, err
	}
	return &PeerGroup{peers: slices.Clone(peers), role: role, measured: measured}, nil
}

// Peers are the peers of the group, in the order Request tries them.
func (g *PeerGroup) Peers() []*Peer { return slices.Clone(g.peers) }

// Available says whether a peer of the group takes requests now, so that
// a request that names no Destination-Host can go out.
func (g *PeerGroup) Available() bool { return slices.ContainsFunc(g.peers, (*Peer).Available) }

// Request sends request to one peer of the group and returns its
// answer.
//
// A request that names a Destination-Host has a fixed destination: only
// the peer of that host can serve it, so it goes there and nowhere
// else, and the group has nothing to send on when that peer is gone
// (RFC 6733 5.5.4). A request that names none may go to any peer of the
// group.
//
// Among those peers it goes in the order of the configuration. A peer
// whose connection is not open refuses the request at once, so the
// request goes out on the first open connection. When that peer does
// not answer in time, when its connection closes before the answer
// arrives, or when it answers DIAMETER_TOO_BUSY, which RFC 6733 7.1.3
// says to send to an alternate peer, the request goes again to the next
// peer, as the failover of RFC 6733 5.5.4 asks.
//
// Any other answer is the group's answer, DIAMETER_UNABLE_TO_DELIVER
// among them: that peer did deliver its answer, and it says that the
// request got no further, not that this peer is gone.
//
// All of the attempts together get the Answer timer, not one each, so
// that a request costs the caller the same whether the group holds one
// peer or ten. A role that makes two of them under the lock of a user,
// as a registration does, then stays well inside the lock's lease
// (ims.UserLockLease). Each attempt gets what is left of that timer
// divided by the peers still to try, so a peer that is open and silent
// cannot use up the time its successors need, and the time a peer
// leaves unused goes to them.
//
// What comes back is the answer, or the error, of the last peer tried;
// the log holds the attempts before it. Every attempt that went out is
// counted, with the answer that came back to it (measurements.go).
func (g *PeerGroup) Request(ctx context.Context, request *diam.Message) (*diam.Message, error) {
	peers := g.serving(destinationHost(request))
	var answer *diam.Message
	// A group with no peer for this request has nothing to send on,
	// which is what a closed connection gives the caller as well.
	err := ErrPeerUnavailable
	if len(peers) == 0 {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, peers[0].node.timers.Answer)
	defer cancel()
	deadline, _ := ctx.Deadline()
	sent := false
	for i, p := range peers {
		message := request
		if sent {
			message = retransmission(request)
		}
		answer, err = attempt(ctx, p, message, time.Until(deadline)/time.Duration(len(peers)-i))
		// A request that never reached the connection, because the peer
		// refused it or never took it, has nothing transmitted to count,
		// and the next peer gets a first attempt rather than a
		// retransmission (errNotSent).
		transmitted := !errors.Is(err, errNotSent)
		if transmitted {
			g.measured.sent(g.role, message)
		}
		if err == nil {
			g.measured.received(g.role, message, answer)
		}
		if err == nil && resultCode(answer) != DiameterTooBusy {
			return answer, nil
		}
		sent = sent || transmitted
		reason := "DIAMETER_TOO_BUSY"
		if err != nil {
			reason = err.Error()
		}
		if i+1 == len(peers) || ctx.Err() != nil {
			p.log.Warn("no peer of the group served the request", "command", commandName(request), "reason", reason)
			break
		}
		p.log.Info("failover", "command", commandName(request), "reason", reason, "next_peer", peers[i+1].host)
	}
	return answer, err
}

// attempt sends request to p and waits at most share for the answer,
// within what is left of the group's own deadline.
func attempt(ctx context.Context, p *Peer, request *diam.Message, share time.Duration) (*diam.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, share)
	defer cancel()
	return p.Request(ctx, request)
}

// serving are the peers of the group that may take a request for host:
// the one peer of that host when the request names one, and all of
// them when it does not. A Diameter identity is an FQDN, which compares
// without case.
//
// SIMPLIFIED: RFC 6733 5.5.4 and TS 29.228 Table 6.1.2.1. Each instance
// of the HSS names itself, so the S-CSCF addresses the requests of a
// registration it has started to the instance that answered its MAR,
// and those requests cannot go to another instance: when that one
// stops, they fail and the user registers again. A commercial network
// keeps them going by letting the instances share one Diameter identity
// behind a load balancer, or by putting an agent in front of them that
// routes on the realm. Neither is built here: the load balancer is
// another node to run and hides which instance answered, and the agent
// is the DRA that the design of the redundancy leaves out.
func (g *PeerGroup) serving(host string) []*Peer {
	if host == "" {
		return g.peers
	}
	for _, p := range g.peers {
		if strings.EqualFold(p.host, host) {
			return []*Peer{p}
		}
	}
	return nil
}

// retransmission is request again for another peer: a copy with the T
// flag set, so that the flag and the Hop-by-Hop Identifier of this
// attempt do not change the message that the peer before may still be
// writing, or that the capture of the first attempt holds. Its
// End-to-End Identifier stays, since that and the Origin-Host are how
// the receiver tells a duplicate of a failover from a new request (RFC
// 6733 5.5.4).
func retransmission(request *diam.Message) *diam.Message {
	header := *request.Header
	header.CommandFlags |= diam.RetransmittedFlag
	m := *request
	m.Header = &header
	m.AVP = slices.Clone(request.AVP)
	return &m
}
