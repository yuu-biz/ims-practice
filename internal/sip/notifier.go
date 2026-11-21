package sip

import (
	"errors"
	"strconv"
	"time"
)

// This file holds the notifier's side of a subscription (RFC 6665 4.2).
// A notifier answers a SUBSCRIBE with 200, which creates the dialog, and
// then sends a NOTIFY at once and again whenever the state changes. The
// S-CSCF is the notifier of the reg event package (TS 24.229 5.4.2.1)
// on a Stack of its own, without a UA.

// NotifierSubscription is one subscription at the notifier: its dialog
// and how long it lasts. Like Dialog, it is plain data that never
// touches the network or a clock, so that a node can keep it in a store.
// The node sends the messages it builds.
type NotifierSubscription struct {
	Dialog Dialog
	// Event is the Event value of the SUBSCRIBE. Every NOTIFY repeats it
	// (RFC 6665 3.2.1).
	Event string
	// Contact is the notifier's URI, which the 200 and every NOTIFY
	// carry: both SUBSCRIBE and NOTIFY are target refresh requests
	// (RFC 6665 3.1, 3.2).
	Contact string
	Expires time.Time
	// State is SubscriptionActive, or SubscriptionTerminated with
	// Reason once the subscription has ended. This notifier never keeps
	// a subscription pending: it decides at once whether to accept it.
	State  SubState
	Reason string
}

// SubscribeExpires is the duration a SUBSCRIBE asks for: its Expires,
// or else the default of the event package (RFC 6665 3.1.1).
func SubscribeExpires(req *Message, packageDefault time.Duration) (time.Duration, error) {
	v := req.Get("Expires")
	if v == "" {
		return packageDefault, nil
	}
	n, err := ParseDeltaSeconds(v)
	return time.Duration(n) * time.Second, err
}

// AcceptSubscription is RFC 6665 4.2.1.1 for an initial SUBSCRIBE that
// the notifier accepts. It builds the 200, which carries Expires,
// Contact and a copy of the Record-Route of the request (RFC 3261
// 12.1.1), and the subscription with the dialog that the 200 creates.
// expires must not be longer than the SUBSCRIBE asked for. The caller
// sends the 200 and then a NOTIFY at once (4.2.1.2). An expires of 0 is
// a fetch (4.4.3): the subscription ends with that NOTIFY.
func AcceptSubscription(subscribe *Message, expires time.Duration, contact string, now time.Time) (*Message, NotifierSubscription, error) {
	ok := NewResponse(subscribe, 200, NewTag())
	for _, rr := range subscribe.GetAll("Record-Route") {
		ok.Add("Record-Route", rr)
	}
	ok.Add("Contact", NameAddr{URI: contact}.String())
	ok.Add("Expires", strconv.Itoa(int(expires/time.Second)))
	d, err := NewUASDialog(subscribe, ok)
	if err != nil {
		return nil, NotifierSubscription{}, err
	}
	sub := NotifierSubscription{Dialog: d, Event: subscribe.Get("Event"), Contact: contact, State: SubscriptionActive}
	sub.renew(expires, now)
	return ok, sub, nil
}

// ErrBadEvent is what ReceiveSubscribe returns for a SUBSCRIBE whose
// Event differs from the subscription's. The notifier answers it with
// 489 (Bad Event) (RFC 6665 4.2.1.1): a second subscription in the same
// dialog is not supported here.
var ErrBadEvent = errors.New("sip: SUBSCRIBE for another event in the dialog")

// ReceiveSubscribe is RFC 6665 4.2.1.4 for a SUBSCRIBE within the
// subscription's dialog: a refresh, or an unsubscribe when expires is 0.
// It returns the 200, after which the caller sends a NOTIFY at once
// (4.2.1.2). A request out of order gives ErrOutOfOrder, which the
// caller answers with 500 (RFC 3261 12.2.2); nothing changes then.
func (n *NotifierSubscription) ReceiveSubscribe(req *Message, expires time.Duration, now time.Time) (*Message, error) {
	if !sameEvent(req.Get("Event"), n.Event) {
		return nil, ErrBadEvent
	}
	if err := n.Dialog.ReceiveRequest(req); err != nil {
		return nil, err
	}
	ok := NewResponse(req, 200, "")
	ok.Add("Contact", NameAddr{URI: n.Contact}.String())
	ok.Add("Expires", strconv.Itoa(int(expires/time.Second)))
	n.renew(expires, now)
	return ok, nil
}

func (n *NotifierSubscription) renew(expires time.Duration, now time.Time) {
	n.Expires = now.Add(expires)
	if expires == 0 {
		// RFC 6665 4.2.1.4: an unsubscribe ends like an expiry.
		n.Terminate(ReasonTimeout)
	}
}

// Terminate ends the subscription. The next NOTIFY, which the notifier
// SHOULD send, says terminated with reason (RFC 6665 4.2.1.4, 4.2.2).
func (n *NotifierSubscription) Terminate(reason string) {
	n.State, n.Reason = SubscriptionTerminated, reason
}

// NewNotify builds the next NOTIFY within the dialog (RFC 6665 4.2.2):
// the Event of the SUBSCRIBE, a Subscription-State with the time left
// while the subscription is active, the notifier's Contact, and body.
// It takes the next local CSeq number of the dialog.
//
// A subscription that is past its expiry by now has ended, even when
// the notifier has not yet seen to it: its NOTIFY is the last one, with
// reason timeout (RFC 6665 4.2.1.4), never one that says active.
func (n *NotifierSubscription) NewNotify(body Body, now time.Time) *Message {
	if n.State != SubscriptionTerminated && !now.Before(n.Expires) {
		n.Terminate(ReasonTimeout)
	}
	req := n.Dialog.NewRequest(NOTIFY)
	req.Add("Contact", NameAddr{URI: n.Contact}.String())
	req.Add("Event", n.Event)
	state := SubscriptionState{State: n.State, Reason: n.Reason}
	if n.State != SubscriptionTerminated {
		state.Expires = max(n.Expires.Sub(now).Round(time.Second), 0)
	}
	req.Add("Subscription-State", state.String())
	body.addTo(req)
	return req
}

// EndsSubscription reports whether a final response to a NOTIFY, or to a
// SUBSCRIBE that refreshes a subscription, ends the subscription (RFC
// 6665 4.1.2.2, 4.2.2). Any other failure leaves it to expire.
func EndsSubscription(statusCode int) bool {
	switch statusCode {
	case 404, 405, 410, 416, 489, 501, 604:
		return true
	}
	return statusCode >= 480 && statusCode <= 485
}
