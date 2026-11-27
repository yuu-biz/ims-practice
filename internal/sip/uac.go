package sip

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Invitation is what the UAC puts in an initial INVITE (RFC 3261
// 13.2.1).
type Invitation struct {
	// RequestURI is the callee: a SIP URI, or a tel URI that a proxy
	// on the way translates.
	RequestURI string
	// From is the caller's URI, and To the callee's; an empty To means
	// RequestURI.
	From string
	To   string
	// FromDisplayName is the display name of From, "" for none. A
	// caller that withholds its identity writes "Anonymous" here (RFC
	// 3323 4.1.1.1).
	FromDisplayName string
	// Route is the preloaded route set, top first, each a Route value
	// such as <sip:pcscf.example.org;lr> (RFC 3261 8.1.1.1). An IMS UE
	// puts its P-CSCF and the Service-Route here (TS 24.229 5.1.2A.1).
	Route []string
	// Headers are further header fields, such as P-Preferred-Identity.
	// A Max-Forwards, Contact, Allow or Supported here replaces the one
	// that the UA writes itself; the header fields that identify the
	// request, From, To, Call-ID, CSeq and Via, are not for Headers.
	Headers []HeaderField
	// Body is normally the SDP offer.
	Body Body
	// Contact, when its URI is set, is the Contact of the INVITE and of
	// every later message of the call instead of the UA's own (RFC 3261
	// 12.1.2): a conference focus gives the conference URI with the
	// isfocus feature parameter here (TS 24.147 5.3.2.5.4 step 3).
	Contact NameAddr
	// RecordRoute puts the UA's own URI in a Record-Route of the INVITE,
	// so that the requests of the peer, which go to Contact, still pass
	// the UA; the UA leaves itself out of its own route set. A routeing
	// B2BUA does this when it passes on the Contact of its other leg
	// (TS 24.229 5.7.5.1).
	RecordRoute bool
	// ManualPRACK leaves the PRACK for each reliable provisional
	// response to the user of the call, who sends it with PRACK. A
	// B2BUA sets it to relay the PRACK of its other leg, with its body.
	// Without it the UA sends the PRACK itself, without a body, except
	// for a response that carries an offer: its PRACK has to carry the
	// answer, which only the user has.
	ManualPRACK bool
	// OnRequest, when set, is called in a goroutine of its own for each
	// request within a dialog of the call that the UA leaves to the
	// user, who answers it with Accept or Reject.
	OnRequest func(*InDialogRequest)
}

// OutgoingCall is a call this UA makes, at the UAC side (RFC 3261 13.2).
// Each UAS that answers the INVITE has a dialog with the UA (Dialogs).
// Update, ReInvite, Send, Ack and Bye work within the one that the call
// goes on in: the dialog that the 2xx confirmed, and before the 2xx the
// earliest early dialog that has not ended.
type OutgoingCall struct {
	call
	// Request is the INVITE as sent, with this UA's Via on top.
	Request *Message

	responses   chan *Message
	manualPRACK bool

	// Guarded by mu.

	// ct is the client transaction of the INVITE, and invite the INVITE
	// that it sent: Request, or the INVITE that went out again after a
	// 422 (RFC 4028 7.3).
	ct        *ClientTransaction
	invite    *Message
	cancelled bool
	// minSE holds the Min-SE that the UA has learned from 422 responses
	// to the INVITE (RFC 4028 7.4).
	minSE SessionTimer
}

// Invite sends an INVITE for inv. The responses come on Responses.
func (ua *UA) Invite(inv Invitation) (*OutgoingCall, error) {
	req := &Message{Method: INVITE, RequestURI: inv.RequestURI}
	for _, route := range inv.Route {
		req.Add("Route", route)
	}
	to := inv.To
	if to == "" {
		to = inv.RequestURI
	}
	// own adds a header field of the UA unless the user gives that one.
	own := func(name, value string) {
		for _, h := range inv.Headers {
			if strings.EqualFold(h.Name, name) {
				return
			}
		}
		req.Add(name, value)
	}
	own("Max-Forwards", "70")
	from := NameAddr{DisplayName: inv.FromDisplayName, URI: inv.From}
	from.Params.Set("tag", NewTag())
	req.Add("From", from.String())
	req.Add("To", withTag(to, ""))
	req.Add("Call-ID", NewCallID())
	req.Add("CSeq", CSeq{Number: 1, Method: INVITE}.String())
	contact := ua.contact()
	if inv.Contact.URI != "" {
		contact = inv.Contact.String()
	}
	own("Contact", contact)
	if inv.RecordRoute {
		req.Add("Record-Route", ua.ownRoute())
	}
	own("Allow", ua.allowHeader())
	// RFC 3262 4: a UAC SHOULD offer 100rel in every INVITE, and leaves
	// it to the UAS whether to use it.
	own("Supported", ua.supportedHeader())
	// RFC 4028 7.1: the session interval that this UA asks for.
	for _, h := range ua.cfg.SessionTimer.RequestHeaders(SessionTimer{}) {
		own(h.Name, h.Value)
	}
	addHeaders(req, inv.Headers)
	inv.Body.addTo(req)

	c := &OutgoingCall{call: newCall(ua, ua.stack), responses: make(chan *Message, responseBuffer)}
	c.onRequest, c.manualPRACK = inv.OnRequest, inv.ManualPRACK
	if inv.Contact.URI != "" {
		c.contact = contact
	}
	ct, err := c.send(req)
	if err != nil {
		return nil, err
	}
	c.ct, c.Request, c.invite = ct, ct.Request, ct.Request
	c.open()
	if !ua.spawn(c.run) {
		// The UA closed as the INVITE went out, and the call ended with
		// it (open).
		close(c.responses)
	}
	return c, nil
}

// Responses delivers every provisional response to the INVITE other
// than 100 (Trying), and then the final response. It is closed after
// the final response, or when the INVITE fails without one; Err then
// tells why. Retransmissions of a 2xx do not show up here: the call
// acknowledges them itself.
//
// When the INVITE was forked, the provisional responses of every UAS
// come here; DialogOf tells which dialog one belongs to. A 199 tells
// that its early dialog has ended (RFC 6228 4). A 422 after which the
// UA sent the INVITE again (RFC 4028 7.3) does not show up.
func (c *OutgoingCall) Responses() <-chan *Message { return c.responses }

// run reads the INVITE client transaction to its end, which comes 64*T1
// after a 2xx so that every retransmission of the 2xx gets its ACK, and
// so does the 2xx of another UAS that a forking proxy reached (RFC 6026
// 7.2).
func (c *OutgoingCall) run() {
	c.mu.Lock()
	ct := c.ct
	c.mu.Unlock()
	answered := false
	for ct != nil {
		var again *ClientTransaction
		for resp := range ct.Responses() {
			switch code := resp.StatusCode; {
			case code == 100:
				// A 100 (Trying) is hop by hop and says nothing about the
				// callee.
			case code < 200:
				if c.receiveProvisional(resp) && !answered {
					c.deliver(resp)
				}
			case code < 300:
				if c.receive2xx(resp) {
					answered = true
					c.deliver(resp)
					close(c.responses)
				}
			default:
				if again = c.inviteAgain(resp); again != nil {
					continue
				}
				c.receiveFailure(resp)
				if !answered {
					answered = true
					c.deliver(resp)
					close(c.responses)
				}
			}
		}
		if again == nil {
			break
		}
		ct = again
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !answered {
		close(c.responses)
		c.end("INVITE failed", ct.Err())
		return
	}
	// RFC 3261 13.2.2.4: the UAC core takes the INVITE transaction as
	// completed 64*T1 after the first 2xx, and the early dialogs that
	// no 2xx confirmed end then.
	for _, d := range c.dialogs {
		if d.dialog.State == DialogEarly {
			d.setState(DialogTerminated, "INVITE completed")
		}
	}
}

// inviteAgain is RFC 4028 7.3 and 7.4 for a 422 (Session Interval Too
// Small) to the INVITE: the INVITE goes out again with the same
// Call-ID, From and To, the next CSeq number, and a Min-SE and
// Session-Expires that take the Min-SE of the 422 into account. It
// returns the new client transaction, or nil when resp asks for no
// second INVITE. The user of the call does not see the 422.
func (c *OutgoingCall) inviteAgain(resp *Message) *ClientTransaction {
	if resp.StatusCode != 422 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	learned, ok := c.minSE.After422(resp)
	if !ok || c.cancelled {
		return nil
	}
	c.minSE = learned
	// RFC 3261 12.3: the 422 ended the early dialogs of the first
	// INVITE.
	for _, d := range c.dialogs {
		d.setState(DialogTerminated, "422 received")
	}
	c.dialogs = nil

	req := c.invite.Clone()
	req.RemoveFirst("Via")
	cseq, _ := ParseCSeq(req.Get("CSeq"))
	req.Set("CSeq", CSeq{Number: cseq.Number + 1, Method: INVITE}.String())
	req.Remove("Min-SE")
	req.Remove("Session-Expires")
	addHeaders(req, c.ua.cfg.SessionTimer.RequestHeaders(c.minSE))
	ct, err := c.send(req)
	if err != nil {
		c.ua.log.Warn("INVITE not sent again after the 422", "error", err.Error())
		return nil
	}
	c.ct, c.invite = ct, ct.Request
	return ct
}

// deliver never blocks the call on a user that does not read: like the
// stack, it keeps the last slot for the final response.
func (c *OutgoingCall) deliver(resp *Message) {
	room := cap(c.responses) - len(c.responses)
	if room == 0 || (resp.StatusCode < 200 && room == 1) {
		c.ua.log.Warn("dropped response the user has not read", "status", resp.StatusCode, "call_id", resp.Get("Call-ID"))
		return
	}
	c.responses <- resp
}

// receiveProvisional is RFC 3261 13.2.2.1: a 101-199 response with a To
// tag creates an early dialog, one for each tag. A reliable response
// also gets a PRACK within its dialog. It reports whether to pass resp
// on.
func (c *OutgoingCall) receiveProvisional(resp *Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	tag := tagOf(resp.Get("To"))
	if tag == "" {
		// Every RFC 3261 UAS tags its responses but 100 (8.2.6.2), so a
		// 101-199 without a tag comes from an RFC 2543 UAS, which knows
		// neither early dialogs nor 100rel.
		return true
	}
	d := c.dialogWith(tag)
	reliable := resp.Requires(Option100rel)
	terminated := resp.StatusCode == 199
	if d == nil {
		if terminated && !reliable {
			// RFC 6228 4: a 199 that overtook the response that would
			// have created its dialog is discarded. A reliable one
			// still gets its PRACK, for which it needs the dialog.
			return false
		}
		dialog, err := NewUACDialog(c.invite, resp)
		if err != nil {
			c.ua.log.Warn("provisional response creates no dialog", "status", resp.StatusCode, "error", err.Error())
			return !terminated
		}
		c.withoutOwnRoute(&dialog)
		d = c.addDialog(dialog, fmt.Sprintf("%d received", resp.StatusCode))
	}
	if d.dialog.State == DialogTerminated {
		// A response for a dialog that a 199 or a BYE has ended.
		return false
	}
	if reliable && !d.reliableInOrder(resp) {
		return false
	}
	d.sessionReceived(resp, reliable)
	if resp.Allows(UPDATE) {
		d.peerAllowsUpdate = true
	}
	// RFC 3262 4: the UAS wants a reliable response acknowledged. A
	// response with an offer waits for the user's answer (RFC 3262 5).
	if reliable && !c.manualPRACK && d.offer != OfferRemote {
		d.autoPRACK(resp)
	}
	if terminated {
		// RFC 6228 4: the early dialog is over, and needs no BYE. The
		// INVITE goes on: another UAS may still answer it. The user of
		// the call sees the 199, and lets go of what it holds for this
		// dialog, such as its early media.
		d.setState(DialogTerminated, "199 received")
	}
	return true
}

// sessionReceived is RFC 3261 13.2.1 and RFC 3262 5 for a session
// description in a response to the INVITE. When the INVITE carried the
// offer, the first description that comes back is the answer, and the
// UAC ignores any later one. When the INVITE carried none, the first
// reliable response has the offer, and the answer goes into the PRACK
// or the ACK. c.mu is held.
func (d *CallDialog) sessionReceived(resp *Message, reliable bool) {
	if len(resp.Body) == 0 {
		return
	}
	switch {
	case d.offer == OfferLocal:
		d.offerEvent(AnswerReceived)
	case d.offer == OfferNone && reliable:
		d.offerEvent(OfferReceived)
	}
}

// dialogWith is the dialog of the call whose remote tag is tag, nil
// when no response with that To tag has created one. c.mu is held.
func (c *OutgoingCall) dialogWith(tag string) *CallDialog {
	for _, d := range c.dialogs {
		if d.dialog.ID.RemoteTag == tag {
			return d
		}
	}
	return nil
}

// current is the dialog that the call goes on in: the one that the 2xx
// confirmed; before the 2xx, the earliest early dialog that has not
// ended; and when every dialog has ended, the first. It is nil while
// the call has no dialog. c.mu is held.
func (c *OutgoingCall) current() *CallDialog {
	if c.kept != nil {
		return c.kept
	}
	for _, d := range c.dialogs {
		if d.dialog.State != DialogTerminated {
			return d
		}
	}
	if len(c.dialogs) > 0 {
		return c.dialogs[0]
	}
	return nil
}

// Dialogs returns the dialogs of the INVITE in the order their first
// responses came: one for each UAS that answered, which are several
// when a proxy or an AS on the way forks the INVITE (RFC 3261
// 13.2.2.4). The methods of the call itself work on one of them: the
// dialog that the 2xx confirmed, and before the 2xx the earliest early
// dialog that has not ended.
func (c *OutgoingCall) Dialogs() []*CallDialog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.dialogs)
}

// DialogOf returns the dialog that resp, a response to the INVITE,
// belongs to, by its To tag. It is nil for a response that created no
// dialog, such as a 4xx from a proxy.
func (c *OutgoingCall) DialogOf(resp *Message) *CallDialog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dialogWith(tagOf(resp.Get("To")))
}

// withoutOwnRoute takes the UA out of the route set of a dialog when
// its INVITE record-routed the UA (Invitation.RecordRoute): the UA is
// no hop on its own way to the peer. c.mu is held.
func (c *OutgoingCall) withoutOwnRoute(d *Dialog) {
	own := c.ua.ownRoute()
	if !slices.Contains(c.invite.GetAll("Record-Route"), own) {
		return
	}
	na, err := ParseNameAddr(own)
	if err != nil {
		return
	}
	d.RouteSet = slices.DeleteFunc(d.RouteSet, func(r string) bool { return r == na.URI })
}

// addDialog adds the dialog that a response created to the call. c.mu
// is held.
func (c *OutgoingCall) addDialog(dialog Dialog, event string) *CallDialog {
	d := &CallDialog{c: &c.call, invite: inviteSent}
	c.dialogs = append(c.dialogs, d)
	d.start(dialog, event)
	// The INVITE keeps its RSeq numbers after its final response, so
	// that a reliable provisional response that comes later still gets
	// its PRACK (RFC 3262 4).
	d.expectReliable(dialog.LocalSeq)
	if len(c.Request.Body) > 0 {
		// The offer of the INVITE is an offer to every UAS that the
		// INVITE reaches.
		d.offerEvent(OfferSent)
		d.sent(BodyOf(c.Request))
	}
	return d
}

// reliableSeq is RFC 3262 4 for the reliable provisional responses to
// one INVITE of this UA within one dialog: last is the RSeq of the last
// one taken in order, 0 before the first, and pracked that of the last
// one that got its PRACK.
type reliableSeq struct {
	last, pracked uint32
}

// expectReliable starts the RSeq numbers of the INVITE of d with the
// CSeq number seq. c.mu is held.
func (d *CallDialog) expectReliable(seq uint32) {
	if d.reliable == nil {
		d.reliable = map[uint32]*reliableSeq{}
	}
	d.reliable[seq] = &reliableSeq{}
}

// reliableTo is the RSeq state of the INVITE that resp answers, nil for
// an INVITE whose reliable provisional responses d does not take. c.mu
// is held.
func (d *CallDialog) reliableTo(resp *Message) *reliableSeq {
	cseq, _ := ParseCSeq(resp.Get("CSeq"))
	return d.reliable[cseq.Number]
}

// reliableInOrder is RFC 3262 4 for a reliable provisional response:
// the one that follows the last in order is taken; a retransmission,
// or one that comes out of order, is discarded. It reports whether resp
// is new and in order. c.mu is held.
func (d *CallDialog) reliableInOrder(resp *Message) bool {
	c := d.c
	seq := d.reliableTo(resp)
	rseq, err := ParseRSeq(resp.Get("RSeq"))
	switch {
	case seq == nil:
		return false
	case err != nil:
		c.ua.log.Warn("discarded reliable provisional response", "status", resp.StatusCode, "error", err.Error())
		return false
	}
	if seq.last != 0 && rseq != seq.last+1 {
		if rseq != seq.last {
			c.ua.log.Warn("discarded reliable provisional response out of order", "status", resp.StatusCode,
				"rseq", rseq, "last_rseq", seq.last)
		}
		return false
	}
	seq.last = rseq
	return true
}

// newPRACK builds the PRACK for resp within its dialog (RFC 3262 4,
// 7.2). c.mu is held.
func (d *CallDialog) newPRACK(resp *Message, body Body, headers []HeaderField) *Message {
	rseq, _ := ParseRSeq(resp.Get("RSeq"))
	cseq, _ := ParseCSeq(resp.Get("CSeq"))
	d.reliableTo(resp).pracked = rseq
	prack := d.newRequest(PRACK, body, headers)
	prack.Add("RAck", RAck{RSeq: rseq, CSeq: cseq}.String())
	return prack
}

// autoPRACK sends the PRACK for resp without a body, and does not wait
// for its answer. c.mu is held. The PRACK takes its turn here, before
// the user of the call sees resp, so that a request that the user sends
// on seeing resp goes after it. A PRACK that shows that the peer has
// lost the dialog ends it, as any other request within the dialog does
// (roundTrip).
func (d *CallDialog) autoPRACK(resp *Message) {
	c := d.c
	prack := d.newPRACK(resp, Body{}, c.ua.own(PRACK, false, nil))
	t := d.takeTurn()
	sent := c.ua.spawn(func() {
		final, err := d.roundTrip(prack, t)
		if err != nil || final.StatusCode >= 300 {
			status := 0
			if final != nil {
				status = final.StatusCode
			}
			c.ua.log.Warn("PRACK failed", "call_id", prack.Get("Call-ID"), "status", status, "error", fmt.Sprint(err))
		}
	})
	if !sent {
		// The UA is closing, and the PRACK does not go out.
		t.end()
	}
}

// PRACK sends the PRACK for resp, a reliable provisional response that
// came on Responses, and returns the final response to the PRACK (RFC
// 3262 4). It is for the responses that the UA leaves to the user: all
// of them with Invitation.ManualPRACK, and otherwise those that carry
// an offer, which body answers (RFC 3262 5). After an answer in resp,
// body may be a new offer, which the 2xx to the PRACK answers.
func (c *OutgoingCall) PRACK(resp *Message, body Body, headers ...HeaderField) (*Message, error) {
	c.mu.Lock()
	d := c.dialogWith(tagOf(resp.Get("To")))
	var seq *reliableSeq
	if d != nil {
		seq = d.reliableTo(resp)
	}
	rseq, err := ParseRSeq(resp.Get("RSeq"))
	switch {
	case seq == nil || err != nil || !resp.Requires(Option100rel):
		err = errors.New("sip: not a reliable provisional response of this call")
	case rseq != seq.last || rseq == seq.pracked:
		err = errors.New("sip: the response is not the one that waits for its PRACK")
	default:
		err = d.usable()
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	offers := false
	switch hasBody := len(body.Content) > 0; {
	case d.offer == OfferRemote && !hasBody:
		err = ErrNoAnswer
	case d.offer == OfferRemote:
		err = d.offerEvent(AnswerSent)
	case hasBody:
		offers = true
		err = d.offerEvent(OfferSent)
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	d.sent(body)
	prack := d.newPRACK(resp, body, headers)
	t := d.takeTurn()
	c.mu.Unlock()

	final, err := d.roundTrip(prack, t)
	if offers {
		c.mu.Lock()
		if final != nil && final.StatusCode < 300 && len(final.Body) > 0 {
			d.offerEvent(AnswerReceived)
		} else {
			d.offerEvent(OfferRejected)
		}
		c.mu.Unlock()
	}
	return final, err
}

// receive2xx is RFC 3261 13.2.2.4: the 2xx confirms its dialog, or
// creates a confirmed one, and gets an ACK. Every retransmission of the
// 2xx gets the same ACK again. The call goes on in the dialog of the
// first 2xx. When a second UAS answers too, its 2xx gets its ACK and
// then a BYE, as the RFC asks for a dialog that the UAC does not want.
// receive2xx reports whether resp is the first 2xx, to pass on.
func (c *OutgoingCall) receive2xx(resp *Message) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	first := c.kept == nil
	event := fmt.Sprintf("%d received", resp.StatusCode)
	d := c.dialogWith(tagOf(resp.Get("To")))
	switch {
	case d == nil:
		dialog, err := NewUACDialog(c.invite, resp)
		if err != nil {
			// Without a dialog there is nowhere to send the ACK to.
			c.ua.log.Warn("2xx creates no dialog", "status", resp.StatusCode, "error", err.Error())
			if first {
				c.end(event, err)
			}
			return first
		}
		c.withoutOwnRoute(&dialog)
		d = c.addDialog(dialog, event)
	case d.dialog.State != DialogEarly:
		// A retransmission of the 2xx gets its ACK again, once the ACK
		// has gone out.
		cseq, _ := ParseCSeq(resp.Get("CSeq"))
		if ack := d.acks[cseq.Number]; ack != nil {
			if err := c.sendACK(ack); err != nil {
				c.ua.log.Warn("ACK not sent", "error", err.Error())
			}
		}
		return false
	default:
		if err := d.dialog.confirmByUAC(c.invite, resp); err != nil {
			c.ua.log.Warn("2xx does not confirm its dialog", "status", resp.StatusCode, "error", err.Error())
			if first {
				c.end(event, err)
			}
			return first
		}
		c.withoutOwnRoute(d.dialog)
		c.ua.logDialog(d.dialog, event, DialogEarly.String())
	}
	d.sessionReceived(resp, true)
	d.invite = noInvite
	if resp.Allows(UPDATE) {
		d.peerAllowsUpdate = true
	}
	cseq, _ := ParseCSeq(c.invite.Get("CSeq"))
	d.acknowledge(cseq.Number)
	if first {
		c.kept = d
		// RFC 4028 7.2: the 2xx tells whether the session has a timer.
		// 7.4: the Min-SE learned from a 422 to the INVITE does not
		// carry over into the dialog.
		d.setSession(SessionTimer{}.After2xx(resp))
	}
	if !first || c.cancelled {
		// RFC 3261 15: with cancelled, the callee accepted while the
		// CANCEL was on its way. The UAC may keep the call or end it
		// with a BYE; the user asked to hang up. bye waits for c.mu,
		// which is held here.
		c.ua.spawn(func() {
			if err := d.byeOwn(nil); err != nil {
				c.ua.log.Warn("BYE for an unwanted 2xx failed", "call_id", c.Request.Get("Call-ID"), "error", err.Error())
			}
		})
	}
	return first
}

// Cancel hangs up before the final response (RFC 3261 9.1, 15). The
// stack sends the CANCEL once a provisional response has come, and the
// final response, normally 487 (Request Terminated), then comes on
// Responses. A 2xx that crosses the CANCEL gets its ACK and then a BYE.
// headers go into the CANCEL, such as a Reason that tells why the
// caller gives up (RFC 3326 2).
func (c *OutgoingCall) Cancel(headers ...HeaderField) error {
	c.mu.Lock()
	c.cancelled = true
	ct := c.ct
	c.mu.Unlock()
	return ct.Cancel(headers...)
}

// receiveFailure is RFC 3261 13.2.2.3 and 12.3: a 300-699 response ends
// every early dialog of the INVITE. The transaction acknowledges it.
func (c *OutgoingCall) receiveFailure(resp *Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.end(fmt.Sprintf("%d received", resp.StatusCode), nil)
}

// Dialog returns a copy of the state of the dialog that the call goes
// on in (Dialogs), and false while the call has none.
func (c *OutgoingCall) Dialog() (Dialog, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.current(); d != nil {
		return d.copyDialog()
	}
	return Dialog{}, false
}

// Bye ends the call once the 2xx has come (RFC 3261 15.1.1). Before
// that, Cancel is how the caller hangs up. headers go into the BYE,
// such as a Reason (RFC 3326 2).
func (c *OutgoingCall) Bye(headers ...HeaderField) error {
	c.mu.Lock()
	d := c.current()
	c.mu.Unlock()
	if d == nil {
		return errNoConfirmedDialog
	}
	return d.Bye(headers...)
}

// currentDialog is current for the methods of the call that work within
// a dialog; before a response has created one, they fail.
func (c *OutgoingCall) currentDialog() (*CallDialog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.current(); d != nil {
		return d, nil
	}
	return nil, errNoDialog
}

// Ack is CallDialog.Ack in the dialog that the call goes on in
// (Dialogs): the ACK with the answer, after an INVITE without an offer.
func (c *OutgoingCall) Ack(body Body, headers ...HeaderField) error {
	d, err := c.currentDialog()
	if err != nil {
		return err
	}
	return d.Ack(body, headers...)
}

// ReInvite is CallDialog.ReInvite in the dialog that the call goes on
// in (Dialogs).
func (c *OutgoingCall) ReInvite(body Body, headers ...HeaderField) (*Message, error) {
	d, err := c.currentDialog()
	if err != nil {
		return nil, err
	}
	return d.ReInvite(body, headers...)
}

// Update is CallDialog.Update in the dialog that the call goes on in
// (Dialogs).
func (c *OutgoingCall) Update(body Body, headers ...HeaderField) (*Message, error) {
	d, err := c.currentDialog()
	if err != nil {
		return nil, err
	}
	return d.Update(body, headers...)
}

// AddSubscriptionUsage is CallDialog.AddSubscriptionUsage in the dialog
// that the call goes on in (Dialogs).
func (c *OutgoingCall) AddSubscriptionUsage() (end func(), err error) {
	d, err := c.currentDialog()
	if err != nil {
		return nil, err
	}
	return d.AddSubscriptionUsage()
}

// Send is CallDialog.Send in the dialog that the call goes on in
// (Dialogs).
func (c *OutgoingCall) Send(method Method, body Body, headers ...HeaderField) (*Message, error) {
	d, err := c.currentDialog()
	if err != nil {
		return nil, err
	}
	return d.Send(method, body, headers...)
}
