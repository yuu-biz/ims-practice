package mmtel

import (
	"context"
	"fmt"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the customized alerting tone (CAT, TS 24.182) at the
// AS of the called user, in the gateway model of 4.5.5.3.7 (RFC 3960,
// TS 24.628 Annex G). The AS asks the MRF for the tone before it sends
// the INVITE to the callee; on the callee's first provisional response
// it answers the caller's offer with the SDP of the MRFP, so that the
// caller hears the tone in early media instead of a ringing tone of its
// own (TS 24.628 4.7.2.1); when the callee answers, the AS stops the
// tone and moves the caller's media to the callee with an UPDATE.
//
// Of the interactions of 4.6, CW (4.6.11) takes the second option: a
// call that waits gets the waiting indication and no CAT. With CDIV
// (4.6.7.1, 4.6.7.2) the diverting user's tone stops when the call is
// diverted, and the AS of the diverted-to user applies that user's CAT:
// option 1 of both clauses. The tone is the same for every caller, so
// OIR (4.6.5) and the order before OIP (4.6.4) change nothing, and TIR
// (4.6.3) does not keep it from the caller: it shows no identity.
//
// SIMPLIFIED: TS 24.182 4.5.5.3.7. An UPDATE with an offer from the
// callee after its 180 should stay at the AS, which answers it from the
// caller's offer and keeps it for the answer. Here it goes on to the
// caller as any request within the dialog does, which moves the
// caller's media away from the tone. The UE simulator sends none then:
// it alerts its user only once its resources are reserved.

// catContent is the value of the content attribute of RFC 4796 on the
// SDP of the tone (TS 24.182 4.5.5.3.7 b 2).
const catContent = "g.3gpp.cat"

// catPhase is how far the CAT of a call has got.
type catPhase int

const (
	// catReserved: the MRFC has answered with the offer of the MRFP,
	// and the caller has nothing of it yet.
	catReserved catPhase = iota
	// catAwaitingAlerting: the callee applied preconditions, its answer
	// went to the caller as it came (4.5.5.3.7 b 1 i), and the tone
	// waits for the 180 that tells that the callee alerts its user.
	catAwaitingAlerting
	// catPlaying: the caller has the SDP of the MRFP, and the MRFP
	// plays.
	catPlaying
	// catStopped: the tone is over or was never given; responses go to
	// the caller as they would without CAT.
	catStopped
)

// alertingTone is the CAT of one call.
type alertingTone struct {
	media *mrfMedia
	// callerOffer is the caller's offer, which the AS keeps (4.5.5.3.7
	// a).
	callerOffer sdp.Session
	phase       catPhase
	// origin is the o= line of the last SDP that the AS sent the
	// caller, whose version goes up with each new one (RFC 3264 8).
	origin sdp.Origin
	// content is the content attribute of RFC 4796 on the SDP of the
	// media: catContent for a tone, "" for an announcement.
	content string
}

// startAlertingTone is 4.5.5.3.7 on the INVITE to the served user: when
// the user has CAT on, the AS asks the MRF for the tone, keeps the
// caller's offer, and takes P-Early-Media out of fields, the INVITE to
// the callee, by local policy (a to c). Nothing happens for a caller
// that cannot take the early media of the MRF (earlyMediaOffer), and
// for a call whose session the AS withholds for a diversion
// (withholdSession). The HSS is asked for the user's CAT only for a
// call that could have one.
func (c *call) startAlertingTone(fields *sip.Message) {
	if c.as.cfg.MRFC == "" || c.withhold {
		return
	}
	offer, ok := c.earlyMediaOffer()
	if !ok {
		return
	}
	tone := c.alertingToneName()
	if tone == "" {
		return
	}
	media, err := c.reserveMedia(tone, -1)
	if err != nil {
		c.log.Warn("CAT: no tone from the MRF", "tone", tone, "error", err.Error())
		return
	}
	fields.Remove("P-Early-Media")
	c.cat = &alertingTone{media: media, callerOffer: offer, content: catContent}
	c.actedOn("CAT")
	c.log.Info("CAT: the tone is reserved", "tone", tone)
}

// alertWithTone is 4.5.5.3.7 on a provisional response of the callee,
// before it goes to the caller. It reports whether it has dealt with
// resp; otherwise relayProvisional relays it as without CAT.
func (c *call) alertWithTone(out *sip.OutgoingCall, resp *sip.Message) bool {
	c.keepCalleeSDP(toTag(resp), sip.BodyOf(resp))
	if resp.StatusCode == 180 && waitingIn(resp) {
		c.announceWaiting()
	}
	t := c.cat
	if t == nil || t.phase == catStopped {
		return false
	}
	switch {
	case t.phase == catPlaying:
		c.relayDuringTone(out, resp)
		return true
	case resp.StatusCode == 180 && waitingIn(resp) && t.content == catContent:
		c.stopAlertingTone("the call waits (TS 24.182 4.6.11)")
		return false
	case t.phase == catAwaitingAlerting:
		if resp.StatusCode != 180 {
			return false
		}
		if !c.offerTone() {
			c.stopAlertingTone("the caller did not take the tone")
			return false
		}
		c.relayDuringTone(out, resp)
		return true
	case resp.Requires(sip.OptionPrecondition):
		// b 1 i: the callee's answer goes to the caller as it came, so
		// that the two settle their preconditions end to end.
		t.phase = catAwaitingAlerting
		return false
	}
	// b 1 ii: the answer to the caller comes from the MRF.
	answer, err := t.media.answerTo(t.callerOffer, newOrigin(t.media.address().Addr()), t.content)
	if err != nil {
		c.log.Warn("CAT: no answer for the caller", "error", err.Error())
		c.stopAlertingTone("no answer for the caller")
		return false
	}
	c.prackCallee(out, resp)
	fields := c.responseFields(resp, c.in.Request, true)
	fields.Set("P-Early-Media", sip.EarlyMediaSendonly)
	t.origin, t.phase = answer.Origin, catPlaying
	c.mu.Lock()
	c.toCaller, c.sessionToCaller = true, true
	c.mu.Unlock()
	if err := c.legs.Progress(resp.StatusCode, sdpBody(answer), fields.Headers...); err != nil {
		c.log.Info("CAT: the answer did not reach the caller", "error", err.Error())
		return true
	}
	// The caller has acknowledged the response: the tone can start
	// without losing its first frames.
	if err := t.media.start(t.callerOffer.Address(*t.callerOffer.AudioMedia())); err != nil {
		c.log.Warn("CAT: the tone did not start", "error", err.Error())
		return true
	}
	c.log.Info("CAT: the tone plays")
	return true
}

// relayDuringTone passes a provisional response of the callee on while
// the caller has the tone: without SDP, as the caller has its answer
// (4.5.5.3.7 c), and without the callee's P-Early-Media, which would
// change the authorization of the tone (RFC 5009).
func (c *call) relayDuringTone(out *sip.OutgoingCall, resp *sip.Message) {
	c.prackCallee(out, resp)
	fields := c.responseFields(resp, c.in.Request, true)
	fields.Remove("P-Early-Media")
	if err := c.legs.Progress(resp.StatusCode, sip.Body{}, fields.Headers...); err != nil {
		c.log.Info("provisional response not relayed", "status", resp.StatusCode, "error", err.Error())
	}
}

// offerTone is 4.5.5.3.7 a 1 on the callee's 180 after preconditions:
// the AS sends the caller an UPDATE whose offer points at the MRFP,
// with the content attribute and P-Early-Media sendonly (TS 24.628
// G.1), and with the preconditions of the session as the callee last
// stated them, which are met: the callee alerts only then (RFC 3312).
// The session is that of the early dialog whose SDP the caller got
// (sessionFrom), the one the caller has.
// The tone starts once the caller's 200 says that its preconditions
// are met too (a 1 ii). It reports whether the tone plays.
//
// SIMPLIFIED: TS 24.182 4.5.5.3.7 b 2 and b 3. The callee's 183
// with preconditions goes to the caller as it came, without the content
// attribute and P-Early-Media, which come with the UPDATE that brings
// the tone. A caller then has no early media authorized while it has
// no tone, and plays its own ringing tone if the tone never comes, as
// for a waiting call. The tone starts on the caller's 200 without a
// look at its preconditions, where 4.5.5.3.7 a 1 ii waits for them to
// be met in that 200 or a later UPDATE: the callee alerted, so both
// sides had their resources already.
func (c *call) offerTone() bool {
	t := c.cat
	c.mu.Lock()
	callee := c.calleeSDP[c.sessionFrom]
	c.mu.Unlock()
	last, err := sdp.Parse(callee)
	if err != nil || last.AudioMedia() == nil {
		c.log.Warn("CAT: no SDP of the callee to go on from", "error", fmt.Sprint(err))
		return false
	}
	conditions, err := last.AudioMedia().Preconditions()
	if err != nil {
		c.log.Warn("CAT: the callee's preconditions are not understood", "error", err.Error())
		return false
	}
	offer, err := sdp.AudioOffer(last.Origin.SessionID, t.media.address(), pcmu)
	if err != nil {
		return false
	}
	// The AS goes on in the session of the callee's SDP, which is the
	// one the caller has (RFC 3264 8).
	offer.Origin = last.Origin
	offer.Origin.Version++
	audio := offer.AudioMedia()
	audio.SetDirection("sendonly")
	if t.content != "" {
		audio.Attributes = append(audio.Attributes, sdp.Attribute{Name: "content", Value: t.content})
	}
	audio.SetPreconditions(conditions)
	resp, err := c.in.Update(sdpBody(offer), sip.PEarlyMedia{Directions: []string{sip.EarlyMediaSendonly}}.Header())
	if err != nil || resp.StatusCode >= 300 {
		c.log.Warn("CAT: the caller refused the tone", "status", statusOf(resp), "error", fmt.Sprint(err))
		return false
	}
	answer, err := sdp.Parse(resp.Body)
	if err != nil || answer.AudioMedia() == nil {
		c.log.Warn("CAT: no answer from the caller", "error", fmt.Sprint(err))
		return false
	}
	t.origin, t.phase = offer.Origin, catPlaying
	if err := t.media.start(answer.Address(*answer.AudioMedia())); err != nil {
		c.log.Warn("CAT: the tone did not start", "error", err.Error())
		return true
	}
	c.log.Info("CAT: the tone plays")
	return true
}

// prackCallee acknowledges a reliable response of the callee: the
// caller's PRACK is for the AS's own response.
func (c *call) prackCallee(out *sip.OutgoingCall, resp *sip.Message) {
	if !resp.Requires(sip.Option100rel) {
		return
	}
	if _, err := out.PRACK(resp, sip.Body{}); err != nil {
		c.log.Warn("PRACK not sent", "status", resp.StatusCode, "error", err.Error())
	}
}

// keepCalleeSDP notes the callee's SDP of a message that came from it
// in the dialog of out whose remote tag is tag.
func (c *call) keepCalleeSDP(tag string, body sip.Body) {
	if len(body.Content) == 0 {
		return
	}
	c.mu.Lock()
	c.calleeSDP[tag] = body.Content
	c.mu.Unlock()
}

// stopAlertingTone ends the dialog with the MRFC, if the call has one.
func (c *call) stopAlertingTone(why string) {
	t := c.cat
	if t == nil || t.phase == catStopped {
		return
	}
	t.phase = catStopped
	c.log.Info("CAT: the tone stops", "why", why)
	t.media.stop()
}

// answerAfterTone is 4.5.5.3.7 on the callee's 200 to the INVITE: the
// AS stops the tone and, if the caller had it, sends the caller an
// UPDATE whose offer is the callee's SDP, so that the media goes
// between the two (a 1 and a 2). The SDP is that of the dialog of the
// 200, which the INVITE may have forked into beyond the AS (RFC 3261
// 13.2.2.4), and the UPDATE is in the caller's early dialog, in the
// session that the AS gave the caller with the tone (offerCaller). It
// reports whether the caller had the tone, in which case the 200 goes
// to the caller without SDP.
func (c *call) answerAfterTone(resp *sip.Message) bool {
	t := c.cat
	if t == nil || t.phase == catStopped {
		return false
	}
	played := t.phase == catPlaying
	c.stopAlertingTone("the callee answered")
	if !played {
		return false
	}
	t.origin, _ = c.offerCaller(toTag(resp), t.origin)
	return true
}

// announceWaiting is TS 24.615 4.5.5.2.3 a 1: when the caller is to
// learn that its call waits, the AS has the MRF play it the configured
// announcement as early media, in place of a CAT that has not reached
// the caller (TS 24.182 4.6.11, second option). The announcement then
// goes as the tone would: in the 180, or in an UPDATE when the caller
// has the callee's SDP already, and until the callee answers.
func (c *call) announceWaiting() {
	if c.as.cfg.Announcement == "" || c.withhold || !c.callerLearnsOfWaiting() ||
		c.cat != nil && (c.cat.phase == catPlaying || c.cat.content == "") {
		return
	}
	offer, ok := c.earlyMediaOffer()
	if !ok {
		return
	}
	c.stopAlertingTone("the call waits (TS 24.182 4.6.11)")
	media, err := c.reserveMedia(c.as.cfg.Announcement, 0)
	if err != nil {
		c.log.Warn("CW: no announcement", "error", err.Error())
		return
	}
	phase := catReserved
	c.mu.Lock()
	if c.sessionToCaller {
		phase = catAwaitingAlerting
	}
	c.mu.Unlock()
	c.cat = &alertingTone{media: media, callerOffer: offer, phase: phase}
	c.actedOn("announcement")
	c.log.Info("CW: the caller hears that its call waits")
}

// waitingIn tells that a 180 says that the call waits (RFC 7462, TS
// 24.615 4.5.5.3.2).
func waitingIn(resp *sip.Message) bool {
	for _, row := range resp.GetAll("Alert-Info") {
		for _, v := range strings.Split(row, ",") {
			if strings.EqualFold(strings.TrimSpace(v), callWaiting) {
				return true
			}
		}
	}
	return false
}

// alertingToneName reads the CAT of the served user from the HSS
// (Sh-Pull, TS 29.328 6.1.1): the name of the tone when the user has
// CAT on, "" otherwise or when the HSS cannot tell. A user without the
// data has no CAT provisioned.
func (c *call) alertingToneName() string {
	ctx, cancel := context.WithTimeout(context.Background(), shTimeout)
	defer cancel()
	data, found, err := c.as.hss.RepositoryData(ctx, c.served.URI, CATServiceIndication)
	if err != nil {
		c.log.Warn("CAT data not read from the HSS", "error", err.Error())
		return ""
	}
	if !found || data.ServiceData == nil {
		return ""
	}
	t, err := ParseCustomizedAlertingTone(data.ServiceData.XML)
	if err != nil {
		c.log.Warn("CAT data not understood", "error", err.Error())
		return ""
	}
	if !t.Active {
		return ""
	}
	return t.Tone
}
