package b2bua

import (
	"errors"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file relays a re-INVITE within the dialogs of a call: the
// request on to the other leg, the provisional responses of that leg
// back, and the PRACK of each reliable one on as the PRACK of the
// response it copies (RFC 3262 4). TS 24.229 5.7.5.1 has a routeing
// B2BUA pass the header fields and the bodies on, so that the two ends
// see each other's reliable 183 and PRACK, and the UPDATE that tells
// that the resources are reserved can follow (RFC 3312 13.1, TS 24.229
// 6.1.4.2 item b).
//
// SIMPLIFIED: RFC 3261 9.1 and RFC 6141 3.3. A CANCEL for a re-INVITE
// that the Call relays ends on the leg it came on, and the Call sends
// none on the other leg. The UA answers the re-INVITE with 487 while no
// reliable provisional response has carried its session description, and
// leaves it to the final response of the other leg once one has
// (sip.InDialogRequest.Cancelled); the far end answers it as it would
// have without the CANCEL, so the two ends can end up with different
// sessions. No UE of this project cancels a re-INVITE.

// reInviteRelay is one re-INVITE that crosses the B2BUA, from its
// arrival to its final response. The Call finds it by the CSeq number
// that the RAck of a PRACK for one of its responses names (RFC 3262
// 7.2).
type reInviteRelay struct {
	// key is where the Call keeps the relay (Call.reInvites).
	key reInviteKey
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
	// counts (RFC 3261 13.2.1, RFC 3262 5). prepareProvisional sets it
	// as the UA hands the responses of to over, one after the other, and
	// reInvite reads it once the final response has come after them.
	described *sip.Message
	// provisionals are the provisional responses of to that the role has
	// prepared, in the order they came, and that wait to go back. A
	// goroutine of their own sends them (relayProvisionals), since the
	// copy of a reliable one waits for its PRACK, and the final response
	// of to must not wait behind it (RFC 3262 3). relayed closes once the
	// last of them has gone.
	provisionals chan provisional
	relayed      chan struct{}
	// done tells that the last copy has gone, so the relay can go once
	// the PRACK of each copy has been taken (forgetWhenDone). c.mu guards
	// it.
	done bool
}

// provisional is a provisional response p of the far leg of a relayed
// re-INVITE, as the role prepared it for the other leg (m). offer tells
// that p carries the offer of the far end, after a re-INVITE without
// one, so its PRACK has to carry the answer (RFC 3262 5).
type provisional struct {
	p     *sip.Message
	m     Message
	offer bool
}

// queuedProvisionals is how many prepared provisional responses of a
// re-INVITE may wait to go back before the UA stops reading the next
// response of the far leg. The far end sends no reliable one before the
// PRACK of the one before (RFC 3262 3), so only provisional responses
// that are not reliable fill it.
const queuedProvisionals = 16

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
// The re-INVITE holds its turn only until the first reliable
// provisional response of to whose copy goes reliably, which shows that
// to has it: the PRACK of that copy and the UPDATE after it come after
// the re-INVITE on the same leg, and would otherwise wait for its final
// response (turns).
//
// RFC 3261 13.2.1: a session description in a reliable provisional
// response is the one that counts, and the 2xx then carries none. When
// the side that sent r offered no 100rel, its copy of that response went
// unreliably, so that side takes the session description only from the
// 2xx: the 2xx that goes back carries the one of the reliable response
// instead of its own, and both sides have the same session.
func (c *Call) reInvite(r *sip.InDialogRequest, to leg, m Message, d Direction, finish func()) (*sip.Message, error) {
	k := &reInviteRelay{
		to: to, d: d, reliable: r.Request.Supports(sip.Option100rel),
		provisionals: make(chan provisional, queuedProvisionals),
		relayed:      make(chan struct{}),
	}
	cseq, _ := sip.ParseCSeq(r.Request.Get("CSeq"))
	k.key = keyFor(r.Request, cseq.Number)
	c.mu.Lock()
	if c.reInvites == nil {
		c.reInvites = map[reInviteKey]*reInviteRelay{}
	}
	c.reInvites[k.key] = k
	c.mu.Unlock()

	// The PRACKs of the copies are the Call's to relay, so the UA hands
	// them over instead of answering them itself.
	r.RelayPRACK()
	go c.relayProvisionals(r, k)
	re := sip.ReInvitation{
		Body:        m.Body,
		Headers:     m.Headers,
		ManualPRACK: k.reliable,
		OnProvisional: func(p *sip.Message) {
			if p.Requires(sip.Option100rel) && k.reliable {
				// to has the re-INVITE, and the side that sent it is to
				// acknowledge the copy of p, and may then send the UPDATE
				// that the far end waits for: those requests of this leg
				// need not wait for the final response of the re-INVITE.
				// Without a reliable copy nothing of the sort follows, and
				// the re-INVITE keeps its turn, up to the ACK that answers
				// an offer of its 2xx (forward).
				finish()
			}
			if pr, ok := c.prepareProvisional(r, k, p); ok {
				k.provisionals <- pr
			}
		},
	}
	resp, err := to.ReInviteWith(re)
	close(k.provisionals)
	if err != nil || resp.StatusCode >= 300 {
		// RFC 3262 3: a final response other than 2xx need not wait for
		// the PRACKs of the provisional responses before it. Once it is
		// sent, a copy that still waits for its PRACK gives up.
		return resp, err
	}
	// A 2xx goes after the copies, as it came after the responses they
	// copy. The far end sends none before the PRACK of a reliable
	// response with a session description, so only the PRACK of a copy
	// without one may still be on its way (RFC 3262 3).
	<-k.relayed
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

// prepareProvisional has the role prepare p, a provisional response to
// the re-INVITE r that the far leg of its relay k sent, for the side
// that sent r. It runs before the UA reads the next response of the far
// leg, so the role sees the responses in the order they came, and a
// final response finds what they changed (Hooks.Unanswered). It reports
// false when the role releases the call instead.
func (c *Call) prepareProvisional(r *sip.InDialogRequest, k *reInviteRelay, p *sip.Message) (provisional, bool) {
	m, ok := c.hooks.Response(p, r.Request, k.d.reverse())
	if !ok {
		c.release()
		return provisional{}, false
	}
	offer := false
	if p.Requires(sip.Option100rel) && sip.HasSessionDescription(p) && k.described == nil {
		k.described = p
		offer = len(r.Request.Body) == 0
	}
	return provisional{p: p, m: m, offer: offer}, true
}

// relayProvisionals sends the prepared provisional responses of k back
// to the side that sent r, one after the other, and closes k.relayed
// once the last has gone.
func (c *Call) relayProvisionals(r *sip.InDialogRequest, k *reInviteRelay) {
	for pr := range k.provisionals {
		c.reInviteProvisional(r, k, pr)
	}
	c.mu.Lock()
	k.done = true
	c.mu.Unlock()
	c.forgetWhenDone(k)
	close(k.relayed)
}

// takeCopy is the response whose copy the next PRACK acknowledges
// (unacked.acknowledged): among the copies of k, a relayed re-INVITE,
// or among those of the INVITE of the call when k is nil.
func (c *Call) takeCopy(k *reInviteRelay) (acked *sip.Message, own bool) {
	if k == nil {
		return c.copies.acknowledged()
	}
	acked, own = k.copies.acknowledged()
	c.forgetWhenDone(k)
	return acked, own
}

// forgetWhenDone lets k go once its last copy has gone and the PRACK of
// each has been taken. The UA hands a PRACK over as it ends the wait of
// Progress, and the goroutine that takes it may find the relay only
// after the final response of the re-INVITE has come (reInviteOf).
func (c *Call) forgetWhenDone(k *reInviteRelay) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k.done && k.copies.empty() {
		delete(c.reInvites, k.key)
	}
}

// reInviteProvisional passes pr, a provisional response to the re-INVITE
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
// the B2BUA's own (RFC 3262 4): the UA of to sends it by itself when r
// offered no 100rel (ReInvitation.ManualPRACK unset), and the Call when
// a reliable copy failed to go. A copy that went and got no PRACK gets
// none of the B2BUA's either: the UA has rejected r with 500, and the
// far end gives up its own re-INVITE the same way (RFC 3262 3). Nor
// does a copy that the final response of r overtook.
//
// RFC 3262 5: after a re-INVITE without an offer, the first reliable
// response with a body has the offer of the far end, and the PRACK of
// its copy the answer. The B2BUA has no answer of its own, so a
// response with that offer whose copy did not go reliably gets no
// PRACK, and the far end gives the re-INVITE up with a 5xx (RFC 3262
// 3).
func (c *Call) reInviteProvisional(r *sip.InDialogRequest, k *reInviteRelay, pr provisional) {
	p, m := pr.p, pr.m
	reliable := p.Requires(sip.Option100rel)
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
	err := send(p.StatusCode, m.Body, m.Headers...)
	if err == nil {
		return
	}
	c.log.Info("provisional response to the re-INVITE not relayed", "status", p.StatusCode, "error", err.Error())
	// No PRACK comes for a copy that did not go, nor any more for one
	// whose re-INVITE has its final response.
	k.copies.dropped(sent)
	over := errors.Is(err, sip.ErrNoPRACK) || errors.Is(err, sip.ErrRequestAnswered) || errors.Is(err, sip.ErrCallEnded)
	if reliable && k.reliable && !over && !pr.offer {
		if _, err := k.to.PRACK(p, sip.Body{}); err != nil {
			c.log.Warn("PRACK not sent", "status", p.StatusCode, "error", err.Error())
		}
	}
}
