package interconnect

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Border is what an IBCF applies to the messages it exchanges with one
// peer network. Its methods run the steps of this package in the order
// of TS 24.229 5.10.2 (exit point) and 5.10.3 (entry point), on a copy
// of a message that the IBCF received on one of its legs. The IBCF then
// builds the message of its other leg from what CrossingFields returns
// and from the body. The methods change only the message; routing
// (removing the IBCF's own Route, choosing the next hop), the dialogs,
// the 100 (Trying) and the TrGW stay with the caller.
type Border struct {
	Peer Profile
	// HomeDomain is the home network's domain: the host of a number in
	// P-Asserted-Identity that leaves as a SIP URI, and of a
	// Request-URI that comes in as one.
	HomeDomain string
	// HomeNumberFormat is how numbers are written inside the home
	// network. A Request-URI and P-Asserted-Identity that come in get
	// it; the empty format keeps the scheme they came with.
	HomeNumberFormat NumberFormat
}

// Validate reports the first mistake in the border's configuration.
func (b Border) Validate() error {
	if err := b.Peer.Validate(); err != nil {
		return err
	}
	switch b.HomeNumberFormat {
	case "", NumberFormatTel, NumberFormatSIP:
	default:
		return fmt.Errorf("interconnect: border with %s: unknown home number format %q", b.Peer.Name, b.HomeNumberFormat)
	}
	if b.HomeDomain == "" && (b.HomeNumberFormat == NumberFormatSIP || b.Peer.NumberFormat == NumberFormatSIP) {
		return fmt.Errorf("interconnect: border with %s: number format sip needs the home domain", b.Peer.Name)
	}
	return nil
}

// ExitRequest prepares a request from the home network for the peer
// (TS 24.229 5.10.2.2, 5.10.2.3). A *Rejection error means the IBCF
// answers the request with Rejection.Response instead; for an ACK,
// which gets no response, the caller drops it.
func (b Border) ExitRequest(req *sip.Message) error {
	if err := b.screenRequest(req); err != nil {
		return err
	}
	RemoveNotApplicable(req)
	ApplyTrustDomain(req, b.Peer, Exit)
	RemoveReceivedRealm(req)
	ApplyPrivacy(req, b.Peer)
	ApplyHeaderRules(req, b.Peer, Exit)
	if err := NormalizeNumbers(req, b.Peer, b.Peer.NumberFormat, b.Peer.Domain, b.HomeDomain); err != nil {
		return err
	}
	return b.filterRequestBody(req)
}

// EntryRequest prepares a request from the peer for the home network
// (TS 24.229 5.10.3.2, 5.10.3.3). The caller calls it before it removes
// its own URI from Route, because the check for orig looks at the
// topmost Route as it arrived. A *Rejection error means the IBCF
// answers the request with Rejection.Response; for an ACK the caller
// drops it.
func (b Border) EntryRequest(req *sip.Message) error {
	if err := CheckOrig(req, b.Peer); err != nil {
		return err
	}
	if err := b.screenRequest(req); err != nil {
		return err
	}
	RemoveNotApplicable(req)
	ApplyTrustDomain(req, b.Peer, Entry)
	ApplyHeaderRules(req, b.Peer, Entry)
	if err := NormalizeNumbers(req, b.Peer, b.HomeNumberFormat, b.HomeDomain, b.Peer.Domain); err != nil {
		return err
	}
	return b.filterRequestBody(req)
}

// ExitResponse prepares a response from the home network for the
// peer, which sent the request.
//
// SIMPLIFIED: TS 29.165 7.1. When the SDP of a response has no codec
// that the peer accepts, the IBCF would have to end the session it
// cannot set up. Here the response goes on with the SDP as it was.
func (b Border) ExitResponse(resp *sip.Message) {
	RemoveNotApplicable(resp)
	ApplyTrustDomain(resp, b.Peer, Exit)
	ApplyPrivacy(resp, b.Peer)
	ApplyHeaderRules(resp, b.Peer, Exit)
	_ = NormalizeNumbers(resp, b.Peer, b.Peer.NumberFormat, "", b.HomeDomain)
	_ = FilterBody(resp, b.Peer.Codecs)
}

// EntryResponse prepares a response from the peer for the home
// network, to a request that ExitRequest prepared. The SDP is filtered
// as ExitResponse does it.
func (b Border) EntryResponse(resp *sip.Message) {
	RemoveNotApplicable(resp)
	ApplyTrustDomain(resp, b.Peer, Entry)
	ApplyHeaderRules(resp, b.Peer, Entry)
	_ = NormalizeNumbers(resp, b.Peer, b.HomeNumberFormat, "", b.Peer.Domain)
	_ = FilterBody(resp, b.Peer.Codecs)
}

// Request prepares a request that crosses the border in direction d,
// with ExitRequest or EntryRequest.
func (b Border) Request(req *sip.Message, d Direction) error {
	if d == Exit {
		return b.ExitRequest(req)
	}
	return b.EntryRequest(req)
}

// Response prepares a response that crosses the border in direction d,
// with ExitResponse or EntryResponse.
func (b Border) Response(resp *sip.Message, d Direction) {
	if d == Exit {
		b.ExitResponse(resp)
		return
	}
	b.EntryResponse(resp)
}

func (b Border) screenRequest(req *sip.Message) error {
	if err := ScreenMethod(req, b.Peer); err != nil {
		return err
	}
	return ApplyOptionTags(req, b.Peer)
}

// filterRequestBody filters the SDP of a request. An offer with no
// codec left is answered with 488 (RFC 3261 21.4.26), and so is an SDP
// body that this project cannot read. An ACK carries an answer to an
// offer that was filtered already, and cannot be refused.
func (b Border) filterRequestBody(req *sip.Message) error {
	if req.Method == sip.ACK {
		return nil
	}
	if err := FilterBody(req, b.Peer.Codecs); err != nil {
		return reject(488, err)
	}
	return nil
}
