package b2bua

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Call is one INVITE that a B2BUA relays, from its arrival to the end of
// the call. in is the dialog with the caller; out, once Attempt has sent
// the INVITE on, is the dialog with the callee.
//
// Each leg's requests within its dialogs go on to the other leg in the
// order they came (turns): the caller's in one line, and the callee's
// of each outgoing leg in a line of their own, so that a request of one
// side never waits for one of the other.
type Call struct {
	in    *sip.IncomingCall
	hooks Hooks
	log   *slog.Logger
	// callerTurns orders the caller's requests.
	callerTurns *turns

	// answered is set once the callee's 2xx has come, and confirmed once
	// the caller has acknowledged the 2xx that the Call passed on: only
	// from then on are both dialogs of the call confirmed, and the role
	// gets them (keepDialogs).
	answered  atomic.Bool
	confirmed atomic.Bool
	// outReady is closed once there is an outgoing leg, or once the
	// role has said that there will be none (NoLeg).
	outReady chan struct{}
	ready    sync.Once

	mu  sync.Mutex
	out *sip.OutgoingCall
	// copies are the reliable provisional responses that the Call sent
	// the caller, and whose PRACK it has not yet taken, in the order
	// they went. The caller acknowledges them in that order, one PRACK
	// for each (RFC 3262 3), and the PRACK of each copy of a reliable
	// response of the callee goes on as the PRACK of that response.
	copies []*copied
	// sessionToCaller tells that a session description went to the
	// caller in a reliable provisional response.
	sessionToCaller bool
	// settled tells that an offer of the caller's or of the callee's own
	// has crossed the confirmed dialogs since the callee answered: the
	// two ends have settled the session between them, and the Call
	// offers the caller no session of its own after that (OfferCaller).
	settled bool
	early   earlyDialogs
}

// copied is a reliable provisional response that the Call sent the
// caller. of is the response of the callee that it copies, when the
// callee wants that one acknowledged too (RFC 3262 4); nil when it does
// not, for a response that came without 100rel. own tells that the role
// sent the response itself (Progress), so that it copies no response of
// the callee at all.
type copied struct {
	of  *sip.Message
	own bool
}

// New starts the Call of in, an INVITE that the B2BUA has taken, and
// has it relay the requests of the caller within the dialog, the PRACKs
// among them. The role calls it before the first response to in, and
// Attempt or NoLeg next.
func New(in *sip.IncomingCall, hooks Hooks, log *slog.Logger) *Call {
	c := &Call{in: in, hooks: hooks, log: log, callerTurns: newTurns(), outReady: make(chan struct{}),
		early: earlyDialogs{last: map[string]*sip.Message{}}}
	in.Handle(c.fromCaller)
	in.RelayPRACK()
	return c
}

// Attempt sends inv on ua toward the callee: the INVITE of the outgoing
// leg. The Call relays the requests of the callee within its dialogs,
// and the PRACK of each reliable provisional response is the caller's,
// which the Call passes on. The responses to the INVITE are the role's
// to read, and it passes them on with RelayProvisional and Answer.
//
// RFC 3261 9.1: when the caller's dialog ends before the callee
// answers, the Call cancels the INVITE, for the same reason (RFC 3326
// 2).
func (c *Call) Attempt(ua *sip.UA, inv sip.Invitation) (*sip.OutgoingCall, error) {
	// The callee's requests of this leg, and its end once Invite has
	// returned, which they watch while they wait for their turn.
	calleeTurns := newTurns()
	var sent *sip.OutgoingCall
	inv.ManualPRACK = true
	inv.OnRequest = func(r *sip.InDialogRequest) {
		c.mu.Lock()
		out := sent
		c.mu.Unlock()
		c.fromCallee(r, out, calleeTurns)
	}
	out, err := ua.Invite(inv)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.out, sent = out, out
	c.mu.Unlock()
	c.ready.Do(func() { close(c.outReady) })
	go func() {
		select {
		case <-c.in.Done():
			if !c.answered.Load() {
				out.Cancel(c.reasons(c.in.EndedBy(), ToCallee)...)
			}
		case <-out.Done():
		}
	}()
	return out, nil
}

// NoLeg tells the Call that the role sends no INVITE toward the callee,
// such as when it could not: the requests of the caller within the
// dialog then find no dialog to go on in.
func (c *Call) NoLeg() {
	c.ready.Do(func() { close(c.outReady) })
}

// Progress sends the caller a provisional response of the role's own,
// from 101 to 199: one that the role makes itself instead of passing a
// response of the callee on, such as the 181 of a call that a service
// forwards, or the 183 that carries the session of an announcement or
// of a tone of the MRF (TS 24.182 4.5.5.3.7). The first response gives
// the caller an early dialog, whether or not the role sends an INVITE
// toward the callee (NoLeg).
//
// A reliable response waits for its PRACK, as sip.IncomingCall.Progress
// does. The Call answers that PRACK with 200 itself, since no response
// of the callee waits for it (RFC 3262 4), and keeps the response in
// the order in which the caller acknowledges them: the PRACK of a copy
// of a response of the callee, which the role may relay meanwhile,
// still goes on as the PRACK of that response.
//
// A session description in body after an INVITE without an offer is the
// role's own offer, and the caller answers it in that PRACK (RFC 3262
// 5). The Call takes that answer no further, since the offer was not
// the callee's: a role that offers the caller a session of its own,
// such as the media of the MRF, answers the caller's offer from that
// media instead (OfferCaller takes the caller off it again). A new
// offer in the PRACK does go on to the callee, as one in any other
// request within the dialog does.
func (c *Call) Progress(code int, body sip.Body, headers ...sip.HeaderField) error {
	c.mu.Lock()
	// RFC 3262 3: the response goes reliably when the caller's INVITE
	// offered 100rel, and only then does the caller send a PRACK for it.
	var sent *copied
	if c.in.Request.Supports(sip.Option100rel) {
		sent = &copied{own: true}
		c.copies = append(c.copies, sent)
		if len(body.Content) > 0 {
			// RFC 3262 5: the offer and the answer of the INVITE are done
			// on the caller's leg, so a later response of the callee
			// carries no further offer to it.
			c.sessionToCaller = true
		}
	}
	c.mu.Unlock()
	// The session description goes out with the response, which a
	// reliable one waits for the PRACK of: the next one that the Call
	// sends goes on from this one (sentToCaller), whether that PRACK
	// comes or not.
	c.sentToCaller(body)
	if err := c.in.Progress(code, body, headers...); err != nil {
		// No PRACK comes for a response that did not go.
		c.mu.Lock()
		c.copies = slices.DeleteFunc(c.copies, func(k *copied) bool { return k == sent })
		c.mu.Unlock()
		return err
	}
	return nil
}

// RelayProvisional passes resp, a provisional response of out, on to
// the caller. A reliable one is acknowledged on out with the PRACK of
// the caller for its copy (fromCaller), so that the PRACK goes from end
// to end as it would without the B2BUA: with preconditions the caller
// and the callee see each other's 183 and PRACK (RFC 3262, RFC 3312).
func (c *Call) RelayProvisional(out *sip.OutgoingCall, resp *sip.Message) {
	reliable := resp.Requires(sip.Option100rel)
	if resp.StatusCode == 199 {
		// RFC 6228: one of the early dialogs of out has ended. They are
		// all one early dialog on in, which goes on.
		if reliable {
			out.PRACK(resp, sip.Body{})
		}
		return
	}
	m := c.hooks.Response(c.toCaller(out, resp), c.in.Request, ToCaller)
	c.mu.Lock()
	// RFC 3262 5: after an INVITE without an offer, the first reliable
	// response with a body has the callee's offer, and the PRACK the
	// caller's answer.
	offer := reliable && len(m.Body.Content) > 0 && len(c.in.Request.Body) == 0 && !c.sessionToCaller
	if reliable && len(m.Body.Content) > 0 {
		c.sessionToCaller = true
	}
	// RFC 3262 3: the copy goes reliably when the caller's INVITE
	// offered 100rel, whichever way resp came, and only then does the
	// caller send a PRACK for it.
	var sent *copied
	if c.in.Request.Supports(sip.Option100rel) {
		sent = &copied{}
		if reliable {
			sent.of = resp
		}
		c.copies = append(c.copies, sent)
	}
	c.mu.Unlock()
	c.setContact(resp)
	c.sentToCaller(m.Body)
	if err := c.in.Progress(resp.StatusCode, m.Body, m.Headers...); err != nil {
		c.log.Info("provisional response not relayed", "status", resp.StatusCode, "error", err.Error())
		// No PRACK comes for a copy that did not go.
		c.mu.Lock()
		c.copies = slices.DeleteFunc(c.copies, func(k *copied) bool { return k == sent })
		c.mu.Unlock()
	} else if len(m.Body.Content) > 0 {
		c.noteCallerDialog()
	}
	if reliable && sent == nil && !offer {
		// The caller sends no PRACK, so the B2BUA sends its own. The
		// PRACK of a response with the callee's offer has to carry the
		// answer, which only the caller has.
		if _, err := out.PRACK(resp, sip.Body{}); err != nil {
			c.log.Warn("PRACK not sent", "status", resp.StatusCode, "error", err.Error())
		}
	}
}

// acknowledged is the response of the callee whose copy the caller's
// PRACK, the next in the order they came, acknowledges: nil when that
// copy was of a response that the callee wants no PRACK for. own tells
// that the response was the role's own (Progress), and copies none of
// the callee's.
func (c *Call) acknowledged() (acked *sip.Message, own bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.copies) == 0 {
		return nil, false
	}
	next := c.copies[0]
	c.copies = c.copies[1:]
	return next.of, next.own
}

// Answer passes resp, the 2xx of out, on to the caller, and then the
// requests within the dialogs both ways, until one side hangs up; the
// other side then gets a BYE from the B2BUA, with the Reason of the
// first (RFC 3326 2). That BYE is also what ends the other leg when one
// leg fails: a caller that never acknowledges the 2xx, or a dialog that
// the far end no longer answers in (RFC 3261 12.2.1.2), ends its leg,
// and the Call ends the other one.
func (c *Call) Answer(out *sip.OutgoingCall, resp *sip.Message) {
	c.answered.Store(true)
	toCaller, session := c.answerFrom(resp)
	m := c.hooks.Answer(toCaller, session)
	c.setContact(resp)
	c.sentToCaller(m.Body)
	ack, err := c.in.Accept(m.Body, m.Headers...)
	if err != nil {
		// No ACK came, and the UA has sent the caller a BYE (RFC 3261
		// 13.3.1.4); or the caller cancelled as the 2xx came.
		c.log.Info("the caller did not take the answer", "error", err.Error())
		out.Bye()
		return
	}
	c.confirmed.Store(true)
	if len(ack.Body) > 0 {
		// The 2xx carried the callee's offer, after an INVITE without
		// one, and the caller's ACK the answer (RFC 3261 13.2.2.4).
		if err := out.Ack(c.hooks.Ack(ack, ToCallee)); err != nil {
			c.log.Warn("answer in the ACK not relayed", "error", err.Error())
		}
	}
	if session != nil {
		// The caller has the session description of another early dialog
		// of the callee's side, and ignores a second one (RFC 3261
		// 13.2.1): it gets the session of the dialog that answered in an
		// offer of the Call's own, now that it has acknowledged the 2xx.
		c.moveSession(out, toTag(resp), len(c.in.Request.Body) == 0 && len(resp.Body) > 0)
	}
	c.keepDialogs()
	c.log.Info("call answered")
	select {
	case <-c.in.Done():
		out.Bye(c.reasons(c.in.EndedBy(), ToCallee)...)
	case <-out.Done():
		c.in.Bye(c.reasons(out.EndedBy(), ToCaller)...)
	}
	c.log.Info("call ended")
}

// reasons are the Reason header fields of ended, the request that ended
// one leg, for the request that ends the other in direction d; none
// when the leg ended without a request of its peer.
func (c *Call) reasons(ended *sip.Message, d Direction) []sip.HeaderField {
	if ended == nil {
		return nil
	}
	return c.hooks.Reasons(ended, d)
}

// setContact has the responses to the caller carry the Contact that the
// role gives for the callee's Contact in resp.
func (c *Call) setContact(resp *sip.Message) {
	if contact, recordRoute, ok := c.hooks.Contact(resp); ok {
		c.in.SetContact(contact, recordRoute)
	}
}

// keepDialogs hands the two dialogs to the role once the caller has
// acknowledged the answer. A request of the callee that crosses the 2xx
// and the ACK hands it nothing: the dialog of the caller does not hold
// until the ACK has come (RFC 3261 13.3.1.4), and the role would keep a
// call that may never be.
func (c *Call) keepDialogs() {
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	if out == nil || !c.confirmed.Load() {
		return
	}
	in, okIn := c.in.Dialog()
	o, okOut := out.Dialog()
	if !okIn || !okOut {
		return
	}
	c.hooks.KeepDialogs(in, o)
}

// leg is what the two legs of a call have in common: both an
// IncomingCall and an OutgoingCall are one.
type leg interface {
	Update(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	ReInvite(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Send(method sip.Method, body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Ack(body sip.Body, headers ...sip.HeaderField) error
}

// fromCaller handles the requests of the caller within the dialog of
// in, which go on to out in the order they came.
func (c *Call) fromCaller(r *sip.InDialogRequest) {
	defer c.keepDialogs()
	finish := c.callerTurns.take(r.Arrival)
	defer finish()
	req := r.Request
	answered := false
	if req.Method == sip.PRACK && len(req.Body) == 0 {
		// The caller has the 200 at once, as from a UA that answers the
		// PRACK itself; a 200 that waited for the callee's would have the
		// caller send the PRACK again meanwhile (RFC 3261 17.1.2.2). The
		// PRACK still takes its turn below, so that the callee gets it
		// before a request that the caller sends after it.
		r.Accept(sip.Body{})
		answered = true
	}
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	var outDone <-chan struct{}
	if out != nil {
		outDone = out.Done()
	}
	if !c.callerTurns.wait(r.Arrival, c.in.Done(), outDone) {
		if req.Method == sip.PRACK {
			// The copy that this PRACK acknowledges leaves the order in
			// which the caller acknowledges them, so that the PRACK after
			// it does not take that copy for its own (acknowledged). A role
			// that sends the INVITE on to another callee (Attempt again)
			// goes on relaying the caller's requests after the leg that this
			// one gave up for has ended.
			c.acknowledged()
		}
		if !answered {
			r.Reject(481)
		}
		return
	}
	var acked *sip.Message
	var own bool
	if req.Method == sip.PRACK {
		acked, own = c.acknowledged()
	}
	select {
	case <-c.outReady:
	case <-c.in.Done():
	}
	c.mu.Lock()
	out = c.out
	c.mu.Unlock()
	switch {
	case acked != nil && out != nil:
		c.prack(r, out, acked, answered, finish)
	case own && !r.Offer:
		// The PRACK of a response that the role sent itself (Progress):
		// the Call answers it (RFC 3262 4), and a session description in
		// it answers the role's own offer, so none of it goes to the
		// callee. A new offer goes on as any other does, below.
		if !answered {
			r.Accept(sip.Body{})
		}
	case answered:
		// The PRACK of a copy of a response that the callee wants no
		// PRACK for: the caller has its 200, and the PRACK ends here.
	case out == nil:
		c.answerWithoutALeg(r)
	default:
		c.forward(r, out, ToCallee, finish)
	}
}

// answerWithoutALeg answers a request of the caller that the call has
// no leg to send on: the role sent no INVITE toward the callee (NoLeg)
// and gave the caller an early dialog of its own all the same, such as
// the one in which it plays it an announcement before it rejects the
// INVITE. A request left unanswered would stay at the caller until its
// transaction times out.
//
// The dialog is there, so an UPDATE gets no 481, which would end it
// along with the announcement (RFC 3261 12.2.1.2): the role keeps the
// session it gave the caller, so a new offer gets 488, whether it comes
// in an UPDATE or in the PRACK of a reliable provisional response (RFC
// 3262 5), while an UPDATE without one changes nothing and gets 200.
// Any other request gets 481, as there is neither a leg nor a dialog
// for it to go on in.
func (c *Call) answerWithoutALeg(r *sip.InDialogRequest) {
	switch {
	case r.Offer:
		r.Reject(488)
	case r.Request.Method == sip.UPDATE:
		r.Accept(sip.Body{})
	default:
		r.Reject(481)
	}
}

// prack sends r, the caller's PRACK for the copy of acked, on to the
// callee as the PRACK of acked (RFC 3262 4), with the caller's answer to
// an offer in acked, or a new offer, when it has one (RFC 3262 5).
// answered tells that the caller has its 200 already.
func (c *Call) prack(r *sip.InDialogRequest, out *sip.OutgoingCall, acked *sip.Message, answered bool, finish func()) {
	m, refused := c.hooks.Request(r.Request, ToCallee)
	switch {
	case refused != nil && answered:
		c.log.Info("PRACK not relayed", "status", refused.Status)
		return
	case refused != nil:
		r.Reject(refused.Status, refused.Headers...)
		return
	}
	resp, err := out.PRACK(acked, m.Body, m.Headers...)
	finish()
	if !answered {
		c.answerRequest(r, m.Body, resp, err, out, ToCallee)
		return
	}
	if err != nil {
		c.log.Warn("PRACK not relayed", "error", err.Error())
	} else if resp.StatusCode >= 300 {
		c.log.Warn("the callee refused the PRACK", "status", resp.StatusCode)
	}
}

// fromCallee handles the requests of the callee within a dialog of out,
// which go on to the caller in the order they came. out is nil when the
// request comes before Invite has returned.
func (c *Call) fromCallee(r *sip.InDialogRequest, out *sip.OutgoingCall, t *turns) {
	defer c.keepDialogs()
	finish := t.take(r.Arrival)
	defer finish()
	var outDone <-chan struct{}
	if out != nil {
		outDone = out.Done()
	}
	if !t.wait(r.Arrival, c.in.Done(), outDone) {
		r.Reject(481)
		return
	}
	c.forward(r, c.in, ToCaller, finish)
}

// forward passes r, which crosses in direction d, on to the other leg,
// and the final response back. Once the other leg has the final
// response, the next request may go (finish); a re-INVITE without an
// offer holds its turn until its ACK has gone on with the answer, since
// a request after it would find the offer of the 2xx still open on the
// other leg (RFC 3261 14.1, RFC 3264).
func (c *Call) forward(r *sip.InDialogRequest, to leg, d Direction, finish func()) {
	req := r.Request
	m, refused := c.hooks.Request(req, d)
	if refused != nil {
		r.Reject(refused.Status, refused.Headers...)
		return
	}
	var resp *sip.Message
	var err error
	switch req.Method {
	case sip.UPDATE:
		if len(m.Body.Content) == 0 {
			// RFC 4028 9: an UPDATE without a body refreshes the session
			// of its own dialog. Each leg has its own session timer, so
			// the B2BUA answers it.
			r.Accept(sip.Body{})
			return
		}
		resp, err = to.Update(m.Body, m.Headers...)
	case sip.INVITE:
		resp, err = to.ReInvite(m.Body, m.Headers...)
	case sip.PRACK:
		// A PRACK with a new offer, for a copy of a response that the
		// callee wanted no PRACK for (RFC 3262 5). The offer goes on in
		// an UPDATE (RFC 3311 5.1).
		resp, err = to.Update(m.Body, m.Headers...)
	default:
		resp, err = to.Send(req.Method, m.Body, m.Headers...)
	}
	if req.Method != sip.INVITE || r.Offer || err != nil || resp.StatusCode >= 300 {
		finish()
	}
	c.answerRequest(r, m.Body, resp, err, to, d)
}

// changesSession reports whether r, a request within a dialog, settles
// the session of the two dialogs between the two ends: a re-INVITE,
// whose offer is in it or in the 2xx to it (RFC 3261 14.1), or any
// other request with an offer, such as an UPDATE (RFC 3311 5.1). A
// re-INVITE that only refreshes the session counts as well, with the
// same offer and answer as before (RFC 4028 7.4): the two ends have
// settled the session between them all the same.
func changesSession(r *sip.InDialogRequest) bool {
	return r.Request.Method == sip.INVITE || r.Offer
}

// settledByTheEnds notes that r, a request of the caller's or of the
// callee's own, has settled the session of the two dialogs between the
// two ends (changesSession): the Call offers the caller no session of
// its own after that (OfferCaller). Only a request that the other end
// answered with a 2xx settles anything; one that it refused leaves the
// session as it was.
func (c *Call) settledByTheEnds(r *sip.InDialogRequest) {
	if !c.answered.Load() || !changesSession(r) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settled = true
}

// answerRequest answers r, a request within a dialog that crossed in
// direction d to the other leg with the body sent, with what that leg
// answered.
func (c *Call) answerRequest(r *sip.InDialogRequest, sent sip.Body, resp *sip.Message, err error, to leg, d Direction) {
	switch {
	case errors.Is(err, sip.ErrInviteInProgress), errors.Is(err, sip.ErrOfferPending):
		// RFC 3261 14.2, RFC 3311 5.2: the request crossed one of the
		// other side.
		r.Reject(491)
	case errors.Is(err, sip.ErrCallEnded):
		r.Reject(481)
	case err != nil:
		c.log.Info("request within the dialog not relayed", "method", string(r.Request.Method), "error", err.Error())
		r.Reject(500)
	case resp.StatusCode < 300:
		if d == ToCaller {
			// The caller took the session description of the request.
			c.sentToCaller(sent)
		}
		m := c.hooks.Response(resp, r.Request, d.reverse())
		if d == ToCallee {
			// The response goes to the caller, which sent the request.
			c.sentToCaller(m.Body)
		}
		offerless := r.Request.Method == sip.INVITE && !r.Offer
		if !offerless {
			c.settledByTheEnds(r)
		}
		ack, err := r.Accept(m.Body, m.Headers...)
		if err == nil && offerless && ack != nil {
			// A re-INVITE without an offer: the 2xx had the other side's
			// offer, and this side's ACK has the answer (RFC 3261 14.1).
			// Only with that answer have the two ends settled the session.
			body := c.hooks.Ack(ack, d)
			if d == ToCaller {
				// The ACK goes to the caller, which the re-INVITE reached.
				c.sentToCaller(body)
			}
			to.Ack(body)
			c.settledByTheEnds(r)
		}
	default:
		m := c.hooks.Response(resp, r.Request, d.reverse())
		r.Reject(resp.StatusCode, m.Headers...)
	}
}
