package sip

import (
	"errors"
	"fmt"
	"slices"
)

// This file holds the dialog of RFC 3261 12: the state that two user
// agents keep between the INVITE and the BYE, or for as long as a
// subscription lasts (RFC 6665 4.4.1), so that the requests in between
// reach the peer through the right proxies, in order. Like the
// transaction state machines, a Dialog never touches the network or a
// clock. The UA in ua.go keeps one per call, a Subscriber one per
// subscription, and they send what it builds.

// DialogState is the state of a dialog (RFC 3261 12).
type DialogState int

const (
	// DialogEarly is a dialog that a 101-199 response with a To tag
	// created (RFC 3261 12.1).
	DialogEarly DialogState = iota
	// DialogConfirmed is a dialog that a 2xx created or confirmed (RFC
	// 3261 12.1, 13.2.2.4, 13.3.1.4).
	DialogConfirmed
	// DialogTerminated is a dialog that a non-2xx final response ended
	// while it was early (RFC 3261 12.3), or that a BYE ended (15).
	DialogTerminated
)

func (s DialogState) String() string {
	switch s {
	case DialogEarly:
		return "Early"
	case DialogConfirmed:
		return "Confirmed"
	case DialogTerminated:
		return "Terminated"
	}
	return "unknown"
}

// DialogID identifies a dialog at one UA (RFC 3261 12). The local tag
// of one UA is the remote tag of its peer.
type DialogID struct {
	CallID    string
	LocalTag  string
	RemoteTag string
}

// ReceivedDialogID is the ID of the dialog that a request received by
// this UA belongs to: the To tag is the local tag and the From tag the
// remote tag (RFC 3261 12).
func ReceivedDialogID(req *Message) DialogID {
	return DialogID{CallID: req.Get("Call-ID"), LocalTag: tagOf(req.Get("To")), RemoteTag: tagOf(req.Get("From"))}
}

// Dialog is the dialog state of RFC 3261 12.
//
// SIMPLIFIED: RFC 3261 12.1.1 and 12.1.2. The "secure" flag is not
// kept. It is true only for a dialog set up over TLS with a SIPS URI,
// and this project sends SIP in plain text.
type Dialog struct {
	ID    DialogID
	State DialogState
	// LocalSeq is the CSeq number of the last request this UA sent in
	// the dialog, and RemoteSeq that of the last request it accepted
	// from the peer. 0 means empty (RFC 3261 12.1.1, 12.1.2): a UAS has
	// sent nothing yet, a UAC has received nothing.
	LocalSeq  uint32
	RemoteSeq uint32
	// LocalURI and RemoteURI are the URIs of From and To that this UA
	// writes in its requests (RFC 3261 12.2.1.1).
	LocalURI  string
	RemoteURI string
	// RemoteTarget is the Contact URI of the peer, where requests go
	// after the route set.
	RemoteTarget string
	// RouteSet is the URIs of the proxies that recorded the route, the
	// one nearest to this UA first.
	RouteSet []string
}

// NewUACDialog builds the dialog that resp, a 101-199 response with a To
// tag or a 2xx, establishes for the INVITE that the UAC sent (RFC 3261
// 12.1.2). A 101-199 response gives an early dialog and a 2xx a
// confirmed one (13.2.2.1, 13.2.2.4).
func NewUACDialog(invite, resp *Message) (Dialog, error) {
	state, err := establishedState(resp)
	if err != nil {
		return Dialog{}, err
	}
	from, to, err := fromAndTo(invite.Get("From"), resp.Get("To"))
	if err != nil {
		return Dialog{}, err
	}
	target, err := contactURI(resp)
	if err != nil {
		return Dialog{}, err
	}
	cseq, err := ParseCSeq(invite.Get("CSeq"))
	if err != nil {
		return Dialog{}, err
	}
	routes, err := recordRouteURIs(resp)
	if err != nil {
		return Dialog{}, err
	}
	// The response lists the proxies from the UAS to the UAC; the UAC's
	// route set starts with the proxy nearest to it.
	slices.Reverse(routes)
	return Dialog{
		ID:           DialogID{CallID: invite.Get("Call-ID"), LocalTag: tagOf(invite.Get("From")), RemoteTag: tagOf(resp.Get("To"))},
		State:        state,
		LocalSeq:     cseq.Number,
		LocalURI:     from.URI,
		RemoteURI:    to.URI,
		RemoteTarget: target,
		RouteSet:     routes,
	}, nil
}

// NewUASDialog builds the dialog that resp, a 101-199 response with a To
// tag or a 2xx that the UAS sends, establishes for invite (RFC 3261
// 12.1.1). A 101-199 response gives an early dialog and a 2xx a
// confirmed one (13.3.1.1, 13.3.1.4). A notifier passes a SUBSCRIBE as
// invite and its 200 as resp (RFC 6665 4.2.1.1).
func NewUASDialog(invite, resp *Message) (Dialog, error) {
	state, err := establishedState(resp)
	if err != nil {
		return Dialog{}, err
	}
	from, to, err := fromAndTo(invite.Get("From"), resp.Get("To"))
	if err != nil {
		return Dialog{}, err
	}
	target, err := contactURI(invite)
	if err != nil {
		return Dialog{}, err
	}
	cseq, err := ParseCSeq(invite.Get("CSeq"))
	if err != nil {
		return Dialog{}, err
	}
	// The request lists the proxies from the UAC to the UAS, so the
	// first one is already the one nearest to the UAS.
	routes, err := recordRouteURIs(invite)
	if err != nil {
		return Dialog{}, err
	}
	return Dialog{
		ID:           DialogID{CallID: invite.Get("Call-ID"), LocalTag: tagOf(resp.Get("To")), RemoteTag: tagOf(invite.Get("From"))},
		State:        state,
		RemoteSeq:    cseq.Number,
		LocalURI:     to.URI,
		RemoteURI:    from.URI,
		RemoteTarget: target,
		RouteSet:     routes,
	}, nil
}

// NewSubscriberDialog is the dialog of an initial SUBSCRIBE as the
// subscriber knows it before any NOTIFY: its own half, made of the
// Call-ID, the From tag, the URIs of From and To, and the CSeq number of
// the SUBSCRIBE. The remote tag stays empty until ReceiveFirstNotify
// completes it. Like every Dialog it is plain data, so that a node can
// keep a subscription it made in a store and complete it on
// whichever node the NOTIFY reaches.
func NewSubscriberDialog(subscribe *Message) (Dialog, error) {
	from, to, err := fromAndTo(subscribe.Get("From"), subscribe.Get("To"))
	if err != nil {
		return Dialog{}, err
	}
	cseq, err := ParseCSeq(subscribe.Get("CSeq"))
	if err != nil {
		return Dialog{}, err
	}
	return Dialog{
		ID:       DialogID{CallID: subscribe.Get("Call-ID"), LocalTag: tagOf(subscribe.Get("From"))},
		LocalSeq: cseq.Number,
		LocalURI: from.URI,
		// The To of the SUBSCRIBE, which every request of the dialog
		// repeats with the notifier's tag (RFC 3261 12.2.1.1).
		RemoteURI: to.URI,
	}, nil
}

// ReceiveFirstNotify turns the half dialog of NewSubscriberDialog into
// the dialog that notify, the first NOTIFY of the subscription,
// establishes (RFC 6665 4.4.1). Unlike the dialog of an INVITE, it is
// the request from the peer that creates it, not the 2xx: the remote
// tag, the remote target and the route set come from the NOTIFY, whose
// Record-Route lists the proxies nearest to the subscriber first, as at
// any UAS (RFC 3261 12.1.1). On an error nothing changes.
func (d *Dialog) ReceiveFirstNotify(notify *Message) error {
	target, err := contactURI(notify)
	if err != nil {
		return err
	}
	cseq, err := ParseCSeq(notify.Get("CSeq"))
	if err != nil {
		return err
	}
	routes, err := recordRouteURIs(notify)
	if err != nil {
		return err
	}
	d.ID.RemoteTag = tagOf(notify.Get("From"))
	d.State = DialogConfirmed
	d.RemoteSeq = cseq.Number
	d.RemoteTarget = target
	d.RouteSet = routes
	return nil
}

func establishedState(resp *Message) (DialogState, error) {
	switch code := resp.StatusCode; {
	case code > 100 && code < 200:
		if tagOf(resp.Get("To")) == "" {
			return 0, fmt.Errorf("sip: %d without a To tag creates no dialog", code)
		}
		return DialogEarly, nil
	case code >= 200 && code < 300:
		// A 2xx without a To tag still creates a dialog, whose remote
		// tag is null (RFC 3261 12.1.2).
		return DialogConfirmed, nil
	default:
		return 0, fmt.Errorf("sip: %d creates no dialog", code)
	}
}

func fromAndTo(from, to string) (NameAddr, NameAddr, error) {
	f, err := ParseNameAddr(from)
	if err != nil {
		return NameAddr{}, NameAddr{}, fmt.Errorf("sip: From: %w", err)
	}
	t, err := ParseNameAddr(to)
	if err != nil {
		return NameAddr{}, NameAddr{}, fmt.Errorf("sip: To: %w", err)
	}
	return f, t, nil
}

// contactURI is the remote target that m offers. Both the request and
// the response that create a dialog MUST carry a Contact (RFC 3261
// 12.1.1, 12.1.2).
func contactURI(m *Message) (string, error) {
	na, err := ParseNameAddr(m.Get("Contact"))
	if err != nil {
		return "", fmt.Errorf("sip: Contact: %w", err)
	}
	return na.URI, nil
}

// recordRouteURIs is the URIs of every Record-Route value of m, in the
// order of the message, with their URI parameters kept (RFC 3261
// 12.1.1, 12.1.2).
func recordRouteURIs(m *Message) ([]string, error) {
	var uris []string
	for _, v := range m.GetAll("Record-Route") {
		na, err := ParseNameAddr(v)
		if err != nil {
			return nil, fmt.Errorf("sip: Record-Route: %w", err)
		}
		uris = append(uris, na.URI)
	}
	return uris, nil
}

// tagOf returns the tag parameter of a From or To value, or "" when it
// has none or the value does not parse.
func tagOf(value string) string {
	na, err := ParseNameAddr(value)
	if err != nil {
		return ""
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// confirmByUAC is RFC 3261 13.2.2.4: a 2xx for an early dialog makes it
// confirmed, and the route set is recomputed from the Record-Route of
// the 2xx. Nothing else is: requests sent within the early dialog have
// already moved the sequence numbers on.
func (d *Dialog) confirmByUAC(invite, ok *Message) error {
	confirmed, err := NewUACDialog(invite, ok)
	if err != nil {
		return err
	}
	d.State = DialogConfirmed
	d.RouteSet = confirmed.RouteSet
	return nil
}

// refreshTarget is RFC 3261 12.2.1.2 for resp, a 2xx to a target refresh
// request that this UA sent: its Contact, if it has one, is the remote
// target from now on.
func (d *Dialog) refreshTarget(resp *Message) {
	if target, err := contactURI(resp); err == nil {
		d.RemoteTarget = target
	}
}

// NewRequest builds a request within the dialog (RFC 3261 12.2.1.1),
// taking the next local sequence number for it. The stack adds Via; the
// caller adds whatever else the method needs, such as Contact in a
// target refresh request.
func (d *Dialog) NewRequest(method Method) *Message {
	// When the local sequence number is empty, this makes it 1, which
	// RFC 3261 8.1.1.5 allows.
	d.LocalSeq++
	return d.request(method, d.LocalSeq)
}

// newACK builds the ACK for a 2xx to the INVITE whose CSeq number is
// inviteSeq: a request within the dialog, except that it carries the
// INVITE's CSeq number and does not use up a local sequence number
// (RFC 3261 13.2.2.4).
func (d *Dialog) newACK(inviteSeq uint32) *Message { return d.request(ACK, inviteSeq) }

// request is RFC 3261 12.2.1.1 with the CSeq number given.
//
// SIMPLIFIED: RFC 3261 12.2.1.1. When the first URI of the route set
// lacks lr, it names a strict router: the request then goes with that
// URI as its Request-URI, and the remote target last in Route. No node
// in this project is a strict router, so the request is built for loose
// routing in every case, and NextHop refuses it if a strict router
// shows up.
func (d *Dialog) request(method Method, seq uint32) *Message {
	m := &Message{Method: method, RequestURI: d.RemoteTarget}
	for _, uri := range d.RouteSet {
		m.Add("Route", NameAddr{URI: uri}.String())
	}
	m.Add("Max-Forwards", "70")
	m.Add("From", withTag(d.LocalURI, d.ID.LocalTag))
	m.Add("To", withTag(d.RemoteURI, d.ID.RemoteTag))
	m.Add("Call-ID", d.ID.CallID)
	m.Add("CSeq", CSeq{Number: seq, Method: method}.String())
	return m
}

// withTag writes uri as a From or To value, with the tag parameter
// unless the tag is null (RFC 3261 12.2.1.1).
func withTag(uri, tag string) string {
	na := NameAddr{URI: uri}
	if tag != "" {
		na.Params.Set("tag", tag)
	}
	return na.String()
}

// ErrOutOfOrder reports a request within a dialog whose CSeq number is
// lower than the last one accepted from the peer. The UAS answers it
// with 500 (Server Internal Error) (RFC 3261 12.2.2).
var ErrOutOfOrder = errors.New("sip: request out of order within the dialog")

// ReceiveRequest is RFC 3261 12.2.2 for req, a request within the
// dialog that the UAS is about to accept: the CSeq number must not go
// down, and it becomes the remote sequence number. A target refresh
// request with a Contact also replaces the remote target. On an error
// nothing changes, since a rejected request changes no state.
//
// The ACK for a 2xx is not a request that goes through here: it carries
// the INVITE's CSeq number, which the requests after the INVITE have
// already passed.
func (d *Dialog) ReceiveRequest(req *Message) error {
	cseq, err := ParseCSeq(req.Get("CSeq"))
	if err != nil {
		return err
	}
	if d.RemoteSeq != 0 && cseq.Number < d.RemoteSeq {
		return ErrOutOfOrder
	}
	d.RemoteSeq = cseq.Number
	// The target refresh requests: re-INVITE (RFC 3261 12.2), UPDATE
	// (RFC 3311 5.1), and SUBSCRIBE and NOTIFY (RFC 6665 3.1, 3.2).
	if req.Method == INVITE || req.Method == UPDATE || req.Method == SUBSCRIBE || req.Method == NOTIFY {
		if target, err := contactURI(req); err == nil {
			d.RemoteTarget = target
		}
	}
	return nil
}
