package ibcf

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Peer is a network that the IBCF interconnects with: the agreement
// with it, and how the IBCF tells its messages apart from those of
// other peers.
type Peer struct {
	Profile interconnect.Profile
	// EntryPoint is the SIP URI of the peer's entry point, its IBCF,
	// where an initial request for the peer goes when no Route names the
	// next hop. TS 24.229 5.10.2.2 NOTE 3 lets the IBCF have it
	// provisioned, as here, or find it with RFC 3263, which an empty
	// EntryPoint asks for: the request then goes to the host of its
	// Request-URI.
	EntryPoint string
	// Hosts are further host names that belong to the peer: those of its
	// IBCFs in Record-Route, when they are neither in Profile.Domain nor
	// the host of EntryPoint.
	Hosts []string
	// Sources are the addresses that the peer's IBCFs send from. A
	// request that comes in from one of them is from this peer. A peer
	// without sources cannot send requests in.
	Sources []netip.Prefix
}

// Peers finds the peer of a message. The IBCF knows the peer of a
// request that comes in by where it comes from, and the peer of one
// that leaves by where it goes. PeerTable is one; a caller may keep the
// peers anywhere else, such as in the configuration of the role.
type Peers interface {
	// BySource finds the peer whose IBCF sent from addr. The external UA
	// asks it about each request in the goroutine of its stack
	// (IBCF.Screen), before anything else of that request happens: it
	// must answer at once, the same way for the same address, and take
	// no lock that the handling of a request may hold.
	BySource(addr netip.Addr) (Peer, bool)
	// ByHost finds the peer that host belongs to: the host of the next
	// hop of a request that leaves, the top Route or the Request-URI.
	ByHost(host string) (Peer, bool)
	// Names are the names of the profiles of every peer, for the peer
	// label of the counters.
	Names() []string
}

// PeerTable is Peers over a list. An IBCF has an agreement with a few
// networks, not one per subscriber, so the lookups go through the list.
type PeerTable struct {
	peers []Peer
}

// NewPeerTable checks each peer and keeps them in order: the first peer
// that matches a source or a host wins.
func NewPeerTable(peers ...Peer) (*PeerTable, error) {
	for i, p := range peers {
		if err := p.Profile.Validate(); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(peers[:i], func(q Peer) bool { return q.Profile.Name == p.Profile.Name }) {
			return nil, fmt.Errorf("ibcf: peer %s listed twice", p.Profile.Name)
		}
		if p.EntryPoint != "" {
			if _, err := sip.ParseURI(p.EntryPoint); err != nil {
				return nil, fmt.Errorf("ibcf: peer %s: entry point: %w", p.Profile.Name, err)
			}
		}
		if p.Profile.Domain == "" && p.EntryPoint == "" && len(p.Hosts) == 0 {
			return nil, errors.New("ibcf: peer " + p.Profile.Name + " has no domain, entry point or host to send to")
		}
	}
	return &PeerTable{peers: slices.Clone(peers)}, nil
}

func (t *PeerTable) BySource(addr netip.Addr) (Peer, bool) {
	addr = addr.Unmap()
	for _, p := range t.peers {
		if slices.ContainsFunc(p.Sources, func(s netip.Prefix) bool { return s.Contains(addr) }) {
			return p, true
		}
	}
	return Peer{}, false
}

func (t *PeerTable) ByHost(host string) (Peer, bool) {
	for _, p := range t.peers {
		if p.hasHost(host) {
			return p, true
		}
	}
	return Peer{}, false
}

func (t *PeerTable) Names() []string {
	var names []string
	for _, p := range t.peers {
		names = append(names, p.Profile.Name)
	}
	return names
}

// hasHost tells whether host is the peer's: in its domain, or one of
// the names of its IBCFs.
func (p Peer) hasHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	if d := strings.ToLower(p.Profile.Domain); d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
		return true
	}
	if u, err := sip.ParseURI(p.EntryPoint); err == nil && strings.EqualFold(u.Host, host) {
		return true
	}
	return slices.ContainsFunc(p.Hosts, func(h string) bool { return strings.EqualFold(h, host) })
}
