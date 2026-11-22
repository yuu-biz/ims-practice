package ibcf

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the IBCF as a B2BUA for an INVITE (TS 24.229 5.10.5):
// the incoming INVITE ends in a dialog with the side that sent it, and
// the IBCF sends a new INVITE in a dialog of its own with the other
// side. Each dialog has its own Call-ID, tags, CSeq, Via and Contact.
// What crosses from one to the other is what interconnect.Border lets
// through (interconnect.CrossingFields), with the SDP through the TrGW.
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
// INVITE, the caller's; out is its dialog with the other side, the
// callee's.
type call struct {
	b *IBCF
	*crossing
	in      *sip.IncomingCall
	inSide  side
	outSide side
	record  CallRecord

	// answered is set once the callee's 2xx has come.
	answered atomic.Bool
	// outReady is closed once Invite has returned; out is then the
	// outgoing leg, or nil when it could not be sent.
	outReady chan struct{}

	mu  sync.Mutex
	out *sip.OutgoingCall
	// pending is the reliable provisional response of out that carried
	// the callee's offer: the caller's answer comes in its PRACK, which
	// the IBCF sends on in the PRACK of pending (RFC 3262 5).
	pending *sip.Message
	// prackRelayed is closed once that PRACK has gone on to out. The UA
	// hands each request of the caller to a goroutine of its own, and
	// the PRACK waits for the TrGW on the way, so a request that the
	// caller sent after it, without waiting for its answer, could reach
	// out first; it waits for prackRelayed instead, and the far end gets
	// the two in the order the caller sent them. Only a request whose
	// goroutine starts after the PRACK's has taken pending waits: the
	// UA tells nothing of the order in which they came. A PRACK without
	// a body the UA answers itself, and relayProvisional sends it on
	// with no such wait.
	prackRelayed chan struct{}
	// sessionToCaller tells that a session description went to the
	// caller in a reliable provisional response.
	sessionToCaller bool
	// The INVITE of out may fork beyond the IBCF, and each UAS that
	// answers it has an early dialog of its own (RFC 3261 13.2.2.4).
	// The caller has one early dialog with the IBCF, which is one UAS:
	// all its responses have one To tag (RFC 3261 8.2.6.2). sessionFrom
	// is the To tag of the dialog of out whose session description the
	// caller got, the first that sent one; the caller takes the first
	// as the answer and ignores the rest (13.2.1), so the IBCF passes
	// on the responses of the other dialogs without theirs. calleeSDP
	// holds the last response with a session description of each
	// dialog of out, by To tag, as it came: the TrGW sends the media to
	// the dialog that answers (answer).
	sessionFrom string
	calleeSDP   map[string]*sip.Message

	// mediaMu orders the requests to the TrGW of the call and the
	// writes of its record. media is its TrGW context, nil until the
	// first SDP with an audio stream. ended is set once the call has
	// ended and its record is gone, so that no later write brings the
	// record back.
	mediaMu sync.Mutex
	media   *MediaContext
	ended   bool
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
	k := &call{b: b, crossing: c, in: in, inSide: s, outSide: b.other(s), outReady: make(chan struct{}), calleeSDP: map[string]*sip.Message{},
		record: CallRecord{Instance: b.instance(), ID: sip.NewTag(), Direction: c.direction, Peer: c.peer.Profile.Name}}
	in.Handle(k.fromCaller)
	k.run()
}

// run sends the INVITE on the other side and relays between the two
// dialogs until the call ends.
func (c *call) run() {
	defer c.end()
	req := c.req
	ua, err := c.outSide.agent()
	if err == nil {
		err = c.throughTrGW(req, c.direction)
	}
	if err != nil {
		c.log.Warn("cannot set up the call", "error", err.Error())
		close(c.outReady)
		c.reject(500)
		return
	}
	from, _ := sip.ParseNameAddr(req.Get("From"))
	to, _ := sip.ParseNameAddr(req.Get("To"))
	// The IBCF takes one hop away, as a proxy would (RFC 3261 16.6 step
	// 3), so that a call that two networks send round in a loop ends.
	headers := append(withoutReplaces(interconnect.CrossingFields(req, inviteFields...)), sip.HeaderField{Name: "Max-Forwards", Value: req.Get("Max-Forwards")})
	contact, transparent := contactFor(c.outSide, req)
	out, err := ua.Invite(sip.Invitation{
		RequestURI:      req.RequestURI,
		From:            from.URI,
		FromDisplayName: from.DisplayName,
		To:              to.URI,
		Route:           c.route,
		Headers:         headers,
		Body:            sip.BodyOf(req),
		Contact:         contact,
		RecordRoute:     transparent,
		// The PRACK of each reliable provisional response is the
		// caller's, which the IBCF passes on (fromCaller).
		ManualPRACK: true,
		OnRequest:   c.fromCallee,
	})
	if err != nil {
		c.log.Warn("cannot send the INVITE on", "error", err.Error())
		close(c.outReady)
		c.reject(504)
		return
	}
	c.mu.Lock()
	c.out = out
	c.mu.Unlock()
	close(c.outReady)
	c.b.metrics.session(c.peer.Profile.Name, c.direction)
	c.log.Info("call across the border", "out_call_id", out.Request.Get("Call-ID"), "request_uri", req.RequestURI)

	// RFC 3261 9.1: when the caller gives up before the answer, the
	// IBCF cancels the other leg, for the same reason (RFC 3326 2).
	settled := make(chan struct{})
	defer close(settled)
	go func() {
		select {
		case <-c.in.Done():
			if !c.answered.Load() {
				out.Cancel(c.reasonsOf(c.in.EndedBy(), c.direction)...)
			}
		case <-settled:
		}
	}()

	var final *sip.Message
	for resp := range out.Responses() {
		switch code := resp.StatusCode; {
		case code < 200:
			c.relayProvisional(out, resp)
		case code < 300:
			c.answer(out, resp)
			return
		default:
			final = resp
		}
	}
	switch {
	case errors.Is(c.in.Err(), sip.ErrCancelled):
		// The UA has answered the caller's INVITE with 487 already.
	case final != nil:
		c.b.metrics.failure(c.peer.Profile.Name, c.direction, final.StatusCode)
		headers, _ := c.back(final, reverse(c.direction))
		c.in.Reject(final.StatusCode, headers...)
	default:
		// 504 (Server Time-out), as the CSCFs answer when their next
		// hop does not (ims.Finish), rather than the 408 of RFC 3261
		// 16.8.
		c.log.Warn("the other side did not answer", "error", errText(out.Err()))
		c.reject(504)
	}
}

// withoutReplaces takes replaces out of the Supported of fields: the
// IBCF does not map a Replaces of one leg to the other (doc.go), so the
// far end must not count on it.
func withoutReplaces(fields []sip.HeaderField) []sip.HeaderField {
	for i, h := range fields {
		if !strings.EqualFold(h.Name, "Supported") {
			continue
		}
		var kept []string
		for _, tag := range strings.Split(h.Value, ",") {
			if tag = strings.TrimSpace(tag); tag != "" && !strings.EqualFold(tag, sip.OptionReplaces) {
				kept = append(kept, tag)
			}
		}
		fields[i].Value = strings.Join(kept, ", ")
	}
	return fields
}

// end releases what the call held: its TrGW context and its record.
// A call that ends because the IBCF closes keeps both, as a process
// that stops without closing would, for ReleaseLeftovers.
func (c *call) end() {
	if c.b.closed.Load() {
		return
	}
	c.mediaMu.Lock()
	media := c.media
	c.ended = true
	c.b.store.DeleteCall(c.record.Instance, c.record.ID)
	c.mediaMu.Unlock()
	if media != nil {
		c.b.releaseMedia(*media, c.log)
	}
}

// reject answers the caller's INVITE with a response of the IBCF's own.
func (c *call) reject(code int) {
	c.b.metrics.reject(c.peer.Profile.Name, c.direction, sip.INVITE, code)
	c.in.Reject(code)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// throughTrGW puts the SDP of m, which crosses the border in direction
// d, through the TrGW context of the call. The first SDP with an audio
// stream gets the call a context of its own, one for each pass through
// the IBCF; that is the offer of the INVITE, or the offer in a
// response when the INVITE has none.
func (c *call) throughTrGW(m *sip.Message, d interconnect.Direction) error {
	if !sip.HasSessionDescription(m) {
		return nil
	}
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	if c.media == nil {
		if !hasAudio(m) {
			return nil
		}
		ctx, cancel := trgwContext()
		mc, err := c.b.reserveMedia(ctx)
		cancel()
		if err != nil {
			return err
		}
		c.media = &mc
		c.record.Media = &mc
		c.b.store.PutCall(c.record)
		c.log.Info("media through the TrGW", "context", mc.ID, "core", mc.Core.String(), "peer", mc.Peer.String())
	}
	return c.b.throughTrGW(m, *c.media, d)
}

// back prepares resp, a response that crosses the border in direction
// d, for the other leg: the header fields to copy and the body.
func (c *call) back(resp *sip.Message, d interconnect.Direction) ([]sip.HeaderField, sip.Body) {
	m := resp.Clone()
	c.border.Response(m, d)
	if err := c.throughTrGW(m, d); err != nil {
		c.log.Warn("the TrGW refused the SDP of a response", "status", resp.StatusCode, "error", err.Error())
	}
	return interconnect.CrossingFields(m), sip.BodyOf(m)
}

// across prepares req, a request within the dialog that crosses the
// border in direction d, for the other leg. A request that the border
// turns away gets the refusal instead.
func (c *call) across(req *sip.Message, d interconnect.Direction) ([]sip.HeaderField, sip.Body, *refusal) {
	m := req.Clone()
	if err := c.border.Request(m, d); err != nil {
		return nil, sip.Body{}, c.b.rejected(req, c.peer, d, err, c.log)
	}
	if err := c.throughTrGW(m, d); err != nil {
		c.log.Warn("the TrGW refused the SDP of a request", "method", string(req.Method), "error", err.Error())
	}
	return interconnect.CrossingFields(m), sip.BodyOf(m), nil
}

// reasonsOf are the Reason header fields of ended, the request that
// ended one leg, as they may cross in direction d, for the request that
// ends the other leg (RFC 3326 2).
func (c *call) reasonsOf(ended *sip.Message, d interconnect.Direction) []sip.HeaderField {
	if ended == nil {
		return nil
	}
	m := ended.Clone()
	_ = c.border.Request(m, d)
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

// setContact has the responses to the caller carry the Contact that
// contactFor gives for the callee's Contact in resp.
func (c *call) setContact(resp *sip.Message) {
	if contact, transparent := contactFor(c.inSide, resp); contact.URI != "" {
		c.in.SetContact(contact, transparent)
	}
}

// relayProvisional passes a provisional response of out on to the
// caller. A reliable one is acknowledged on out once the caller has
// acknowledged it on in, so that the PRACK goes from end to end as it
// would without the IBCF: with preconditions the caller and the callee
// see each other's 183 and PRACK (RFC 3262, RFC 3312).
func (c *call) relayProvisional(out *sip.OutgoingCall, resp *sip.Message) {
	reliable := resp.Requires(sip.Option100rel)
	if resp.StatusCode == 199 {
		// RFC 6228: one of the early dialogs of out has ended. They are
		// all one early dialog on in, which goes on.
		if reliable {
			out.PRACK(resp, sip.Body{})
		}
		return
	}
	headers, body := c.back(c.fromCallerSession(resp), reverse(c.direction))
	c.mu.Lock()
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
	c.mu.Unlock()
	c.setContact(resp)
	if err := c.in.Progress(resp.StatusCode, body, headers...); err != nil {
		c.log.Info("provisional response not relayed", "status", resp.StatusCode, "error", err.Error())
	}
	if reliable && !offer {
		if _, err := out.PRACK(resp, sip.Body{}); err != nil {
			c.log.Warn("PRACK not sent", "status", resp.StatusCode, "error", err.Error())
		}
	}
}

// fromCallerSession is resp, a response of a dialog of out, as the
// caller may get it: without its session description when another
// dialog of out gave the caller one already (sessionFrom). It notes
// the session description of resp for its dialog.
func (c *call) fromCallerSession(resp *sip.Message) *sip.Message {
	if !sip.HasSessionDescription(resp) {
		return resp
	}
	tag := toTag(resp)
	c.mu.Lock()
	c.calleeSDP[tag] = resp
	if c.sessionFrom == "" {
		c.sessionFrom = tag
	}
	from := c.sessionFrom
	c.mu.Unlock()
	if from == tag {
		return resp
	}
	c.log.Info("session description of another early dialog of the callee's side kept at the IBCF",
		"status", resp.StatusCode, "to_tag", tag, "caller_has", from)
	return withoutBody(resp)
}

// answerFrom points the TrGW at the dialog of out that answered with
// resp, a 2xx, when it is not the one whose session description the
// caller got, and returns resp as the caller gets it then: without a
// session description, since the caller ignores a second one (RFC
// 3261 13.2.1).
//
// SIMPLIFIED: RFC 3261 13.2.1 and 14.1. The caller keeps the session
// description of the dialog that sent one first. The IBCF does not
// offer the caller the session of the dialog that answered in a
// re-INVITE, so the call goes on with the codec that the first dialog
// chose; the media flows when both chose the same. After an INVITE
// without an offer, the offer of a dialog that the caller did not
// answer gets no answer from the IBCF, and that UAS ends its dialog.
func (c *call) answerFrom(resp *sip.Message) *sip.Message {
	tag := toTag(resp)
	c.mu.Lock()
	from, kept := c.sessionFrom, c.calleeSDP[tag]
	c.mu.Unlock()
	if from == "" || from == tag {
		return resp
	}
	if sip.HasSessionDescription(resp) {
		kept = resp
	}
	if kept != nil {
		if err := c.throughTrGW(kept.Clone(), reverse(c.direction)); err != nil {
			c.log.Warn("the TrGW refused the SDP of the dialog that answered", "error", err.Error())
		}
	}
	c.log.Info("answered in another early dialog than the one whose session description the caller has",
		"to_tag", tag, "caller_has", from)
	return withoutBody(resp)
}

// toTag is the To tag of m.
func toTag(m *sip.Message) string {
	na, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		return ""
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// withoutBody is a copy of m without its body.
func withoutBody(m *sip.Message) *sip.Message {
	m = m.Clone()
	m.Body = nil
	m.Remove("Content-Type")
	return m
}

// answer passes the 2xx of out on to the caller, and then the requests
// within the dialogs both ways, until one side hangs up; the other side
// then gets a BYE from the IBCF, with the Reason of the first (RFC 3326
// 2). That BYE is also what ends the other leg when one leg fails: a
// caller that never acknowledges the 2xx, or a dialog that the far end
// no longer answers in (RFC 3261 12.2.1.2), ends its leg, and the IBCF
// ends the other one.
func (c *call) answer(out *sip.OutgoingCall, resp *sip.Message) {
	c.answered.Store(true)
	headers, body := c.back(c.answerFrom(resp), reverse(c.direction))
	c.setContact(resp)
	ack, err := c.in.Accept(body, headers...)
	if err != nil {
		// No ACK came, and the UA has sent the caller a BYE (RFC 3261
		// 13.3.1.4); or the caller cancelled as the 2xx came.
		c.log.Info("the caller did not take the answer", "error", err.Error())
		out.Bye()
		return
	}
	if len(ack.Body) > 0 {
		// The 2xx carried the callee's offer, after an INVITE without
		// one, and the caller's ACK the answer (RFC 3261 13.2.2.4).
		m := ack.Clone()
		if err := c.throughTrGW(m, c.direction); err != nil {
			c.log.Warn("the TrGW refused the answer in the ACK", "error", err.Error())
		}
		if err := out.Ack(sip.BodyOf(m)); err != nil {
			c.log.Warn("answer in the ACK not relayed", "error", err.Error())
		}
	}
	c.keepDialogs()
	c.log.Info("call answered")
	select {
	case <-c.in.Done():
		out.Bye(c.reasonsOf(c.in.EndedBy(), c.direction)...)
	case <-out.Done():
		c.in.Bye(c.reasonsOf(out.EndedBy(), reverse(c.direction))...)
	}
	c.log.Info("call ended")
}

// keepDialogs writes the two dialogs into the record of the call, for
// the instance that takes this one's place (ReleaseLeftovers): once the
// call is answered, and again after each request within it, since TS
// 24.229 5.10.2.3 and 5.10.3.3 (items 2 and 3) have the IBCF save the
// Contact and CSeq of each subsequent request. A target refresh request
// changes the remote target of its dialog (RFC 3261 12.2), and so where
// the BYE of ReleaseLeftovers goes.
func (c *call) keepDialogs() {
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	if out == nil || !c.answered.Load() {
		return
	}
	in, okIn := c.in.Dialog()
	o, okOut := out.Dialog()
	if !okIn || !okOut {
		return
	}
	c.mediaMu.Lock()
	defer c.mediaMu.Unlock()
	if c.ended {
		return
	}
	if c.direction == interconnect.Exit {
		c.record.Home, c.record.PeerSide = &in, &o
	} else {
		c.record.Home, c.record.PeerSide = &o, &in
	}
	c.b.store.PutCall(c.record)
}

// leg is what the two legs of a call have in common: both an
// IncomingCall and an OutgoingCall are one.
type leg interface {
	Update(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	ReInvite(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Send(method sip.Method, body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Ack(body sip.Body, headers ...sip.HeaderField) error
}

// fromCaller handles the requests of the caller within the dialog of
// in (TS 24.229 5.10.2.3, 5.10.3.3), which go on to out.
func (c *call) fromCaller(r *sip.InDialogRequest) {
	defer c.keepDialogs()
	<-c.outReady
	c.mu.Lock()
	out := c.out
	pending := c.pending
	before := c.prackRelayed
	if r.Request.Method == sip.PRACK && pending != nil {
		c.pending = nil
		c.prackRelayed = make(chan struct{})
		defer close(c.prackRelayed)
	}
	c.mu.Unlock()
	if out == nil {
		r.Reject(481)
		return
	}
	if r.Request.Method == sip.PRACK && pending != nil {
		// The PRACK with the caller's answer to the callee's offer goes
		// on as the PRACK of the callee's response (RFC 3262 5).
		headers, body, refused := c.across(r.Request, c.direction)
		if refused != nil {
			r.Reject(refused.status, refused.headers...)
			return
		}
		resp, err := out.PRACK(pending, body, headers...)
		c.answerRequest(r, resp, err, out, c.direction)
		return
	}
	if before != nil {
		select {
		case <-before:
		case <-c.in.Done():
		}
	}
	c.forward(r, out, c.direction)
}

// fromCallee handles the requests of the callee within a dialog of out,
// which go on to the caller.
func (c *call) fromCallee(r *sip.InDialogRequest) {
	defer c.keepDialogs()
	c.forward(r, c.in, reverse(c.direction))
}

// forward passes r, which crosses the border in direction d, on to the
// other leg, and the final response back.
func (c *call) forward(r *sip.InDialogRequest, to leg, d interconnect.Direction) {
	req := r.Request
	headers, body, refused := c.across(req, d)
	if refused != nil {
		r.Reject(refused.status, refused.headers...)
		return
	}
	var resp *sip.Message
	var err error
	switch req.Method {
	case sip.UPDATE:
		if len(body.Content) == 0 {
			// RFC 4028 9: an UPDATE without a body refreshes the session
			// of its own dialog. Each leg has its own session timer, so
			// the IBCF answers it.
			r.Accept(sip.Body{})
			return
		}
		resp, err = to.Update(body, headers...)
	case sip.INVITE:
		resp, err = to.ReInvite(body, headers...)
	case sip.PRACK:
		// A PRACK with a new offer, after the callee's response had the
		// answer (RFC 3262 5). The IBCF has acknowledged that response
		// on the other leg already, so the offer goes on in an UPDATE
		// (RFC 3311 5.1).
		resp, err = to.Update(body, headers...)
	default:
		resp, err = to.Send(req.Method, body, headers...)
	}
	c.answerRequest(r, resp, err, to, d)
}

// answerRequest answers r, a request within a dialog that crossed in
// direction d to the other leg, with what that leg answered.
func (c *call) answerRequest(r *sip.InDialogRequest, resp *sip.Message, err error, to leg, d interconnect.Direction) {
	switch {
	case errors.Is(err, sip.ErrInviteInProgress), errors.Is(err, sip.ErrOfferPending):
		// RFC 3261 14.2, RFC 3311 5.2: the request crossed one of the
		// other side.
		c.rejectInDialog(r, d, 491)
	case errors.Is(err, sip.ErrCallEnded):
		c.rejectInDialog(r, d, 481)
	case err != nil:
		c.log.Info("request within the dialog not relayed", "method", string(r.Request.Method), "error", err.Error())
		c.rejectInDialog(r, d, 500)
	case resp.StatusCode < 300:
		headers, body := c.back(resp, reverse(d))
		ack, err := r.Accept(body, headers...)
		if err == nil && r.Request.Method == sip.INVITE && !r.Offer && ack != nil {
			// A re-INVITE without an offer: the 2xx had the other side's
			// offer, and this side's ACK has the answer (RFC 3261 14.1).
			m := ack.Clone()
			if err := c.throughTrGW(m, d); err != nil {
				c.log.Warn("the TrGW refused the answer in the ACK", "error", err.Error())
			}
			to.Ack(sip.BodyOf(m))
		}
	default:
		headers, _ := c.back(resp, reverse(d))
		r.Reject(resp.StatusCode, headers...)
	}
}

// rejectInDialog answers r with a response of the IBCF's own.
func (c *call) rejectInDialog(r *sip.InDialogRequest, d interconnect.Direction, code int) {
	c.b.metrics.reject(c.peer.Profile.Name, d, r.Request.Method, code)
	r.Reject(code)
}
