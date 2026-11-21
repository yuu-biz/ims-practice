package mrfc

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/netann"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// join is the conference service of RFC 4240 5: the dialog is a party
// of the mix of the conference until it ends.
func (m *MRFC) join(c *sip.IncomingCall, u sip.URI, conf netann.Conference, log *slog.Logger) {
	log = log.With("conference", conf.ID)
	var o offered
	hasOffer := len(c.Request.Body) > 0
	direction := "sendrecv"
	if hasOffer {
		var err error
		if o, err = readOffer(c.Request.Body); err != nil {
			m.refuseMedia(c, u, err, log)
			return
		}
		direction = sdp.AnswerDirection(o.direction)
	}
	media, t, err := m.attach(conf.ID, agw.TerminationRequest{Mode: agw.Mode(direction), Remote: o.remote}, log)
	if err != nil {
		log.Warn("no termination for the conference", "error", err.Error())
		m.refuse(c, u, &netann.Error{Status: 503, Reason: "Service Unavailable"}, log)
		return
	}
	l := newLeg(Processor{Name: media.MRFP, Control: m.byName[media.MRFP]}, media.Context, t, log)
	defer func() {
		if l.release() {
			// The last party has left, so the MRFP has deleted the
			// context, and the conference is over (RFC 4240 5).
			m.store.RemoveConference(conf.ID, media)
			log.Info("conference ended")
		}
	}()
	c.Handle(m.handle(l, sdp.AnswerDirection))

	var body sip.Body
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
		log.Info("party not joined", "error", err.Error())
		return
	}
	if !hasOffer {
		if err := l.takeAnswer(ack.Body); err != nil {
			log.Warn("no answer in the ACK", "error", err.Error())
			bye(c, log)
			return
		}
	}
	log.Info("party joined", "mrfp", media.MRFP, "context", media.Context, "termination", string(t.ID))
	<-c.Done()
	log.Info("party left")
}

// attachAttempts bounds how often attach starts over after another
// instance changed the conference between its reads and writes.
const attachAttempts = 3

// attach adds a termination for a party to the context of the
// conference id: the one that the Store names, or else a new one on the
// MRFP that choose gives, which then goes into the Store (TS 23.333
// 6.2.9.2.2, 6.2.9.2.5).
func (m *MRFC) attach(id string, req agw.TerminationRequest, log *slog.Logger) (ConferenceMedia, agw.Termination, error) {
	for range attachAttempts {
		if media, ok := m.store.Conference(id); ok {
			control, known := m.byName[media.MRFP]
			if !known {
				return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("mrfc: the conference is on MRFP %q, which this instance does not know", media.MRFP)
			}
			ctx, cancel := mp()
			t, err := control.AddTermination(ctx, media.Context, req)
			cancel()
			if errors.Is(err, agw.ErrNoContext) {
				// The last party left and the context went with it
				// before the Store forgot it.
				m.store.RemoveConference(id, media)
				continue
			}
			return media, t, err
		}
		p := m.choose(id)
		ctx, cancel := mp()
		cx, err := p.Control.Add(ctx, req)
		cancel()
		if err != nil {
			return ConferenceMedia{}, agw.Termination{}, err
		}
		var t agw.Termination
		for _, only := range cx.Terminations {
			t = *only
		}
		media := ConferenceMedia{MRFP: p.Name, Context: cx.ID}
		if _, created := m.store.CreateConference(id, media); created {
			log.Info("conference created", "mrfp", p.Name, "context", cx.ID)
			return media, t, nil
		}
		// Another instance created the conference at the same time: give
		// up this context and join that one.
		newLeg(p, cx.ID, t, log).release()
	}
	return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("mrfc: conference %q kept changing", id)
}
