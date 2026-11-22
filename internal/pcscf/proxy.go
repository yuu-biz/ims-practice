package pcscf

import (
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// proxyInvite forwards out, the initial INVITE of call, to dst from
// stack, the way a stateful proxy does (RFC 3261 16.6, 16.7): it is
// sip.ServerTransaction.ProxyWith with one more way for the INVITE to
// end. When the network has no resources for the session, or takes
// them away while the INVITE is on its way (call.abort), the P-CSCF
// gives the INVITE up itself: it answers the caller 500 and cancels the
// INVITE towards the callee with a Reason that gives 503 (TS 24.229
// 5.2.7.2, 5.2.8.1.1). It gives the INVITE up in the same way, with a
// plain 500 (RFC 3261 21.5.1) and a CANCEL without a Reason, when it
// cannot keep the dialog: it could not release the session later, nor
// free what the session holds.
//
// Like ProxyWith it returns the final response that is still to be
// sent, or nil after a 2xx. It also returns nil once the P-CSCF has
// given the INVITE up, and sets call.givenUp.
//
// The loop repeats ProxyWith because package sip has no proxy that its
// user can end; the two must stay alike. Timer C is the default one,
// since the stack's own setting cannot be read from here.
func (p *PCSCF) proxyInvite(st *sip.ServerTransaction, out *sip.Message, dst sip.Destination, ref *ims.StackRef, call *setup) (*sip.Message, error) {
	stack, err := ref.Get()
	if err != nil {
		return nil, err
	}
	ct, err := stack.SendRequest(out, dst)
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
	// giveUp gives the INVITE up for why, an error of call.response, or
	// nil when the network took the resources away.
	giveUp := func(why error) {
		call.givenUp = true
		if errors.Is(why, errStore) {
			st.Respond(storeFailed(st.Request, why, call.log))
			ct.Cancel()
			return
		}
		st.Respond(call.unavailable())
		ct.Cancel(sip.HeaderField{Name: "Reason", Value: reasonServiceUnavailable})
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
					tag := tagOf(resp.Get("To"))
					if call.crossedCancel(resp, !crossed[tag]) {
						crossed[tag] = true
					}
				}
			case code < 200:
				provisional = true
				// 16.7 step 2.
				timerC.Reset(sip.DefaultTimerC)
				if err := call.response(resp); err != nil {
					giveUp(err)
					continue
				}
				st.Respond(resp)
			case code < 300:
				refused := call.response(resp)
				if !call.settle() {
					// The session was aborted just before its 2xx came.
					giveUp(nil)
					continue
				}
				if errors.Is(refused, errStore) {
					// The callee has answered, but the caller gets no 2xx
					// for a dialog that the P-CSCF cannot release. Its
					// next retransmission is acknowledged, and the callee
					// gets a BYE, as for a 2xx that crossed the CANCEL.
					giveUp(refused)
					continue
				}
				answered = true
				timerC.Stop()
				timerCFired = nil
				st.Respond(resp)
				if refused != nil {
					// The callee has answered, so the caller gets the 2xx,
					// and the dialog is then released (5.2.8.1.2).
					go p.release(call.key, call.log)
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
				giveUp(nil)
			}
		case <-timerCFired:
			timerCFired = nil
			if !provisional {
				return sip.NewResponse(st.Request, 408, sip.NewTag()), nil
			}
			ct.Cancel()
		}
	}
}
