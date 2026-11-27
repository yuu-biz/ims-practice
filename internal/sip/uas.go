package sip

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
)

// IncomingCall is a call this UA answers, at the UAS side (RFC 3261
// 13.3). Progress, Accept and Reject may be called from any goroutine;
// each one waits for the one before it to finish, and a reliable
// Progress for its PRACK (provisionals).
//
// The call has one dialog, the embedded CallDialog, which its first
// response creates: Update, ReInvite, Send and Bye work within it, and
// Handle sets who answers the requests of the caller.
type IncomingCall struct {
	call
	CallDialog
	provisionals
	// Request is the INVITE.
	Request *Message

	st *ServerTransaction
	// tag is the To tag of every response to the INVITE (RFC 3261
	// 8.2.6.2), until a 199 ends the early dialog and the next one gets
	// a tag of its own (EarlyDialogTerminated). Guarded by mu.
	tag   string
	acked chan struct{}

	// Guarded by mu.
	final *Message // the final response to the INVITE, once sent
	ack   *Message // the ACK for the 2xx, once it arrived
	// sessionAnswer is what the UA answers to the session timer of the
	// INVITE (RFC 4028 9).
	sessionAnswer SessionTimerAnswer
}

// provisionals is RFC 3262 3 at the UAS of one INVITE, the INVITE of an
// IncomingCall: whether its provisional responses go reliably, and the
// reliable one that waits for its PRACK.
type provisionals struct {
	// reliable tells that the INVITE has 100rel in Supported or Require.
	reliable bool
	// answering lets one response to the INVITE go at a time. A reliable
	// provisional response holds it until its PRACK has come, so that a
	// reliable provisional response, with or without SDP, always has its
	// PRACK before the next one or the final response goes out. RFC 3262
	// 3 asks this for the next reliable one, and for a 2xx after one with
	// SDP; for the rest it is allowed.
	answering sync.Mutex

	// Guarded by the mu of the call.

	// relay hands every PRACK to the user (RelayPRACK).
	relay bool
	// rseq is the RSeq of the last reliable provisional response, and
	// unacked that response while it waits for its PRACK; pracked is
	// closed when the PRACK comes.
	rseq    uint32
	unacked *Message
	pracked chan struct{}
}

// receiveInvite is RFC 3261 8.2 and 13.3.1 for an INVITE outside a
// dialog.
//
// SIMPLIFIED: RFC 3261 8.2.2.1, 8.2.2.2, 8.2.3 and 13.3.1 step 1. The
// UA takes every Request-URI, where a UAS SHOULD answer 404 or 416 to
// one it does not serve; the P-CSCF only sends the UE what is for its
// Contact. It does not look for merged requests, which SHOULD get 482;
// only a forking proxy makes them, and no node here forks. It leaves
// the body to OnCall, which rejects an offer it cannot take with 488,
// so the 415 with Accept for an unknown body type is never sent. And it
// ignores an Expires in the INVITE, after which a UAS SHOULD answer
// 487; the UAC of this package sends none.
func (ua *UA) receiveInvite(st *ServerTransaction) {
	req := st.Request
	if ua.refuseUnknownExtensions(st) {
		return
	}
	if _, err := contactURI(req); err != nil {
		// RFC 3261 8.1.1.8: an INVITE MUST carry a Contact, or no
		// request of the dialog could reach the caller.
		ua.respondOwn(st, NewResponse(req, 400, NewTag()))
		return
	}
	session := ua.cfg.SessionTimer.Answer(req, SessionTimer{})
	if session.TooSmall > 0 {
		// RFC 4028 9: the session interval is below this UA's minimum.
		resp := NewResponse(req, 422, NewTag())
		resp.Add("Min-SE", deltaSeconds(session.TooSmall))
		ua.respondOwn(st, resp)
		return
	}
	c := &IncomingCall{
		call:         newCall(ua, st.stack),
		provisionals: provisionals{reliable: req.Supports(Option100rel)},
		Request:      req,
		st:           st,
		tag:          NewTag(),
		acked:        make(chan struct{}),
	}
	c.CallDialog = CallDialog{c: &c.call, in: c, invite: inviteReceived, peerAllowsUpdate: req.Allows(UPDATE),
		peerInvite: &c.provisionals}
	c.sessionAnswer = session
	if len(req.Body) > 0 {
		c.offer = OfferRemote
	}
	c.dialogs = []*CallDialog{&c.CallDialog}
	c.kept = &c.CallDialog
	c.open()
	ua.spawn(c.watchCancel)
	go ua.cfg.OnCall(c)
}

// Handle has f answer the requests within the dialog that the UA leaves
// to the user of the call. f is called in a goroutine of its own for
// each, and answers with Accept or Reject. OnCall calls Handle before
// its first response, so that no request comes before it.
func (c *IncomingCall) Handle(f func(*InDialogRequest)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRequest = f
}

// RelayPRACK has the UA hand every PRACK of the caller to the handler
// of the call (Handle), with or without a body, where it otherwise
// answers a PRACK without a body with 200 itself. The handler answers it
// with Accept. A B2BUA that relays each reliable provisional response
// of its other leg, and the PRACK for it back (RFC 3262 4), needs the
// PRACK among the requests it relays in the order they came (Arrival):
// the caller may send an UPDATE right after the PRACK, and the far end
// has to get the two in that order. The user calls it before the first
// response, as Handle.
func (c *IncomingCall) RelayPRACK() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.relay = true
}

// SetContact has the responses of the call and its later messages carry
// contact instead of the UA's own Contact (RFC 3261 12.1.1), such as the
// conference URI with the isfocus feature parameter in the 200 of a
// conference focus (TS 24.147 5.3.2.3.1). With recordRoute the UA also
// puts its own URI on top of the Record-Route of each response that
// creates the dialog, so that the caller's requests to contact still
// pass the UA: a routeing B2BUA does this when it passes on the Contact
// of its other leg (TS 24.229 5.7.5.1). The user calls it before the
// first response; a later call changes only the messages after it.
func (c *IncomingCall) SetContact(contact NameAddr, recordRoute bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contact, c.recordRoute = contact.String(), recordRoute
}

// ErrCancelled is why an incoming call ends when the caller cancels it.
var ErrCancelled = errors.New("sip: the caller cancelled the call")

// watchCancel answers the INVITE with 487 (Request Terminated) when the
// caller cancels it before the final response (RFC 3261 9.2). The stack
// has answered the CANCEL with 200 already.
func (c *IncomingCall) watchCancel() {
	select {
	case <-c.st.Cancelled():
	case <-c.done:
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.final == nil {
		c.endedBy = c.st.CancelRequest()
		c.sendFailure(487, "CANCEL received", ErrCancelled, c.ua.own(INVITE, true, nil))
	}
}

// response builds a response to the INVITE. A response that creates the
// dialog, 101-199 or 2xx, copies the Record-Route values in order and
// carries Contact (RFC 3261 12.1.1). headers are those that the user of
// the call adds.
func (c *IncomingCall) response(code int, body Body, headers []HeaderField) *Message {
	resp := NewResponse(c.Request, code, c.tag)
	if code > 100 && code < 300 {
		if c.recordRoute {
			// The UA is the hop nearest to itself, so its own value comes
			// first, as the latest proxy's would (RFC 3261 16.6 step 4).
			resp.Add("Record-Route", c.ua.ownRoute())
		}
		for _, rr := range c.Request.GetAll("Record-Route") {
			resp.Add("Record-Route", rr)
		}
		resp.Add("Contact", c.contactLocked())
	}
	addHeaders(resp, headers)
	body.addTo(resp)
	return resp
}

// ErrAnswered is what Progress, Accept and Reject return once the INVITE
// has its final response.
var ErrAnswered = errors.New("sip: the INVITE has its final response already")

// Progress sends a provisional response from 101 to 199, such as 180
// (Ringing) or 183 (Session Progress) with an SDP answer for early
// media (RFC 3261 13.3.1.1). The first one creates the early dialog. A
// UAS that takes more than a minute to answer sends one every minute,
// so that proxies keep the INVITE going.
//
// When the INVITE offers or requires 100rel, the response goes reliably
// (RFC 3262 3): the UA retransmits it until the PRACK comes, and
// Progress returns then. After 64*T1 without a PRACK, the UA rejects
// the INVITE with 500 and Progress returns ErrNoPRACK. Without 100rel,
// Progress returns once the response is sent.
//
// headers go into the response, such as P-Early-Media, the Alert-Info
// of a waiting call, or the Require of an extension that the body uses.
func (c *IncomingCall) Progress(code int, body Body, headers ...HeaderField) error {
	switch {
	case code <= 100 || code >= 200:
		return fmt.Errorf("sip: %d is not a provisional response that Progress can send", code)
	case code == 199:
		return errors.New("sip: a 199 ends the early dialog; EarlyDialogTerminated sends it")
	}
	c.answering.Lock()
	defer c.answering.Unlock()
	return c.progress(code, body, headers, c.provisionals.reliable)
}

// progress sends a provisional response, reliably or not, and waits for
// the PRACK of a reliable one. c.answering is held.
func (c *IncomingCall) progress(code int, body Body, headers []HeaderField, reliable bool) error {
	c.mu.Lock()
	if c.final != nil {
		c.mu.Unlock()
		return ErrAnswered
	}
	resp := c.response(code, body, headers)
	if reliable {
		c.makeReliable(resp)
	}
	if c.dialog == nil {
		d, err := NewUASDialog(c.Request, resp)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		c.start(d, fmt.Sprintf("%d sent", code))
	}
	if reliable {
		c.sessionSent(body)
	}
	err := c.st.Respond(resp)
	pracked := c.pracked
	c.mu.Unlock()
	if err != nil || !reliable {
		return err
	}

	// RFC 3262 3: the interval doubles without the T2 cap of a 2xx,
	// since a PRACK is not sent again for each retransmission. 64*T1
	// passes before the interval could reach it.
	t := c.stack.cfg.Timers
	err = c.retransmit(c.st, resp, 64*t.T1, pracked, ErrNoPRACK)
	if errors.Is(err, ErrNoPRACK) {
		// RFC 3262 3: the UAS SHOULD reject the INVITE with a 5xx.
		c.mu.Lock()
		if c.final == nil {
			c.sendFailure(500, "no PRACK", ErrNoPRACK, nil)
		}
		c.mu.Unlock()
	}
	return err
}

// ErrNo199 is what EarlyDialogTerminated returns when the INVITE does
// not list 199 in Supported: the UAS must not send a 199 then (RFC 6228
// 5).
var ErrNo199 = errors.New("sip: the caller does not support 199")

// EarlyDialogTerminated ends the early dialog of the call with a 199
// (Early Dialog Terminated) response and leaves the INVITE pending (RFC
// 6228 5). The next response starts a new early dialog, with a To tag
// of its own, the way the responses of another UAS behind a forking
// proxy would. An AS does this when it turns from one callee to the
// next: the caller can let go of what it set up for the first one.
//
// cause is the status code that tells why the dialog ended; it goes
// into the Reason of the 199. The 199 goes unreliably, unless the
// INVITE requires 100rel.
func (c *IncomingCall) EarlyDialogTerminated(cause int, headers ...HeaderField) error {
	c.answering.Lock()
	defer c.answering.Unlock()
	c.mu.Lock()
	switch {
	case c.final != nil:
		c.mu.Unlock()
		return ErrAnswered
	case c.dialog == nil:
		c.mu.Unlock()
		return errors.New("sip: no early dialog to end")
	case !c.Request.Supports(Option199):
		c.mu.Unlock()
		return ErrNo199
	}
	c.mu.Unlock()
	reason := NewReason(ReasonSIP, cause, ReasonPhrase(cause)).Header()
	if err := c.progress(199, Body{}, append([]HeaderField{reason}, headers...), c.Request.Requires(Option100rel)); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.final != nil {
		return nil
	}
	// What follows belongs to a new dialog: a new To tag, and the offer
	// of the INVITE open again. The RSeq numbers go on, since they
	// count within the INVITE transaction (RFC 3262 3).
	c.setState(DialogTerminated, "199 sent")
	c.dialog, c.tag = nil, NewTag()
	c.offer = OfferNone
	if len(c.Request.Body) > 0 {
		c.offer = OfferRemote
	}
	c.updatePending, c.localSDP = false, Body{}
	return nil
}

// sessionSent is RFC 3261 13.2.1 and RFC 3262 5 for a session
// description in a reliable response to the INVITE. When the INVITE
// carried the offer, it is the answer. When the INVITE carried none, it
// is the offer, and the answer comes in the PRACK or the ACK. The same
// description in a response that is not reliable, or again in a later
// response, changes nothing. c.mu is held.
func (c *IncomingCall) sessionSent(body Body) {
	if len(body.Content) == 0 {
		return
	}
	switch c.offer {
	case OfferRemote:
		c.offerEvent(AnswerSent)
		c.sent(body)
	case OfferNone:
		c.offerEvent(OfferSent)
		c.sent(body)
	}
}

// ErrNoPRACK is what Progress returns, and why the call ends, when a
// reliable provisional response gets no PRACK within 64*T1 (RFC 3262
// 3).
var ErrNoPRACK = errors.New("sip: no PRACK for the reliable provisional response")

// makeReliable turns resp into a reliable provisional response, which
// waits for its PRACK (RFC 3262 3). The mu of the call is held.
func (p *provisionals) makeReliable(resp *Message) {
	if p.rseq == 0 {
		// The first RSeq is RECOMMENDED to be random from 1 to 2**31-1.
		p.rseq = rand.Uint32N(1<<31-1) + 1
	} else {
		p.rseq++
	}
	resp.AddOption("Require", Option100rel)
	resp.Add("RSeq", strconv.FormatUint(uint64(p.rseq), 10))
	p.unacked = resp
	p.pracked = make(chan struct{})
}

// receivePRACK takes a PRACK within the dialog.
func (d *CallDialog) receivePRACK(st *ServerTransaction) {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	before := *d.dialog
	if d.inOrder(st) && !d.acceptPRACK(st) {
		// RFC 3261 12.2.2: a rejected request changes no state.
		*d.dialog = before
	}
}

// acceptPRACK is RFC 3262 3: the PRACK that matches the reliable
// provisional response waiting for it, by RSeq and by the response's
// CSeq, stops the retransmissions. That is a response to the last
// INVITE of the peer in the dialog (peerInvite). Any other PRACK gets
// 481. It reports whether the PRACK matched. c.mu is held.
//
// A PRACK without a body gets its 200 here, unless the user relays the
// PRACKs (RelayPRACK). One with a body carries the answer to an offer
// in the response, or a new offer (RFC 3262 5), and goes to the user of
// the call, who reads the answer or gives the answer for the 200.
func (d *CallDialog) acceptPRACK(st *ServerTransaction) bool {
	c := d.c
	req := st.Request
	p := d.peerInvite
	rack, err := ParseRAck(req.Get("RAck"))
	if err != nil || p == nil || p.unacked == nil || rack != rackFor(p.unacked) {
		c.ua.respondOwn(st, NewResponse(req, 481, ""))
		return false
	}
	r := &InDialogRequest{Request: req, d: d, st: st, dialog: d.dialog}
	if len(req.Body) > 0 {
		if d.offer == OfferLocal {
			d.offerEvent(AnswerReceived)
		} else if err := d.offerEvent(OfferReceived); err != nil {
			// The offer comes before the answer to the last one.
			d.respond(st, 500, retryAfter())
			return false
		} else {
			r.Offer = true
		}
	}
	p.unacked = nil
	close(p.pracked)
	switch {
	case c.onRequest != nil && (len(req.Body) > 0 || p.relay):
		c.handOver(r)
	case len(req.Body) == 0:
		c.ua.respondOwn(st, NewResponse(req, 200, ""))
	case r.Offer:
		// The UA answers in a goroutine of its own, so a BYE can get there
		// first (endPending).
		d.addPending(r)
		c.ua.spawn(func() { r.Reject(488, append(c.ua.own(PRACK, true, nil), c.ua.noSessionChange())...) })
	default:
		c.ua.respondOwn(st, NewResponse(req, 200, ""))
	}
	return true
}

// rackFor is the RAck of a PRACK for resp.
func rackFor(resp *Message) RAck {
	rseq, _ := ParseRSeq(resp.Get("RSeq"))
	cseq, _ := ParseCSeq(resp.Get("CSeq"))
	return RAck{RSeq: rseq, CSeq: cseq}
}

// Accept answers the INVITE with 200 (OK), whose body is normally the
// SDP answer (RFC 3261 13.3.1.4). The UA core retransmits the 200 until
// the ACK arrives, and Accept returns the ACK. When no ACK comes within
// 64*T1, the UA ends the call with a BYE and Accept returns ErrNoACK.
// headers go into the 200.
func (c *IncomingCall) Accept(body Body, headers ...HeaderField) (*Message, error) {
	c.answering.Lock()
	defer c.answering.Unlock()
	c.mu.Lock()
	if c.final != nil {
		c.mu.Unlock()
		return nil, ErrAnswered
	}
	ok := c.response(200, body, headers)
	ok.Add("Allow", c.ua.allowHeader())
	ok.Add("Supported", c.ua.supportedHeader())
	addHeaders(ok, c.sessionAnswer.Headers)
	event := "200 sent"
	if c.dialog == nil {
		d, err := NewUASDialog(c.Request, ok)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.start(d, event)
	} else {
		c.setState(DialogConfirmed, event)
	}
	c.sessionSent(body)
	// RFC 4028 9: the session expiration counts from this 2xx.
	c.setSession(c.sessionAnswer.Timer)
	c.final = ok
	// The INVITE has its final response, so a re-INVITE may come before
	// the ACK (RFC 3261 14.2).
	c.invite = noInvite
	err := c.st.Respond(ok)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// RFC 6026 8.1, which replaces this paragraph of RFC 3261 13.3.1.4.
	t := c.stack.cfg.Timers
	if err := c.retransmit(c.st, ok, t.T2, c.acked, ErrNoACK); err != nil {
		if errors.Is(err, ErrNoACK) {
			// RFC 3261 13.3.1.4: the dialog is confirmed, but the
			// session SHOULD end with a BYE.
			if err := c.CallDialog.byeOwn(ErrNoACK); err != nil {
				c.ua.log.Warn("BYE after a missing ACK failed", "call_id", c.Request.Get("Call-ID"), "error", err.Error())
			}
		}
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ack, nil
}

// Reject answers the INVITE with a final response from 300 to 699, such
// as 486 (Busy Here) or 480 (Temporarily Unavailable) (RFC 3261
// 13.3.1.2, 13.3.1.3). The INVITE server transaction retransmits it
// until the ACK, and an early dialog ends (12.3). headers go into the
// response, such as the Warning that tells why, or the Contact of a
// 3xx.
func (c *IncomingCall) Reject(code int, headers ...HeaderField) error {
	if code < 300 || code > 699 {
		return fmt.Errorf("sip: %d is not a final response that Reject can send", code)
	}
	c.answering.Lock()
	defer c.answering.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.final != nil {
		return ErrAnswered
	}
	return c.sendFailure(code, fmt.Sprintf("%d sent", code), nil, headers)
}

// sendFailure sends a 300-699 response to the INVITE and ends the call
// with err. c.mu is held.
func (c *IncomingCall) sendFailure(code int, event string, err error, headers []HeaderField) error {
	c.final = c.response(code, Body{}, headers)
	c.end(event, err)
	return c.st.Respond(c.final)
}

// Bye ends the call after Accept. It waits for Accept to return: the
// callee MUST NOT send a BYE before the ACK has come or the 2xx has
// been given up, nor in an early dialog (RFC 3261 15). headers go into
// the BYE, such as a Reason (RFC 3326 2).
func (c *IncomingCall) Bye(headers ...HeaderField) error {
	c.answering.Lock()
	defer c.answering.Unlock()
	return c.CallDialog.bye(nil, headers)
}

// receiveACK takes the ACK for the 2xx, which stops the retransmissions
// of the 2xx. The ACK carries the CSeq number of the INVITE (RFC 3261
// 13.2.2.4).
func (c *IncomingCall) receiveACK(ack *Message) {
	cseq, err := ParseCSeq(ack.Get("CSeq"))
	invite, _ := ParseCSeq(c.Request.Get("CSeq"))
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || cseq.Number != invite.Number || c.final == nil || c.final.StatusCode >= 300 {
		c.ua.log.Info("discarded ACK that matches no 2xx", "call_id", ack.Get("Call-ID"), "cseq", ack.Get("CSeq"))
		return
	}
	if c.ack == nil {
		if len(ack.Body) > 0 && c.offer == OfferLocal {
			// The 2xx carried the offer, and the ACK has the answer (RFC
			// 3261 13.2.2.4).
			c.offerEvent(AnswerReceived)
		}
		c.ack = ack
		close(c.acked)
	}
}
