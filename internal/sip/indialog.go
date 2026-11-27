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
// user of the call answers, with Accept or Reject: a re-INVITE, which
// may get provisional responses with Progress first, an UPDATE, a PRACK
// with a session description (any PRACK, after RelayPRACK), or a
// request of a method that the UA does not handle by itself, such as
// INFO or REFER. The UA has checked that it is in order (RFC 3261
// 12.2.2), and that an offer in it does not cross another offer.
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
	// answered tells that the request has its final response, and ended
	// that a BYE in the dialog is what answered it (endPending). Both are
	// guarded by the mu of the call.
	answered bool
	ended    bool

	// For a re-INVITE: cseq is its CSeq number, by which the ACK for
	// its 2xx is found. settled is closed with its final response, and
	// acked when the ACK has come, which is then in ack.
	cseq    uint32
	settled chan struct{}
	acked   chan struct{}
	ack     *Message
	// provisionals are the responses of the re-INVITE before its final
	// one (Progress). described tells that a reliable one has carried
	// its session description: the answer to its offer, or the offer
	// when it had none (RFC 3262 5); it is guarded by the mu of the call.
	provisionals
	described bool

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
// the ACK carries the answer. When a reliable provisional response has
// carried the session description already, body may be empty (RFC
// 3262 5). Accept waits for a Progress that waits for its PRACK, so the
// 2xx never passes a reliable provisional response (RFC 3262 3). For
// any other request the returned message is nil.
//
// A BYE in the dialog, in either direction, answers the request with
// 487 before the user of the call gets to it (RFC 3261 15.1.2). Accept
// then sends nothing and returns ErrCallEnded.
func (r *InDialogRequest) Accept(body Body, headers ...HeaderField) (*Message, error) {
	r.answering.Lock()
	defer r.answering.Unlock()
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
// request. Like Accept, Reject waits for a Progress to return, and
// sends nothing and returns ErrCallEnded once a BYE has answered the
// request with 487.
func (r *InDialogRequest) Reject(code int, headers ...HeaderField) error {
	if code < 300 || code > 699 {
		return fmt.Errorf("sip: %d is not a final response that Reject can send", code)
	}
	r.answering.Lock()
	defer r.answering.Unlock()
	return r.reject(code, headers)
}

// reject is Reject for a response that does not wait for Progress.
func (r *InDialogRequest) reject(code int, headers []HeaderField) error {
	resp := NewResponse(r.Request, code, "")
	addHeaders(resp, headers)
	return r.respond(resp, false)
}

// ErrRequestAnswered is what Accept and Reject return once the request
// has its final response.
var ErrRequestAnswered = errors.New("sip: the request has its final response already")

// answerable reports why the user of the call can no longer answer the
// request: it has its final response already, or a BYE in the dialog
// answered it with 487 (endPending). c.mu is held.
func (r *InDialogRequest) answerable() error {
	switch {
	case r.ended:
		return ErrCallEnded
	case r.answered:
		return ErrRequestAnswered
	}
	return nil
}

// respond sends the one final response of the request, and makes the
// changes of state that come with it.
func (r *InDialogRequest) respond(resp *Message, accepted bool) error {
	c := r.d.c
	c.mu.Lock()
	if err := r.answerable(); err != nil {
		c.mu.Unlock()
		return err
	}
	r.settle(resp, accepted)
	c.mu.Unlock()
	return r.st.Respond(resp)
}

// settle marks the request answered by resp and makes the changes of
// state that the response brings. accepted tells that resp is a 2xx.
// c.mu is held.
func (r *InDialogRequest) settle(resp *Message, accepted bool) {
	d := r.d
	r.answered = true
	d.removePending(r)
	if d.dialog != r.dialog {
		// The dialog of the request is gone (EarlyDialogTerminated), so
		// the response has no state left to change.
		return
	}
	switch r.Request.Method {
	case UPDATE:
		d.updatePending = false
	case INVITE:
		// Only a re-INVITE that was refused or ended by a BYE comes here;
		// acceptReInvite has its own way.
		d.invite = noInvite
		close(r.settled)
	}
	switch {
	case r.described:
		// A reliable provisional response of the re-INVITE carried its
		// session description (RFC 3262 5). An offer there that has no
		// answer yet goes with the re-INVITE (RFC 3261 14.1); an answer
		// that went there has been taken.
		if d.offer == OfferLocal {
			d.offerEvent(OfferRejected)
		}
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
}

// receiveOther takes a request of a method that the UA does not handle
// by itself, for the user of the call to answer. Without a user, the
// method is one the dialog does not take (takes).
func (d *CallDialog) receiveOther(st *ServerTransaction) {
	c := d.c
	c.mu.Lock()
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

// usable reports whether requests of the session can go out within the
// dialog: it exists, it has not ended, and nor has its session. c.mu is
// held.
func (d *CallDialog) usable() error {
	switch {
	case d.dialog == nil:
		return errNoDialog
	case d.dialog.State == DialogTerminated || d.sessionEnded:
		return ErrCallEnded
	}
	return nil
}

// Send sends a request within the dialog, early or confirmed, and
// returns its final response. It is for the methods that the UA has no
// method of its own for, such as INFO or REFER: a B2BUA relays what its
// one leg received to the other leg with it. Once a BYE has ended the
// session, Send sends only the SUBSCRIBEs and NOTIFYs of the
// subscriptions that the dialog lives on for (AddSubscriptionUsage).
func (d *CallDialog) Send(method Method, body Body, headers ...HeaderField) (*Message, error) {
	switch method {
	case INVITE, ACK, CANCEL, BYE, PRACK, UPDATE:
		return nil, fmt.Errorf("sip: Send cannot send %s; the call has a method of its own for it", method)
	}
	c := d.c
	c.mu.Lock()
	err := d.usable()
	if d.sessionEnded && d.dialog.State != DialogTerminated && ofSubscription(method) {
		err = nil
	}
	if err != nil {
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
// lost the dialog: the UA ends the session with a BYE. A dialog that
// has a subscription besides the session keeps it (endSession). The
// failure of a SUBSCRIBE or a NOTIFY in such a dialog ends only the
// subscription it was of (RFC 5057 5.1, 5.2), which is for the user of
// the call to end (AddSubscriptionUsage).
func (d *CallDialog) lost(req, final *Message) {
	c := d.c
	status := 0
	if final != nil {
		status = final.StatusCode
	}
	c.ua.log.Warn("request within the dialog failed", "method", string(req.Method), "call_id", req.Get("Call-ID"), "status", status)
	c.mu.Lock()
	subscriptionOnly := ofSubscription(req.Method) && d.subscriptions > 0
	c.mu.Unlock()
	if subscriptionOnly {
		return
	}
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
// request carried an offer that is still open: no reliable provisional
// response has answered it (RFC 3262 5). c.mu is held.
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
	// The UA answers below, outside the lock, so a BYE can get there
	// first (endPending).
	d.addPending(r)
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

// ReInvitation is what the user of a call puts in a re-INVITE, and how
// it takes the provisional responses to it.
type ReInvitation struct {
	// Body is normally the offer. Without one, the re-INVITE asks the
	// peer for an offer, which may come in a reliable provisional
	// response. The PRACK of that response carries the answer (RFC 3262
	// 5), which the user sends with PRACK once OnProvisional has handed
	// it the response.
	Body Body
	// Headers are further header fields of the re-INVITE.
	Headers []HeaderField
	// OnProvisional, when set, is called with each provisional response
	// other than 100 (Trying) that the UA takes, in the order they come:
	// not a retransmission of a reliable one, nor one that comes out of
	// order (RFC 3262 4). It runs in the goroutine of ReInviteWith, which
	// reads the next response only once it returns, so it should hand
	// any wait, such as for a PRACK of its own, to a goroutine. A B2BUA
	// that relays the provisional responses passes each one on to its
	// other leg here.
	OnProvisional func(*Message)
	// ManualPRACK leaves the PRACK for each reliable provisional response
	// to the user, who sends it with PRACK, and so needs OnProvisional
	// too. A B2BUA that relays the PRACK of its other leg, with its body,
	// sets it.
	ManualPRACK bool
}

// ReInvite is ReInviteWith for a re-INVITE with body and headers, whose
// provisional responses the UA takes by itself. It is for a re-INVITE
// with an offer. One without an offer may get the offer of the peer in
// a reliable provisional response, whose PRACK has to carry the answer
// (RFC 3262 5): only the user has it, and ReInvite does not hand the
// response over. The UA then logs a warning and sends no PRACK, and the
// UAS gives the re-INVITE up with a 5xx after 64*T1 (RFC 3262 3). A user
// that sends a re-INVITE without an offer uses ReInviteWith with
// OnProvisional.
func (d *CallDialog) ReInvite(body Body, headers ...HeaderField) (*Message, error) {
	return d.ReInviteWith(ReInvitation{Body: body, Headers: headers})
}

// ReInviteWith sends an INVITE within the confirmed dialog and returns
// its final response (RFC 3261 14.1). A re-INVITE changes the session:
// a UE puts a call on hold with one whose offer makes the media
// sendonly, and takes it back with the next. The offer is the whole
// session description, not only what changes.
//
// With a body, the re-INVITE carries the offer, the 2xx the answer, and
// the UA sends the ACK itself. Without a body, the 2xx carries the
// offer, and the user sends the ACK with the answer (Ack).
//
// The re-INVITE lists 100rel in Supported, as every request within the
// dialog does, so the peer may send its provisional responses reliably.
// Each one gets its PRACK (RFC 3262 4). Without ManualPRACK the UA
// sends it itself, without a body, except for a response that carries
// an offer: its PRACK has to carry the answer, which the user sends
// with PRACK (RFC 3262 5).
//
// The first reliable provisional response with a session description
// carries the answer to the offer of the re-INVITE, or the offer when
// the re-INVITE had none, and the 2xx then carries none that counts
// (RFC 3261 13.2.1, RFC 3262 5). OnProvisional is where the user takes
// it. A provisional response that is not reliable changes nothing: what
// session description it has only shows the answer before it comes.
//
// A final response other than 2xx leaves the session as it was. After a
// 491 (Request Pending), the re-INVITE crossed one of the peer; the
// user may try again after GlareDelay.
func (d *CallDialog) ReInviteWith(re ReInvitation) (*Message, error) {
	c := d.c
	c.mu.Lock()
	body, headers := re.Body, re.Headers
	offers := len(body.Content) > 0
	var err error
	switch {
	case d.dialog == nil || d.dialog.State != DialogConfirmed:
		err = errNoConfirmedDialog
	case d.sessionEnded:
		err = ErrCallEnded
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
	d.expectReliable(cseq.Number)
	t := d.takeTurn()
	c.mu.Unlock()

	reinvite := reInviteSent{offers: offers, manualPRACK: re.ManualPRACK, handsOver: re.OnProvisional != nil}
	var final *Message
	ct, err := t.send(c, req)
	if err == nil {
		for resp := range ct.Responses() {
			t.took(resp)
			if resp.StatusCode >= 200 {
				final = resp
				break
			}
			if resp.StatusCode > 100 && d.reInviteProvisional(resp, &reinvite) && re.OnProvisional != nil {
				re.OnProvisional(resp)
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
	// RFC 3262 4: the UAC keeps the RSeq until the final response. One
	// that comes after it is discarded, which the RFC allows.
	seq := d.reliable[cseq.Number]
	delete(d.reliable, cseq.Number)
	offerIn2xx := accepted && !offers && !reinvite.described && len(final.Body) > 0
	switch {
	case !accepted && reinvite.offerRSeq > seq.pracked:
		// The offer of a reliable response has no answer in a PRACK yet,
		// and goes with the re-INVITE (RFC 3261 14.1). An offer that the
		// peer made after an answer, in an UPDATE, stays open.
		d.offerEvent(OfferRejected)
	case offerIn2xx:
		d.offerEvent(OfferReceived)
	}
	d.answered(final, offers && !reinvite.described)
	if accepted && d.dialog.State == DialogConfirmed {
		d.acknowledge(cseq.Number, offerIn2xx)
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

// reInviteSent is a re-INVITE of this UA, from the time it goes out to
// its final response.
type reInviteSent struct {
	// offers tells that the re-INVITE carried the offer.
	offers      bool
	manualPRACK bool
	// handsOver tells that the user takes the provisional responses
	// (OnProvisional).
	handsOver bool
	// described tells that a reliable provisional response has carried
	// the session description of the re-INVITE: the answer to its
	// offer, or the offer when it had none (RFC 3262 5).
	described bool
	// offerRSeq is the RSeq of that response when it carried the offer,
	// whose answer goes into its PRACK; 0 otherwise.
	offerRSeq uint32
}

// reInviteProvisional is RFC 3262 4 and 5 and RFC 6141 4.7 for resp, a
// 101-199 response to the re-INVITE r. A reliable one gets its PRACK,
// from the UA unless r leaves it to the user; its Contact, if it has
// one, is the remote target from now on; and the first one with a
// session description carries the answer or the offer. One that is not
// reliable changes nothing. A retransmission, or one that comes out of
// order, is discarded. It reports whether to pass resp on to the user.
func (d *CallDialog) reInviteProvisional(resp *Message, r *reInviteSent) bool {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if !resp.Requires(Option100rel) {
		return true
	}
	if d.usable() != nil || !d.reliableInOrder(resp) {
		return false
	}
	rseq := d.reliableTo(resp).last
	// RFC 6141 4.3: the target stays refreshed even when the re-INVITE
	// then fails.
	d.dialog.refreshTarget(resp)
	if HasSessionDescription(resp) && !r.described {
		r.described = true
		if r.offers {
			d.offerEvent(AnswerReceived)
		} else {
			d.offerEvent(OfferReceived)
			r.offerRSeq = rseq
		}
	}
	switch {
	case !r.manualPRACK && rseq != r.offerRSeq:
		d.autoPRACK(resp)
	case !r.handsOver:
		// The PRACK is the user's, for ManualPRACK or for the answer to the
		// offer of resp, but the user never gets resp.
		c.ua.log.Warn("nobody takes the reliable provisional response to the re-INVITE, which gets no PRACK",
			"status", resp.StatusCode, "call_id", resp.Get("Call-ID"), "rseq", rseq, "offer", rseq == r.offerRSeq)
	}
	return true
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
	r.reliable = st.Request.Supports(Option100rel)
	d.invite = inviteReceived
	d.peerInvite = &r.provisionals
	c.ua.spawn(r.watchCancel)
	c.handOver(r)
}

// watchCancel answers the re-INVITE with 487 (Request Terminated) when
// the peer cancels it before its final response (RFC 3261 9.2). The 487
// does not wait for the PRACK of a reliable provisional response, as
// Reject would: RFC 3262 3 lets a final response other than 2xx pass
// one.
//
// SIMPLIFIED: RFC 6141 3.8. A re-INVITE whose change has been made gets
// a 2xx, not a 487, when the peer cancels it (RFC 6141 3.3), and an
// answer in a reliable provisional response makes the change unless
// preconditions hold it back. The UA answers 487 all the same: the
// preconditions are in the SDP, which the UA does not read, and with
// them the change waits for the user, so 487 is right. No node of this
// project cancels a re-INVITE.
func (r *InDialogRequest) watchCancel() {
	select {
	case <-r.st.Cancelled():
		r.reject(487, r.d.c.ua.own(INVITE, true, nil))
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
	err := r.answerable()
	if err == nil {
		switch hasBody := len(body.Content) > 0; {
		case r.described:
			// The session description went in a reliable provisional
			// response, so the 2xx carries none that counts (RFC 3262 5).
		case r.Offer && !hasBody:
			err = ErrNoAnswer
		case r.Offer:
			err = d.offerEvent(AnswerSent)
		case !hasBody:
			err = errNoOfferFor2xx
		default:
			err = d.offerEvent(OfferSent)
		}
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	r.answered = true
	d.removePending(r)
	close(r.settled)
	if !r.described {
		d.sent(body)
	}
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
	err = c.retransmit(r.st, ok, c.stack.cfg.Timers.T2, r.acked, nil, ErrNoACK)
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

// Progress sends a provisional response from 101 to 198 to a
// re-INVITE, such as a 183 (Session Progress) whose answer has the peer
// reserve the resources for a change of the session first (RFC 3312),
// or a provisional response of its other leg that a B2BUA passes on.
//
// When the re-INVITE has 100rel in Supported or Require, the response
// goes reliably (RFC 3262 3), with the Contact of the call: the
// re-INVITE is a target refresh request, and a reliable provisional
// response to it gives the peer the local target (RFC 6141 4.5), and
// keeps the remote target that the re-INVITE gave even when the
// re-INVITE then fails (4.6). The UA retransmits the response until its
// PRACK comes, and Progress returns then. After 64*T1 without a PRACK,
// the UA rejects the re-INVITE with 500 and Progress returns
// ErrNoPRACK. Without 100rel, the response goes unreliably, and
// Progress returns once it is sent. Accept and Reject wait for Progress
// to return.
//
// The first reliable response with a session description carries the
// answer to the offer of the re-INVITE, or the offer when the
// re-INVITE had none, whose answer comes in the PRACK (RFC 3262 5): the
// user gets that PRACK like any request within the dialog, and accepts
// it. The 2xx then needs no session description. A session description
// in a response that is not reliable changes nothing. Once the answer
// has gone in a reliable response, the change of the session counts as
// made unless preconditions hold it back, and the user SHOULD accept
// the re-INVITE rather than reject it (RFC 6141 3.3).
//
// headers go into the response. Progress returns ErrRequestAnswered
// once the re-INVITE has its final response, and ErrCallEnded once a
// BYE has answered it.
func (r *InDialogRequest) Progress(code int, body Body, headers ...HeaderField) error {
	switch {
	case r.Request.Method != INVITE:
		// Provisional responses other than 100 are for INVITE only (RFC
		// 4320).
		return fmt.Errorf("sip: Progress is for a re-INVITE, not for %s", r.Request.Method)
	case code <= 100 || code >= 199:
		// A 199 ends an early dialog (RFC 6228), and a re-INVITE comes
		// in a confirmed one.
		return fmt.Errorf("sip: %d is not a provisional response that Progress can send to a re-INVITE", code)
	}
	r.answering.Lock()
	defer r.answering.Unlock()
	c := r.d.c
	resp := NewResponse(r.Request, code, "")
	c.mu.Lock()
	if err := r.answerable(); err != nil {
		c.mu.Unlock()
		return err
	}
	if r.reliable {
		resp.Add("Contact", c.contactLocked())
	}
	addHeaders(resp, headers)
	body.addTo(resp)
	if r.reliable {
		if err := r.sessionSent(body); err != nil {
			c.mu.Unlock()
			return err
		}
		r.makeReliable(resp)
		// RFC 6141 4.6: the target refresh of the re-INVITE holds from
		// now on, whatever its final response.
		r.prevTarget = ""
	}
	err := r.st.Respond(resp)
	pracked := r.pracked
	c.mu.Unlock()
	if err != nil || !r.reliable {
		return err
	}

	// RFC 3262 3, as for the INVITE of a call (IncomingCall.progress).
	err = c.retransmit(r.st, resp, 64*c.stack.cfg.Timers.T1, pracked, r.settled, ErrNoPRACK)
	switch {
	case errors.Is(err, ErrNoPRACK):
		// RFC 3262 3: the UAS SHOULD reject the re-INVITE with a 5xx.
		r.reject(500, c.ua.own(INVITE, true, nil))
	case errors.Is(err, ErrRequestAnswered):
		c.mu.Lock()
		err = r.answerable()
		c.mu.Unlock()
	}
	return err
}

// RelayPRACK has the UA hand every PRACK of the peer for the reliable
// provisional responses to the re-INVITE to the handler of the call,
// with or without a body, as IncomingCall.RelayPRACK does for the
// INVITE of a call. A B2BUA that passes the reliable provisional
// responses of its other leg on with Progress relays their PRACKs back
// this way (RFC 3262 4). The user calls it before the first Progress.
func (r *InDialogRequest) RelayPRACK() {
	c := r.d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	r.relay = true
}

var errNoOfferInReliable = errors.New("sip: the first reliable provisional response to a re-INVITE without an offer needs the offer")

// sessionSent is RFC 3262 5 for body in a reliable provisional
// response to the re-INVITE: the first session description is the
// answer to the offer of the re-INVITE, or, when it had none, the offer,
// which the first reliable response MUST carry. A later one changes
// nothing. c.mu is held.
func (r *InDialogRequest) sessionSent(body Body) error {
	switch {
	case r.described:
		return nil
	case len(body.Content) == 0 && !r.Offer:
		return errNoOfferInReliable
	case len(body.Content) == 0:
		return nil
	}
	event := OfferSent
	if r.Offer {
		event = AnswerSent
	}
	if err := r.d.offerEvent(event); err != nil {
		return err
	}
	r.described = true
	r.d.sent(body)
	return nil
}
