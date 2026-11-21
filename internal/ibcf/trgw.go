package ibcf

import (
	"context"
	"net/netip"
	"time"
)

// Side names a termination of a TrGW context: the TrGW sits on the
// border, in the media path (TS 29.165 5.2.2), with one termination in
// the home network and one towards the peer's TrGW, at the Izi
// reference point (TS 29.165 3.2, 5.1). TS 29.238 Table 5.4.1 has a
// context of two IP terminations.
type Side string

const (
	// CoreSide faces the home network.
	CoreSide Side = "core"
	// PeerSide faces the peer network.
	PeerSide Side = "peer"
)

// MediaContext is one context in the TrGW, with the local address of
// each termination: where the TrGW takes the RTP of that side, and what
// the SDP shows to it. RTCP is on the port above, as in internal/agw.
type MediaContext struct {
	ID   string
	Core netip.AddrPort
	Peer netip.AddrPort
}

// local is the address of the termination on side s.
func (c MediaContext) local(s Side) netip.AddrPort {
	if s == CoreSide {
		return c.Core
	}
	return c.Peer
}

// TrGW is the transition gateway that the IBCF controls over Ix (TS
// 29.165 3.2, TS 29.238 1). The operations are the call related procedures of
// TS 29.238 Table 5.17.2.1.1 that the IBCF uses: Reserve TrGW
// Connection Point, Configure TrGW Connection Point and Release TrGW
// Termination. AGWClient is one over the control API of internal/agw.
//
// SIMPLIFIED: TS 29.238. No implementation over H.248 (Ix) stands
// behind this interface; the H.248 profiles in internal/h248/mediactl
// (Iq, Mp) already show the protocol.
type TrGW interface {
	// Reserve makes a context with a termination on each side.
	Reserve(ctx context.Context) (MediaContext, error)
	// Configure tells the termination on side s where to send: the
	// address that the SDP of that side gave.
	Configure(ctx context.Context, id string, s Side, remote netip.AddrPort) error
	// Release removes the context.
	Release(ctx context.Context, id string) error
}

// trgwTimeout bounds each request to the TrGW, so that a TrGW that
// stalls fails the call instead of holding it up for good. It is the
// bound that the P-CSCF puts on the media relay (agw.RequestTimeout).
const trgwTimeout = 2 * time.Second

func trgwContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), trgwTimeout)
}
