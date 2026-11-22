package mmtel

import (
	"context"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is what the AS adds to the messages that b2bua.Call relays
// between the two legs of a call, which TS 24.229 5.7.5.1 has a routeing
// B2BUA pass on: the header fields of the other leg (fields.go), the
// Contact of a conference focus (focusrelay.go), the services that act
// on a response (sessioncase.go), the media that the MRF gives the
// caller (cat.go), and the record of the call (call.go).

var _ b2bua.Hooks = (*call)(nil)

// Request prepares req, a request within a dialog that crosses in d,
// for the other leg. The AS refuses none: the services decide on the
// initial INVITE, and a routeing B2BUA passes the requests within the
// dialogs on, whatever their method.
func (c *call) Request(req *sip.Message, d b2bua.Direction) (b2bua.Message, *b2bua.Refusal) {
	if d == b2bua.ToCaller {
		// The callee sent it, so its session description is the callee's
		// in that dialog, such as the one of an UPDATE that states the
		// preconditions (keepCalleeSDP).
		c.keepCalleeSDP(fromTag(req), sip.BodyOf(req))
	}
	return b2bua.Message{Headers: c.requestFields(req).Headers, Body: sip.BodyOf(req)}, nil
}

// Response prepares resp, a response to req that crosses in d, for the
// other leg, with the services that act on a response that goes to the
// caller's side (servicesOnResponse).
func (c *call) Response(resp, req *sip.Message, d b2bua.Direction) b2bua.Message {
	toCaller := d == b2bua.ToCaller
	if toCaller {
		c.keepCalleeSDP(toTag(resp), sip.BodyOf(resp))
		if req == c.in.Request && resp.StatusCode < 200 {
			c.sessionToTheCaller(resp)
		}
	}
	return b2bua.Message{Headers: c.responseFields(resp, req, toCaller).Headers, Body: sip.BodyOf(resp)}
}

// sessionToTheCaller notes the early dialog of the callee's side whose
// session description a provisional response brings the caller, as the
// Call has prepared it: the body is gone when the caller keeps the
// session description of another early dialog. CAT goes on in the
// session of that dialog (offerTone), and the 2xx carries its answer
// when no reliable response did (Answer).
func (c *call) sessionToTheCaller(resp *sip.Message) {
	if !sip.HasSessionDescription(resp) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionFrom = toTag(resp)
	if resp.Requires(sip.Option100rel) {
		// RFC 3262 5: the offer and the answer of the INVITE are done on
		// the caller's leg, so a later response carries no further offer
		// to it.
		c.sessionToCaller = true
	}
}

// Answer prepares the callee's 2xx for the caller. The AS stops a tone
// that the caller has and moves the caller's media to the dialog that
// answered (answerAfterTone), and the 2xx carries the answer that the AS
// withheld for a possible diversion (withholdSession).
//
// session is the last response with a session description of the dialog
// that answered, when the caller has that of another early dialog: the
// 2xx then goes without one, since the caller takes only the first (RFC
// 3261 13.2.1), and the Call offers it that session once the caller has
// acknowledged the 2xx. When no reliable response carried the answer to
// the offer of the INVITE, the 2xx is the response that has to carry it,
// and it carries the one the caller already has (13.2.1, RFC 3262 5).
func (c *call) Answer(resp, session *sip.Message) b2bua.Message {
	tag := toTag(resp)
	body := sip.BodyOf(resp)
	// When the caller follows another early dialog, resp comes without
	// its session description, and session is the last one of this
	// dialog, which the 2xx itself may carry.
	if session != nil {
		c.keepCalleeSDP(tag, sip.BodyOf(session))
	} else {
		c.keepCalleeSDP(tag, body)
	}
	c.mu.Lock()
	held, withheld := c.heldAnswer[tag], c.withhold
	answered := len(c.in.Request.Body) == 0 || c.sessionToCaller
	callerHas := c.calleeSDP[c.sessionFrom]
	c.mu.Unlock()
	switch {
	case c.answerAfterTone(resp):
		// TS 24.182 4.5.5.3.7: the caller had its answer in a reliable
		// provisional response, so the 2xx has no session description.
		body = sip.Body{}
	case withheld && len(body.Content) == 0:
		body = held
	case session != nil && !answered:
		body = sip.Body{Type: sessionDescription, Content: callerHas}
	}
	return b2bua.Message{Headers: c.responseFields(resp, c.in.Request, true).Headers, Body: body}
}

// sessionDescription is the media type of a session description (RFC
// 8866 5).
const sessionDescription = "application/sdp"

// Ack is the body of ack, the ACK that answers the offer of a 2xx (RFC
// 3261 13.2.2.4), as the other leg gets it.
func (c *call) Ack(ack *sip.Message, _ b2bua.Direction) sip.Body { return sip.BodyOf(ack) }

// Contact is the Contact that the leg to the caller takes for the
// Contact of resp: that of a conference focus crosses as it came, and
// the AS record-routes itself in the dialog (focusContact). The UA
// writes its own Contact for any other.
func (c *call) Contact(resp *sip.Message) (sip.NameAddr, bool, bool) {
	contact, ok := focusContact(resp)
	return contact, true, ok
}

// Reasons are the Reason header fields of ended, the request that ended
// one leg, for the request that ends the other: they cross as they came,
// so that the far end learns the cause (RFC 3326 2).
func (c *call) Reasons(ended *sip.Message, _ b2bua.Direction) []sip.HeaderField {
	return sip.ReasonFields(ended)
}

// KeepDialogs writes the record of the call into the Store, with both
// dialogs as they are now: once the callee has answered, and again after
// each request within the dialogs. A target refresh request changes the
// remote target of its dialog (RFC 3261 12.2), and each request the CSeq
// numbers: another instance that ends the dialogs needs both (12.2.1.1,
// 12.2.2). Before the answer and after the end of the call it writes
// nothing.
//
// The caller and the callee have their responses by then, so no request
// waits for the record and a failure of the store is logged. The call
// goes on in this instance; a request that looks the call up in the
// store, such as an INVITE whose Replaces names one of its legs, does
// not find it, or finds the dialogs as they were, until a later write
// succeeds.
func (c *call) KeepDialogs(in, out sip.Dialog) {
	c.recordMu.Lock()
	defer c.recordMu.Unlock()
	if !c.recorded {
		return
	}
	// sip.IncomingCall carries no context. The Store bounds each of its
	// operations itself: an adapter on a store outside the process gives
	// each one a time limit.
	if err := c.as.store.PutCall(context.Background(), c.record(in, out)); err != nil {
		c.log.Warn("call not kept in the store", "error", err.Error())
	}
}
