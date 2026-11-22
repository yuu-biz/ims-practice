package sip

import (
	"errors"
	"fmt"
	"sync"
)

// This file holds what a UA does within a dialog besides the BYE: the
// requests that the user of a call sends, and the requests of the peer
// that the user answers.

// inviteProgress tells which INVITE transaction is in progress within
// a dialog: the INVITE that created it, or a re-INVITE. RFC 3261 14.1
// forbids a UA to send an INVITE while there is one, and 14.2 tells how
// to answer one that comes all the same.
type inviteProgress int

const (
	noInvite inviteProgress = iota
	// inviteSent is an INVITE of this UA that has no final response.
	inviteSent
	// inviteReceived is an INVITE of the peer that has no final
	// response. Once its 2xx is sent the peer may send the next INVITE
	// (14.1), even before the ACK, which takes its own path.
	inviteReceived
)

// InDialogRequest is a request of the peer within a dialog that the
// user of the call answers, with Accept or Reject: a re-INVITE, an
// UPDATE, a PRACK with a session description, or a request of a method
// that the UA does not handle by itself, such as INFO or REFER. The UA
// has checked that it is in order (RFC 3261 12.2.2), and that an offer
// in it does not cross another offer.
type InDialogRequest struct {
	// Request is the request as received.
	Request *Message
	// Offer tells that the body of Request is an offer. The user gives
	// the answer to Accept.
	Offer bool
	// Arrival is the place of the request among those of the call that
	// the UA hands to the user: 1 for the first, and one up for each
	// after it, in the order in which the UA took them. That is the
	// order of their CSeq numbers within a dialog (RFC 3261 12.2.2). The
	// UA hands each request to a goroutine of its own, so one that came
	// later may run first; a B2BUA that relays the requests to its other
	// leg uses Arrival to send them on in the order they came. A request
	// that the UA answers itself, such as one out of order, takes no
	// number.
	Arrival uint64

	d  *CallDialog
	st *ServerTransaction
	// dialog is the dialog state that the request came in. The
	// CallDialog of a UAS gets a new one when a 199 ends its early
	// dialog.
	dialog *Dialog
	// prevTarget is the remote target of the dialog before a target
	// refresh request replaced it.
	prevTarget string
	// answered is guarded by the mu of the call.
	answered bool

	// For a re-INVITE: cseq is its CSeq number, by which the ACK for
	// its 2xx is found. settled is closed with its final response, and
	// acked when the ACK has come, which is then in ack.
	cseq    uint32
	settled chan struct{}
	acked   chan struct{}
	ack     *Message

	// session is what the UA answers to the session timer of a re-INVITE
	// or UPDATE within the confirmed dialog; nil for any other request.
	session *SessionTimerAnswer
}

// Dialog is the dialog that the request came in.
func (r *InDialogRequest) Dialog() *CallDialog { return r.d }

// Accept answers the request with 200 (OK). body and headers go into
// the 200; when the request carried an offer, body is the answer.
//
// For a re-INVITE, Accept returns the ACK. The UA sends the 200 again
// until the ACK arrives, and ends the call with a BYE when none comes
// within 64*T1; Accept then returns ErrNoACK (RFC 3261 14.2). A
// re-INVITE without an offer asks for one: body is then the offer, and
// the ACK carries the answer. For any other request the returned
// message is nil.
func (r *InDialogRequest) Accept(body Body, headers ...HeaderField) (*Message, error) {
	if r.Request.Method == INVITE {
		return r.acceptReInvite(body, headers)
	}
	resp := NewResponse(r.Request, 200, "")
	if r.Request.Method == UPDATE {
		// The 2xx to a target refresh request carries the Contact that
		// the peer sends its next requests to (RFC 3261 12.2.1.2).
		resp.Add("Contact", r.d.c.contactValue())
	}
	if r.session != nil {
		addHeaders(resp, r.session.Headers)
	}
	addHeaders(resp, headers)
	body.addTo(resp)
	if r.Offer && len(body.Content) == 0 {
		return nil, ErrNoAnswer
	}
	return nil, r.respond(resp, true)
}

// ErrNoAnswer is what Accept returns for a request with an offer when
// it gets no body: the 2xx has to carry the answer (RFC 3311 5.2).
var ErrNoAnswer = errors.New("sip: the 2xx to a request with an offer needs the answer")

// Reject answers the request with a final response from 300 to 699,
// such as 488 (Not Acceptable Here) for an offer that the user cannot
// take. The session and the dialog stay as they were before the
// request.
func (r *InDialogRequest) Reject(code int, headers ...HeaderField) error {
	if code < 300 || code > 699 {
		return fmt.Errorf("sip: %d is not a final response that Reject can send", code)
	}
	resp := NewResponse(r.Request, code, "")
	addHeaders(resp, headers)
	return r.respond(resp, false)
}

// ErrRequestAnswered is what Accept and Reject return once the request
// has its final response.
var ErrRequestAnswered = errors.New("sip: the request has its final response already")

// respond sends the one final response of the request, and makes the
// changes of state that come with it.
func (r *InDialogRequest) respond(resp *Message, accepted bool) error {
	d := r.d
	c := d.c
	c.mu.Lock()
	if r.answered {
		c.mu.Unlock()
		return ErrRequestAnswered
	}
	r.answered = true
	if d.dialog != r.dialog {
		// The dialog of the request is gone (EarlyDialogTerminated), so
		// the response has no state left to change.
		c.mu.Unlock()
		return r.st.Respond(resp)
	}
	switch r.Request.Method {
	case UPDATE:
		d.updatePending = false
	case INVITE:
		// Only a rejected re-INVITE ends here; acceptReInvite has its
		// own way.
		d.invite = noInvite
		close(r.settled)
	}
	switch {
	case r.Offer && accepted:
		d.offerEvent(AnswerSent)
		d.sent(BodyOf(resp))
	case r.Offer:
		d.offerEvent(OfferRejected)
	}
	if accepted && r.session != nil {
		d.setSession(r.session.Timer)
	}
	if !accepted && r.prevTarget != "" {
		// RFC 3261 12.2.2: a rejected request changes no state.
		d.dialog.RemoteTarget = r.prevTarget
	}
	c.mu.Unlock()
	return r.st.Respond(resp)
}

// receiveOther takes a request of a method that the UA does not handle
// by itself. The user of the call answers it; without a handler it gets
// 405 (RFC 3261 8.2.1).
func (d *CallDialog) receiveOther(st *ServerTransaction) {
	c := d.c
	c.mu.Lock()
	handler := c.onRequest
	if handler == nil {
		c.mu.Unlock()
		c.ua.respondNotAllowed(st)
		return
	}
	if st.Request.Method == REFER && len(st.Request.GetAll("Refer-To")) != 1 {
		// RFC 3515 2.4.2: a REFER has exactly one Refer-To.
		c.mu.Unlock()
		d.respond(st, 400)
		return
	}
	if d.inOrder(st) {
		c.handOver(&InDialogRequest{Request: st.Request, d: d, st: st, dialog: d.dialog})
	}
	c.mu.Unlock()
}

var errNoDialog = errors.New("sip: no dialog to send the request in")

// usable reports whether requests can go out within the dialog: it
// exists and has not ended. c.mu is held.
func (d *CallDialog) usable() error {
	switch {
	case d.dialog == nil:
		return errNoDialog
	case d.dialog.State == DialogTerminated:
		return ErrCallEnded
	}
	return nil
}

// Send sends a request within the dialog, early or confirmed, and
// returns its final response. It is for the methods that the UA has no
// method of its own for, such as INFO or REFER: a B2BUA relays what its
// one leg received to the other leg with it.
func (d *CallDialog) Send(method Method, body Body, headers ...HeaderField) (*Message, error) {
	switch method {
	case INVITE, ACK, CANCEL, BYE, PRACK, UPDATE:
		return nil, fmt.Errorf("sip: Send cannot send %s; the call has a method of its own for it", method)
	}
	c := d.c
	c.mu.Lock()
	if err := d.usable(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	req := d.newRequest(method, body, headers)
	t := d.takeTurn()
	c.mu.Unlock()
	return d.roundTrip(req, t)
}

// newRequest builds a request within the dialog with what the user of
// the call adds to it. c.mu is held.
func (d *CallDialog) newRequest(method Method, body Body, headers []HeaderField) *Message {
	req := d.dialog.NewRequest(method)
	// RFC 4028 7.1: every request but ACK lists timer in Supported.
	req.Add("Supported", d.c.ua.supportedHeader())
	addHeaders(req, headers)
	body.addTo(req)
	return req
}

// turn is the place of a request among the requests that this UA sends
// within a dialog. RFC 3261 12.2.1.1 numbers them by CSeq, one up for
// each, and 12.2.2 has the peer reject with 500 a request whose number
// is lower than that of one it has already taken. Requests that are on
// their way at the same time can pass each other: a large one goes over
// TCP while a small one goes over UDP (RFC 3261 18.1.1). So the UA
// sends a request only once the peer has shown that it took the one
// before: by its final response, or by a provisional response other
// than 100 (Trying), which only tells that the next hop has it.
//
// The INVITE that creates the dialog takes no turn: the PRACKs and
// UPDATEs of the early dialog go while it is pending (RFC 3262, RFC
// 3311). Nor does the BYE: hanging up does not wait, and a request that
// the BYE passes finds the dialog ended.
type turn struct {
	// before is closed once the peer has taken the request before; nil
	// for the first request of the dialog.
	before <-chan struct{}
	// taken is closed by end, once the peer has taken this request or
	// it is clear that the request will get no response.
	taken chan struct{}
	end   func()
}

// takeTurn puts the request that newRequest has just built after the
// requests that this UA sent before it in the dialog, in the order of
// their CSeq numbers. c.mu is held.
func (d *CallDialog) takeTurn() turn {
	taken := make(chan struct{})
	t := turn{before: d.lastTurn.taken, taken: taken, end: sync.OnceFunc(func() { close(taken) })}
	d.lastTurn = t
	return t
}

// send sends req once the peer has taken the request before it. The
// turn ends with the first response of the peer (turn), or at once
// when req does not go out.
func (t turn) send(c *call, req *Message) (*ClientTransaction, error) {
	if t.before != nil {
		<-t.before
	}
	ct, err := c.send(req)
	if err != nil {
		t.end()
	}
	return ct, err
}

// took ends the turn when resp shows that the peer has taken the
// request.
func (t turn) took(resp *Message) {
	if resp.StatusCode > 100 {
		t.end()
	}
}

// ErrDialogLost is why a call ends when the peer answers a request
// within the dialog with 481 or 408, or not at all (RFC 3261 12.2.1.2).
var ErrDialogLost = errors.New("sip: the peer no longer answers within the dialog")

// roundTrip sends a non-INVITE request within the dialog and waits for
// its final response, after the requests before it (turn).
//
// RFC 3261 12.2.1.2: a 481 (Call/Transaction Does Not Exist), a 408
// (Request Timeout), or no response at all tells that the peer has lost
// the dialog, and the UAC SHOULD end it, with a BYE for the dialog of
// an INVITE.
//
// SIMPLIFIED: RFC 3261 12.2.1.2. In an early dialog the loss is only
// logged. The callee must not send a BYE there (RFC 3261 15), and for
// the caller the final response to the INVITE ends the dialog anyway.
func (d *CallDialog) roundTrip(req *Message, t turn) (*Message, error) {
	c := d.c
	ct, err := t.send(c, req)
	if err != nil {
		return nil, err
	}
	defer t.end()
	var final *Message
	for resp := range ct.Responses() {
		t.took(resp)
		final = resp
	}
	if final == nil || final.StatusCode == 408 || final.StatusCode == 481 {
		d.lost(req, final)
	}
	if final == nil {
		return nil, ct.Err()
	}
	return final, nil
}

// lost is RFC 3261 12.2.1.2 for a request that showed that the peer has
// lost the dialog.
func (d *CallDialog) lost(req, final *Message) {
	c := d.c
	status := 0
	if final != nil {
		status = final.StatusCode
	}
	c.ua.log.Warn("request within the dialog failed", "method", string(req.Method), "call_id", req.Get("Call-ID"), "status", status)
	reason := ErrDialogLost
	if status == 0 || status == 408 {
		// No answer came in time, which a Reason can tell (TS 24.229
		// Table 7.2A.18.11-2); a 481 is no timeout.
		reason = fmt.Errorf("%w: %w", ErrDialogLost, ErrTimeout)
	}
	if err := d.byeOwn(reason); err != nil && !errors.Is(err, errNoConfirmedDialog) && !errors.Is(err, ErrCallEnded) {
		c.ua.log.Warn("BYE for a lost dialog failed", "call_id", req.Get("Call-ID"), "error", err.Error())
	}
}

// Update sends an UPDATE within the dialog and returns its final
// response (RFC 3311 5.1). An UPDATE changes the session while the
// INVITE is still pending, which a re-INVITE cannot: a caller tells the
// callee with it that the resources for the media are reserved. In a
// confirmed dialog it refreshes the session, and may change it too.
//
// With a body, the UPDATE carries an offer, and the 2xx the answer.
// Update returns ErrOfferPending or ErrOfferUnanswered, and sends
// nothing, while another offer is open in the dialog: before the answer
// to the offer of the INVITE has come in a reliable response, or while
// an offer of the peer waits for its answer. Without a body, the UPDATE
// leaves the session as it is.
//
// A final response other than 2xx leaves the session unchanged (5.3).
// After a 491 (Request Pending), the offer crossed one of the peer; the
// user may try again after GlareDelay.
func (d *CallDialog) Update(body Body, headers ...HeaderField) (*Message, error) {
	c := d.c
	c.mu.Lock()
	if err := d.usable(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	offers := len(body.Content) > 0
	if offers {
		if err := d.offerEvent(OfferSent); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		d.sent(body)
	}
	req := d.newRequest(UPDATE, body, append(d.sessionRequestHeaders(), headers...))
	// UPDATE is a target refresh request (RFC 3311 5.1).
	req.Add("Contact", c.contactLocked())
	t := d.takeTurn()
	c.mu.Unlock()

	resp, err := d.roundTrip(req, t)
	c.mu.Lock()
	defer c.mu.Unlock()
	d.answered(resp, offers)
	return resp, err
}

// answered makes the changes of state that the final response to a
// target refresh request of this UA brings (RFC 3261 12.2.1.2, 14.1;
// RFC 3311 5.3). resp is nil when none came. offers tells that the
// request carried an offer. c.mu is held.
func (d *CallDialog) answered(resp *Message, offers bool) {
	accepted := resp != nil && resp.StatusCode < 300
	if accepted && d.dialog.State != DialogTerminated {
		d.dialog.refreshTarget(resp)
	}
	if d.dialog.State == DialogConfirmed && resp != nil {
		// The request was a session refresh request (RFC 4028 7.2, 7.3).
		switch {
		case accepted:
			d.setSession(d.session.After2xx(resp))
		case resp.StatusCode == 422:
			d.session, _ = d.session.After422(resp)
		}
	}
	switch {
	case !offers:
	case accepted && len(resp.Body) > 0:
		d.offerEvent(AnswerReceived)
	default:
		// The session stays as it was before the offer.
		d.offerEvent(OfferRejected)
	}
}

// receiveUpdate is RFC 3311 5.2 for an UPDATE of the peer. The user of
// the call answers one that carries an offer. An UPDATE without a body
// changes nothing in the session, so a call without a handler answers
// it with 200 itself.
func (d *CallDialog) receiveUpdate(st *ServerTransaction) {
	c := d.c
	c.mu.Lock()
	if d.updatePending {
		// 5.2: the UPDATE before this one has no final response yet.
		c.mu.Unlock()
		d.respond(st, 500, retryAfter())
		return
	}
	r := d.admit(st)
	if r == nil {
		c.mu.Unlock()
		return
	}
	d.updatePending = true
	if c.onRequest != nil {
		c.handOver(r)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	switch {
	case r.Offer:
		r.Reject(488, append(c.ua.own(UPDATE, true, nil), c.ua.noSessionChange())...)
	default:
		r.Accept(Body{}, c.ua.own(UPDATE, true, nil)...)
	}
}

// noSessionChange is the Warning of the 488 (Not Acceptable Here) that
// the UA sends for an offer within a dialog when the call has no
// handler, so that nobody is there to answer it. RFC 3261 14.2 and RFC
// 3311 5.2 ask for a Warning in that 488; 399 is the code for a warning
// that fits none of the others (RFC 3261 20.43).
func (ua *UA) noSessionChange() HeaderField {
	return HeaderField{Name: "Warning", Value: "399 " + ua.cfg.SentBy + ` "The session of this call cannot be changed"`}
}

// admit is what a re-INVITE and an UPDATE of the peer share (RFC 3261
// 12.2.2, 14.2; RFC 3311 5.2): the request has to be in order, and an
// offer in it must not cross another offer. admit answers a request
// that fails either check and returns nil. c.mu is held.
func (d *CallDialog) admit(st *ServerTransaction) *InDialogRequest {
	req := st.Request
	prevTarget, prevSeq := d.dialog.RemoteTarget, d.dialog.RemoteSeq
	if !d.inOrder(st) {
		return nil
	}
	r := &InDialogRequest{Request: req, Offer: len(req.Body) > 0, d: d, st: st, dialog: d.dialog, prevTarget: prevTarget}
	// reject answers the request; a rejected request changes no state
	// (RFC 3261 12.2.2).
	reject := func(code int, headers ...HeaderField) *InDialogRequest {
		d.dialog.RemoteTarget, d.dialog.RemoteSeq = prevTarget, prevSeq
		d.respond(st, code, headers...)
		return nil
	}
	if d.dialog.State == DialogConfirmed {
		// Within a confirmed dialog, the request is a session refresh
		// request (RFC 4028 9).
		a := d.c.ua.cfg.SessionTimer.Answer(req, d.session)
		if a.TooSmall > 0 {
			return reject(422, HeaderField{Name: "Min-SE", Value: deltaSeconds(a.TooSmall)})
		}
		r.session = &a
	}
	if !r.Offer {
		return r
	}
	switch err := d.offerEvent(OfferReceived); {
	case err == nil:
		return r
	case errors.Is(err, ErrOfferPending):
		// The offer crosses one of this UA.
		return reject(491)
	default:
		// An earlier offer of the peer has no answer yet.
		return reject(500, retryAfter())
	}
}

// respond answers a request within the dialog that the UA does not
// pass on to the user.
func (d *CallDialog) respond(st *ServerTransaction, code int, headers ...HeaderField) {
	resp := NewResponse(st.Request, code, "")
	addHeaders(resp, headers)
	d.c.ua.respondOwn(st, resp)
}

// ErrInviteInProgress is what ReInvite returns while another INVITE
// transaction is in progress within the dialog (RFC 3261 14.1).
var ErrInviteInProgress = errors.New("sip: an INVITE transaction is in progress within the dialog")

// ReInvite sends an INVITE within the confirmed dialog and returns its
// final response (RFC 3261 14.1). A re-INVITE changes the session: a
// UE puts a call on hold with one whose offer makes the media sendonly,
// and takes it back with the next. The offer is the whole session
// description, not only what changes.
//
// With a body, the re-INVITE carries the offer, the 2xx the answer, and
// the UA sends the ACK itself. Without a body, the 2xx carries the
// offer, and the user sends the ACK with the answer (Ack).
//
// A final response other than 2xx leaves the session as it was. After a
// 491 (Request Pending), the re-INVITE crossed one of the peer; the
// user may try again after GlareDelay.
func (d *CallDialog) ReInvite(body Body, headers ...HeaderField) (*Message, error) {
	c := d.c
	c.mu.Lock()
	offers := len(body.Content) > 0
	var err error
	switch {
	case d.dialog == nil || d.dialog.State != DialogConfirmed:
		err = errNoConfirmedDialog
	case d.invite != noInvite || d.awaitsACK():
		// 14.1: the UAS waits for the ACK of its 2xx, by which its INVITE
		// server transaction is confirmed, before it sends an INVITE.
		err = ErrInviteInProgress
	case offers:
		err = d.offerEvent(OfferSent)
	default:
		// The peer can only offer in its 2xx when no offer is open.
		_, err = d.offer.Next(OfferReceived)
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	d.sent(body)
	req := d.newRequest(INVITE, body, append(d.sessionRequestHeaders(), headers...))
	// A re-INVITE is a target refresh request (RFC 3261 12.2).
	req.Add("Contact", c.contactLocked())
	req.Add("Allow", c.ua.allowHeader())
	cseq, _ := ParseCSeq(req.Get("CSeq"))
	d.invite = inviteSent
	t := d.takeTurn()
	c.mu.Unlock()

	var final *Message
	ct, err := t.send(c, req)
	if err == nil {
		for resp := range ct.Responses() {
			t.took(resp)
			if resp.StatusCode >= 200 {
				final = resp
				break
			}
		}
		t.end()
		if final == nil {
			err = ct.Err()
		}
	}
	accepted := final != nil && final.StatusCode < 300

	c.mu.Lock()
	d.invite = noInvite
	if accepted && !offers && len(final.Body) > 0 {
		d.offerEvent(OfferReceived)
	}
	d.answered(final, offers)
	if accepted && d.dialog.State == DialogConfirmed {
		d.acknowledge(cseq.Number)
	}
	c.mu.Unlock()

	switch {
	case accepted:
		c.ua.spawn(func() { d.acknowledgeAgain(ct, cseq.Number) })
	case ct != nil && (final == nil || final.StatusCode == 408 || final.StatusCode == 481):
		// RFC 3261 14.1: the peer has lost the dialog.
		d.lost(req, final)
	}
	return final, err
}

// awaitsACK tells that a 2xx of this UA to an INVITE of the peer
// within the dialog has no ACK yet. c.mu is held.
func (d *CallDialog) awaitsACK() bool {
	// A re-INVITE stays in unacked until the goroutine of its Accept
	// wakes up, which may be after the next request has arrived; the
	// ACK itself is already recorded in r.ack.
	for _, r := range d.unacked {
		if r.ack == nil {
			return true
		}
	}
	return d.in != nil && d.in.final != nil && d.in.final.StatusCode < 300 && d.in.ack == nil
}

// acknowledgeAgain sends the ACK again for each retransmission of the
// 2xx to a re-INVITE, until the client transaction ends 64*T1 after the
// first 2xx (RFC 3261 13.2.2.4, RFC 6026 7.2).
func (d *CallDialog) acknowledgeAgain(ct *ClientTransaction, seq uint32) {
	c := d.c
	for resp := range ct.Responses() {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		c.mu.Lock()
		if ack := d.acks[seq]; ack != nil {
			if err := c.sendACK(ack); err != nil {
				c.ua.log.Warn("ACK not sent", "error", err.Error())
			}
		}
		c.mu.Unlock()
	}
}

// receiveReInvite is RFC 3261 14.2 for an INVITE of the peer within the
// dialog. The user of the call answers it; a call without a handler
// cannot change its session and answers 488 (Not Acceptable Here),
// which leaves the session as it was.
func (d *CallDialog) receiveReInvite(st *ServerTransaction) {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	switch d.invite {
	case inviteReceived:
		// 14.2: the INVITE before this one has no final response yet.
		d.respond(st, 500, retryAfter())
		return
	case inviteSent:
		// 14.2: it crosses an INVITE of this UA.
		d.respond(st, 491)
		return
	}
	if c.onRequest == nil {
		d.respond(st, 488, c.ua.noSessionChange())
		return
	}
	r := d.admit(st)
	if r == nil {
		return
	}
	cseq, _ := ParseCSeq(st.Request.Get("CSeq"))
	r.cseq, r.settled, r.acked = cseq.Number, make(chan struct{}), make(chan struct{})
	d.invite = inviteReceived
	c.ua.spawn(r.watchCancel)
	c.handOver(r)
}

// watchCancel answers the re-INVITE with 487 (Request Terminated) when
// the peer cancels it before its final response (RFC 3261 9.2).
func (r *InDialogRequest) watchCancel() {
	select {
	case <-r.st.Cancelled():
		r.Reject(487, r.d.c.ua.own(INVITE, true, nil)...)
	case <-r.settled:
	case <-r.d.c.done:
	}
}

var errNoOfferFor2xx = errors.New("sip: the 2xx to a re-INVITE without an offer needs the offer")

// acceptReInvite is Accept for a re-INVITE (RFC 3261 14.2).
func (r *InDialogRequest) acceptReInvite(body Body, headers []HeaderField) (*Message, error) {
	d := r.d
	c := d.c
	ok := NewResponse(r.Request, 200, "")
	ok.Add("Contact", c.contactValue())
	ok.Add("Allow", c.ua.allowHeader())
	ok.Add("Supported", c.ua.supportedHeader())
	if r.session != nil {
		addHeaders(ok, r.session.Headers)
	}
	addHeaders(ok, headers)
	body.addTo(ok)

	c.mu.Lock()
	var err error
	switch hasBody := len(body.Content) > 0; {
	case r.answered:
		err = ErrRequestAnswered
	case r.Offer && !hasBody:
		err = ErrNoAnswer
	case r.Offer:
		err = d.offerEvent(AnswerSent)
	case !hasBody:
		err = errNoOfferFor2xx
	default:
		err = d.offerEvent(OfferSent)
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	r.answered = true
	close(r.settled)
	d.sent(body)
	if r.session != nil {
		d.setSession(r.session.Timer)
	}
	// The re-INVITE has its final response, so the next one may come
	// (RFC 3261 14.2); the ACK of this 2xx is found by its CSeq.
	d.invite = noInvite
	if d.unacked == nil {
		d.unacked = map[uint32]*InDialogRequest{}
	}
	d.unacked[r.cseq] = r
	c.mu.Unlock()
	if err := r.st.Respond(ok); err != nil {
		c.mu.Lock()
		delete(d.unacked, r.cseq)
		c.mu.Unlock()
		return nil, err
	}

	// RFC 6026 8.1: the UAS core retransmits the 2xx until the ACK.
	err = c.retransmit(r.st, ok, c.stack.cfg.Timers.T2, r.acked, ErrNoACK)
	c.mu.Lock()
	delete(d.unacked, r.cseq)
	ack := r.ack
	c.mu.Unlock()
	if errors.Is(err, ErrNoACK) {
		// RFC 3261 14.2: without an ACK, the UAS SHOULD end the dialog
		// with a BYE.
		if err := d.byeOwn(ErrNoACK); err != nil {
			c.ua.log.Warn("BYE after a missing ACK failed", "call_id", r.Request.Get("Call-ID"), "error", err.Error())
		}
	}
	if err != nil {
		return nil, err
	}
	return ack, nil
}
