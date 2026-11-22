package mmtel

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// call is one INVITE that the S-CSCF sent the AS, from its arrival to
// its end. in is the leg toward the S-CSCF that sent it, on the side of
// the caller; the outgoing leg is the one that the AS sends back to the
// S-CSCF, on the side of the callee. A diversion sends a new outgoing
// leg to the diverted-to user, while in stays. legs relays the call
// between the two (b2bua.Call); this file keeps the order of the call:
// the services, the diversions and their retries, the no reply timer,
// the media of the MRF, and the record of the call.
type call struct {
	as     *AS
	in     *sip.IncomingCall
	legs   *b2bua.Call
	served sip.PServedUser
	// services are the settings of the served user's services, as the
	// HSS had them when the INVITE came.
	services simservs.MMTelServices
	// route is the Route of the requests the AS sends outside a dialog:
	// the received Route without the AS's own (TS 24.229 5.7.5.2.1
	// steps 1 and 4), which holds the S-CSCF with its original dialog
	// identifier.
	route []string
	log   *slog.Logger

	// recordMu orders the writes of the record of the call into the
	// Store (KeepDialogs). recorded tells that the record is there: from
	// the answer until the call ends, after which no write may put it
	// back.
	recordMu sync.Mutex
	recorded bool

	mu sync.Mutex
	// sessionToCaller tells that a session description went to the
	// caller in a reliable provisional response, after which the offer
	// and answer of the INVITE are done on in.
	sessionToCaller bool
	// sessionFrom is the To tag of the early dialog of the callee's side
	// whose session description the caller got. The INVITE of the
	// outgoing leg may fork beyond the AS, and each UAS that answers it
	// has an early dialog of its own (RFC 3261 13.2.2.4). The caller has
	// one early dialog with the AS, which is one UAS: all its responses
	// have one To tag (8.2.6.2), and it takes the first session
	// description as the answer and ignores the rest (13.2.1). The Call
	// decides which dialog that is, and the AS reads it from the
	// responses that the Call passes on (sessionToTheCaller): CAT goes on
	// in the session of that dialog (offerTone).
	sessionFrom string
	// toCaller tells that a provisional response went to the caller, so
	// that in has an early dialog.
	toCaller bool
	// withhold has the AS keep the callee's answer until the 2xx, which
	// then carries the held answer of the dialog that answered
	// (withholdSession, withholdAnswer). heldAnswer holds the answer of
	// each early dialog of the callee's side, by its To tag.
	withhold   bool
	heldAnswer map[string]sip.Body
	// acted are the services that acted on the call, for its record.
	acted []string
	// cat is the media of the MRF that the caller has until the callee
	// answers: the customized alerting tone (startAlertingTone), or the
	// announcement of a waiting call (announceWaiting). nil when there
	// is none. Only the goroutine of the call's legs reads and sets it.
	cat *alertingTone
	// calleeSDP holds the last SDP of the callee in each early dialog of
	// its side, by its To tag: of its provisional responses, of its
	// requests, and of its responses to the caller's requests. CAT goes
	// on in the session of the dialog the caller has (offerTone), and
	// the 2xx carries its answer when no reliable response did (Answer).
	calleeSDP map[string][]byte
}

// serve is UAConfig.OnCall: an INVITE from the S-CSCF.
func (as *AS) serve(in *sip.IncomingCall) {
	req := in.Request
	log := as.log.With("call_id", req.Get("Call-ID"))
	// TS 24.229 5.7.1.3A: the S-CSCF names the served user in
	// P-Served-User, and this S-CSCF always adds the session case. A
	// request without one came by no filter criterion. The INVITE to a
	// conference URI comes that way, by DNS (conference.go).
	served, err := sip.ParsePServedUser(req.Get("P-Served-User"))
	if err != nil || served.SessionCase == sip.SessionCaseNone {
		if _, ok := as.conferenceID(req.RequestURI); ok {
			as.joinConference(in, log)
			return
		}
		log.Info("INVITE without a served user and its session case", "p_served_user", req.Get("P-Served-User"))
		in.Reject(403, as.warning("No served user"))
		return
	}
	// Step 1 of TS 24.229 5.7.5.2.1: the top Route names the AS, since
	// that is how the request got here.
	route := req.GetAll("Route")
	if len(route) < 2 {
		log.Info("INVITE without a Route back to the S-CSCF", "route", route)
		in.Reject(403, as.warning("No route back to the S-CSCF"))
		return
	}
	services, err := as.services(served.URI)
	if err != nil {
		// Without the settings, no service can be given as the user set
		// it up. A 5xx before any provisional response lets the S-CSCF
		// apply the default handling of the filter criterion (TS 24.229
		// 5.4.3.2 step 5, 5.4.3.3 step 4): go on without the AS, or end
		// the call.
		log.Warn("service data not read from the HSS", "served_user", served.URI, "error", err.Error())
		in.Reject(500, as.warning("Service data unavailable"))
		return
	}
	c := &call{as: as, in: in, served: served, services: services, route: route[1:],
		heldAnswer: map[string]sip.Body{}, calleeSDP: map[string][]byte{},
		log: log.With("served_user", served.URI, "session_case", string(served.SessionCase))}
	c.legs = b2bua.New(in, c, c.log)
	c.log.Info("call", "request_uri", req.RequestURI, "regstate", served.RegState)
	c.run()
}

// warning is the Warning header field of a response of the AS: code
// 399, the miscellaneous warning (RFC 3261 20.43), with the AS as the
// agent.
func (as *AS) warning(text string) sip.HeaderField {
	return sip.HeaderField{Name: "Warning", Value: "399 " + as.cfg.Host + " " + strconv.Quote(text)}
}

// newInvitation is the INVITE of a new outgoing leg to requestURI. It
// copies what TS 24.229 5.7.5.2.1 has a routeing B2BUA copy: the rest
// of the Route (step 4), the P-Asserted-Identity (step 5), and the
// other header fields that no service changes (5.7.5.1). The callee
// also sees the caller's Supported, Allow and session timer, so that it
// uses no extension that the caller does not take.
//
// Each leg is a dialog of its own, and RFC 4028 runs a session timer
// per dialog. The callee is asked for the interval the caller asked
// for. On the outgoing leg the AS is the UAC, so when the callee leaves
// the refreshes to the UAC, the AS refreshes that leg; on the incoming
// leg the caller refreshes as it would without the AS. A refresh within
// one leg ends at the AS (b2bua.Call).
//
// The services change the header fields in fields, which go into the
// Invitation once they are done. The error is a failure of the Store,
// which then cannot tell the call that a Replaces names
// (replacesOnTheOtherSide).
func (c *call) newInvitation(requestURI string) (sip.Invitation, *sip.Message, error) {
	req := c.in.Request
	fields := carried(req, "Allow", "Supported", "Session-Expires", "Min-SE")
	if pcv := chargingVector(req); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	// The AS takes away one hop, as a proxy would (RFC 3261 16.6 step 3),
	// so that a call that the services send round in a loop ends.
	hops := req.Clone()
	sip.DecrementMaxForwards(hops)
	fields.Set("Max-Forwards", hops.Get("Max-Forwards"))
	if err := c.replacesOnTheOtherSide(fields); err != nil {
		return sip.Invitation{}, nil, err
	}
	from, _ := sip.ParseNameAddr(req.Get("From"))
	to, _ := sip.ParseNameAddr(req.Get("To"))
	contact, fromFocus := focusContact(req)
	return sip.Invitation{
		RequestURI:      requestURI,
		From:            from.URI,
		FromDisplayName: from.DisplayName,
		To:              to.URI,
		Route:           c.route,
		Body:            sip.BodyOf(req),
		Contact:         contact,
		RecordRoute:     fromFocus,
	}, fields, nil
}

// relay sends inv on a new outgoing leg, passes on what comes back,
// and gives the caller the final response of the leg.
func (c *call) relay(inv sip.Invitation) {
	r := c.attempt(inv, 0)
	c.finish(r)
}

// finish ends the INVITE of the caller after an attempt that did not
// get the call answered: with the final response of the leg, or with a
// response of the AS when the leg got none.
func (c *call) finish(r attemptResult) {
	c.legs.NoLeg()
	switch {
	case r.answered, r.cancelled:
		// The call is over, or the UA has answered the CANCEL with 487.
	case r.final != nil:
		c.in.Reject(r.final.StatusCode, c.responseFields(r.final, c.in.Request, true).Headers...)
	case errors.Is(r.err, sip.ErrTimeout):
		c.in.Reject(408)
	default:
		c.log.Warn("outgoing leg failed", "error", errText(r.err))
		c.in.Reject(500)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// toTag is the To tag of m.
func toTag(m *sip.Message) string { return tagOf(m, "To") }

// fromTag is the From tag of m: the tag of the dialog's peer in a
// request that the peer sent (RFC 3261 12.1.1).
func fromTag(m *sip.Message) string { return tagOf(m, "From") }

// tagOf is the tag of the To or From of m, named by field.
func tagOf(m *sip.Message, field string) string {
	na, err := sip.ParseNameAddr(m.Get(field))
	if err != nil {
		return ""
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// attemptResult is how an outgoing leg ended.
type attemptResult struct {
	// answered: the callee answered, and the call is over by now.
	answered bool
	// final is a final response from 300 to 699 that the AS has not
	// passed on yet, for a service to act on.
	final *sip.Message
	// err is why no final response came.
	err error
	// provisional tells that a response from 101 to 199 came on the leg
	// before its final response (TS 24.604 4.5.2.6.6).
	provisional bool
	// noReply: the no reply timer fired, and the AS cancelled the leg
	// (TS 24.604 4.5.2.6.3 item 2).
	noReply bool
	// cancelled: the caller cancelled the call, and the AS the leg.
	cancelled bool
}

// attempt sends inv on a new outgoing leg and relays between it and
// in, until the leg ends or the callee answers; after an answer, until
// the call ends. When noReply is not zero, the no reply timer runs from
// the first 180 (Ringing) for that long.
func (c *call) attempt(inv sip.Invitation, noReply time.Duration) attemptResult {
	select {
	case <-c.in.Done():
		return attemptResult{cancelled: true}
	default:
	}
	ua := c.as.ua.Load()
	if ua == nil {
		return attemptResult{err: errNotListening}
	}
	// The Call relays the requests of the callee within the dialogs of
	// the leg, cancels its INVITE when the caller's leg ends before the
	// answer (RFC 3261 9.1), and ends one leg with the other.
	out, err := c.legs.Attempt(ua, inv)
	if err != nil {
		return attemptResult{err: err}
	}
	c.log.Info("outgoing leg", "request_uri", inv.RequestURI, "call_id", out.Request.Get("Call-ID"))

	// settled stops the watch of the no reply timer once the leg has its
	// final response.
	settled := make(chan struct{})
	defer close(settled)

	var r attemptResult
	var timer func() bool
	var timedOut chan struct{}
	defer func() {
		if timer != nil {
			timer()
		}
	}()
	for resp := range out.Responses() {
		switch code := resp.StatusCode; {
		case code < 200:
			r.provisional = true
			if r.noReply {
				continue
			}
			if code == 180 && noReply > 0 && timer == nil {
				// TS 24.604 4.5.2.6.3 item 2: the first 180 starts the no
				// reply timer, and a 180 of another fork does not start
				// it again.
				timedOut = make(chan struct{})
				timer = c.as.cfg.AfterFunc(noReply, func() { close(timedOut) })
				go c.cancelOnNoReply(out, timedOut, settled)
			}
			c.relayProvisional(out, resp)
		case code < 300:
			if c.noReplyFired(timedOut) {
				// The 2xx crossed the CANCEL; the UA ends that dialog with
				// a BYE (RFC 3261 15).
				r.noReply = true
				continue
			}
			c.answerCall(out, resp)
			return attemptResult{answered: true}
		default:
			r.final = resp
		}
	}
	r.noReply = r.noReply || c.noReplyFired(timedOut)
	// The caller's leg has ended before the answer, however it ended: the
	// UA has given the INVITE its final response already (487 to a
	// CANCEL, RFC 3261 9.2 and 15.1.2; 500 for a missing PRACK, RFC 3262
	// 3), and no diversion goes on for a caller who is gone.
	select {
	case <-c.in.Done():
		r.cancelled = true
	default:
	}
	if r.final == nil {
		r.err = out.Err()
	}
	return r
}

// cancelOnNoReply is TS 24.604 4.5.2.6.3 item 2: when the no reply
// timer expires, the AS cancels the leg to the diverting user with a
// Reason of SIP cause 408.
func (c *call) cancelOnNoReply(out *sip.OutgoingCall, timedOut, settled <-chan struct{}) {
	select {
	case <-timedOut:
		c.log.Info("no reply timer expired")
		out.Cancel(sip.NewReason(sip.ReasonSIP, 408, sip.ReasonPhrase(408)).Header())
	case <-settled:
	}
}

func (c *call) noReplyFired(timedOut chan struct{}) bool {
	if timedOut == nil {
		return false
	}
	select {
	case <-timedOut:
		return true
	default:
		return false
	}
}

// relayProvisional gives a provisional response of the outgoing leg
// out to the services of the served user, and then to the Call, which
// passes it on to the caller. The customized alerting tone and the
// announcement of a waiting call answer the caller from the MRF
// instead (alertWithTone), and a call whose answer the AS withholds
// for a possible diversion gets the status code without the session
// description (withholdAnswer).
//
// SIMPLIFIED: RFC 3262 5. After an INVITE without an offer, the first
// reliable response with a body in each early dialog has the offer of
// its UAS. The caller answers the offer of the first dialog, and the AS
// does not offer it those of the other dialogs in an UPDATE of its early
// dialog: the Call passes the responses of those dialogs on without
// their session description. Such an offer gets no answer and its
// response no PRACK, so that UAS ends its early dialog with a 5xx (RFC
// 3262 3). The caller's answer to the first offer may still be on its
// way then, and a caller that does not allow UPDATE can take no other
// offer before the 2xx.
func (c *call) relayProvisional(out *sip.OutgoingCall, resp *sip.Message) {
	if resp.StatusCode != 199 {
		if c.alertWithTone(out, resp) || c.withholdAnswer(out, resp) {
			return
		}
		c.mu.Lock()
		c.toCaller = true
		c.mu.Unlock()
	}
	c.legs.RelayProvisional(out, resp)
}

// withholdAnswer keeps the callee's answer from the caller until the
// 2xx (withholdSession): the caller gets the status code of resp
// without the session description, and without P-Early-Media, since
// without a session description there is no early media. The AS
// acknowledges a reliable response of the callee itself, as it does
// while the caller has a tone (relayDuringTone): the PRACK of the
// caller is for the response that the AS sent of its own. It reports
// whether it has dealt with resp.
//
// Only an INVITE that carried the offer has its answer withheld
// (withholdSession), so what the AS keeps back is always the callee's
// answer, never the offer of a UAS.
func (c *call) withholdAnswer(out *sip.OutgoingCall, resp *sip.Message) bool {
	if !c.withhold {
		return false
	}
	c.mu.Lock()
	if body := sip.BodyOf(resp); resp.Requires(sip.Option100rel) && len(body.Content) > 0 {
		c.heldAnswer[toTag(resp)] = body
	}
	c.toCaller = true
	c.mu.Unlock()
	c.passFocusContact(resp)
	fields := c.responseFields(resp, c.in.Request, true)
	fields.Remove("P-Early-Media")
	if err := c.legs.Progress(resp.StatusCode, sip.Body{}, fields.Headers...); err != nil {
		c.log.Info("provisional response not relayed", "status", resp.StatusCode, "error", err.Error())
	}
	c.prackCallee(out, resp)
	return true
}

// answerCall passes the 2xx of out on to the caller and has the Call
// relay the call until it ends, with the record of the call in the
// Store while it lasts: the Call writes it once the caller has
// acknowledged the 2xx and after each request within the dialogs
// (KeepDialogs).
func (c *call) answerCall(out *sip.OutgoingCall, resp *sip.Message) {
	c.recordMu.Lock()
	c.recorded = true
	c.recordMu.Unlock()
	defer c.removeRecord(out)
	c.legs.Answer(out, resp)
}

// removeRecord forgets the record of the call that has ended, so that
// no instance finds a call that is over. The dialogs keep the
// identifiers by which the record is deleted, however often it was
// written again.
func (c *call) removeRecord(out *sip.OutgoingCall) {
	c.recordMu.Lock()
	defer c.recordMu.Unlock()
	if !c.recorded {
		return
	}
	c.recorded = false
	in, _ := c.in.Dialog()
	outgoing, _ := out.Dialog()
	if err := c.as.store.DeleteCall(context.Background(), c.record(in, outgoing)); err != nil {
		// Both legs have ended, and no request waits for this. The
		// record stays, as that of a call whose BYE no instance sees;
		// its dialogs are never used again.
		c.log.Warn("ended call left in the store", "error", err.Error())
	}
}

// offerCaller offers the caller the session of the early dialog of the
// callee's side whose To tag is tag: the callee's last SDP in that
// dialog, in the session that the caller has with the AS, whose o= line
// was origin. The line stays, with its version one up (RFC 3264 8). In
// the early dialog the offer goes in an UPDATE (RFC 3311): no re-INVITE
// goes while the INVITE is in progress (RFC 3261 14.1). Once the dialog
// is confirmed, a caller that does not list UPDATE in its Allow gets the
// offer in a re-INVITE. When the offer crosses one of the caller, the AS
// tries again after GlareDelay (RFC 3261 14.1, RFC 3311 5.3), at most
// glareTries times in all. offerCaller returns the o= line of the offer,
// and the caller's 2xx with the answer, nil when the caller did not take
// the offer.
//
// CAT offers this way (answerAfterTone). The dialog it offers is one
// whose provisional responses the AS answered itself, from the MRF, so
// they never reached the Call: the Call keeps the session descriptions
// of the dialogs whose responses it passed on, and has none of this one
// to offer (b2bua.Call.OfferCaller).
func (c *call) offerCaller(tag string, origin sdp.Origin) (sdp.Origin, *sip.Message) {
	c.mu.Lock()
	callee := c.calleeSDP[tag]
	c.mu.Unlock()
	offer, err := sdp.Parse(callee)
	if err != nil {
		c.log.Warn("no SDP of the callee to offer the caller", "to_tag", tag, "error", err.Error())
		return origin, nil
	}
	offer.Origin = origin
	offer.Origin.Version++
	method, send := sip.UPDATE, c.in.Update
	if d, _ := c.in.Dialog(); d.State == sip.DialogConfirmed && !c.in.Request.Allows(sip.UPDATE) {
		method, send = sip.INVITE, c.in.ReInvite
	}
	resp, err := send(sdpBody(offer))
	for try := 1; try < glareTries && crossed(resp, err); try++ {
		c.log.Info("the offer to the caller crossed one of the caller", "method", string(method), "try", try)
		// The caller created the Call-ID of its dialog with the AS.
		select {
		case <-c.in.Done():
			return offer.Origin, nil
		case <-time.After(sip.GlareDelay(false)):
		}
		resp, err = send(sdpBody(offer))
	}
	switch {
	case err != nil:
		c.log.Warn("the callee's session not offered to the caller", "method", string(method), "error", err.Error())
		return offer.Origin, nil
	case resp.StatusCode >= 300:
		c.log.Warn("the caller refused the callee's session", "method", string(method), "status", resp.StatusCode)
		return offer.Origin, nil
	}
	c.log.Info("the caller has the session of the callee's dialog", "method", string(method), "to_tag", tag)
	return offer.Origin, resp
}

// glareTries is how many offers the AS sends the caller in all for one
// session it moves, when each crosses an offer of the caller. RFC 3261
// 14.1 leaves the number to the UAC.
const glareTries = 3

// crossed reports whether an offer within the dialog crossed one of the
// peer: the peer refused it with 491 (Request Pending), or the UA did
// not send it while an offer or an INVITE of the peer was open (RFC
// 3261 14.1, RFC 3311 5.2).
func crossed(resp *sip.Message, err error) bool {
	if err != nil {
		return errors.Is(err, sip.ErrOfferUnanswered) || errors.Is(err, sip.ErrInviteInProgress)
	}
	return resp.StatusCode == 491
}

// record is the record of the call with its two dialogs.
func (c *call) record(in, out sip.Dialog) CallRecord {
	r := CallRecord{Incoming: in, Outgoing: out, ServedUser: c.served.URI, SessionCase: c.served.SessionCase}
	c.mu.Lock()
	r.Services = append(r.Services, c.acted...)
	c.mu.Unlock()
	return r
}

// actedOn notes that a service acted on the call.
func (c *call) actedOn(service string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acted = append(c.acted, service)
}

// responseFields are the header fields of resp, a response of one leg,
// that go into the response to req on the other leg. toCaller tells
// that the response goes to the caller's side, where the services of
// the served user act on it (servicesOnResponse).
func (c *call) responseFields(resp, req *sip.Message, toCaller bool) *sip.Message {
	fields := carried(resp)
	if pcv := responseChargingVector(req, c.as.cfg.HomeDomain); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	if toCaller {
		c.servicesOnResponse(resp, req, fields)
	}
	return fields
}

// requestFields are the header fields of req, a request of one leg,
// that go into the request on the other leg.
func (c *call) requestFields(req *sip.Message) *sip.Message {
	fields := carried(req)
	if pcv := chargingVector(req); pcv != "" {
		fields.Set("P-Charging-Vector", pcv)
	}
	return fields
}
