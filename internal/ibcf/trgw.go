package ibcf

import (
	"context"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/agw"
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
// 29.165 3.2, TS 29.238 1). The operations carry the names of the H.248
// commands, on the types of internal/agw, and each is one of the call
// related procedures of TS 29.238 Table 5.17.2.1.1 that the IBCF uses.
// *agw.Client implements it, over the HTTP API of the media relay,
// which then serves as the TrGW.
//
// SIMPLIFIED: TS 29.238. This project has no H.248 profile of Ix. The
// Controller of internal/h248/mediactl implements TrGW with the profile
// of Iq, whose termination names the IBCF uses; the profiles of Iq and
// Mp already show the protocol.
type TrGW interface {
	// Add is Reserve TrGW Connection Point, for a termination on each
	// side of one new context.
	Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error)
	// Modify is Configure TrGW Connection Point: it tells the
	// termination of one side where that side takes the media.
	Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error
	// Subtract is Release TrGW Termination, for both terminations of
	// the context.
	Subtract(ctx context.Context, id string) error
}

// terminations names the termination of a TrGW context that faces each
// side. The names are those of the IMS-AGW, access and core, which the
// media relay knows: the core termination faces the home network, and
// the access termination faces the peer.
var terminations = map[Side]agw.TerminationID{CoreSide: agw.Core, PeerSide: agw.Access}

// trgwContext bounds each request to the TrGW, so that a TrGW that
// stalls fails the call instead of holding it up for good. It is the
// bound that the P-CSCF puts on the media relay.
func trgwContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), agw.RequestTimeout)
}
