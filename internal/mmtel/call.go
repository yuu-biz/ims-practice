package mmtel

import (
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// call is one INVITE that the S-CSCF sent the AS, from its arrival to
// its end. in is the leg toward the S-CSCF that sent it, on the side of
// the caller; out is the leg that the AS sent back to the S-CSCF, on
// the side of the callee. A diversion replaces out with a leg to the
// diverted-to user, while in stays.
type call struct {
	as     *AS
	in     *sip.IncomingCall
	served sip.PServedUser
	// services are the settings of the served user's services, as the
	// HSS had them when the INVITE came.
	services simservs.MMTelServices
	// route is the Route of the requests the AS sends outside a dialog:
	// the received Route without the AS's own (TS 24.229 5.7.5.2.1
	// steps 1 and 4), which holds the S-CSCF with its original dialog
	// identifier.
	route []string
	log   *slog.Logger

	mu sync.Mutex
	// out is the outgoing leg the call goes on in. outReady is closed
	// once there is one.
	out      *sip.OutgoingCall
	outReady chan struct{}
	// pending is the reliable provisional response of out that carried
	// the callee's offer: the caller's answer comes in its PRACK, which
	// the AS sends on in the PRACK of pending (RFC 3262 5).
	pending *sip.Message
	// sessionToCaller tells that a session description went to the
	// caller in a reliable provisional response, after which the offer
	// and answer of the INVITE are done on in.
	sessionToCaller bool
	// toCaller tells that a provisional response went to the caller, so
	// that in has an early dialog.
	toCaller bool
	// withhold has the AS keep the callee's answer until the 2xx, which
	// then carries heldAnswer (withholdSession).
	withhold   bool
	heldAnswer sip.Body
	// acted are the services that acted on the call, for its record.
	acted []string
	// cat is the media of the MRF that the caller has until the callee
	// answers: the customized alerting tone (startAlertingTone), or the
	// announcement of a waiting call (announceWaiting). nil when there
	// is none. Only the goroutine of the call's legs reads and sets it.
	cat *alertingTone
	// calleeSDP is the last SDP of the callee, which the caller gets
	// when the callee answers after early media of the MRF (TS 24.182
	// 4.5.5.3.7).
	calleeSDP []byte
}

// serve is UAConfig.OnCall: an INVITE from the S-CSCF.
func (as *AS) serve(in *sip.IncomingCall) {
	req := in.Request
	log := as.log.With("call_id", req.Get("Call-ID"))
	// TS 24.229 5.7.1.3A: the S-CSCF names the served user in
	// P-Served-User, and this S-CSCF always adds the session case. A
	// request without one did not come from the S-CSCF.
	served, err := sip.ParsePServedUser(req.Get("P-Served-User"))
	if err != nil || served.SessionCase == sip.SessionCaseNone {
		log.Info("INVITE without a served user and its session case", "p_served_user", req.Get("P-Served-User"))
		in.Reject(403, as.warning("No served user"))
		return
	}
	// Step 1 of TS 24.229 5.7.5.2.1: the top Route names the AS, since
	// that is how the request got here.
	route := req.GetAll("Route")
	if len(route) < 2 {
		log.Info("INVITE without a Route back to the S-CSCF", "route", route)
		in.Reject(403, as.warning("No route back to the S-CSCF"))
		return
	}
	services, err := as.services(served.URI)
	if err != nil {
		// Without the settings, no service can be given as the user set
		// it up. A 5xx before any provisional response lets the S-CSCF
		// apply the default handling of the filter criterion (TS 24.229
		// 5.4.3.2 step 5, 5.4.3.3 step 4): go on without the AS, or end
		// the call.
		log.Warn("service data not read from the HSS", "served_user", served.URI, "error", err.Error())
		in.Reject(500, as.warning("Service data unavailable"))
		return
	}
	c := &call{as: as, in: in, served: served, services: services, route: route[1:], outReady: make(chan struct{}),
		log: log.With("served_user", served.URI, "session_case", string(served.SessionCase))}
	in.Handle(c.fromCaller)
	c.log.Info("call", "request_uri", req.RequestURI, "regstate", served.RegState)
	c.run()
}

// warning is the Warning header field of a response of the AS: code
// 399, the miscellaneous warning (RFC 3261 20.43), with the AS as the
// agent.
func (as *AS) warning(text string) sip.HeaderField {
	return sip.HeaderField{Name: "Warning", Value: "399 " + as.cfg.Host + " " + strconv.Quote(text)}
}

// newInvitation is the INVITE of a new outgoing leg to requestURI. It
// copies what TS 24.229 5.7.5.2.1 has a routeing B2BUA copy: the rest
// of the Route (step 4), the P-Asserted-Identity (step 5), and the
// other header fields that no service changes (5.7.5.1). The callee
// also sees the caller's Supported, Allow and session timer, so that it
// uses no extension that the caller does not take.
//
// Each leg is a dialog of its own, and RFC 4028 runs a session timer
// per dialog. The callee is asked for the interval the caller asked
// for. On the outgoing leg the AS is the UAC, so when the callee leaves
// the refreshes to the UAC, the AS refreshes that leg; on the incoming
// leg the caller refreshes as it would without the AS. A refresh within
// one leg ends at the AS (forward).
//
// The services change the header fields in fields, which go into the
// Invitation once they are done.
func (c *call) newInvitation(requestURI string) (sip.Invitation, *sip.Message) {
	req := c.in.Request
	fields := carried(req, "Allow", "Supported", "Session-Expires", "Min-SE")
	if pcv := chargingVector(req); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	// The AS takes away one hop, as a proxy would (RFC 3261 16.6 step 3),
	// so that a call that the services send round in a loop ends.
	hops := req.Clone()
	sip.DecrementMaxForwards(hops)
	fields.Set("Max-Forwards", hops.Get("Max-Forwards"))
	c.replacesOnTheOtherSide(fields)
	from, _ := sip.ParseNameAddr(req.Get("From"))
	to, _ := sip.ParseNameAddr(req.Get("To"))
	contact, fromFocus := focusContact(req)
	return sip.Invitation{
		RequestURI:      requestURI,
		From:            from.URI,
		FromDisplayName: from.DisplayName,
		To:              to.URI,
		Route:           c.route,
		Body:            sip.BodyOf(req),
		// The PRACK of each reliable provisional response is the caller's,
		// which the AS passes on (fromCaller).
		ManualPRACK: true,
		Contact:     contact,
		RecordRoute: fromFocus,
	}, fields
}

// relay sends inv on a new outgoing leg, passes on what comes back,
// and gives the caller the final response of the leg.
func (c *call) relay(inv sip.Invitation) {
	r := c.attempt(inv, 0)
	c.finish(r)
}

// finish ends the INVITE of the caller after an attempt that did not
// get the call answered: with the final response of the leg, or with a
// response of the AS when the leg got none.
func (c *call) finish(r attemptResult) {
	switch {
	case r.answered, r.cancelled:
		// The call is over, or the UA has answered the CANCEL with 487.
	case r.final != nil:
		c.in.Reject(r.final.StatusCode, c.responseFields(r.final, c.in.Request, true).Headers...)
	case errors.Is(r.err, sip.ErrTimeout):
		c.in.Reject(408)
	default:
		c.log.Warn("outgoing leg failed", "error", errText(r.err))
		c.in.Reject(500)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// attemptResult is how an outgoing leg ended.
type attemptResult struct {
	// answered: the callee answered, and the call is over by now.
	answered bool
	// final is a final response from 300 to 699 that the AS has not
	// passed on yet, for a service to act on.
	final *sip.Message
	// err is why no final response came.
	err error
	// provisional tells that a response from 101 to 199 came on the leg
	// before its final response (TS 24.604 4.5.2.6.6).
	provisional bool
	// noReply: the no reply timer fired, and the AS cancelled the leg
	// (TS 24.604 4.5.2.6.3 item 2).
	noReply bool
	// cancelled: the caller cancelled the call, and the AS the leg.
	cancelled bool
}

// attempt sends inv on a new outgoing leg and relays between it and
// in, until the leg ends or the callee answers; after an answer, until
// the call ends. When noReply is not zero, the no reply timer runs from
// the first 180 (Ringing) for that long.
func (c *call) attempt(inv sip.Invitation, noReply time.Duration) attemptResult {
	select {
	case <-c.in.Done():
		return attemptResult{cancelled: true}
	default:
	}
	inv.OnRequest = c.fromCallee
	ua := c.as.ua.Load()
	if ua == nil {
		return attemptResult{err: errNotListening}
	}
	out, err := ua.Invite(inv)
	if err != nil {
		return attemptResult{err: err}
	}
	c.mu.Lock()
	first := c.out == nil
	c.out = out
	c.mu.Unlock()
	if first {
		close(c.outReady)
	}
	c.log.Info("outgoing leg", "request_uri", inv.RequestURI, "call_id", out.Request.Get("Call-ID"))

	// RFC 3261 9.1: when the caller gives up, the AS cancels the leg, for
	// the same reason (RFC 3326 2).
	settled := make(chan struct{})
	defer close(settled)
	go func() {
		select {
		case <-c.in.Done():
			if errors.Is(c.in.Err(), sip.ErrCancelled) {
				out.Cancel(reasons(c.in.EndedBy())...)
			}
		case <-settled:
		}
	}()

	var r attemptResult
	var timer func() bool
	var timedOut chan struct{}
	defer func() {
		if timer != nil {
			timer()
		}
	}()
	for resp := range out.Responses() {
		switch code := resp.StatusCode; {
		case code < 200:
			r.provisional = true
			if r.noReply {
				continue
			}
			if code == 180 && noReply > 0 && timer == nil {
				// TS 24.604 4.5.2.6.3 item 2: the first 180 starts the no
				// reply timer, and a 180 of another fork does not start
				// it again.
				timedOut = make(chan struct{})
				timer = c.as.cfg.AfterFunc(noReply, func() { close(timedOut) })
				go c.cancelOnNoReply(out, timedOut, settled)
			}
			c.relayProvisional(out, resp)
		case code < 300:
			if c.noReplyFired(timedOut) {
				// The 2xx crossed the CANCEL; the UA ends that dialog with
				// a BYE (RFC 3261 15).
				r.noReply = true
				continue
			}
			c.answer(out, resp)
			return attemptResult{answered: true}
		default:
			r.final = resp
		}
	}
	r.noReply = r.noReply || c.noReplyFired(timedOut)
	r.cancelled = errors.Is(c.in.Err(), sip.ErrCancelled)
	if r.final == nil {
		r.err = out.Err()
	}
	return r
}

// cancelOnNoReply is TS 24.604 4.5.2.6.3 item 2: when the no reply
// timer expires, the AS cancels the leg to the diverting user with a
// Reason of SIP cause 408.
func (c *call) cancelOnNoReply(out *sip.OutgoingCall, timedOut, settled <-chan struct{}) {
	select {
	case <-timedOut:
		c.log.Info("no reply timer expired")
		out.Cancel(sip.NewReason(sip.ReasonSIP, 408, sip.ReasonPhrase(408)).Header())
	case <-settled:
	}
}

func (c *call) noReplyFired(timedOut chan struct{}) bool {
	if timedOut == nil {
		return false
	}
	select {
	case <-timedOut:
		return true
	default:
		return false
	}
}

// relayProvisional passes a provisional response of out on to the
// caller. A reliable one is acknowledged on out once the caller has
// acknowledged it on in, so that the PRACK goes from end to end as it
// would without the AS: with preconditions the caller and the callee
// see each other's 183 and PRACK (RFC 3262, RFC 3312).
func (c *call) relayProvisional(out *sip.OutgoingCall, resp *sip.Message) {
	if resp.StatusCode != 199 && c.alertWithTone(out, resp) {
		return
	}
	if resp.StatusCode == 199 {
		// RFC 6228: one of the early dialogs of out has ended. The early
		// dialogs of the callee's side are all one early dialog on in,
		// which goes on.
		if resp.Requires(sip.Option100rel) {
			out.PRACK(resp, sip.Body{})
		}
		return
	}
	body := sip.BodyOf(resp)
	reliable := resp.Requires(sip.Option100rel)
	fields := c.responseFields(resp, c.in.Request, true)
	c.mu.Lock()
	if c.withhold {
		if reliable && len(body.Content) > 0 {
			c.heldAnswer = body
		}
		body = sip.Body{}
		// Without a session description there is no early media.
		fields.Remove("P-Early-Media")
	}
	// RFC 3262 5: after an INVITE without an offer, the first reliable
	// response with a body has the callee's offer, and the PRACK the
	// caller's answer.
	offer := reliable && len(body.Content) > 0 && len(c.in.Request.Body) == 0 && !c.sessionToCaller
	if offer {
		c.pending = resp
	}
	if reliable && len(body.Content) > 0 {
		c.sessionToCaller = true
	}
	c.toCaller = true
	c.mu.Unlock()

	c.passFocusContact(resp)
	err := c.in.Progress(resp.StatusCode, body, fields.Headers...)
	if err != nil {
		c.log.Info("provisional response not relayed", "status", resp.StatusCode, "error", err.Error())
	}
	if reliable && !offer {
		if _, err := out.PRACK(resp, sip.Body{}); err != nil {
			c.log.Warn("PRACK not sent", "status", resp.StatusCode, "error", err.Error())
		}
	}
}

// answer passes the 2xx of out on to the caller, and then the requests
// within the dialog both ways, until one side hangs up; the other side
// then gets the BYE, with the Reason of the first (RFC 3326 2).
func (c *call) answer(out *sip.OutgoingCall, resp *sip.Message) {
	body := sip.BodyOf(resp)
	if c.answerAfterTone(resp) {
		// TS 24.182 4.5.5.3.7: the caller had its answer in a reliable
		// provisional response, so the 2xx has no SDP.
		body = sip.Body{}
	}
	c.mu.Lock()
	if c.withhold && len(body.Content) == 0 {
		body = c.heldAnswer
	}
	c.mu.Unlock()
	c.passFocusContact(resp)
	ack, err := c.in.Accept(body, c.responseFields(resp, c.in.Request, true).Headers...)
	if err != nil {
		// No ACK came, and the UA has sent the caller a BYE (RFC 3261
		// 13.3.1.4).
		c.log.Info("caller did not acknowledge the answer", "error", err.Error())
		out.Bye()
		return
	}
	if len(ack.Body) > 0 {
		// The 2xx carried the callee's offer, after an INVITE without
		// one, and the caller's ACK the answer (RFC 3261 13.2.2.4).
		if err := out.Ack(sip.BodyOf(ack)); err != nil {
			c.log.Warn("answer in the ACK not relayed", "error", err.Error())
		}
	}
	record := c.record(out)
	c.as.store.PutCall(record)
	defer c.as.store.DeleteCall(record)
	c.log.Info("call answered", "services", c.acted)
	select {
	case <-c.in.Done():
		out.Bye(reasons(c.in.EndedBy())...)
	case <-out.Done():
		c.in.Bye(reasons(out.EndedBy())...)
	}
	c.log.Info("call ended")
}

// record is the record of the call once both dialogs are confirmed.
func (c *call) record(out *sip.OutgoingCall) CallRecord {
	r := CallRecord{ServedUser: c.served.URI, SessionCase: c.served.SessionCase}
	r.Incoming, _ = c.in.Dialog()
	r.Outgoing, _ = out.Dialog()
	c.mu.Lock()
	r.Services = append(r.Services, c.acted...)
	c.mu.Unlock()
	return r
}

// actedOn notes that a service acted on the call.
func (c *call) actedOn(service string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acted = append(c.acted, service)
}

// responseFields are the header fields of resp, a response of one leg,
// that go into the response to req on the other leg. toCaller tells
// that the response goes to the caller's side, where the services of
// the served user act on it (servicesOnResponse).
func (c *call) responseFields(resp, req *sip.Message, toCaller bool) *sip.Message {
	fields := carried(resp)
	if pcv := responseChargingVector(req, c.as.cfg.HomeDomain); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	if toCaller {
		c.servicesOnResponse(resp, req, fields)
	}
	return fields
}

// leg is what the two legs of a call have in common: both an
// IncomingCall and an OutgoingCall are one.
type leg interface {
	Update(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	ReInvite(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Send(method sip.Method, body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Ack(body sip.Body, headers ...sip.HeaderField) error
}

// fromCaller is the handler of the requests of the caller within the
// dialog of in, which go on to the leg the call goes on in.
func (c *call) fromCaller(r *sip.InDialogRequest) {
	<-c.outReady
	c.mu.Lock()
	out := c.out
	pending := c.pending
	if r.Request.Method == sip.PRACK {
		c.pending = nil
	}
	c.mu.Unlock()
	if r.Request.Method == sip.PRACK && pending != nil {
		// The PRACK with the caller's answer to the callee's offer goes
		// on as the PRACK of the callee's response (RFC 3262 5).
		resp, err := out.PRACK(pending, sip.BodyOf(r.Request), c.requestFields(r.Request).Headers...)
		c.answerRequest(r, resp, err, out)
		return
	}
	c.forward(r, out)
}

// fromCallee is the handler of the requests of the callee within a
// dialog of an outgoing leg, which go on to the caller.
func (c *call) fromCallee(r *sip.InDialogRequest) {
	c.keepCalleeSDP(sip.BodyOf(r.Request))
	c.forward(r, c.in)
}

// forward passes r on to the other leg, and the final response back
// (TS 24.229 5.7.5.1). The re-INVITE of a HOLD goes this way too, as it
// came: lowering the bandwidth of the held media and an announcement to
// the held user are network options of TS 24.610 4.5.2.4.2 and
// 4.5.2.4.3 that this AS does not take, and GSMA IR.92 2.3.10 asks for
// neither.
func (c *call) forward(r *sip.InDialogRequest, to leg) {
	req := r.Request
	body := sip.BodyOf(req)
	fields := c.requestFields(req).Headers
	var resp *sip.Message
	var err error
	switch req.Method {
	case sip.UPDATE:
		if len(body.Content) == 0 {
			// RFC 4028 9: an UPDATE without a body refreshes the session
			// of its own dialog. Each leg has its own session timer, so
			// the AS answers it.
			r.Accept(sip.Body{})
			return
		}
		resp, err = to.Update(body, fields...)
	case sip.INVITE:
		resp, err = to.ReInvite(body, fields...)
	case sip.PRACK:
		// A PRACK with a new offer, after the callee's response had the
		// answer (RFC 3262 5). The AS has acknowledged that response on
		// the other leg already, so the offer goes on in an UPDATE
		// (RFC 3311 5.1).
		resp, err = to.Update(body, fields...)
	default:
		resp, err = to.Send(req.Method, body, fields...)
	}
	c.answerRequest(r, resp, err, to)
}

// answerRequest answers r, a request within a dialog that the AS sent
// on to the other leg, with what that leg answered.
func (c *call) answerRequest(r *sip.InDialogRequest, resp *sip.Message, err error, to leg) {
	toCaller := to != leg(c.in)
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
		if toCaller {
			c.keepCalleeSDP(sip.BodyOf(resp))
		}
		ack, err := r.Accept(sip.BodyOf(resp), c.responseFields(resp, r.Request, toCaller).Headers...)
		if err == nil && r.Request.Method == sip.INVITE && !r.Offer {
			// A re-INVITE without an offer: the 2xx had the other side's
			// offer, and this side's ACK has the answer (RFC 3261 14.1).
			to.Ack(sip.BodyOf(ack))
		}
	default:
		r.Reject(resp.StatusCode, c.responseFields(resp, r.Request, toCaller).Headers...)
	}
}

// requestFields are the header fields of req, a request of one leg,
// that go into the request on the other leg.
func (c *call) requestFields(req *sip.Message) *sip.Message {
	fields := carried(req)
	if pcv := chargingVector(req); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	return fields
}
