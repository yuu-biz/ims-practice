package b2bua

import "github.com/yuu-biz/ims-practice/internal/sip"

// This file relays a re-INVITE within the dialogs of a call: the
// request on to the other leg, the provisional responses of that leg
// back, and the PRACK of each reliable one on as the PRACK of the
// response it copies (RFC 3262 4). TS 24.229 5.7.5.1 has a routeing
// B2BUA pass the header fields and the bodies on, so that the two ends
// see each other's reliable 183 and PRACK, and the UPDATE that tells
// that the resources are reserved can follow (RFC 3312 13.1, TS 24.229
// 6.1.4.2 item b).

// reInviteRelay is one re-INVITE that crosses the B2BUA, from its
// arrival to its final response. The Call finds it by the CSeq number
// that the RAck of a PRACK for one of its responses names (RFC 3262
// 7.2).
type reInviteRelay struct {
	// to is the leg that the re-INVITE went on in, to which the PRACK of
	// each copy of a reliable provisional response of that leg goes.
	to leg
	// d is the way the re-INVITE crosses, so its responses cross the
	// other way.
	d Direction
	// reliable tells that the re-INVITE offered 100rel, so the copy of a
	// reliable provisional response of to goes reliably too, and the side
	// that sent the re-INVITE acknowledges it with a PRACK (RFC 3262 3).
	// Without it that side sends no PRACK, and the UA of to acknowledges
	// the reliable responses itself.
	reliable bool
	// copies are the copies that wait for a PRACK of that side (prack.go).
	copies unacked
	// described is the first reliable provisional response of to that
	// carried a session description: the answer to the offer of the
	// re-INVITE, or the offer when it had none, and the only one that
	// counts (RFC 3261 13.2.1, RFC 3262 5). Only the goroutine of
	// reInvite touches it, which takes the responses of to one after the
	// other.
	described *sip.Message
}

// reInviteKey names a re-INVITE that a Call relays, by the dialog it
// came in and its CSeq number.
type reInviteKey struct {
	callID string
	cseq   uint32
}

// keyFor is the key of the re-INVITE with the CSeq number cseq in the
// dialog of m, a message of that dialog.
func keyFor(m *sip.Message, cseq uint32) reInviteKey {
	return reInviteKey{callID: m.Get("Call-ID"), cseq: cseq}
}

// reInviteOf is the re-INVITE that the Call relays and whose response
// req, a PRACK, acknowledges the copy of: the one in the same dialog
// with the CSeq number that the RAck names (RFC 3262 7.2). It is nil
// for the PRACK of a response to the INVITE of the call, whose copies
// the Call keeps itself (Call.copies), and for one that names no
// re-INVITE being relayed.
func (c *Call) reInviteOf(req *sip.Message) *reInviteRelay {
	if req.Method != sip.PRACK {
		return nil
	}
	rack, err := sip.ParseRAck(req.Get("RAck"))
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reInvites[keyFor(req, rack.CSeq.Number)]
}

// reInvite sends r, a re-INVITE that crosses the B2BUA in direction d,
// on in to as the role prepared it (m), and returns its final response.
//
// The provisional responses of to go back to the side that sent r
// (relayed), so that an answer in a reliable one reaches it and it can
// send the UPDATE that follows (RFC 3311 5.1). It acknowledges each copy
// with a PRACK, which the Call takes among the requests of its leg and
// sends on as the PRACK of the response that the copy is of (Call.prack).
// The re-INVITE holds its turn only until the first provisional response
// of to, which shows that to has it: its own PRACKs and UPDATEs come
// after it on the same leg, and would otherwise wait for its final
// response (turns).
//
// RFC 3261 13.2.1: a session description in a reliable provisional
// response is the one that counts, and the 2xx then carries none. When
// the side that sent r offered no 100rel, its copy of that response went
// unreliably, so that side takes the session description only from the
// 2xx: the 2xx that goes back carries the one of the reliable response
// instead of its own, and both sides have the same session.
func (c *Call) reInvite(r *sip.InDialogRequest, to leg, m Message, d Direction, finish func()) (*sip.Message, error) {
	k := &reInviteRelay{to: to, d: d, reliable: r.Request.Supports(sip.Option100rel)}
	cseq, _ := sip.ParseCSeq(r.Request.Get("CSeq"))
	key := keyFor(r.Request, cseq.Number)
	c.mu.Lock()
	if c.reInvites == nil {
		c.reInvites = map[reInviteKey]*reInviteRelay{}
	}
	c.reInvites[key] = k
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.reInvites, key)
		c.mu.Unlock()
	}()

	// The PRACKs of the copies are the Call's to relay, so the UA hands
	// them over instead of answering them itself.
	r.RelayPRACK()
	re := sip.ReInvitation{
		Body:        m.Body,
		Headers:     m.Headers,
		ManualPRACK: k.reliable,
		OnProvisional: func(p *sip.Message) {
			// to has the re-INVITE, so the requests of this leg that came
			// after it need not wait for its final response.
			finish()
			c.reInviteProvisional(r, k, p)
		},
	}
	resp, err := to.ReInviteWith(re)
	if err != nil || resp.StatusCode >= 300 {
		return resp, err
	}
	switch {
	case k.described == nil:
	case k.reliable:
		// The copy of described carried the session description, so the
		// 2xx carries none that counts.
		resp = withoutBody(resp)
	default:
		// The role prepares that session description for the other leg
		// again, as it did for the copy (Call.answerRequest).
		resp = resp.Clone()
		resp.Set("Content-Type", k.described.Get("Content-Type"))
		resp.Body = k.described.Body
	}
	return resp, err
}

// reInviteProvisional passes p, a provisional response to the re-INVITE
// r that the far leg of its relay k sent, back to the side that sent r.
//
// The copy goes reliably when p came reliably and r offered 100rel. It
// then waits for its PRACK, as sip.InDialogRequest.Progress does, and
// the Call sends that PRACK on as the PRACK of p (Call.prack): it comes
// while Progress waits, so the relay is still there to be found
// (reInviteOf). A p that came without 100rel is copied unreliably even
// where r offered 100rel: a session description in it is no answer, and
// a reliable copy would have the side that sent r take it for the
// answer and leave the answer that follows unread (RFC 3261 13.2.1, RFC
// 3262 5). A reliable p whose copy did not go reliably gets a PRACK of
// the B2BUA's own (RFC 3262 4).
//
// RFC 3262 5: after a re-INVITE without an offer, the first reliable
// response with a body has the offer of the far end, and the PRACK of
// its copy the answer. The B2BUA has no answer of its own, so a
// response with that offer whose copy did not go reliably gets no
// PRACK, and the far end gives the re-INVITE up with a 5xx (RFC 3262
// 3).
func (c *Call) reInviteProvisional(r *sip.InDialogRequest, k *reInviteRelay, p *sip.Message) {
	reliable := p.Requires(sip.Option100rel)
	m, ok := c.hooks.Response(p, r.Request, k.d.reverse())
	if !ok {
		c.release()
		return
	}
	offer := false
	if reliable && len(m.Body.Content) > 0 && k.described == nil {
		k.described = p
		offer = len(r.Request.Body) == 0
	}
	var sent *copied
	send := r.ProgressUnreliably
	if reliable && k.reliable {
		sent = &copied{of: p}
		k.copies.sent(sent)
		send = r.Progress
	}
	if k.d == ToCallee {
		// The response goes to the caller, which sent the re-INVITE.
		c.sentToCaller(m.Body)
	}
	if err := send(p.StatusCode, m.Body, m.Headers...); err != nil {
		c.log.Info("provisional response to the re-INVITE not relayed", "status", p.StatusCode, "error", err.Error())
		// No PRACK comes for a copy that did not go.
		k.copies.dropped(sent)
		sent = nil
	}
	if reliable && sent == nil && !offer {
		if _, err := k.to.PRACK(p, sip.Body{}); err != nil {
			c.log.Warn("PRACK not sent", "status", p.StatusCode, "error", err.Error())
		}
	}
}
