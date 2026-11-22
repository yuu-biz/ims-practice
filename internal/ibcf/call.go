package ibcf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
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
	ua, err := c.outSide.agent()
	if err == nil {
		err = c.throughTrGW(req, c.direction)
	}
	if err != nil {
		c.log.Warn("cannot set up the call", "error", err.Error())
		c.legs.NoLeg()
		c.reject(500)
		return
	}
	from, _ := sip.ParseNameAddr(req.Get("From"))
	to, _ := sip.ParseNameAddr(req.Get("To"))
	// The IBCF takes one hop away, as a proxy would (RFC 3261 16.6 step
	// 3), so that a call that two networks send round in a loop ends.
	headers := append(withoutReplaces(interconnect.CrossingFields(req, inviteFields...)), sip.HeaderField{Name: "Max-Forwards", Value: req.Get("Max-Forwards")})
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
	case errors.Is(c.in.Err(), sip.ErrCancelled):
		// The UA has answered the caller's INVITE with 487 already.
	case final != nil:
		c.b.metrics.failure(c.peer.Profile.Name, c.direction, final.StatusCode)
		c.in.Reject(final.StatusCode, c.Response(final, c.in.Request, b2bua.ToCaller).Headers...)
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
		c.log.Info("media through the TrGW", "context", mc.ID, "core", mc.Core.String(), "peer", mc.Peer.String())
		if err := c.b.store.PutCall(context.Background(), c.record); err != nil {
			kept = fmt.Errorf("%w: %w", errNotKept, err)
		}
	}
	if err := c.b.throughTrGW(m, *c.media, d); err != nil {
		return err
	}
	return kept
}

// mediaFailed logs err, which throughTrGW returned for the SDP of what.
func (c *call) mediaFailed(what string, err error, args ...any) {
	if errors.Is(err, errNotKept) {
		c.log.Warn("cannot keep the TrGW context in the record of the call", append(args, "error", err.Error())...)
		return
	}
	c.log.Warn("the TrGW refused the SDP of "+what, append(args, "error", err.Error())...)
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
// the refusal instead, which counts among the IBCF's.
//
// A request whose SDP gets the call its TrGW context gets 500 (Server
// Internal Error, RFC 3261 21.5.1) when the record of the call cannot
// then be written, as the INVITE does in the same case (run).
func (c *call) Request(req *sip.Message, d b2bua.Direction) (b2bua.Message, *b2bua.Refusal) {
	m := req.Clone()
	if err := c.border.Request(m, c.across(d)); err != nil {
		r := c.b.rejected(req, c.peer, c.across(d), err, c.log)
		return b2bua.Message{}, &b2bua.Refusal{Status: r.status, Headers: r.headers}
	}
	if err := c.throughTrGW(m, c.across(d)); err != nil {
		c.mediaFailed("a request", err, "method", string(req.Method))
		if errors.Is(err, errNotKept) {
			return b2bua.Message{}, &b2bua.Refusal{Status: 500}
		}
	}
	return b2bua.Message{Headers: interconnect.CrossingFields(m), Body: sip.BodyOf(m)}, nil
}

// Response prepares resp, a response that crosses the border, for the
// other leg: the header fields to copy and the body.
func (c *call) Response(resp, _ *sip.Message, d b2bua.Direction) b2bua.Message {
	m := resp.Clone()
	c.border.Response(m, c.across(d))
	if err := c.throughTrGW(m, c.across(d)); err != nil {
		c.mediaFailed("a response", err, "status", resp.StatusCode)
	}
	return b2bua.Message{Headers: interconnect.CrossingFields(m), Body: sip.BodyOf(m)}
}

// Answer prepares the callee's 2xx for the caller. When the dialog
// that answered is not the one whose session description the caller
// got, session points the TrGW at the dialog that answered.
func (c *call) Answer(resp, session *sip.Message) b2bua.Message {
	if session != nil {
		if err := c.throughTrGW(session.Clone(), c.across(b2bua.ToCaller)); err != nil {
			c.mediaFailed("the dialog that answered", err)
		}
	}
	return c.Response(resp, c.in.Request, b2bua.ToCaller)
}

// Ack puts the answer in ack through the TrGW.
func (c *call) Ack(ack *sip.Message, d b2bua.Direction) sip.Body {
	m := ack.Clone()
	if err := c.throughTrGW(m, c.across(d)); err != nil {
		c.mediaFailed("the ACK", err)
	}
	return sip.BodyOf(m)
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
