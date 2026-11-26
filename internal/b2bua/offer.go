package b2bua

import (
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file moves the caller's media to the early dialog of the
// callee's side that answered, when that is not the dialog whose
// session description the caller has. The caller takes the first
// session description of its early dialog as the answer and ignores any
// later one (RFC 3261 13.2.1), so it learns of the session of the
// dialog that answered from an offer of the B2BUA's own, which goes out
// once the caller has acknowledged the 2xx.

// sessionDescription is the media type of a session description (RFC
// 8866 5).
const sessionDescription = "application/sdp"

// moveSession offers the caller the session of the dialog of out whose
// remote tag is tag, the dialog that answered. calleeOffered tells that
// its 2xx carried the offer of its UAS, after an INVITE without one:
// the caller's answer then goes to that UAS in the ACK, and a caller
// that gave no answer leaves the Call to end that dialog, as a UAC that
// cannot answer the offer of a 2xx does (RFC 3261 13.2.2.4).
func (c *Call) moveSession(out *sip.OutgoingCall, tag string, calleeOffered bool) {
	// The offer goes between the requests of the caller that the Call
	// relays, not while one of them is on its way to the callee: the
	// caller may send a re-INVITE of its own as soon as it has
	// acknowledged the 2xx, such as the one that puts the call on hold.
	if !c.callerTurns.settle(c.in.Done(), out.Done()) {
		return
	}
	_, answer := c.OfferCaller(tag, c.callerOrigin())
	switch {
	case !calleeOffered:
	case len(answer.Content) == 0:
		out.Bye()
	default:
		if err := out.Ack(answer); err != nil {
			c.log.Warn("answer in the ACK not relayed", "error", err.Error())
		}
	}
}

// OfferCaller offers the caller the session of the early dialog of the
// callee's side whose To tag is tag: the last session description that
// dialog sent, as the role prepares it for the caller (Hooks.Response),
// in the session that the caller has, whose o= line is origin. That
// line stays, with its version one up (RFC 3264 8). A role offers it in
// the early dialog when it has given the caller a session of its own,
// such as the tone of the MRF, and takes the caller off it (TS 24.182
// 4.5.5.3.7).
//
// Before the 2xx the offer goes in an UPDATE (RFC 3311 5.1), since no
// re-INVITE goes while the INVITE is in progress (RFC 3261 14.1). Once
// the dialog is confirmed, a caller whose INVITE does not list UPDATE
// in Allow gets it in a re-INVITE. An offer that crosses one of the
// caller goes again after GlareDelay (RFC 3261 14.1, RFC 3311 5.3), at
// most glareTries times in all, and no more once one of the legs has
// ended.
//
// No offer goes once an offer of the caller's or of the callee's own
// has crossed the confirmed dialogs (changesSession): the two ends have
// settled the session between them, and the session description that
// their dialog sent before is stale. An offer of the Call's own would
// take the caller back to it, and undo a hold that the caller has just
// asked for.
//
// It returns the o= line of the offer, from which the role's next offer
// goes on, and the caller's answer as the callee's side may take it
// (Hooks.Response toward the callee). The answer is empty when the
// caller did not take the offer. When the role cannot pass the offer or
// the answer on (Hooks), the Call releases the call, and the answer is
// empty too.
func (c *Call) OfferCaller(tag string, origin sdp.Origin) (sdp.Origin, sip.Body) {
	c.mu.Lock()
	last := c.early.last[tag]
	c.mu.Unlock()
	if last == nil {
		c.log.Warn("no session description of the dialog that answered to offer the caller", "to_tag", tag)
		return origin, sip.Body{}
	}
	prepared, ok := c.hooks.Response(last, c.in.Request, ToCaller)
	if !ok {
		c.release()
		return origin, sip.Body{}
	}
	offer, err := sdp.Parse(prepared.Body.Content)
	if err != nil {
		c.log.Warn("the session of the dialog that answered is not understood", "to_tag", tag, "error", err.Error())
		return origin, sip.Body{}
	}
	offer.Origin = origin
	offer.Origin.Version++
	method, send := sip.UPDATE, c.in.Update
	if d, _ := c.in.Dialog(); d.State == sip.DialogConfirmed && !c.in.Request.Allows(sip.UPDATE) {
		method, send = sip.INVITE, c.in.ReInvite
	}
	body := sip.Body{Type: sessionDescription, Content: offer.Bytes()}
	var resp *sip.Message
	for try := 1; ; try++ {
		if c.endsSettled() {
			c.log.Info("the session of the dialog that answered is no longer the one to offer the caller",
				"to_tag", tag, "try", try)
			return offer.Origin, sip.Body{}
		}
		resp, err = send(body)
		if !crossed(resp, err) || try >= glareTries {
			break
		}
		c.log.Info("the offer to the caller crossed one of the caller", "method", string(method), "try", try)
		// The caller created the Call-ID of its dialog with the B2BUA.
		select {
		case <-c.in.Done():
			return offer.Origin, sip.Body{}
		case <-c.outDone():
			return offer.Origin, sip.Body{}
		case <-time.After(sip.GlareDelay(false)):
		}
	}
	switch {
	case err != nil:
		c.log.Warn("the session of the dialog that answered not offered to the caller", "method", string(method), "error", err.Error())
		return offer.Origin, sip.Body{}
	case resp.StatusCode >= 300:
		c.log.Warn("the caller refused the session of the dialog that answered", "method", string(method), "status", resp.StatusCode)
		return offer.Origin, sip.Body{}
	}
	c.sentToCaller(body)
	c.log.Info("the caller has the session of the dialog that answered", "method", string(method), "to_tag", tag)
	answer, ok := c.hooks.Response(resp, c.in.Request, ToCallee)
	if !ok {
		c.release()
		return offer.Origin, sip.Body{}
	}
	return offer.Origin, answer.Body
}

// glareTries is how many offers the Call sends the caller in all for
// one session it moves, when each crosses an offer of the caller. RFC
// 3261 14.1 leaves the number to the UAC.
const glareTries = 3

// crossed reports whether an offer within the dialog crossed one of the
// peer: the peer refused it with 491 (Request Pending), or the UA did
// not send it while an offer or an INVITE of either side was open (RFC
// 3261 14.1, RFC 3311 5.2). An offer of this UA that waits for its
// answer is one of the Call's own that has not finished, which the
// answer of the peer still ends, so it is worth another try as much as
// the others (answerRequest answers a request that crossed it with 491
// in the same way).
func crossed(resp *sip.Message, err error) bool {
	if err != nil {
		return errors.Is(err, sip.ErrOfferUnanswered) || errors.Is(err, sip.ErrOfferPending) ||
			errors.Is(err, sip.ErrInviteInProgress)
	}
	return resp.StatusCode == 491
}

// endsSettled tells that an offer of the caller's or of the callee's
// own has crossed the confirmed dialogs since the callee answered.
func (c *Call) endsSettled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settled
}

// outDone is the end of the outgoing leg, nil while there is none.
func (c *Call) outDone() <-chan struct{} {
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	if out == nil {
		return nil
	}
	return out.Done()
}

// sentToCaller notes the o= line of body, a session description that
// has gone to the caller: an offer of the Call's own goes on in that
// session, with the version one above the last (RFC 3264 8).
//
// A response to the INVITE is noted as it goes out, before the PRACK or
// the ACK that may follow it: the caller answers no response with a
// status code, so one that has gone out is one that it has, and a
// reliable provisional response would otherwise be noted only once its
// PRACK came, after the session descriptions that went out meanwhile.
// A session description in a request of the Call's own is noted once
// the caller has accepted it (OfferCaller, answerRequest), since a
// caller that refuses one keeps the session it had.
func (c *Call) sentToCaller(body sip.Body) {
	if len(body.Content) == 0 || body.Type != sessionDescription {
		return
	}
	session, err := sdp.Parse(body.Content)
	if err != nil {
		c.log.Info("the session description that went to the caller is not understood", "error", err.Error())
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.early.origin = session.Origin
}

// callerOrigin is the o= line of the last session description that went
// to the caller.
func (c *Call) callerOrigin() sdp.Origin {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.early.origin
}
