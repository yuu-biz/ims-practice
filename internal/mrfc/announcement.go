package mrfc

import (
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/netann"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// announcementLimit is the longest that an announcement plays, whatever
// its repeat and duration: the local policy that RFC 4240 3 and 8 ask
// for, so that repeat=forever cannot hold an MRFP for good.
const announcementLimit = 10 * time.Minute

// notifyGrace is how long after the expected end of an announcement the
// MRFC waits for the Notify of the MRFP, before it ends the dialog as a
// timed session that has run out (TS 24.229 5.8.3.2.1).
const notifyGrace = 5 * time.Second

// announce is the announcement service of RFC 4240 3: the dialog plays
// the prompt once the ACK has come, and ends with the MRFC's BYE when
// the prompt has played.
func (m *MRFC) announce(c *sip.IncomingCall, u sip.URI, a netann.Announcement, log *slog.Logger) {
	name, provisioned := a.Provisioned()
	if !provisioned {
		// SIMPLIFIED: RFC 4240 3 and TS 24.229 10.3.2.2. The MRFC plays
		// announcements provisioned in its MRFPs only; it fetches no
		// prompt from a URL over Cr.
		m.refuse(c, u, &netann.Error{Status: 400, Reason: "Announcement content could not be retrieved"}, log)
		return
	}
	var o offered
	hasOffer := len(c.Request.Body) > 0
	if hasOffer {
		var err error
		if o, err = readOffer(c.Request.Body); err != nil {
			m.refuseMedia(c, u, err, log)
			return
		}
	}
	direction := "sendonly"
	if hasOffer {
		direction = announcementDirection(o.direction)
	}
	p, found, cx, refusal := m.reserve(c.Request.Get("Call-ID"), name, agw.TerminationRequest{Mode: agw.Mode(direction), Remote: o.remote}, log)
	if refusal != nil {
		m.refuse(c, u, refusal, log)
		return
	}
	var t agw.Termination
	for _, only := range cx.Terminations {
		t = *only
	}
	l := newLeg(p, cx.ID, t, log)
	defer l.release()
	c.Handle(m.handle(l, announcementDirection))

	var body sip.Body
	var err error
	if hasOffer {
		body, err = l.answer(o, direction)
	} else {
		body, err = l.offer(direction)
	}
	if err != nil {
		m.refuseMedia(c, u, err, log)
		return
	}
	ack, err := c.Accept(body, assertedIdentity(u))
	if err != nil {
		log.Info("announcement not set up", "error", err.Error())
		return
	}
	if !hasOffer {
		if err := l.takeAnswer(ack.Body); err != nil {
			log.Warn("no answer in the ACK", "error", err.Error())
			bye(c, log)
			return
		}
	}

	// TS 24.229 5.8.2.2.1: the ACK starts the announcement.
	events, completed := m.events()
	defer m.forget(events)
	play := &agw.Play{Announcement: name, Repeat: a.Repeat, DelayMS: int(a.Delay / time.Millisecond), DurationMS: int(limited(a.Duration) / time.Millisecond)}
	if err := l.modify(agw.TerminationRequest{Signals: &agw.Signals{Play: play}, Events: events}); err != nil {
		log.Warn("announcement not started", "error", err.Error())
		bye(c, log)
		return
	}
	log.Info("announcement started", "announcement", name, "repeat", a.Repeat)
	select {
	case <-completed:
		// TS 24.229 5.8.3.2.1: the MRFP reports that the announcement has
		// ended.
		log.Info("announcement completed")
		bye(c, log)
	case <-time.After(expectedLength(a, time.Duration(found.DurationMS)*time.Millisecond) + notifyGrace):
		log.Warn("no Notify for the end of the announcement")
		bye(c, log)
	case <-c.Done():
		// TS 24.229 5.8.3.1.1: a BYE from the AS stops the announcement,
		// which the Subtract of release does.
		log.Info("announcement stopped by the peer")
	}
}

// reserve finds the MRFP that plays the announcement name and the
// context for it: the first of the candidates for key that holds the
// announcement and gives a context (TS 29.333 5.17.2.2). It returns the
// refusal for the INVITE when none does, which is that of the last MRFP
// tried, so that one MRFP alone gives the same answer as before there
// were several.
//
// An MRFP that does not answer at all is passed over, and so is one
// that gives no context, as an MRFP out of service does. An MRFP that
// answers that it does not hold the announcement is not: every MRFP of
// the network is provisioned with the same announcements, so the name
// is wrong wherever it is asked for, and the MRFC says so with 404
// rather than ask every MRFP in turn.
func (m *MRFC) reserve(key, name string, req agw.TerminationRequest, log *slog.Logger) (Processor, agw.Announcement, agw.Context, *netann.Error) {
	var refusal *netann.Error
	for _, p := range m.candidates(key) {
		ctx, cancel := mp()
		found, ok, err := p.Announcements.Announcement(ctx, name)
		cancel()
		if err != nil {
			log.Warn("MRFP not reached", "mrfp", p.Name, "error", err.Error())
			refusal = &netann.Error{Status: 400, Reason: "Announcement content could not be retrieved"}
			continue
		}
		if !ok {
			return Processor{}, agw.Announcement{}, agw.Context{}, &netann.Error{Status: 404, Reason: "Announcement content not found"}
		}
		ctx, cancel = mp()
		cx, err := p.Control.Add(ctx, req)
		cancel()
		if err != nil {
			log.Warn("no termination in the MRFP", "mrfp", p.Name, "error", err.Error())
			refusal = &netann.Error{Status: 503, Reason: "Service Unavailable"}
			continue
		}
		return p, found, cx, nil
	}
	return Processor{}, agw.Announcement{}, agw.Context{}, refusal
}

// refuseMedia answers an INVITE whose SDP the MRFP cannot take.
func (m *MRFC) refuseMedia(c *sip.IncomingCall, u sip.URI, err error, log *slog.Logger) {
	log.Info("INVITE refused for its SDP", "error", err.Error())
	if err := c.Reject(488, m.warning(305, "Incompatible media format"), assertedIdentity(u)); err != nil {
		log.Warn("refusal not sent", "error", err.Error())
	}
}

// limited is the duration of an announcement under announcementLimit.
func limited(d time.Duration) time.Duration {
	if d == 0 || d > announcementLimit {
		return announcementLimit
	}
	return d
}

// expectedLength is how long the announcement plays, at most.
func expectedLength(a netann.Announcement, clip time.Duration) time.Duration {
	length := limited(a.Duration)
	if a.Repeat != netann.RepeatForever {
		plays := max(a.Repeat, 1)
		length = min(length, time.Duration(plays)*clip+time.Duration(plays-1)*a.Delay)
	}
	return length
}
