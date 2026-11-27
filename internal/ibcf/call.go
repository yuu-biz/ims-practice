package ibcf

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the IBCF as a B2BUA for an INVITE (TS 24.229 5.10.5):
// the incoming INVITE ends in a dialog with the side that sent it, and
// the IBCF sends a new INVITE in a dialog of its own with the other
// side. Each dialog has its own Call-ID, tags, CSeq, Via and Contact.
// b2bua.Call relays the call between the two; what crosses from one to
// the other is what interconnect.Border lets through
// (interconnect.CrossingFields), with the SDP through the TrGW: the
// Hooks of the call.
//
// The Contact of each dialog is the IBCF's own URI on that side, with
// the feature tags of the far end, except where TS 24.229 5.10.5 has it
// cross as it came (contactFor).
//
// SIMPLIFIED: TS 24.229 5.10.5. The Contact of a 3xx does not cross, so
// a redirection from the other network reaches the caller without its
// targets; the IBCF does not recurse on it, as 5.10.2.2 and 5.10.3.2
// ask. That Contact names where to try next, not a dialog, and no node
// of this project answers with a 3xx.
//
// SIMPLIFIED: TS 24.229 5.10.2.2 and 5.10.3.2. The IBCF does not ask
// for a session timer of its own (RFC 4028 8). It carries the caller's
// Session-Expires and Min-SE into its INVITE, and each dialog then runs
// the session timer that its two ends agree on.

// inviteFields are the leg fields of an initial INVITE that cross all
// the same (interconnect.CrossingFields).
var inviteFields = []string{"Allow", "Supported", "Session-Expires", "Min-SE"}

// call is one INVITE that crossed the border, from its arrival to the
// end of the call. in is the IBCF's dialog with the side that sent the
// INVITE, the caller's; legs relays between it and the dialog with the
// other side, the callee's.
type call struct {
	b *IBCF
	*crossing
	in      *sip.IncomingCall
	inSide  side
	outSide side
	record  CallRecord
	legs    *b2bua.Call

	// mediaMu orders the requests to the TrGW of the call and the
	// writes of its record. media is its TrGW context, nil until the
	// first SDP with an audio stream. ended is set once the call has
	// ended and its record is gone, so that no later write brings the
	// record back.
	//
	// sends is where each termination of the context sends now, and
	// agreed where it sent when the last offer and answer of its side
	// were done (agree), to which it goes back when an offer within a
	// dialog gets no 2xx (Unanswered). offered holds the offer of a side
	// in a response, until the ACK or the PRACK answers it (offers).
	// previews holds, for a termination that the session description of
	// a provisional response that is not reliable moved, the request that
	// response answered (previewed).
	mediaMu  sync.Mutex
	media    *MediaContext
	ended    bool
	sends    map[Side]netip.AddrPort
	agreed   map[Side]netip.AddrPort
	offered  map[Side]responseOffer
	previews map[Side]*sip.Message
}

// responseOffer is an offer that one side made in a response to a
// request without one: in a 2xx, which the ACK answers, or in a reliable
// provisional response, which its PRACK answers (RFC 3261 13.2.1, RFC
// 3262 5). cseq is the CSeq of that request, which the RAck of the PRACK
// names (RFC 3262 7.2), and sends where the termination facing that side
// sends for the offer.
type responseOffer struct {
	cseq  sip.CSeq
	sends netip.AddrPort
}

// onCall is the UAConfig.OnCall of side s: an INVITE outside a dialog,
// which the IBCF handles as an exit point (TS 24.229 5.10.2.2) or an
// entry point (5.10.3.2).
func (b *IBCF) onCall(s side, in *sip.IncomingCall) {
	c, r := b.prepare(s, in.Request, sourceOf(in.Request))
	if r != nil {
		in.Reject(r.status, r.headers...)
		return
	}
	k := &call{b: b, crossing: c, in: in, inSide: s, outSide: b.other(s),
		record: CallRecord{Instance: b.instance(), ID: sip.NewTag(), Direction: c.direction, Peer: c.peer.Profile.Name}}
	k.legs = b2bua.New(in, k, k.log)
	k.run()
}

// run sends the INVITE on the other side and relays between the two
// dialogs until the call ends.
func (c *call) run() {
	defer c.end()
	req := c.req
	if err := c.replacesOnTheOtherSide(req); err != nil {
		// RFC 3261 21.5.1, as the IBCF answers when it cannot write the
		// record of a call.
		c.log.Warn("cannot read the call that the Replaces names", "error", err.Error())
		c.legs.NoLeg()
		c.reject(500)
		return
	}
	ua, err := c.outSide.agent()
	if err == nil {
		err = c.throughTrGW(req, c.direction)
	}
	if err != nil {
		c.log.Warn("cannot set up the call", "error", err.Error())
		c.legs.NoLeg()
		if errors.Is(err, errMalformedSDP) {
			// As the border answers an SDP that it cannot read
			// (interconnect.Border), and notThroughTrGW one within a dialog.
			c.reject(488)
			return
		}
		c.reject(500)
		return
	}
	from, _ := sip.ParseNameAddr(req.Get("From"))
	to, _ := sip.ParseNameAddr(req.Get("To"))
	// The IBCF takes one hop away, as a proxy would (RFC 3261 16.6 step
	// 3), so that a call that two networks send round in a loop ends.
	headers := append(interconnect.CrossingFields(req, inviteFields...), sip.HeaderField{Name: "Max-Forwards", Value: req.Get("Max-Forwards")})
	contact, transparent := contactFor(c.outSide, req)
	out, err := c.legs.Attempt(ua, sip.Invitation{
		RequestURI:      req.RequestURI,
		From:            from.URI,
		FromDisplayName: from.DisplayName,
		To:              to.URI,
		Route:           c.route,
		Headers:         headers,
		Body:            sip.BodyOf(req),
		Contact:         contact,
		RecordRoute:     transparent,
	})
	if err != nil {
		c.log.Warn("cannot send the INVITE on", "error", err.Error())
		c.legs.NoLeg()
		c.reject(504)
		return
	}
	c.b.metrics.session(c.peer.Profile.Name, c.direction)
	c.log.Info("call across the border", "out_call_id", out.Request.Get("Call-ID"), "request_uri", req.RequestURI)

	var final *sip.Message
	for resp := range out.Responses() {
		switch code := resp.StatusCode; {
		case code < 200:
			c.legs.RelayProvisional(out, resp)
		case code < 300:
			c.legs.Answer(out, resp)
			return
		default:
			final = resp
		}
	}
	switch {
	case answered(c.in):
		// The UA has answered the caller's INVITE with 487 already, for
		// its CANCEL; or the call has answered it with 500 and cancelled
		// the INVITE on this side, to release the call (b2bua.Refusal,
		// b2bua.Hooks).
	case final != nil:
		c.b.metrics.failure(c.peer.Profile.Name, c.direction, final.StatusCode)
		m, _ := c.Response(final, c.in.Request, b2bua.ToCaller)
		c.in.Reject(final.StatusCode, m.Headers...)
	default:
		// 504 (Server Time-out), as the CSCFs answer when their next
		// hop does not (ims.Finish), rather than the 408 of RFC 3261
		// 16.8.
		c.log.Warn("the other side did not answer", "error", errText(out.Err()))
		c.reject(504)
	}
}

// replacesOnTheOtherSide rewrites the Replaces of m, an INVITE that
// crosses the border in c.direction (RFC 3891). The Replaces names a
// dialog as the IBCF has it on the side where the INVITE came in: its
// to-tag is the IBCF's own tag there (RFC 3891 3). When that dialog is
// one side of a call that crosses the border here, the INVITE goes on
// with the dialog of the other side instead, the one that the next hop
// knows. As an IMS-ALG the IBCF correlates the dialog identifiers of
// the two dialogs that it manages (TS 24.229 5.10.5), and 5.7.5.1 spells
// this rule out for an AS that is a routeing B2BUA. The call may be one
// that another instance of the IBCF holds; the Store finds it by the
// dialog.
//
// A Replaces that names no call here goes on as it came, as 5.7.5.1 has
// the AS pass it on. It may name a dialog of a B2BUA beyond the IBCF,
// such as the MMTel AS of the other network, which maps it in turn; and
// when no node has the dialog, the UA at the end answers 481 (RFC 3891
// 3). The error is a failure of the Store, after which the IBCF cannot
// tell which of the two the Replaces names.
//
// SIMPLIFIED: RFC 3891 3. A call has its dialogs in its record only
// once both are confirmed, so a Replaces of an early dialog goes on as
// it came. The IBCF would have to write each early dialog of a call as
// it starts, and no node of this project replaces an early dialog.
func (c *call) replacesOnTheOtherSide(m *sip.Message) error {
	values := m.GetAll("Replaces")
	if len(values) != 1 {
		// None, or more than one, which the far end refuses with 400.
		return nil
	}
	r, err := sip.ParseReplaces(values[0])
	if err != nil {
		return nil
	}
	// sip.IncomingCall carries no context; the Store bounds the read.
	record, ok, err := c.b.store.CallOf(context.Background(), r.DialogID())
	if err != nil || !ok {
		return err
	}
	if record.Peer != c.peer.Profile.Name {
		// A call with another peer network: its dialog on the link to
		// that network is no business of this one, which the INVITE goes
		// to or came from.
		return nil
	}
	near, far := record.Home, record.PeerSide
	if c.direction == interconnect.Entry {
		near, far = far, near
	}
	if near == nil || far == nil || near.ID != r.DialogID() {
		// It names the dialog on the side where the INVITE goes, which the
		// next hop knows as it is.
		return nil
	}
	// The next hop compares to-tag with its own tag, which is the IBCF's
	// remote tag on that side.
	other := sip.Replaces{CallID: far.ID.CallID, ToTag: far.ID.RemoteTag, FromTag: far.ID.LocalTag, EarlyOnly: r.EarlyOnly}
	m.Set("Replaces", other.String())
	c.log.Info("Replaces names the other side of a call", "replaces", r.String(), "now", other.String(),
		"call_record", record.ID, "instance", record.Instance)
	return nil
}

// end releases what the call held: its TrGW context and its record.
// A call that ends because the IBCF closes keeps both, as a process
// that stops without closing would, for ReleaseLeftovers.
//
// No request waits for the removal of the record, so a failure of the
// store is logged. The record then stays until the next process of the
// instance ends the call again: its BYEs get 481, and the TrGW no
// longer has the context.
func (c *call) end() {
	if c.b.closed.Load() {
		return
	}
	c.mediaMu.Lock()
	media := c.media
	c.ended = true
	if err := c.b.store.DeleteCall(context.Background(), c.record.Instance, c.record.ID); err != nil {
		c.log.Warn("cannot remove the record of the call", "error", err.Error())
	}
	c.mediaMu.Unlock()
	if media != nil {
		c.b.releaseMedia(*media, c.log)
	}
}

// reject answers the caller's INVITE with a response of the IBCF's own,
// which counts among its refusals once it has gone: the INVITE may have
// its final response already, such as the 487 of a CANCEL.
func (c *call) reject(code int) {
	if c.in.Reject(code) == nil {
		c.b.metrics.reject(c.peer.Profile.Name, c.direction, sip.INVITE, code)
	}
}

// answered reports whether the caller's INVITE has its final response.
func answered(in *sip.IncomingCall) bool {
	select {
	case <-in.Done():
		return true
	default:
		return false
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// errNotKept is what throughTrGW returns when the record of the call,
// with the TrGW context that it has just reserved, cannot be written.
// The SDP of the message went through the context all the same.
var errNotKept = errors.New("ibcf: the record of the call cannot be written")

// throughTrGW puts the SDP of m, which crosses the border in direction
// d, through the TrGW context of the call. The first SDP with an audio
// stream gets the call a context of its own, one for each pass through
// the IBCF; that is the offer of the INVITE, or the offer in a
// response when the INVITE has none. The record of the call is written
// with the context, so that a later process of the instance can release
// it (ReleaseLeftovers). When that write fails, throughTrGW returns
// errNotKept and the call keeps the context: each later write is of the
// whole record, so the next one that succeeds has the context too.
//
// sip.IncomingCall carries no context, so the store has the time limit
// of its own adapter.
func (c *call) throughTrGW(m *sip.Message, d interconnect.Direction) error {
	if !sip.HasSessionDescription(m) {
		return nil
	}
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	var kept error
	if c.media == nil {
		audio, err := hasAudio(m)
		if err != nil || !audio {
			return err
		}
		ctx, cancel := trgwContext()
		mc, err := c.b.reserveMedia(ctx)
		cancel()
		if err != nil {
			return err
		}
		c.media = &mc
		c.record.Media = &mc
		c.log.Info("media through the TrGW", "context", mc.ID, "core", mc.Core.String(), "peer", mc.Peer.String())
		if err := c.b.store.PutCall(context.Background(), c.record); err != nil {
			kept = fmt.Errorf("%w: %w", errNotKept, err)
		}
	}
	sender, err := c.b.throughTrGW(m, *c.media, d)
	if sender.IsValid() {
		from, _ := facing(d)
		if c.sends == nil {
			c.sends = map[Side]netip.AddrPort{}
		}
		c.sends[from] = sender
	}
	if err != nil {
		return err
	}
	return kept
}

// agree notes that an offer and its answer are done, the answer having
// come from the side that faces where a message crossing in d comes
// from. The termination facing that side sends where the answer said,
// as it does now, and the termination facing the side of the offer
// sends to offer, where that offer said. Only these two count: the
// other side, or this one, may have an offer of its own on the way in
// another request at the same time, which is not done: a final
// response other than 2xx to that request leaves the session without it
// (RFC 3261 14.1, RFC 3311 5.3).
// offer is not valid when the IBCF has no address for the offer.
func (c *call) agree(d interconnect.Direction, offer netip.AddrPort) {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	from, to := facing(d)
	if c.agreed == nil {
		c.agreed = map[Side]netip.AddrPort{}
	}
	if sends, ok := c.sends[from]; ok {
		c.agreed[from] = sends
	}
	if offer.IsValid() {
		c.agreed[to] = offer
	}
	// Both terminations are where the offer and the answer say, so
	// neither is left where a provisional response that is not reliable
	// put it.
	delete(c.previews, from)
	delete(c.previews, to)
}

// previewed notes that the termination facing where a message crossing
// in d comes from was moved by the session description of a provisional
// response to req that is not reliable. That description is no answer
// (RFC 3261 13.2.1), so the session is still the one of the last offer
// and answer, and the termination goes back there when req then gets no
// 2xx (Unanswered).
func (c *call) previewed(d interconnect.Direction, req *sip.Message) {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	from, _ := facing(d)
	if c.previews == nil {
		c.previews = map[Side]*sip.Message{}
	}
	c.previews[from] = req
}

// answersFor reports whether the session description of resp is the
// answer of the offer in the request it answers, or the offer of a
// request without one: the one in a 2xx, or in a reliable provisional
// response. The same description may come in a provisional response
// that is not reliable before it, where it changes nothing (RFC 3261
// 13.2.1, RFC 3262 5).
func answersFor(resp *sip.Message) bool {
	return resp.StatusCode >= 200 || resp.Requires(sip.Option100rel)
}

// offers notes that the side that faces where a message crossing in d
// comes from has made an offer in a response to req, a request without
// one (responseOffer).
func (c *call) offers(d interconnect.Direction, req *sip.Message) {
	cseq, _ := sip.ParseCSeq(req.Get("CSeq"))
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	from, _ := facing(d)
	if c.offered == nil {
		c.offered = map[Side]responseOffer{}
	}
	c.offered[from] = responseOffer{cseq: cseq, sends: c.sends[from]}
}

// offeredBy is where the termination facing the side that a message
// crossing in d goes to sent for the offer of that side in a response
// (offers).
func (c *call) offeredBy(d interconnect.Direction) netip.AddrPort {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	_, to := facing(d)
	return c.offered[to].sends
}

// prackAnswers reports whether prack, a PRACK with SDP that crosses in
// d, answers the offer that the side it goes to made in the reliable
// provisional response that prack acknowledges (RFC 3262 5), and where
// that offer said. The RAck of prack names the request of that response
// (RFC 3262 7.2). Any other PRACK with SDP carries an offer of its own.
func (c *call) prackAnswers(prack *sip.Message, d interconnect.Direction) (netip.AddrPort, bool) {
	rack, err := sip.ParseRAck(prack.Get("RAck"))
	if err != nil {
		return netip.AddrPort{}, false
	}
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	_, to := facing(d)
	offer, ok := c.offered[to]
	if !ok || offer.cseq != rack.CSeq {
		return netip.AddrPort{}, false
	}
	delete(c.offered, to)
	return offer.sends, true
}

// previewOf is the request to which a provisional response that is not
// reliable moved the termination facing where a message crossing in d
// comes from, or nil (previewed).
func (c *call) previewOf(d interconnect.Direction) *sip.Message {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	from, _ := facing(d)
	return c.previews[from]
}

// offerOf is the address of the audio stream in the SDP of m, or not
// valid when m has none that can be read.
func offerOf(m *sip.Message) netip.AddrPort {
	if !sip.HasSessionDescription(m) {
		return netip.AddrPort{}
	}
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return netip.AddrPort{}
	}
	sender, _, err := description.Audio()
	if err != nil {
		return netip.AddrPort{}
	}
	return sender
}

// Refused counts the response with status that the call sent for req, a
// request that crossed in d, in the place of relaying it or its answer
// (b2bua.Hooks), among the IBCF's refusals.
func (c *call) Refused(req *sip.Message, d b2bua.Direction, status int) {
	c.b.metrics.reject(c.peer.Profile.Name, c.across(d), req.Method, status)
}

// Unanswered puts the TrGW back after req, a request within a dialog
// that crossed in d and got no 2xx beyond the IBCF (b2bua.Hooks). The
// IBCF told the termination that faces the side that sent the offer of
// req to send where that offer said (throughTrGW), as TS 29.162 9.1.1.2
// lets it do on the offer, and the one that faces the other side to
// send where a provisional response that is not reliable said
// (previewed). The final response other than 2xx leaves the session as
// it was before req (RFC 3261 14.1, RFC 3311 5.3), so each side takes
// its media where the last offer and answer said, and each termination
// is to send there again. Neither TS 29.162 9.1.3 nor TS 24.229 6.7.1
// names a procedure of its own for this; it follows from the session
// that RFC 3261 14.1 leaves. When the TrGW does not take a change back,
// the termination keeps sending where the side no longer takes the
// media, and the IBCF releases the call (ok false).
func (c *call) Unanswered(req *sip.Message, d b2bua.Direction) bool {
	ok := true
	for _, side := range c.movedFor(req, c.across(d)) {
		if err := c.goBack(side); err != nil {
			c.log.Warn("the TrGW does not go back to the last offer and answer: the IBCF releases the call",
				"method", string(req.Method), "side", string(side), "error", err.Error())
			ok = false
		}
	}
	return ok
}

// movedFor are the terminations that the IBCF moved for req, a request
// within a dialog that crosses in d: the one facing the side that sent
// an offer in req, and the one facing the other side when a provisional
// response to req that is not reliable moved it (previewed). A
// termination that another request moved, one with an offer of its own
// on the way, is not among them: that request leaves its own offer
// open, and puts its own termination back when it fails.
func (c *call) movedFor(req *sip.Message, d interconnect.Direction) []Side {
	from, to := facing(d)
	var sides []Side
	if sip.HasSessionDescription(req) {
		sides = append(sides, from)
	}
	c.mediaMu.Lock()
	previewed := c.previews[to] == req
	c.mediaMu.Unlock()
	if previewed {
		sides = append(sides, to)
	}
	return sides
}

// goBack has the termination facing side s send where it sent when the
// last offer and answer of that side were done.
func (c *call) goBack(s Side) error {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	agreed, ok := c.agreed[s]
	if c.media == nil || c.ended || !ok || c.sends[s] == agreed {
		return nil
	}
	ctx, cancel := trgwContext()
	defer cancel()
	if err := c.b.trgw.Modify(ctx, c.media.ID, terminations[s], agw.TerminationRequest{Remote: agreed}); err != nil {
		return err
	}
	c.sends[s] = agreed
	delete(c.previews, s)
	c.log.Info("the TrGW sends where the last offer and answer said again", "context", c.media.ID, "side", string(s), "remote", agreed.String())
	return nil
}

// notPassedOn reports whether the IBCF holds back what, a response or an
// ACK, after err, the error of throughTrGW for its SDP, and logs err.
//
// Nobody can turn a response or an ACK down, and the side that sent it
// has taken the session that its SDP describes. The IBCF still does not
// pass that SDP on, for the reason of notThroughTrGW: it names the
// address of the side that sent it, past the TrGW. So the call cannot go
// on, whatever the TrGW answered, and the IBCF releases it (b2bua.Hooks).
// Once the call is answered, that is a BYE to each side, as TS 24.229
// 5.10.2.4 and 5.10.3.4 let the IBCF send for an error of the transport
// plane. The request that a response answers gets 500 (RFC 3261 21.5.1)
// in its place, as does the caller's INVITE while it has no final
// response, whose INVITE towards the callee is then cancelled (RFC 3261
// 9.1); those clauses speak of the BYEs only. Each 500 counts among the
// IBCF's refusals (Refused).
//
// With errNotKept the SDP did go through the TrGW, and only the record
// of the call was not written, which the next write mends; the message
// goes on.
func (c *call) notPassedOn(what string, err error, args ...any) bool {
	if errors.Is(err, errNotKept) {
		c.log.Warn("cannot keep the TrGW context in the record of the call", append(args, "error", err.Error())...)
		return false
	}
	c.log.Warn("the SDP of "+what+" is not put through the TrGW: the IBCF releases the call", append(args, "error", err.Error())...)
	return true
}

// across is the direction in which a message that the call relays in d
// crosses the border.
func (c *call) across(d b2bua.Direction) interconnect.Direction {
	if d == b2bua.ToCallee {
		return c.direction
	}
	return reverse(c.direction)
}

// Request prepares req, a request within the dialog that crosses the
// border, for the other leg. A request that the border turns away gets
// the refusal instead, which counts among the IBCF's, and so does one
// whose SDP does not go through the TrGW (notThroughTrGW).
func (c *call) Request(req *sip.Message, d b2bua.Direction) (b2bua.Message, *b2bua.Refusal) {
	m := req.Clone()
	if err := c.border.Request(m, c.across(d)); err != nil {
		r := rejection(err, c.log)
		return b2bua.Message{}, &b2bua.Refusal{Status: r.status, Headers: r.headers}
	}
	if err := c.throughTrGW(m, c.across(d)); err != nil {
		return b2bua.Message{}, c.notThroughTrGW(req, d, err)
	}
	if req.Method == sip.PRACK && sip.HasSessionDescription(req) {
		if offer, ok := c.prackAnswers(req, c.across(d)); ok {
			c.agree(c.across(d), offer)
		}
	}
	return b2bua.Message{Headers: interconnect.CrossingFields(m), Body: sip.BodyOf(m)}, nil
}

// notThroughTrGW is the refusal of req, a request within the dialog that
// crosses in d, whose SDP throughTrGW did not put through the TrGW for
// err.
//
// As an IMS-ALG the IBCF passes on an SDP of its own, with the address
// of the TrGW towards the side where it goes (TS 24.229 6.7.1.4.1 and
// 6.7.2.1, TS 29.162 9.1.1.1 and 9.1.3), and it has none. TS 24.229
// 5.10.6.3 has the IBCF modify the SDP according to 6.7.1 when the TrGW
// translates the addresses, which it cannot do here. The SDP that came
// names the address of the side that sent it, and the other side would
// send its media there, past the TrGW. So the request goes no further. The IBCF answers
// it 500 (Server Internal Error, RFC 3261 21.5.1), or 488 (Not
// Acceptable Here, 21.4.26) for an SDP that it cannot read, as the
// border answers one (interconnect.Border); either counts among its
// refusals.
//
// A final response other than 2xx leaves the session as it was before
// the request (RFC 3261 14.1, RFC 3311 5.3), and only a 481 or a 408 ends
// the dialog (RFC 3261 12.2.1.2). The call stays when it can go on as it
// was (callStays). Otherwise the IBCF releases it: once the call is
// answered with a BYE to each side, as TS 24.229 5.10.2.4 and 5.10.3.4
// let it for an error of the transport plane, and before that with a 500
// to the caller's INVITE (RFC 3261 21.5.1) and a CANCEL of the INVITE
// towards the callee (RFC 3261 9.1), of which those clauses do not speak.
// That is so for:
//   - A TrGW that has lost the context or its termination, as one that
//     restarted without them. The media of the call are gone already,
//     and every later offer would fail the same way.
//   - A TrGW that did not answer in time, or whose answer did not reach
//     the IBCF. It may have carried the Modify out, and its termination
//     then sends where the refused SDP said, which the side that sent it
//     no longer takes after the 500.
//
// The PRACK of a reliable response of the callee ends the INVITE
// whatever the TrGW answered, since that response then gets no PRACK
// (b2bua.Refusal). A PRACK with an offer for a response that the callee
// wanted no PRACK for goes on as an UPDATE, and is refused as one.
func (c *call) notThroughTrGW(req *sip.Message, d b2bua.Direction, err error) *b2bua.Refusal {
	status := 500
	if errors.Is(err, errMalformedSDP) {
		status = 488
	}
	release := !callStays(err)
	if release {
		c.log.Warn("the SDP of the request is not put through the TrGW: the IBCF releases the call",
			"method", string(req.Method), "status", status, "error", err.Error())
	} else {
		c.log.Warn("the SDP of the request is not put through the TrGW: the request goes no further",
			"method", string(req.Method), "status", status, "error", err.Error())
	}
	return &b2bua.Refusal{Status: status, Release: release}
}

// callStays reports whether a call can go on as it was after err, an
// error of throughTrGW for the SDP of a request that then gets a final
// response other than 2xx. The media go on through the TrGW as the last
// offer and answer set them up, when:
//   - The SDP cannot be read (errMalformedSDP), and the TrGW was not
//     asked.
//   - The TrGW answered with an error (agw.ErrRejected), and so carried
//     out none of the request, but not that it has no such context or
//     termination (agw.ErrNoContext, agw.ErrNoTermination). That holds
//     for a Modify that it turned down, and for the first SDP with audio
//     when it had no context to reserve, before which the call had no
//     media through it.
//   - The TrGW reserved the context for that SDP, and only the record of
//     the call that keeps it was not written (errNotKept). The context
//     stays with the call, so a request sent again passes, and the next
//     write of the record, which is of the whole record, keeps it.
//
// Any other error, such as one of a TrGW that did not answer in time,
// leaves the IBCF unable to tell whether the TrGW carried the request
// out.
func callStays(err error) bool {
	switch {
	case errors.Is(err, errMalformedSDP), errors.Is(err, errNotKept):
		return true
	case errors.Is(err, agw.ErrNoContext), errors.Is(err, agw.ErrNoTermination):
		return false
	}
	return errors.Is(err, agw.ErrRejected)
}

// Response prepares resp, a response to req that crosses the border, for
// the other leg: the header fields to copy and the body. The SDP of a
// provisional response or a 2xx goes through the TrGW, and when it does
// not, the IBCF holds resp back (notPassedOn). A final response other
// than 2xx crosses without its body (b2bua.Call), so its SDP, such as
// the media capabilities in a 488 (RFC 3261 21.4.26), does not go to the
// TrGW.
func (c *call) Response(resp, req *sip.Message, d b2bua.Direction) (b2bua.Message, bool) {
	m := resp.Clone()
	c.border.Response(m, c.across(d))
	if resp.StatusCode < 300 {
		if err := c.throughTrGW(m, c.across(d)); err != nil && c.notPassedOn("a response", err, "status", resp.StatusCode) {
			return b2bua.Message{}, false
		}
		switch {
		case !sip.HasSessionDescription(resp):
			// A response without SDP, such as a 180, ends no offer. A 2xx
			// to an offer that only a provisional response that is not
			// reliable answered should carry the answer (RFC 3261
			// 13.3.1.4); without one, the caller takes the first session
			// description it got as the answer and keeps it (RFC 3261
			// 13.2.1, RFC 6337 3.1.1), and the side that answers sent it as
			// its own. So that one is the answer once req has its 2xx.
			if resp.StatusCode/100 == 2 && sip.HasSessionDescription(req) && c.previewOf(c.across(d)) == req {
				c.agree(c.across(d), offerOf(req))
			}
		case !answersFor(resp):
			// The same answer before the response that counts, which the
			// IBCF put through the TrGW all the same: the far end may have
			// it only there.
			c.previewed(c.across(d), req)
		case sip.HasSessionDescription(req):
			// The answer to the offer of req.
			c.agree(c.across(d), offerOf(req))
		default:
			// An offer to a request without one, which the ACK or the
			// PRACK answers.
			c.offers(c.across(d), req)
		}
	}
	return b2bua.Message{Headers: interconnect.CrossingFields(m), Body: sip.BodyOf(m)}, true
}

// Answer prepares the callee's 2xx for the caller. When the dialog
// that answered is not the one whose session description the caller
// got, session points the TrGW at the dialog that answered.
func (c *call) Answer(resp, session *sip.Message) (b2bua.Message, bool) {
	if session != nil {
		if err := c.throughTrGW(session.Clone(), c.across(b2bua.ToCaller)); err != nil && c.notPassedOn("the dialog that answered", err) {
			return b2bua.Message{}, false
		}
		if sip.HasSessionDescription(c.in.Request) {
			// The answer of the dialog that answered, to the offer of
			// the INVITE.
			c.agree(c.across(b2bua.ToCaller), offerOf(c.in.Request))
		}
	}
	return c.Response(resp, c.in.Request, b2bua.ToCaller)
}

// Ack puts the answer in ack through the TrGW, or holds it back
// (notPassedOn).
//
// When the TrGW does not take the answer in an ACK, the 2xx with the
// offer of the other side gets an ACK that rejects every stream of that
// offer, and a BYE right after it (b2bua.Call, RFC 3261 13.2.2.4).
func (c *call) Ack(ack *sip.Message, d b2bua.Direction) (sip.Body, bool) {
	m := ack.Clone()
	if err := c.throughTrGW(m, c.across(d)); err != nil && c.notPassedOn("the ACK", err) {
		return sip.Body{}, false
	}
	if sip.HasSessionDescription(ack) {
		// The answer to the offer in the 2xx of the other side.
		c.agree(c.across(d), c.offeredBy(c.across(d)))
	}
	return sip.BodyOf(m), true
}

// Reasons are the Reason header fields of ended, the request that ended
// one leg, as they may cross the border, for the request that ends the
// other leg (RFC 3326 2).
func (c *call) Reasons(ended *sip.Message, d b2bua.Direction) []sip.HeaderField {
	m := ended.Clone()
	_ = c.border.Request(m, c.across(d))
	return sip.ReasonFields(m)
}

// isfocus is the feature parameter of the Contact of a conference
// focus (RFC 3840 9 and 10.18, TS 24.147 5.3.2.3.1).
const isfocus = "isfocus"

// contactFor is the Contact that the IBCF gives the dialog on side s for
// the Contact of m, a message that crosses to s (TS 24.229 5.10.5).
//
// A Contact that is a GRUU (isGRUU), or that has a
// feature tag telling what the far end can do at that URI, crosses as
// it came, and transparent is true: the far end keeps the URI, such as
// the conference URI with isfocus, which a participant stores and
// subscribes to (TS 24.147 5.3.1.3.2), and with which it rejoins the
// conference (the NOTE of 5.10.5). The IBCF then puts its own URI in
// the Record-Route of that dialog (Invitation.RecordRoute, or
// IncomingCall.SetContact with recordRoute), so that the requests of
// the far end, whose Request-URI is that URI, still come to it. It
// ends them there, as in any dialog of the B2BUA, and sends a request
// of its own on the other side, to the remote target of that dialog:
// the Contact as it came.
//
// Any other Contact is the IBCF's URI on side s, with the feature tags
// of m: what the far end announced it can do (RFC 3840), such as the
// ICSI of MMTel, goes on to the other side. It is empty when m has no
// feature tags, and the UA writes its own Contact.
func contactFor(s side, m *sip.Message) (contact sip.NameAddr, transparent bool) {
	na, err := sip.ParseNameAddr(m.Get("Contact"))
	if err != nil {
		return sip.NameAddr{}, false
	}
	if _, focus := na.Params.Get(isfocus); focus || isGRUU(na.URI) {
		return na, true
	}
	if len(na.Params) == 0 {
		return sip.NameAddr{}, false
	}
	params := na.Params
	params.Remove("expires")
	return sip.NameAddr{URI: sip.URI{Scheme: "sip", Host: s.own.Host, Port: s.own.Port}.String(), Params: params}, false
}

// isGRUU reports whether uri is a GRUU, public or temporary: both have
// the gr URI parameter (RFC 5627 3.1, 4.5).
func isGRUU(uri string) bool {
	u, err := sip.ParseURI(uri)
	if err != nil {
		return false
	}
	_, ok := u.Params.Get("gr")
	return ok
}

// Contact is the Contact that contactFor gives the caller's dialog for
// the callee's Contact in resp.
func (c *call) Contact(resp *sip.Message) (sip.NameAddr, bool, bool) {
	contact, transparent := contactFor(c.inSide, resp)
	return contact, transparent, contact.URI != ""
}

// KeepDialogs writes the two dialogs into the record of the call, for
// the instance that takes this one's place (ReleaseLeftovers): once the
// call is answered, and again after each request within it, since TS
// 24.229 5.10.2.3 and 5.10.3.3 (items 2 and 3) have the IBCF save the
// Contact and CSeq of each subsequent request. A target refresh request
// changes the remote target of its dialog (RFC 3261 12.2), and so where
// the BYE of ReleaseLeftovers goes.
//
// The response to the 2xx or to the request has gone by then, so a
// failure of the store is logged. Until a later write succeeds, the
// store has the dialogs as they were before, or none: a later process
// of the instance then sends its BYE to the target of the last record
// written, or only releases the TrGW context.
func (c *call) KeepDialogs(in, out sip.Dialog) {
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	if c.ended {
		return
	}
	if c.direction == interconnect.Exit {
		c.record.Home, c.record.PeerSide = &in, &out
	} else {
		c.record.Home, c.record.PeerSide = &out, &in
	}
	if err := c.b.store.PutCall(context.Background(), c.record); err != nil {
		c.log.Warn("cannot keep the dialogs in the record of the call", "error", err.Error())
	}
}
