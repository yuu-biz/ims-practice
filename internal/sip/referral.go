package sip

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file holds the REFER method within a dialog (RFC 3515) and its
// implicit subscription to the refer event: the referrer sends the
// REFER and follows the NOTIFYs that report how the referenced request
// went (Referral), and the UA that accepts the REFER sends those
// NOTIFYs (ReferSubscription). A participant of a conference asks the
// focus this way to invite another user (TS 24.147 5.3.1.5.3,
// 5.3.2.5.2), within the dialog of the conference session (GSMA IR.92
// 2.3.3).
//
// TS 24.147 needs the implicit subscription: the participant ends its
// call with the invited user once a NOTIFY reports 200 (5.3.1.3.3 step
// 3). RFC 4488 lets a referrer ask for no subscription (Refer-Sub:
// false); TS 24.147 does not use it, and neither does this package.

// REFER asks its receiver to send a request to the URI of its Refer-To
// (RFC 3515 2).
const REFER Method = "REFER"

// EventRefer is the event package of the subscription that a REFER
// creates (RFC 3515 3.1).
const EventRefer = "refer"

// SipfragType is the body type of every NOTIFY of the refer event: a
// fragment of a SIP message, here the status line of the response to
// the referenced request (RFC 3515 2.4.5, RFC 3420 2).
const SipfragType = "message/sipfrag;version=2.0"

// ReferStatus is one NOTIFY of a Referral, as the referrer reads it.
type ReferStatus struct {
	// StatusCode and Reason are those of the status line in the body:
	// how the referenced request went so far. 100 is pending, and a
	// final status ends the subscription (RFC 3515 2.4.5, 2.4.7).
	StatusCode int
	Reason     string
	State      SubscriptionState
	Request    *Message
}

// Referral is a REFER that this UA sent within a dialog, and the
// implicit subscription that the REFER created when the peer accepted
// it (RFC 3515 2.4.4).
type Referral struct {
	// Request is the REFER as sent, and Response its final response. A
	// final response other than 2xx created no subscription, and
	// Statuses is closed at once then.
	Request  *Message
	Response *Message

	d *CallDialog
	// id is the CSeq number of the REFER, which the NOTIFYs give as the
	// id of their Event (RFC 3515 2.4.6).
	id       string
	statuses chan ReferStatus
	// Guarded by the mu of the call. stopTimerN stops Timer N, which
	// runs from the REFER to the first NOTIFY; stopExpiry stops the
	// timer of the duration that the last NOTIFY gave.
	ended      bool
	err        error
	stopTimerN func() bool
	stopExpiry func() bool
}

// Statuses delivers each NOTIFY of the subscription as it comes. It is
// closed when the subscription ends: with a NOTIFY that says
// terminated, when it expires, or when the dialog ends; Err then tells
// which. A status comes once the Referral has taken in its NOTIFY: the
// duration it gave already runs.
func (r *Referral) Statuses() <-chan ReferStatus { return r.statuses }

// Err tells why the subscription ended once Statuses is closed: nil
// after a NOTIFY that said terminated or a REFER that was refused,
// ErrNoNotify when no NOTIFY came at all, ErrSubscriptionExpired when
// the duration of the last NOTIFY ran out (or was 0) before one said
// terminated, and ErrCallEnded when the dialog ended first.
func (r *Referral) Err() error {
	c := r.d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	return r.err
}

// statusBuffer is how many NOTIFYs a Referral holds for a user who does
// not read them at once; the UA answers each NOTIFY without waiting for
// the user (RFC 6665 4.1.3). A reference rarely has more than three:
// 100, perhaps a 180, and the final status.
const statusBuffer = 8

// Refer sends a REFER with to as its Refer-To within the dialog (RFC
// 3515 2.4.1) and returns once the REFER has its final response. The
// REFER carries Contact, which it must (Table 1), and headers, such as
// a Referred-By (RFC 3892). The NOTIFYs of the subscription may come
// before the 2xx (RFC 3515 2.4.4): the Referral takes them from the
// moment the REFER goes out. As for a SUBSCRIBE, Timer N runs from then:
// without a NOTIFY within 64*T1, the subscription ends with ErrNoNotify
// (RFC 6665 4.1.2.4), since the subscription of a REFER is the same as
// one that a SUBSCRIBE creates (RFC 3515 2.4.4).
func (d *CallDialog) Refer(to ReferTo, headers ...HeaderField) (*Referral, error) {
	c := d.c
	c.mu.Lock()
	if err := d.usable(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	req := d.newRequest(REFER, Body{}, append([]HeaderField{to.Header()}, headers...))
	req.Add("Contact", c.contactLocked())
	cseq, _ := ParseCSeq(req.Get("CSeq"))
	r := &Referral{Request: req, d: d, id: strconv.FormatUint(uint64(cseq.Number), 10), statuses: make(chan ReferStatus, statusBuffer)}
	if d.referrals == nil {
		d.referrals = map[string]*Referral{}
	}
	d.referrals[r.id] = r
	if d.firstReferral == "" {
		d.firstReferral = r.id
	}
	r.stopTimerN = c.ua.afterFunc(64*c.stack.cfg.Timers.T1, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		r.end(ErrNoNotify)
	})
	t := d.takeTurn()
	c.mu.Unlock()

	resp, err := d.roundTrip(req, t)
	c.mu.Lock()
	defer c.mu.Unlock()
	r.Response = resp
	switch {
	case err != nil:
		r.end(err)
		return nil, err
	case resp.StatusCode >= 300:
		// RFC 3515 2.4.2: only an accepted REFER creates a subscription.
		// A 202 from a notifier of RFC 3515 is a 2xx like the 200 of RFC
		// 7647 5, which asks the referrer to take it as identical to 200.
		r.end(nil)
	}
	return r, nil
}

// Refer is CallDialog.Refer in the dialog that the call goes on in
// (Dialogs).
func (c *OutgoingCall) Refer(to ReferTo, headers ...HeaderField) (*Referral, error) {
	d, err := c.currentDialog()
	if err != nil {
		return nil, err
	}
	return d.Refer(to, headers...)
}

// end ends the subscription of the Referral. c.mu is held.
func (r *Referral) end(err error) {
	if r.ended {
		return
	}
	r.ended, r.err = true, err
	r.stopTimers()
	delete(r.d.referrals, r.id)
	close(r.statuses)
}

// stopTimers stops Timer N and the expiry timer. c.mu is held.
func (r *Referral) stopTimers() {
	for _, stop := range []func() bool{r.stopTimerN, r.stopExpiry} {
		if stop != nil {
			stop()
		}
	}
	r.stopTimerN, r.stopExpiry = nil, nil
}

// referralOf is the Referral that a NOTIFY of the refer event names by
// the id of its Event; a NOTIFY without one is for the first REFER of
// the dialog (RFC 3515 2.4.6). nil when there is none. c.mu is held.
func (d *CallDialog) referralOf(event string) *Referral {
	_, id, hasID := splitEvent(event)
	if !hasID {
		id = d.firstReferral
	}
	return d.referrals[id]
}

// receiveNotify takes a NOTIFY within the dialog. One of the refer
// event goes to the Referral it reports on. Any other, such as one of a
// subscription that shares the dialog (RFC 6665 4.5.2), or one that a
// B2BUA relays, goes to the user of the call like any other method; a
// NOTIFY that nobody takes gets 481 (RFC 6665 4.1.3).
func (d *CallDialog) receiveNotify(st *ServerTransaction) {
	c := d.c
	req := st.Request
	c.mu.Lock()
	var r *Referral
	if EventType(req.Get("Event")) == EventRefer {
		r = d.referralOf(req.Get("Event"))
	}
	handler := c.onRequest
	if r == nil {
		c.mu.Unlock()
		if handler == nil {
			d.respond(st, 481)
			return
		}
		d.receiveOther(st)
		return
	}
	if !d.inOrder(st) {
		c.mu.Unlock()
		return
	}
	defer c.mu.Unlock()
	state, err := ParseSubscriptionState(req.Get("Subscription-State"))
	if err != nil {
		d.respond(st, 400)
		return
	}
	code, reason, err := ParseSipfragStatus(req.Body)
	if err != nil {
		d.respond(st, 400)
		return
	}
	d.respond(st, 200)
	if r.stopTimerN != nil {
		r.stopTimerN()
		r.stopTimerN = nil
	}
	// The Referral follows the NOTIFY before the user sees it: a user who
	// has read a status finds the timers already running for the
	// duration that the NOTIFY gave. A NOTIFY that ends the subscription
	// is delivered before end closes Statuses.
	ends, endErr := false, error(nil)
	switch {
	case state.State == SubscriptionTerminated:
		ends = true
	case HasExpires(req.Get("Subscription-State")) && state.Expires == 0:
		// RFC 6665 4.1.3: the expires of an active or pending NOTIFY is
		// the time left, and the notifier may shorten it to nothing
		// (4.2.2). The subscription has ended, although no NOTIFY said
		// terminated.
		ends, endErr = true, ErrSubscriptionExpired
	case HasExpires(req.Get("Subscription-State")):
		// RFC 3515 3.4: the notifier tells in each NOTIFY how long the
		// subscription lasts; the referrer does not refresh it. When no
		// NOTIFY says terminated within that time, the subscription has
		// expired. A NOTIFY without expires, from a notifier of RFC
		// 3265, leaves the duration as it was.
		if r.stopExpiry != nil {
			r.stopExpiry()
		}
		r.stopExpiry = c.ua.afterFunc(state.Expires, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			r.end(ErrSubscriptionExpired)
		})
	}
	select {
	case r.statuses <- ReferStatus{StatusCode: code, Reason: reason, State: state, Request: req}:
	default:
		c.ua.log.Warn("NOTIFY of a REFER not delivered: the user does not read them", "call_id", req.Get("Call-ID"))
	}
	if ends {
		r.end(endErr)
	}
}

// endReferrals ends the subscriptions of the REFERs that this UA sent
// in the dialog, which end with it. c.mu is held.
func (d *CallDialog) endReferrals() {
	for _, r := range d.referrals {
		r.end(ErrCallEnded)
	}
}

// ParseSipfragStatus reads the status line that starts the body of a
// NOTIFY of the refer event, such as "SIP/2.0 200 OK" (RFC 3515 2.4.5).
func ParseSipfragStatus(body []byte) (code int, reason string, err error) {
	line, _, _ := strings.Cut(string(body), "\n")
	version, rest, _ := strings.Cut(strings.TrimRight(line, "\r"), " ")
	codeText, reason, _ := strings.Cut(rest, " ")
	code, err = strconv.Atoi(codeText)
	if version != SIPVersion || err != nil || code < 100 || code > 699 {
		return 0, "", fmt.Errorf("sip: sipfrag without a status line: %q", line)
	}
	return code, reason, nil
}

// ReferSubscription is the implicit subscription of a REFER that this
// UA accepted, from the notifier's side: it reports the progress of the
// request that the REFER asked for in NOTIFYs within the dialog of the
// REFER (RFC 3515 2.4.4).
type ReferSubscription struct {
	d *CallDialog
	// event is the Event of every NOTIFY, with the CSeq number of the
	// REFER as its id (RFC 3515 2.4.6).
	event   string
	expires time.Time

	mu    sync.Mutex
	ended bool
	// last is the status that the last NOTIFY reported, which the NOTIFY
	// at the expiry repeats. stopExpiry stops the timer that ends the
	// subscription at expires.
	last       int
	stopExpiry func() bool
}

// ErrNotRefer is what AcceptRefer returns for a request other than a
// REFER.
var ErrNotRefer = errors.New("sip: AcceptRefer is for a REFER")

// ErrReferEnded is what Notify returns once the subscription has ended.
var ErrReferEnded = errors.New("sip: the subscription of the REFER has ended")

// AcceptRefer answers a REFER with 200 (OK) and sends the first NOTIFY
// of its subscription at once, with 100 (Trying): the reference has not
// been followed yet (RFC 3515 2.4.2, 2.4.5). RFC 3515 answered with 202
// (Accepted); RFC 7647 5 forbids that now and asks for 200 wherever RFC
// 3515 says 202, as RFC 6665 8.3.1 does for SUBSCRIBE: a proxy may
// absorb a 202, and it means nothing that a 200 does not. expires is how
// long the subscription lasts; it should be longer than the referenced
// request may take (RFC 3515 3.4). headers go into the 200. The user
// then follows the Refer-To and reports how it went with Notify.
func (r *InDialogRequest) AcceptRefer(expires time.Duration, headers ...HeaderField) (*ReferSubscription, error) {
	if r.Request.Method != REFER {
		return nil, ErrNotRefer
	}
	c := r.d.c
	resp := NewResponse(r.Request, 200, "")
	// Table 1 of RFC 3515: the 2xx to a REFER carries Contact.
	resp.Add("Contact", c.contactValue())
	addHeaders(resp, headers)
	if err := r.respond(resp, true); err != nil {
		return nil, err
	}
	cseq, _ := ParseCSeq(r.Request.Get("CSeq"))
	s := &ReferSubscription{d: r.d, event: EventRefer + ";id=" + strconv.FormatUint(uint64(cseq.Number), 10),
		expires: time.Now().Add(expires), stopExpiry: func() bool { return false }}
	return s, s.Notify(100)
}

// expire ends the subscription when its duration has run out before a
// final status: the notifier removes it and tells the referrer with a
// NOTIFY of terminated;reason=timeout (RFC 6665 4.2.1.4). The body
// repeats the last status, since every NOTIFY of the refer event
// carries one (RFC 3515 2.4.5).
func (s *ReferSubscription) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	if err := s.send(s.last, SubscriptionState{State: SubscriptionTerminated, Reason: ReasonTimeout}); err != nil {
		s.d.c.ua.log.Info("the NOTIFY at the expiry of a REFER failed", "event", s.event, "error", err.Error())
	}
}

// Notify sends a NOTIFY whose body is the status line of statusCode,
// the response that the referenced request got so far. A final status,
// 200 or above, ends the subscription with reason noresource: the state
// it reports is complete (RFC 3515 2.4.7). An active NOTIFY gives the
// time left in expires (RFC 6665 4.2.2), in whole seconds; the
// notifier then keeps no more than that, so that the referrer, which
// takes the expires as the duration (4.1.3), and the notifier end the
// subscription at the same time. With less than a second left, the
// subscription has expired, and the NOTIFY says terminated with reason
// timeout as the one at the expiry does (4.2.1.4). The subscription
// also ends when the referrer answers a NOTIFY with a response that
// ends it (4.2.2). After the end, Notify returns ErrReferEnded.
func (s *ReferSubscription) Notify(statusCode int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return ErrReferEnded
	}
	s.last = statusCode
	now := time.Now()
	state := SubscriptionState{State: SubscriptionActive, Expires: s.expires.Sub(now).Truncate(time.Second)}
	switch {
	case statusCode >= 200:
		state = SubscriptionState{State: SubscriptionTerminated, Reason: ReasonNoResource}
	case state.Expires < time.Second:
		state = SubscriptionState{State: SubscriptionTerminated, Reason: ReasonTimeout}
	}
	s.stopExpiry()
	if state.State == SubscriptionTerminated {
		s.ended = true
	} else {
		s.expires = now.Add(state.Expires)
		s.stopExpiry = s.d.c.ua.afterFunc(state.Expires, s.expire)
	}
	return s.send(statusCode, state)
}

// send sends one NOTIFY of the subscription. s.mu is held.
func (s *ReferSubscription) send(statusCode int, state SubscriptionState) error {
	body := Body{Type: SipfragType, Content: []byte(fmt.Sprintf("%s %d %s\r\n", SIPVersion, statusCode, ReasonPhrase(statusCode)))}
	resp, err := s.d.Send(NOTIFY, body,
		HeaderField{Name: "Event", Value: s.event},
		HeaderField{Name: "Subscription-State", Value: state.String()},
		// NOTIFY is a target refresh request (RFC 6665 3.2).
		HeaderField{Name: "Contact", Value: s.d.c.contactValue()})
	switch {
	case err != nil:
		return err
	case resp.StatusCode >= 300:
		if EndsSubscription(resp.StatusCode) {
			s.ended = true
			s.stopExpiry()
		}
		return fmt.Errorf("sip: NOTIFY answered %d %s", resp.StatusCode, resp.Reason)
	}
	return nil
}
