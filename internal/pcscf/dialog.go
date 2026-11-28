package pcscf

import (
	"net/netip"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Dialog is what the P-CSCF keeps of an INVITE dialog of a UE it serves,
// from the first response that creates the dialog until the dialog ends.
// TS 24.229 has the P-CSCF save it "such that the P-CSCF is able to
// release the session if needed": the Contact, CSeq and Record-Route of
// the INVITE (5.2.6.3.3 item 8, 5.2.6.4.3 items 2 and 3) and the dialog
// ID, Contact, From, To and Record-Route of its response (5.2.6.3.4
// items 2, 3 and 6, 5.2.6.4.4 items 4 and 5). With them the P-CSCF can
// send a BYE in the name of either party (5.2.8.1.2).
//
// It is plain data in the Store, so that any P-CSCF instance can handle
// the next request of the dialog.
//
// SIMPLIFIED: TS 24.229 5.2.8.1.4 and 5.2.8.3. A dialog that ends
// without a BYE through the P-CSCF stays in the Store: the dialogs of a
// user are not discarded when the registration ends, which would need
// an index of the dialogs by user, and the P-CSCF asks for no session
// timer (RFC 4028) to notice a session that hangs. Such a dialog, once
// answered, also stays in the answered sessions that the P-CSCF counts
// (TS 32.409 4.1.2.5) until the instance that counted it starts again.
//
// SIMPLIFIED: TS 29.214 A.3. The early dialogs of a forked INVITE are
// not told apart: the dialog of the calling UE follows the To tag of
// the response seen last, and its relay context and policy session take
// the SDP answer of that response. A 2xx without SDP leaves them with
// that answer, also when it came in another early dialog. The S-CSCF of
// this project does not fork.
type Dialog struct {
	Key DialogKey
	// Originating is true when the served UE is the caller, and false
	// when it is the callee (TS 24.229 5.2.6.3, 5.2.6.4).
	Originating bool
	// Confirmed is false for an early dialog, and true after the 2xx to
	// the INVITE (RFC 3261 12.1).
	Confirmed bool
	// Acknowledged is true once the ACK for the 2xx has passed the
	// P-CSCF (RFC 3261 13.2.2.4).
	Acknowledged bool
	// ReleasePending is true when the P-CSCF is to release the dialog
	// but the 2xx has had no ACK yet. The BYEs then wait for the ACK
	// (RFC 3261 15), and whichever P-CSCF instance the ACK passes sends
	// them.
	ReleasePending bool
	// Released is true once the P-CSCF has started to release the
	// dialog itself (TS 24.229 5.2.8.1.1, 5.2.8.1.2) and has freed what
	// it held. A request within the dialog then gets 481 (5.2.8.1.3). An
	// early dialog that another instance released tells the instance that
	// forwards its INVITE to end the INVITE.
	Released bool
	// ReleaseCause is why the P-CSCF releases the dialog, from the moment
	// it decides to (ReleasePending or Released). The instance that ends
	// the INVITE of an early dialog answers it with the 500 of this
	// cause, whichever instance released the dialog.
	ReleaseCause ReleaseCause
	Caller       Party
	Callee       Party
	// Relay is the context of the dialog in the media relay, or nil when
	// its media does not go through the relay.
	Relay *RelayContext
	// PolicySession is the policy session in which the PCRF authorised
	// the media of the dialog (TS 29.214 4.4.1), or empty.
	PolicySession policy.SessionID
	// KeepUntil is when the Store forgets the dialog, or zero to keep it
	// until the P-CSCF takes it (TakeDialog). A dialog that the P-CSCF
	// releases gets one (releasedKeep), so that it goes also when the
	// instance that is to end it stops first.
	KeepUntil time.Time
	// ICID is the icid-value of the INVITE: the one that the P-CSCF
	// wrote in it for the calling UE (TS 24.229 5.2.6.3.3 step 7), or
	// the one that it stored from it for the called UE (5.2.6.4.3 item
	// 9). The later requests of the dialog and their responses carry it
	// (chargeRequest, chargeResponse). Empty when the INVITE had none.
	ICID string
}

// ReleaseCause is why the P-CSCF releases a dialog itself.
type ReleaseCause string

const (
	// ResourcesLost is a release for resources that the network has
	// taken away from the session, or would not give it (TS 24.229
	// 5.2.8.1).
	ResourcesLost ReleaseCause = "resources-lost"
	// SDPNotRelayed is a release for an SDP that the P-CSCF could not
	// point at the media relay: an answer that the relay did not take, the
	// SDP of a request within the dialog (notRelayed), or an offer that got
	// no 2xx and that the relay did not go back from (goBack).
	SDPNotRelayed ReleaseCause = "sdp-not-relayed"
)

// err is the cause as setup and proxyInvite tell it: errResources or
// errRelay, or nil for a cause that the P-CSCF does not know, such as
// none, which gets the plain 500.
func (c ReleaseCause) err() error {
	switch c {
	case ResourcesLost:
		return errResources
	case SDPNotRelayed:
		return errRelay
	}
	return nil
}

// DialogKey names a dialog at the P-CSCF: the Call-ID and the tag of
// the served UE. A request from the UE has that tag in From, and a
// request to the UE has it in To, so every request of the dialog leads
// to its Dialog with one keyed read. The tag of the other
// party is left out of the key: the calling UE's dialog has a key before
// the first response names that tag. So every dialog of a forked INVITE
// has the key of the calling UE's Dialog (ofAnotherDialog).
//
// When this P-CSCF serves both the caller and the callee, the dialog
// passes it twice and has two Dialogs, one per UE.
type DialogKey struct {
	CallID string
	UETag  string
}

// Party is one end of a dialog, as the P-CSCF needs it to send a
// request to that end, or in its name (TS 24.229 5.2.8.1.2).
type Party struct {
	// NameAddr is the From value of the caller, or the To value of the
	// callee, with its tag.
	NameAddr string
	// Target is the URI of the party's Contact, where requests to it go
	// after the route set (RFC 3261 12.2.1.1). A target refresh request
	// replaces it (TS 24.229 5.2.6.3.5 item 5, 5.2.6.4.5 item 5).
	Target string
	// Route is the route set from the P-CSCF to the party, the nearest
	// hop first. It is empty towards the served UE.
	Route []string
	// CSeq is the sequence number of the last request the party sent in
	// the dialog, or 0 when it has sent none.
	CSeq uint32
}

// RelayContext is a context of a media relay as a Dialog keeps it: the
// relay that holds it, by its MediaRelay name, its ID in that relay,
// and the address of each termination as the SDP shows it. The changes
// and the release of the context go to that relay, from whichever
// P-CSCF instance handles the request of the dialog.
type RelayContext struct {
	Relay string
	ID    string
	// Access is where the UE sends its media, and Core where the network
	// does.
	Access netip.AddrPort
	Core   netip.AddrPort
	// AccessRemote and CoreRemote are where the access and the core
	// termination send for the last offer and answer that are done: where
	// the UE, and the party on the network side, take their media. A
	// request whose offer gets no 2xx leaves the session as it was before
	// the request (RFC 3261 14.1, RFC 3311 5.3), and the termination that
	// the offer moved goes back there (goBack). The relay cannot be asked,
	// and the next request of the dialog may pass another P-CSCF
	// instance, so the Dialog keeps them. Not valid in a Dialog of a
	// P-CSCF that kept neither.
	AccessRemote netip.AddrPort
	CoreRemote   netip.AddrPort
}

// agreed is where the termination side sends for the last offer and
// answer that are done.
func (rc RelayContext) agreed(side agw.TerminationID) netip.AddrPort {
	if side == agw.Core {
		return rc.CoreRemote
	}
	return rc.AccessRemote
}

// agreeing is rc with remote as where the termination side sends for the
// last offer and answer. It is a copy, since the Dialogs that the Store
// gives out may share one RelayContext. An address that is not valid, of
// an SDP whose audio the relay was not told of, leaves side as it was.
func (rc RelayContext) agreeing(side agw.TerminationID, remote netip.AddrPort) *RelayContext {
	switch {
	case !remote.IsValid():
	case side == agw.Core:
		rc.CoreRemote = remote
	default:
		rc.AccessRemote = remote
	}
	return &rc
}

// facingSender is the termination of a relay context that faces where a
// message comes from: the core termination for a message towards the
// UE, and the access termination for one away from it.
func facingSender(toUE bool) agw.TerminationID {
	if toUE {
		return agw.Core
	}
	return agw.Access
}

// relatching is the latching that goes with a Modify of the termination
// side which tells it to send to another address, to, than the one it
// was told before, from: the Latching Requirement of Configure AGW
// Connection Point (TS 23.334 8.4, Table 8.4.1), or none.
//
// Only the access termination latches (addContext), and for it the
// address of the SDP is not the one that works: a UE behind a NAT that
// its SDP does not show takes its media where the NAT sends it from,
// and a termination that has latched goes on sending there whatever a
// later Remote descriptor says (H.248.37 (06/2008) 6.6.2, 6.6.4). So
// when the UE moves its media, the P-CSCF asks the termination to latch
// onto the new source of it. The gateway follows a move only when its
// controller asks for each move it expects (H.248.37 1.1.1), and a move
// that the UE announces in an offer or an answer is one such.
//
// It asks for NAPTRelatch, not NAPTLatch: the packets still on their way
// from the port the UE had would otherwise pin the termination there,
// since NAPTLatch takes the first packet of any source (6.6.2). Until a
// packet comes from the new source the termination sends where it
// latched, which is where the UE listened until it moved.
//
// The core termination does not latch, and sends where the SDP says, so
// its Modify carries no latching.
func relatching(side agw.TerminationID, from, to netip.AddrPort) agw.Latch {
	if side != agw.Access || !from.IsValid() || !to.IsValid() || from == to {
		return ""
	}
	return agw.NAPTRelatch
}

// ue and remote are the party the P-CSCF serves and the other one.
func (d *Dialog) ue() *Party {
	if d.Originating {
		return &d.Caller
	}
	return &d.Callee
}

func (d *Dialog) remote() *Party {
	if d.Originating {
		return &d.Callee
	}
	return &d.Caller
}

// subsequentKey is the key of the dialog that req, a request within a
// dialog, belongs to. toUE tells which way req passes the P-CSCF.
func subsequentKey(req *sip.Message, toUE bool) DialogKey {
	header := "From"
	if toUE {
		header = "To"
	}
	return DialogKey{CallID: req.Get("Call-ID"), UETag: tagOf(req.Get(header))}
}

func tagOf(nameAddr string) string {
	na, err := sip.ParseNameAddr(nameAddr)
	if err != nil {
		return ""
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// parties are the party that sent a request within the dialog and the
// one it goes to. toUE tells which way the request passes the P-CSCF.
func (d *Dialog) parties(toUE bool) (sender, receiver *Party) {
	if toUE {
		return d.remote(), d.ue()
	}
	return d.ue(), d.remote()
}

// ofAnotherDialog reports whether req, a request within a dialog whose
// key leads to d, belongs to another dialog of the same forked INVITE.
// The key has the tag of the served UE alone, so every dialog of the
// INVITE leads to d. Once a 2xx has confirmed d, a request of d carries
// the tag of its other party, and one with another tag belongs to
// another dialog. Before that 2xx the early dialogs are not told apart
// (Dialog). toUE tells which way req passes the P-CSCF.
func (d *Dialog) ofAnotherDialog(req *sip.Message, toUE bool) bool {
	header := "To"
	if toUE {
		header = "From"
	}
	return d.Confirmed && tagOf(req.Get(header)) != tagOf(d.remote().NameAddr)
}

// noteRequest keeps the CSeq of the party that sent req, a request
// within the dialog. TS 24.229 names the CSeq only for target refresh
// requests (5.2.6.3.5 item 5, 5.2.6.4.5 item 5); every request counts
// here, because the BYE of 5.2.8.1.2 needs a number above all of them
// (RFC 3261 12.2.1.1).
func (d *Dialog) noteRequest(req *sip.Message, toUE bool) {
	sender, _ := d.parties(toUE)
	if cseq, err := sip.ParseCSeq(req.Get("CSeq")); err == nil && cseq.Number > sender.CSeq {
		sender.CSeq = cseq.Number
	}
}

// noteTargetRefresh keeps the Contacts of a target refresh request and
// of resp, a 1xx or 2xx response to it: each replaces the target of the
// party that sent it (TS 24.229 5.2.6.3.5 item 5, 5.2.6.3.6 item 2,
// 5.2.6.4.5 item 5, 5.2.6.4.6 item 3). The Contact of the request counts
// only once such a response has come (5.2.6.3.5 NOTE, 5.2.6.4.5 NOTE 2).
func (d *Dialog) noteTargetRefresh(req, resp *sip.Message, toUE bool) {
	sender, receiver := d.parties(toUE)
	if contact := contactOf(req); contact != "" {
		sender.Target = contact
	}
	if contact := contactOf(resp); contact != "" {
		receiver.Target = contact
	}
}

// routeSets splits the Record-Route of a response to the INVITE at the
// P-CSCF's own value. Each proxy on the way from the caller to the
// callee put its URI on top (RFC 3261 16.6 step 4), so the values below
// the P-CSCF's lead to the caller, the nearest first, and the values
// above it lead to the callee, the nearest last. upstream is how many
// values the INVITE carried when it reached the P-CSCF; counting them
// finds the P-CSCF's own value also when the dialog passes it twice.
func routeSets(recordRoute []string, upstream int) (toCaller, toCallee []string, ok bool) {
	own := len(recordRoute) - 1 - upstream
	if own < 0 {
		return nil, nil, false
	}
	for i := own - 1; i >= 0; i-- {
		toCallee = append(toCallee, recordRoute[i])
	}
	return append(toCaller, recordRoute[own+1:]...), toCallee, true
}
