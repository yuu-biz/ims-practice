package mmtel

import (
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// maxAnnouncement is the longest that the AS keeps a call it rejects
// waiting for the end of its announcement, should the MRFC never end
// the dialog.
const maxAnnouncement = time.Minute

// reject rejects the caller's INVITE for a service with code. When the
// configuration names an announcement, the caller first hears it in
// early media, and the final response then carries a Reason with the
// cause of the rejection (TS 24.628 4.2.4, G.1). Communication barring
// allows this before its 603 or 433 (TS 24.611 4.5.2.4.1, 4.5.2.6.1,
// 4.5.2.6.2).
func (c *call) reject(code int, headers ...sip.HeaderField) {
	c.noLeg()
	if c.announce() {
		headers = append(headers, sip.NewReason(sip.ReasonSIP, code, sip.ReasonPhrase(code)).Header())
	}
	if err := c.in.Reject(code, headers...); err != nil {
		c.log.Info("rejection not sent", "status", code, "error", err.Error())
	}
}

// announce plays the configured announcement to the caller as early
// media, in the gateway model of TS 24.628 Annex G: the MRFC's dialog
// is set up first, the caller gets a reliable 183 whose answer points
// at the MRFP with P-Early-Media sendonly, and the ACK to the MRFC
// starts the play once the caller has acknowledged the 183. It returns
// when the MRFC ends the dialog after the play (TS 24.229 5.8.3.2.1),
// or when the caller gives up, and tells whether the caller got the
// announcement.
func (c *call) announce() bool {
	name := c.as.cfg.Announcement
	if name == "" {
		return false
	}
	offer, ok := c.earlyMediaOffer()
	if !ok {
		c.log.Info("no announcement: the caller takes no early media of the MRF", "announcement", name)
		return false
	}
	media, err := c.reserveMedia(name, 0)
	if err != nil {
		c.log.Warn("no announcement", "announcement", name, "error", err.Error())
		return false
	}
	defer media.stop()
	answer, err := media.answerTo(offer, newOrigin(media.address().Addr()), "")
	if err != nil {
		c.log.Warn("no announcement", "announcement", name, "error", err.Error())
		return false
	}
	c.mu.Lock()
	c.toCaller, c.sessionToCaller = true, true
	c.mu.Unlock()
	fields := []sip.HeaderField{sip.PEarlyMedia{Directions: []string{sip.EarlyMediaSendonly}}.Header()}
	if pcv := responseChargingVector(c.in.Request, c.as.cfg.HomeDomain); pcv != "" {
		fields = append(fields, sip.HeaderField{Name: "P-Charging-Vector", Value: pcv})
	}
	if err := c.in.Progress(183, sdpBody(answer), fields...); err != nil {
		c.log.Info("announcement not offered to the caller", "error", err.Error())
		return false
	}
	if err := media.start(offer.Address(*offer.AudioMedia())); err != nil {
		c.log.Warn("announcement not started", "error", err.Error())
		return false
	}
	c.actedOn("announcement")
	select {
	case <-media.dialog.Done():
	case <-c.in.Done():
	case <-time.After(maxAnnouncement):
		c.log.Warn("the announcement did not end", "announcement", name)
	}
	return true
}

// earlyMediaOffer is the caller's SDP offer when the MRF can play early
// media to it: the INVITE has an offer with PCMU, and the caller
// takes reliable provisional responses, which the gateway model needs
// (TS 24.628 G.1). A caller that requires preconditions gets none: the
// MRFC writes no precondition attributes.
func (c *call) earlyMediaOffer() (sdp.Session, bool) {
	req := c.in.Request
	if !req.Supports(sip.Option100rel) && !req.Requires(sip.Option100rel) || req.Requires(sip.OptionPrecondition) {
		return sdp.Session{}, false
	}
	offer, err := sdp.Parse(req.Body)
	if err != nil || !hasPCMU(offer) {
		return sdp.Session{}, false
	}
	return offer, true
}
