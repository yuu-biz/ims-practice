package b2bua

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Call is one INVITE that a B2BUA relays, from its arrival to the end of
// the call. in is the dialog with the caller; out, once Attempt has sent
// the INVITE on, is the dialog with the callee.
type Call struct {
	in    *sip.IncomingCall
	hooks Hooks
	log   *slog.Logger

	// answered is set once the callee's 2xx has come.
	answered atomic.Bool
	// outReady is closed once there is an outgoing leg, or once the
	// role has said that there will be none (NoLeg).
	outReady chan struct{}
	ready    sync.Once

	mu  sync.Mutex
	out *sip.OutgoingCall
	// pending is the reliable provisional response of out that carried
	// the callee's offer: the caller's answer comes in its PRACK, which
	// the Call sends on in the PRACK of pending (RFC 3262 5).
	pending *sip.Message
	// prackRelayed is closed once that PRACK has gone on to out. The UA
	// hands each request of the caller to a goroutine of its own, and
	// the role may take its time to prepare the PRACK (Hooks.Request),
	// so a request that the caller sent after it, without waiting for
	// its answer, could reach out first; it waits for prackRelayed
	// instead, and the far end gets the two in the order the caller
	// sent them. Only a request whose goroutine starts after the
	// PRACK's has taken pending waits: the UA tells nothing of the order
	// in which they came. A PRACK without a body the UA answers itself,
	// and RelayProvisional sends it on with no such wait.
	prackRelayed chan struct{}
	// sessionToCaller tells that a session description went to the
	// caller in a reliable provisional response.
	sessionToCaller bool
	early           earlyDialogs
}

// New starts the Call of in, an INVITE that the B2BUA has taken, and
// has it relay the requests of the caller within the dialog. The role
// calls it before the first response to in, and Attempt or NoLeg next.
func New(in *sip.IncomingCall, hooks Hooks, log *slog.Logger) *Call {
	c := &Call{in: in, hooks: hooks, log: log, outReady: make(chan struct{}), early: earlyDialogs{last: map[string]*sip.Message{}}}
	in.Handle(c.fromCaller)
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
	inv.ManualPRACK = true
	inv.OnRequest = c.fromCallee
	out, err := ua.Invite(inv)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.out = out
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

// RelayProvisional passes resp, a provisional response of out, on to
// the caller. A reliable one is acknowledged on out once the caller has
// acknowledged it on in, so that the PRACK goes from end to end as it
// would without the B2BUA: with preconditions the caller and the callee
// see each other's 183 and PRACK (RFC 3262, RFC 3312).
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
	m := c.hooks.Response(c.toCaller(resp), c.in.Request, ToCaller)
	c.mu.Lock()
	// RFC 3262 5: after an INVITE without an offer, the first reliable
	// response with a body has the callee's offer, and the PRACK the
	// caller's answer.
	offer := reliable && len(m.Body.Content) > 0 && len(c.in.Request.Body) == 0 && !c.sessionToCaller
	if offer {
		c.pending = resp
	}
	if reliable && len(m.Body.Content) > 0 {
		c.sessionToCaller = true
	}
	c.mu.Unlock()
	c.setContact(resp)
	if err := c.in.Progress(resp.StatusCode, m.Body, m.Headers...); err != nil {
		c.log.Info("provisional response not relayed", "status", resp.StatusCode, "error", err.Error())
	}
	if reliable && !offer {
		if _, err := out.PRACK(resp, sip.Body{}); err != nil {
			c.log.Warn("PRACK not sent", "status", resp.StatusCode, "error", err.Error())
		}
	}
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
	m := c.hooks.Answer(c.answerFrom(resp))
	c.setContact(resp)
	ack, err := c.in.Accept(m.Body, m.Headers...)
	if err != nil {
		// No ACK came, and the UA has sent the caller a BYE (RFC 3261
		// 13.3.1.4); or the caller cancelled as the 2xx came.
		c.log.Info("the caller did not take the answer", "error", err.Error())
		out.Bye()
		return
	}
	if len(ack.Body) > 0 {
		// The 2xx carried the callee's offer, after an INVITE without
		// one, and the caller's ACK the answer (RFC 3261 13.2.2.4).
		if err := out.Ack(c.hooks.Ack(ack, ToCallee)); err != nil {
			c.log.Warn("answer in the ACK not relayed", "error", err.Error())
		}
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

// keepDialogs hands the two dialogs to the role once the call is
// answered.
func (c *Call) keepDialogs() {
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	if out == nil || !c.answered.Load() {
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
// in, which go on to out.
func (c *Call) fromCaller(r *sip.InDialogRequest) {
	defer c.keepDialogs()
	<-c.outReady
	c.mu.Lock()
	out := c.out
	pending := c.pending
	before := c.prackRelayed
	if r.Request.Method == sip.PRACK && pending != nil {
		c.pending = nil
		c.prackRelayed = make(chan struct{})
		defer close(c.prackRelayed)
	}
	c.mu.Unlock()
	if out == nil {
		r.Reject(481)
		return
	}
	if r.Request.Method == sip.PRACK && pending != nil {
		// The PRACK with the caller's answer to the callee's offer goes
		// on as the PRACK of the callee's response (RFC 3262 5).
		m, refused := c.hooks.Request(r.Request, ToCallee)
		if refused != nil {
			r.Reject(refused.Status, refused.Headers...)
			return
		}
		resp, err := out.PRACK(pending, m.Body, m.Headers...)
		c.answerRequest(r, resp, err, out, ToCallee)
		return
	}
	if before != nil {
		select {
		case <-before:
		case <-c.in.Done():
		}
	}
	c.forward(r, out, ToCallee)
}

// fromCallee handles the requests of the callee within a dialog of out,
// which go on to the caller.
func (c *Call) fromCallee(r *sip.InDialogRequest) {
	defer c.keepDialogs()
	c.forward(r, c.in, ToCaller)
}

// forward passes r, which crosses in direction d, on to the other leg,
// and the final response back.
func (c *Call) forward(r *sip.InDialogRequest, to leg, d Direction) {
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
		// A PRACK with a new offer, after the callee's response had the
		// answer (RFC 3262 5). The B2BUA has acknowledged that response
		// on the other leg already, so the offer goes on in an UPDATE
		// (RFC 3311 5.1).
		resp, err = to.Update(m.Body, m.Headers...)
	default:
		resp, err = to.Send(req.Method, m.Body, m.Headers...)
	}
	c.answerRequest(r, resp, err, to, d)
}

// answerRequest answers r, a request within a dialog that crossed in
// direction d to the other leg, with what that leg answered.
func (c *Call) answerRequest(r *sip.InDialogRequest, resp *sip.Message, err error, to leg, d Direction) {
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
		m := c.hooks.Response(resp, r.Request, d.reverse())
		ack, err := r.Accept(m.Body, m.Headers...)
		if err == nil && r.Request.Method == sip.INVITE && !r.Offer && ack != nil {
			// A re-INVITE without an offer: the 2xx had the other side's
			// offer, and this side's ACK has the answer (RFC 3261 14.1).
			to.Ack(c.hooks.Ack(ack, d))
		}
	default:
		m := c.hooks.Response(resp, r.Request, d.reverse())
		r.Reject(resp.StatusCode, m.Headers...)
	}
}
