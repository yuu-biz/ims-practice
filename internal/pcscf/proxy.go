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
	// sent are the responses that the caller has been sent, as they went:
	// the last reliable provisional one of each early dialog, and each
	// 2xx, by To tag. A UAS sends the next reliable provisional response
	// of a dialog only once the last one has its PRACK (RFC 3262 3), so
	// only the last one comes again.
	// The callee sends each again until its PRACK or its ACK comes (RFC
	// 3262 3, RFC 6026 8.1), and the caller gets each retransmission as
	// the first one went. An SDP whose version has not changed has to be
	// the same SDP (RFC 3264 8), and the relay need not be asked again:
	// a relay that does not take the answer this time would otherwise
	// leave the SDP that names the far party.
	sentReliable, sent2xx := map[string]reliableSent{}, map[string]*sip.Message{}
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
				rseq, reliable := reliableSeq(resp)
				tag := tagOf(resp.Get("To"))
				if last, ok := sentReliable[tag]; reliable && ok && rseq <= last.rseq {
					// A retransmission, of the last one or, delayed, of one
					// that the caller has acknowledged already, which the
					// caller would discard (RFC 3262 4). The release of the
					// early dialog still ends the INVITE here, as with any
					// response.
					if err := call.released(); err != nil {
						giveUp(err)
						continue
					}
					if rseq == last.rseq {
						st.Respond(last.response.Clone())
					}
					continue
				}
				if err := call.response(resp); err != nil {
					giveUp(err)
					continue
				}
				if reliable {
					sentReliable[tag] = reliableSent{rseq: rseq, response: resp.Clone()}
				}
				if err := st.Respond(resp); err == nil {
					p.sessionSucceeded(st, resp, call.leg)
				}
			case code < 300:
				tag := tagOf(resp.Get("To"))
				if first, ok := sent2xx[tag]; ok {
					// A retransmission (RFC 6026 7.2). It goes on as the
					// first one went, also after the P-CSCF has released the
					// dialog that the 2xx confirmed.
					st.Respond(first.Clone())
					continue
				}
				if crossed[tag] {
					acknowledge(resp)
					continue
				}
				refused := call.response(resp)
				if !call.settle() {
					// The session was aborted just before its 2xx came.
					giveUp(call.abortCause)
					continue
				}
				switch {
				case !answered && errors.Is(refused, errStore):
					// The callee has answered, but the caller gets no 2xx
					// for a dialog that the P-CSCF cannot release. Its
					// next retransmission is acknowledged, and the callee
					// gets a BYE, as for a 2xx that crossed the CANCEL.
					giveUp(refused)
					continue
				case answered && (errors.Is(refused, errStore) || errors.Is(refused, errRelay) || errors.Is(refused, errReleased)):
					// The 2xx of another dialog of a forked INVITE, after the
					// caller has the 2xx of the first (RFC 3261 13.2.2.4).
					// The caller has a final response, so this 2xx can only
					// go on or go no further, and its SDP would lead the
					// caller past the relay. It is acknowledged in the
					// caller's name and its dialog ended with a BYE, as for
					// a 2xx that crossed the CANCEL.
					acknowledge(resp)
					continue
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
				sent2xx[tag] = resp.Clone()
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

// reliableSent is the last reliable provisional response of an early
// dialog that the caller has been sent, and its RSeq.
type reliableSent struct {
	rseq     uint32
	response *sip.Message
}

// reliableSeq is the RSeq of a reliable provisional response, which tells
// its retransmissions apart from the other responses of its dialog:
// they have the same To tag and RSeq (RFC 3262 4). reliable is false for
// a response that is not reliable, or whose RSeq does not parse.
func reliableSeq(resp *sip.Message) (rseq uint32, reliable bool) {
	if !resp.Requires(sip.Option100rel) {
		return 0, false
	}
	rseq, err := sip.ParseRSeq(resp.Get("RSeq"))
	return rseq, err == nil
}
