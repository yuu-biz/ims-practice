package ue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yuu-biz/ims-practice/internal/precondition"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallFailed is a call that ended with a final response other than 2xx,
// such as 486 (Busy Here) or 480 (Temporarily Unavailable).
type CallFailed struct {
	StatusCode int
	Reason     string
}

func (e *CallFailed) Error() string { return fmt.Sprintf("call failed: %d %s", e.StatusCode, e.Reason) }

// routeSet is the preloaded Route of a request from the UE: the P-CSCF,
// then the Service-Route of the registration (TS 24.229 5.1.2A.1.1).
func (u *UE) routeSet(reg Registration) []string {
	return append([]string{"<" + u.pcscfURI() + ";lr>"}, reg.ServiceRoute...)
}

// CallState is how far a call of the UE has got, as "ue serve" shows it
// to its controller.
type CallState string

const (
	// CallCalling is a call whose INVITE is out, with no ringing yet.
	CallCalling CallState = "calling"
	// CallRinging is a call whose callee is alerted: the caller has a
	// 180, the callee has alerted its user.
	CallRinging CallState = "ringing"
	// CallActive is an answered call, until one side hangs up.
	CallActive CallState = "active"
)

// Progress hears how far a call has got. callID is the Call-ID of its
// INVITE.
type Progress func(state CallState, callID string)

func (p Progress) report(state CallState, callID string) {
	if p != nil {
		p(state, callID)
	}
}

// CallOptions are the choices of Call besides whom to call.
type CallOptions struct {
	// GiveUpAfter, when not 0, has the caller hang up with CANCEL when the
	// callee has not answered by then (RFC 3261 9.1).
	GiveUpAfter time.Duration
	// StayUp keeps the call after the audio until ctx ends or the callee
	// hangs up. Without it the caller hangs up when the audio ends.
	StayUp   bool
	Progress Progress
	// Codecs, when not nil, are offered instead of those of the
	// CallConfig.
	Codecs []sdp.Codec
	// Session, when set, gets the session of the call once the INVITE is
	// out, and Changed hears of each change of it.
	Session func(*Session)
	Changed func()
	// Answered, when set, hears of the 2xx to the INVITE once the call is
	// answered, with the call: a UE that created a conference reads the
	// conference URI from it, and sends its REFERs on the call.
	Answered func(call *sip.OutgoingCall, ok *sip.Message)
}

// updateResult is the final response to an UPDATE that a call sent.
type updateResult struct {
	resp *sip.Message
	err  error
}

// Call calls target, a SIP or tel URI, and plays speech once the callee
// answers; then it hangs up with BYE (TS 24.229 5.1.3, RFC 3261 13.2
// and 15). reg is the registration the call goes out under. A caller
// that gives up, or whose ctx ends before the answer, hangs up with
// CANCEL (RFC 3261 9.1), and the INVITE ends with 487.
//
// With the precondition mechanism, the INVITE says which local
// resources are reserved, and an UPDATE tells the callee once they are
// (TS 24.229 5.1.3.1, 6.1.2; IR.92 2.4.1).
func (u *UE) Call(ctx context.Context, ua *sip.UA, reg Registration, target string, media *Media, speech Speech, opts CallOptions) error {
	codecs := opts.Codecs
	if codecs == nil {
		codecs = u.cfg.Calls.codecs()
	}
	s := u.newSession(media, codecs, opts.Changed)
	s.caller = true
	stopFollowing := s.followResources(u.cfg.Calls.Resources, !u.cfg.Calls.NoPreconditions, precondition.Originating)
	defer stopFollowing()
	offer, err := s.initialOffer()
	if err != nil {
		return err
	}
	call, err := ua.Invite(sip.Invitation{
		RequestURI: target,
		From:       reg.DefaultIdentity(),
		To:         target,
		Route:      u.routeSet(reg),
		// TS 24.229 5.1.3.1, IR.92 2.2.7: the UE takes early media
		// authorization.
		Headers: with(u.inviteHeaders(), sip.PEarlyMedia{Supported: true}.Header()),
		Body:    offer,
		// The UE sends its PRACKs itself: with the header fields of its
		// other requests (IR.92 2.6), and so that the UPDATE below can
		// wait for their 2xx.
		ManualPRACK: true,
		OnRequest:   s.handle,
	})
	if err != nil {
		return err
	}
	s.setDialog(call, call.Bye)
	if opts.Session != nil {
		opts.Session(s)
	}
	callID := call.Request.Get("Call-ID")
	opts.Progress.report(CallCalling, callID)
	var giveUp <-chan time.Time
	if opts.GiveUpAfter > 0 {
		t := time.NewTimer(opts.GiveUpAfter)
		defer t.Stop()
		giveUp = t.C
	}
	// RFC 3261 13.2.1: the first session description in a response is
	// the answer, and any later one is ignored. The callee may answer in
	// a reliable 183 and send the 200 without one (RFC 3262 5).
	var final *sip.Message
	answered := false
	stopped := ctx.Done()
	cancelled := false
	hangUp := func(why string) {
		giveUp, stopped, cancelled = nil, nil, true
		u.log.Info(why + ", cancelling the call")
		// TS 24.229 5.1.3.1: a Reason with the release cause, and no
		// P-Access-Network-Info in a CANCEL (5.1.2A.1.1).
		if err := call.Cancel(sip.HeaderField{Name: "User-Agent", Value: UserAgent}, reasonUserEndsCall.Header()); err != nil {
			u.log.Warn("CANCEL failed", "error", err.Error())
		}
	}
	updated := make(chan updateResult, 1)
	updating := false
	// RFC 3262 4: each reliable provisional response gets a PRACK, which
	// carries no body: the INVITE had the offer.
	//
	// SIMPLIFIED: RFC 3261 12.2.1.2. A 481 or 408 to the PRACK, or no
	// answer at all, SHOULD end the dialog. Here it is only logged; the
	// final response to the INVITE still ends the call.
	pracked, loopDone := make(chan struct{}), make(chan struct{})
	pracking := 0
	prack := func(resp *sip.Message) {
		final, err := call.PRACK(resp, sip.Body{}, s.requestHeaders...)
		if err != nil || final == nil || final.StatusCode >= 300 {
			status := 0
			if final != nil {
				status = final.StatusCode
			}
			u.log.Warn("PRACK failed", "status", status, "error", fmt.Sprint(err))
		}
		select {
		case pracked <- struct{}{}:
		case <-loopDone:
		}
	}
	// The INVITE goes on after a CANCEL, until its final response.
	for done := false; !done; {
		select {
		case resp, ok := <-call.Responses():
			if !ok {
				done = true
				break
			}
			u.log.Info("call progress", "status", resp.StatusCode, "reason", resp.Reason)
			tag := toTag(resp)
			switch {
			case resp.StatusCode == 199:
				u.log.Info("early dialog terminated", "to_tag", tag, "reason", resp.Get("Reason"))
				s.earlyDialogTerminated(tag)
				answered = s.hasAnswer()
				s.changed()
				continue
			case resp.StatusCode < 200:
				s.earlyMediaAuthorized(tag, resp)
				s.changed()
				if resp.Requires(sip.Option100rel) {
					pracking++
					go prack(resp)
				}
			default:
				final = resp
			}
			if resp.StatusCode == 180 {
				s.alerted(tag)
				opts.Progress.report(CallRinging, callID)
				s.changed()
			}
			if !answered && len(resp.Body) > 0 && resp.StatusCode < 300 {
				if err := s.receiveAnswer(resp); err != nil {
					u.log.Warn("unusable SDP answer", "status", resp.StatusCode, "error", err.Error())
				} else {
					answered = true
				}
				s.changed()
			}
		case r := <-updated:
			updating = false
			s.updated(r)
		case <-pracked:
			pracking--
		case <-s.kicked:
		case <-giveUp:
			hangUp("no answer")
		case <-stopped:
			hangUp("stopped")
		}
		// TS 24.229 5.1.3.1: once the local resources are reserved, the
		// next request tells the callee: an UPDATE, which IR.92 2.4.1
		// asks for. The UPDATE goes in the early dialog of the answer,
		// after the 2xx to the PRACK: the UPDATE is large and goes over
		// TCP, the PRACK over UDP (RFC 3261 18.1.1), and an UPDATE that
		// overtakes the PRACK has the callee reject the PRACK with 500
		// (RFC 3261 12.2.2).
		if answered && !updating && pracking == 0 && !cancelled && final == nil && s.needsOffer() {
			if body, err := s.reoffer(); err == nil {
				updating = true
				headers := with(s.requestHeaders, s.preconditionHeaders(false)...)
				go func() {
					resp, err := call.Update(body, headers...)
					updated <- updateResult{resp, err}
				}()
			}
		}
	}
	close(loopDone)
	if updating {
		// TS 24.229 5.1.3.1 NOTE 5: the 2xx to the INVITE may come before
		// the 200 to the UPDATE.
		s.updated(<-updated)
	}
	if final == nil {
		return fmt.Errorf("no answer to the INVITE: %w", call.Err())
	}
	if final.StatusCode >= 300 {
		return &CallFailed{StatusCode: final.StatusCode, Reason: final.Reason}
	}
	if cancelled {
		// RFC 3261 15: the 2xx crossed the CANCEL. The UA ends the call
		// with a BYE, so there is no audio to play.
		return errors.Join(errors.New("the callee answered as the call was cancelled"), ctx.Err())
	}
	if !answered {
		call.Bye(s.requestHeaders...)
		return errors.New("no response to the INVITE carried a usable SDP answer")
	}
	s.setAnswered()
	stream, _ := media.Stream()
	u.log.Info("call answered", "media_to", stream.Remote.String(), "codec", codecInfo(stream).String())
	opts.Progress.report(CallActive, callID)
	if opts.Answered != nil {
		opts.Answered(call, final)
	}
	s.changed()
	// RFC 3261 15: either side may hang up. When the callee's BYE comes,
	// the audio stops, and there is no BYE left to send.
	talk, stopTalking := context.WithCancel(ctx)
	defer stopTalking()
	go func() {
		select {
		case <-call.Done():
			stopTalking()
		case <-talk.Done():
		}
	}()
	sendErr := media.Start(speech)
	select {
	case <-media.Played():
	case <-talk.Done():
		sendErr = errors.Join(sendErr, talk.Err())
	}
	if opts.StayUp && sendErr == nil {
		<-talk.Done()
	}
	select {
	case <-call.Done():
		media.Stop()
		if err := s.ended(); err != nil {
			return err
		}
		logEndedBy(u.log, call.EndedBy())
		return nil
	default:
	}
	if err := s.ended(); err != nil {
		media.Stop()
		return err
	}
	// Give the last packets from the callee time to arrive.
	time.Sleep(200 * time.Millisecond)
	media.Stop()
	// TS 24.229 5.1.5, IR.92 2.2.4.
	if err := call.Bye(with(s.requestHeaders, reasonUserEndsCall.Header())...); err != nil && !errors.Is(err, sip.ErrCallEnded) {
		return fmt.Errorf("BYE: %w", err)
	}
	u.log.Info("call ended")
	return sendErr
}

// updated reads the final response to an UPDATE of this UE: its 2xx
// has the answer (RFC 3311 5.3).
func (s *Session) updated(r updateResult) {
	defer s.changed()
	switch {
	case r.err != nil:
		s.log.Warn("UPDATE failed", "error", r.err.Error())
	case r.resp.StatusCode >= 300:
		s.log.Warn("UPDATE refused", "status", r.resp.StatusCode, "reason", r.resp.Reason)
	case len(r.resp.Body) == 0:
		s.log.Warn("the 200 to the UPDATE has no SDP answer")
	default:
		if err := s.receiveAnswer(r.resp); err != nil {
			s.log.Warn("unusable SDP answer to the UPDATE", "error", err.Error())
		}
	}
	if r.err == nil && s.caller {
		// RFC 5009 Table 1: the 2xx to an UPDATE may change the early
		// media authorization.
		s.earlyMediaAuthorized(toTag(r.resp), r.resp)
	}
}

// followResources keeps the session up to date with the resources of
// the access network until the returned function is called. With
// preconditions, the table starts from them (TS 24.229 6.1.2): a UE
// whose access network needs no reservation has them from the start.
func (s *Session) followResources(r *Resources, preconditions bool, role precondition.Role) (stop func()) {
	s.mu.Lock()
	s.reservationNeeded = r != nil
	s.mu.Unlock()
	reserved, changed := r.State()
	if preconditions {
		s.usePreconditions(role, reserved)
	} else {
		s.setReserved(reserved)
	}
	if changed == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-changed:
			case <-done:
				return
			}
			reserved, changed = r.State()
			s.log.Info("local resources for media", "reserved", reserved)
			s.setReserved(reserved)
			s.changed()
			s.kick()
		}
	}()
	return func() { close(done) }
}

// handle answers the requests of the peer within the dialog of the
// call: an offer in an UPDATE, a re-INVITE or a PRACK (RFC 3311, RFC
// 3261 14, RFC 3262 5), the answer to this UE's offer in a PRACK, and a
// session refresh without a body (RFC 4028 9).
func (s *Session) handle(r *sip.InDialogRequest) {
	defer s.kick()
	defer s.changed()
	req := r.Request
	if s.caller {
		// RFC 5009 Table 1: so may an UPDATE or a PRACK of the callee.
		if from, err := sip.ParseNameAddr(req.Get("From")); err == nil {
			tag, _ := from.Params.Get("tag")
			s.earlyMediaAuthorized(tag, req)
		}
	}
	switch req.Method {
	case sip.INVITE, sip.UPDATE, sip.PRACK:
	default:
		// Not a request of the session, such as a NOTIFY of a
		// subscription that shares the dialog.
		s.mu.Lock()
		other := s.other
		s.mu.Unlock()
		if other == nil || !other(r) {
			r.Reject(405, s.responseHeaders...)
		}
		return
	}
	switch {
	case r.Offer:
		answer, err := s.answerTo(req.Body, false)
		if err != nil {
			code := 488
			var na *errNotAcceptable
			if errors.As(err, &na) {
				code = na.code
			}
			s.log.Info("refused an offer", "method", string(req.Method), "status", code, "error", err.Error())
			r.Reject(code, s.responseHeaders...)
			return
		}
		ack, err := r.Accept(answer, with(s.responseHeaders, s.answerHeaders(req)...)...)
		if err != nil {
			s.log.Warn("answer not sent", "method", string(req.Method), "error", err.Error())
			return
		}
		if ack != nil && len(ack.Body) > 0 {
			s.log.Warn("the ACK of a re-INVITE with an offer carries a body, which is ignored")
		}
	case len(req.Body) > 0:
		// A PRACK with the answer to the offer in this UE's reliable
		// response (RFC 3262 5).
		if err := s.receiveAnswer(req); err != nil {
			s.log.Warn("unusable SDP answer", "method", string(req.Method), "error", err.Error())
		}
		r.Accept(sip.Body{}, s.responseHeaders...)
	case req.Method == sip.INVITE:
		// A re-INVITE without an offer asks for one; the ACK brings the
		// answer (RFC 3261 14.2).
		offer, err := s.reoffer()
		if err != nil {
			r.Reject(500, s.responseHeaders...)
			return
		}
		ack, err := r.Accept(offer, with(s.responseHeaders, s.answerHeaders(req)...)...)
		if err != nil {
			s.log.Warn("offer not sent", "error", err.Error())
			return
		}
		if len(ack.Body) > 0 {
			if err := s.receiveAnswer(ack); err != nil {
				s.log.Warn("unusable SDP answer in the ACK", "error", err.Error())
			}
		}
	case req.Method == sip.UPDATE:
		// A session refresh (RFC 4028 9).
		r.Accept(sip.Body{}, s.responseHeaders...)
	default:
		r.Reject(405, s.responseHeaders...)
	}
}

// answerHeaders are those of a 2xx with an SDP answer to req: Require
// with precondition while the mechanism is in use, when req supports or
// requires it (TS 24.229 5.1.3.1 a, 5.1.4.1 a, 5.1.4A.2).
func (s *Session) answerHeaders(req *sip.Message) []sip.HeaderField {
	if !req.Supports(sip.OptionPrecondition) {
		return nil
	}
	return s.preconditionHeaders(true)
}

// AnswerOptions are the choices of Answer.
type AnswerOptions struct {
	// PickUp delivers when the callee answers, such as time.After(ring).
	PickUp <-chan time.Time
	// Reject, when not 0, turns the call down with that status code
	// instead, such as 486.
	Reject   int
	Progress Progress
	// Session, when set, gets the session of the call before its first
	// response, and Changed hears of each change of it.
	Session func(*Session)
	Changed func()
	// Waiting tells that another call is on the line: the 180 then says
	// so with the Alert-Info of call waiting (TS 24.615 4.5.5.3.2 d).
	Waiting bool
}

// callWaiting is the Alert-Info value of a waiting call (RFC 7462, TS
// 24.615 4.5.5.3.2).
const callWaiting = "<urn:alert:service:call-waiting>"

// ringingRefresh is how often a callee that keeps ringing sends another
// provisional response (RFC 3261 13.3.1.1).
var ringingRefresh = time.Minute

// Answer takes an incoming call: a 183 with the SDP answer, reliable
// when the caller supports 100rel (RFC 3262), and, once the callee may
// alert its user, a 180; then 200 when opts.PickUp delivers, and the
// audio until the caller hangs up or ctx ends (RFC 3261 13.3, 15). When
// ctx ends while the call rings, the callee turns it down with 480
// (Temporarily Unavailable).
//
// The callee alerts its user once the preconditions are met (RFC 3312
// 6, TS 24.229 5.1.4.1), and without preconditions once its own
// resources are reserved (IR.92 2.4.1).
func (u *UE) Answer(ctx context.Context, ic *sip.IncomingCall, media *Media, speech Speech, opts AnswerOptions) error {
	if opts.Reject != 0 {
		u.log.Info("rejecting the call", "status", opts.Reject)
		return ic.Reject(opts.Reject, u.responseHeaders()...)
	}
	callID := ic.Request.Get("Call-ID")
	s := u.newSession(media, u.cfg.Calls.codecs(), opts.Changed)
	// TS 24.229 5.1.4.1 a): the callee uses the mechanism when the
	// INVITE supports it and the mechanism is enabled.
	preconditions := !u.cfg.Calls.NoPreconditions && ic.Request.Supports(sip.OptionPrecondition)
	stopFollowing := s.followResources(u.cfg.Calls.Resources, preconditions, precondition.Terminating)
	defer stopFollowing()
	ic.Handle(s.handle)
	s.setDialog(ic, ic.Bye)
	if opts.Session != nil {
		opts.Session(s)
	}
	reliable := ic.Request.Supports(sip.Option100rel)
	var sdpBody sip.Body
	if len(ic.Request.Body) == 0 {
		// IR.92 2.2.4: the offer goes in the first reliable response, and
		// the answer comes in its PRACK (RFC 3262 5). Without 100rel it
		// goes in the 200, and the answer in the ACK (RFC 3261 13.2.1).
		offer, err := s.initialOffer()
		if err != nil {
			ic.Reject(500, s.responseHeaders...)
			return err
		}
		sdpBody = offer
		u.log.Info("call without an offer: offering", "preconditions", s.preconditionHeaders(true) != nil)
	} else {
		answer, err := s.answerTo(ic.Request.Body, true)
		if err != nil {
			code := 488
			var na *errNotAcceptable
			if errors.As(err, &na) {
				code = na.code
			}
			ic.Reject(code, s.responseHeaders...)
			return fmt.Errorf("INVITE without a usable offer: %w", err)
		}
		sdpBody = answer
		stream, _ := media.Stream()
		u.log.Info("call offered", "codec", codecInfo(stream).String(), "preconditions", s.preconditionHeaders(true) != nil)
	}
	s.changed()
	if reliable || len(ic.Request.Body) > 0 {
		if err := ic.Progress(183, sdpBody, with(s.responseHeaders, s.preconditionHeaders(true)...)...); err != nil {
			return fmt.Errorf("183: %w", err)
		}
	}
	if reliable {
		if err := s.awaitAlerting(ctx, ic); err != nil {
			return err
		}
	} else if len(ic.Request.Body) > 0 && !s.readyToAlert() {
		// Without reliable provisional responses there is no PRACK or
		// early UPDATE to go on with; the callee waits for its own
		// resources only (IR.92 2.4.1).
		if err := s.awaitAlerting(ctx, ic); err != nil {
			return err
		}
	}
	ringing := s.responseHeaders
	if opts.Waiting {
		ringing = with(ringing, sip.HeaderField{Name: "Alert-Info", Value: callWaiting})
	}
	if err := ic.Progress(180, sip.Body{}, ringing...); err != nil {
		return fmt.Errorf("180: %w", err)
	}
	opts.Progress.report(CallRinging, callID)
	// RFC 3261 13.3.1.1: a proxy may cancel an INVITE that gets no
	// response for three minutes, so a callee that keeps ringing sends a
	// provisional response every minute.
	refresh := time.NewTicker(ringingRefresh)
	defer refresh.Stop()
	for picked := false; !picked; {
		select {
		case <-opts.PickUp:
			picked = true
		case <-refresh.C:
			if err := ic.Progress(180, sip.Body{}, ringing...); err != nil {
				return fmt.Errorf("180: %w", err)
			}
		case <-ic.Done():
			return ic.Err()
		case <-ctx.Done():
			// RFC 3261 21.4.18: the callee's UE was reached, but its user
			// is not there to take the call.
			ic.Reject(480, s.responseHeaders...)
			return ctx.Err()
		}
	}
	// RFC 3262 5: the SDP of a reliable 183 counts; the 200 repeats it
	// only when the 183 went unreliably, or when there was none.
	body := sip.Body{}
	if !reliable {
		body = sdpBody
	}
	ack, err := ic.Accept(body, s.responseHeaders...)
	if err != nil {
		return fmt.Errorf("200: %w", err)
	}
	if !reliable && len(ic.Request.Body) == 0 {
		if err := s.receiveAnswer(ack); err != nil {
			ic.Bye(s.requestHeaders...)
			return fmt.Errorf("the ACK has no usable answer: %w", err)
		}
	}
	s.setAnswered()
	stream, _ := media.Stream()
	u.log.Info("call answered", "media_to", stream.Remote.String(), "codec", codecInfo(stream).String())
	opts.Progress.report(CallActive, callID)
	s.changed()
	if err := media.Start(speech); err != nil {
		u.log.Warn("no media sent", "error", err.Error())
	}
	select {
	case <-ic.Done():
		logEndedBy(u.log, ic.EndedBy())
	case <-ctx.Done():
		ic.Bye(with(s.requestHeaders, reasonUserEndsCall.Header())...)
	}
	media.Stop()
	u.log.Info("call ended")
	if err := s.ended(); err != nil {
		return err
	}
	if err := ic.Err(); err != nil && !errors.Is(err, sip.ErrCallEnded) {
		return err
	}
	return nil
}

// logEndedBy logs the BYE with which the peer, or the network, ended
// the call, with its Reason (RFC 3326), such as the one that the P-CSCF
// writes when the network has lost the bearer (TS 24.229 5.2.8.1.2).
func logEndedBy(log interface{ Info(string, ...any) }, bye *sip.Message) {
	if bye == nil {
		return
	}
	log.Info("call ended by the peer", "method", string(bye.Method), "reason", bye.Get("Reason"))
}

// awaitAlerting waits until the callee may alert its user, and answers
// the changes of the session meanwhile. A request for confirmation of
// the callee's own resources gets an UPDATE once they are reserved (TS
// 24.229 5.1.4.1).
//
// Before alerting, the session needs its answer too: after an INVITE
// without an offer, the PRACK brings it.
func (s *Session) awaitAlerting(ctx context.Context, ic *sip.IncomingCall) error {
	for !s.readyToAlert() || !s.settled() {
		if s.needsOffer() {
			if body, err := s.reoffer(); err == nil {
				resp, err := ic.Update(body, with(s.requestHeaders, s.preconditionHeaders(false)...)...)
				s.updated(updateResult{resp, err})
				continue
			}
		}
		select {
		case <-s.kicked:
		case <-ic.Done():
			return ic.Err()
		case <-ctx.Done():
			ic.Reject(480, s.responseHeaders...)
			return ctx.Err()
		}
	}
	return nil
}
