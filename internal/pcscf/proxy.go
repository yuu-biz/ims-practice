package pcscf

import (
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// proxyInvite forwards out, the initial INVITE of call, to the first of
// targets from the stack of ref, the way a stateful proxy does (RFC 3261
// 16.6, 16.7): it is sip.ServerTransaction.ProxyWith with one more way
// for the INVITE to end. When the network has no resources for the session, or takes
// them away while the INVITE is on its way (call.abort), the P-CSCF
// gives the INVITE up itself: it answers the caller 500 and cancels the
// INVITE towards the callee with a Reason that gives 503 (TS 24.229
// 5.2.7.2, 5.2.8.1.1). It gives the INVITE up in the same way, with a
// plain 500 (RFC 3261 21.5.1) and a CANCEL without a Reason, when it
// cannot keep the dialog: it could not release the session later, nor
// free what the session holds. TS 24.229 names no procedure for a media
// relay that does not take the answer of a response, and the P-CSCF
// gives the INVITE up then too, as for lost resources but with a plain
// 500: FAILURE_CAUSE tells of radio or bearer resources (7.2A.18.12.2).
// That response goes no further, since its SDP would lead its party
// past the relay. When the answer is that of an UPDATE within the early
// dialog, the release comes through call.abort instead, or, when the
// UPDATE passed another instance of the P-CSCF, as errReleased with the
// next response to the INVITE; that response goes no further either.
//
// Like ProxyWith it returns the final response that is still to be
// sent, or nil after a 2xx. It also returns nil once the P-CSCF has
// given the INVITE up, and sets call.givenUp.
//
// The loop repeats ProxyWith because package sip has no proxy that its
// user can end; the two must stay alike. Timer C is the default one,
// since the stack's own setting cannot be read from here.
func (p *PCSCF) proxyInvite(st *sip.ServerTransaction, out *sip.Message, targets []sip.Destination, ref *ims.StackRef, call *setup) (*sip.Message, error) {
	stack, err := ref.Get()
	if err != nil {
		return nil, err
	}
	ct, err := stack.SendRequest(out, targets...)
	if err != nil {
		return nil, err
	}
	timerC := time.NewTimer(sip.DefaultTimerC)
	defer timerC.Stop()
	timerCFired := timerC.C
	cancelled, abort := st.Cancelled(), call.abort
	var final *sip.Message
	answered, provisional := false, false
	// The callee tags of the 2xx that crossed the CANCEL, whose dialogs
	// the P-CSCF has sent its BYE in.
	crossed := map[string]bool{}
	// The reliable provisional responses that the caller has been sent,
	// by reliableID. The callee sends each again until its PRACK comes
	// (RFC 3262 3).
	passed := map[string]bool{}
	// giveUp gives the INVITE up for why: an error of call.response or the
	// abortCause of call. Only a session that the network has no
	// resources for gets the 500 that says so; a dialog released for a
	// cause the P-CSCF no longer knows gets a plain one.
	giveUp := func(why error) {
		call.givenUp = true
		switch {
		case errors.Is(why, errStore):
			st.Respond(storeFailed(st.Request, why, call.log))
			ct.Cancel()
			return
		case errors.Is(why, errResources):
			st.Respond(call.unavailable())
		default:
			st.Respond(call.failed())
		}
		ct.Cancel(sip.HeaderField{Name: "Reason", Value: reasonServiceUnavailable})
	}
	// acknowledge is crossedCancel for a 2xx that the caller does not get,
	// the first one of its dialog and each retransmission.
	acknowledge := func(resp *sip.Message) {
		tag := tagOf(resp.Get("To"))
		if call.crossedCancel(resp, !crossed[tag]) {
			crossed[tag] = true
		}
	}
	for {
		select {
		case resp, ok := <-ct.Responses():
			if !ok {
				switch {
				case answered || call.givenUp:
					return nil, nil
				case final != nil:
					return final, nil
				}
				return nil, ct.Err()
			}
			resp = resp.Clone()
			resp.RemoveFirst("Via")
			switch code := resp.StatusCode; {
			case code == 100:
				// 16.7 step 5: a 100 (Trying) is hop by hop.
				provisional = true
			case call.givenUp:
				// The caller has its 500; what the callee still sends, in
				// the end the 487 to the CANCEL, goes no further.
				if code/100 == 2 {
					acknowledge(resp)
				}
			case code < 200:
				provisional = true
				// 16.7 step 2.
				timerC.Reset(sip.DefaultTimerC)
				reliable := reliableID(resp)
				switch err := call.response(resp); {
				case errors.Is(err, errRelay) && reliable != "" && passed[reliable]:
					// The caller has this reliable response, whose answer the
					// relay took, so this one is a retransmission. It goes on
					// as it came, as a retransmitted 2xx does.
				case err != nil:
					giveUp(err)
					continue
				case reliable != "":
					passed[reliable] = true
				}
				if err := st.Respond(resp); err == nil {
					p.sessionSucceeded(st, resp, call.leg)
				}
			case code < 300:
				refused := call.response(resp)
				if !call.settle() {
					// The session was aborted just before its 2xx came.
					giveUp(call.abortCause)
					continue
				}
				switch {
				case errors.Is(refused, errStore):
					// The callee has answered, but the caller gets no 2xx
					// for a dialog that the P-CSCF cannot release. Its
					// next retransmission is acknowledged, and the callee
					// gets a BYE, as for a 2xx that crossed the CANCEL.
					giveUp(refused)
					continue
				case (errors.Is(refused, errRelay) || errors.Is(refused, errReleased)) && answered:
					// The caller has the 2xx whose answer the relay took,
					// so this one is a retransmission (RFC 6026 7.2). It
					// goes on as it came, also after the P-CSCF has
					// released the dialog that the 2xx confirmed.
					refused = nil
				case errors.Is(refused, errRelay) || errors.Is(refused, errReleased):
					// The callee has answered, but the caller gets 500 in
					// the place of the 2xx. The 2xx is acknowledged at
					// once, and the callee gets a BYE, as for a 2xx that
					// crossed the CANCEL. Only retransmissions of the 2xx
					// come after it, so what the call holds goes now, not
					// when the client transaction ends. The BYE goes with
					// the ACK of a retransmission when this ACK cannot go
					// out, and the CSeq of the caller is read before the
					// dialog goes.
					giveUp(refused)
					call.callerSeq = call.lastCallerSeq()
					acknowledge(resp)
					go call.end()
					continue
				}
				if !answered {
					p.measurements.sessions.Answered(role, call.leg.direction())
				}
				answered = true
				timerC.Stop()
				timerCFired = nil
				if err := st.Respond(resp); err == nil {
					p.sessionSucceeded(st, resp, call.leg)
				}
				if refused != nil {
					// The callee has answered, so the caller gets the 2xx,
					// and the dialog is then released (5.2.8.1.2).
					go p.release(call.key, ResourcesLost, false, call.log)
				}
			default:
				final = resp
			}
		case <-cancelled:
			cancelled = nil
			// RFC 3326 2, as ProxyWith does.
			ct.Cancel(sip.ReasonFields(st.CancelRequest())...)
		case <-abort:
			abort = nil
			if !call.givenUp {
				giveUp(call.abortCause)
			}
		case <-timerCFired:
			timerCFired = nil
			if !provisional {
				// As ProxyWith does: the INVITE goes to no other target.
				ct.Cancel()
				return sip.NewResponse(st.Request, 408, sip.NewTag()), nil
			}
			ct.Cancel()
		}
	}
}

// reliableID tells the retransmissions of a reliable provisional
// response apart from the other responses of its dialog: they have the
// same To tag and RSeq (RFC 3262 4). It is empty for a response that is
// not reliable.
func reliableID(resp *sip.Message) string {
	if !resp.Requires(sip.Option100rel) {
		return ""
	}
	return tagOf(resp.Get("To")) + " " + resp.Get("RSeq")
}
