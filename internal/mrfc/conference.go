package mrfc

import (
	"context"
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
	// sip.IncomingCall carries no context to bound the work for the
	// INVITE. The Store bounds each of its operations itself: an adapter
	// on a store outside the process gives each one a time limit.
	ctx := context.Background()
	req := o.request(direction)
	media, t, err := m.attach(ctx, conf.ID, req, log)
	if errors.Is(err, errStore) {
		log.Warn("conference store failed", "error", err.Error())
		m.refuse(c, u, &netann.Error{Status: 500, Reason: "Server Internal Error"}, log)
		return
	}
	if err != nil {
		log.Warn("no termination for the conference", "error", err.Error())
		m.refuse(c, u, &netann.Error{Status: 503, Reason: "Service Unavailable"}, log)
		return
	}
	l := newLeg(Processor{Name: media.MRFP, Control: m.byName[media.MRFP]}, media.Context, t, req.LocalMedia.Codecs[0], log)
	defer func() {
		if l.release() {
			// The last party has left, so the MRFP has deleted the
			// context, and the conference is over (RFC 4240 5).
			if err := m.store.RemoveConference(ctx, conf.ID, media); err != nil {
				// No request waits for an answer here. The item stays
				// until a later party finds that the context is gone
				// (attach).
				log.Warn("ended conference left in the store", "error", err.Error())
			}
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

// errStore marks a failure of the Store. The MRFC then cannot tell
// which context holds the conference, and refuses the party with 500
// (Server Internal Error) rather than risk a mix apart from the other
// parties. 503 (Service Unavailable) stays for an MRFP that cannot take
// the party.
var errStore = errors.New("mrfc: the conference store failed")

// attachAttempts bounds how often attach starts over after another
// instance changed the conference between its reads and writes.
const attachAttempts = 3

// attach adds a termination for a party to the context of the
// conference id: the one that the Store names, or else a new one on the
// MRFP that choose gives, which then goes into the Store (TS 23.333
// 6.2.9.2.2, 6.2.9.2.5).
func (m *MRFC) attach(ctx context.Context, id string, req agw.TerminationRequest, log *slog.Logger) (ConferenceMedia, agw.Termination, error) {
	for range attachAttempts {
		media, ok, err := m.store.Conference(ctx, id)
		if err != nil {
			return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("%w: %w", errStore, err)
		}
		if ok {
			control, known := m.byName[media.MRFP]
			if !known {
				return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("mrfc: the conference is on MRFP %q, which this instance does not know", media.MRFP)
			}
			mpCtx, cancel := mp()
			t, err := control.AddTermination(mpCtx, media.Context, req)
			cancel()
			if errors.Is(err, agw.ErrNoContext) {
				// The last party left and the context went with it
				// before the Store forgot it.
				if err := m.store.RemoveConference(ctx, id, media); err != nil {
					return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("%w: %w", errStore, err)
				}
				continue
			}
			return media, t, err
		}
		p, cx, err := m.create(id, req, log)
		if err != nil {
			return ConferenceMedia{}, agw.Termination{}, err
		}
		var t agw.Termination
		for _, only := range cx.Terminations {
			t = *only
		}
		media = ConferenceMedia{MRFP: p.Name, Context: cx.ID}
		_, created, err := m.store.CreateConference(ctx, id, media)
		if err != nil {
			// The store may hold this context or not. Give it up: a
			// party that the store sends to it later finds it gone, and
			// the conference starts again (agw.ErrNoContext above).
			newLeg(p, cx.ID, t, req.LocalMedia.Codecs[0], log).release()
			return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("%w: %w", errStore, err)
		}
		if created {
			log.Info("conference created", "mrfp", p.Name, "context", cx.ID)
			return media, t, nil
		}
		// Another instance created the conference at the same time: give
		// up this context and join that one.
		newLeg(p, cx.ID, t, req.LocalMedia.Codecs[0], log).release()
	}
	return ConferenceMedia{}, agw.Termination{}, fmt.Errorf("mrfc: conference %q kept changing", id)
}

// create makes the context of a new conference on the first of the
// candidates for id that gives one (TS 23.333 6.2.9.2.2). An MRFP that
// does not answer, that has no port left, or that an operator has taken
// out of service gives none, and the next one is asked. The error is
// that of the last MRFP tried.
//
// Only a conference that has no context yet is placed this way. A party
// of one that exists goes to the MRFP that the Store names, since that
// is where the mix is (attach).
func (m *MRFC) create(id string, req agw.TerminationRequest, log *slog.Logger) (Processor, agw.Context, error) {
	var last error
	for _, p := range m.candidates(id) {
		mpCtx, cancel := mp()
		cx, err := p.Control.Add(mpCtx, req)
		cancel()
		if err == nil {
			return p, cx, nil
		}
		log.Warn("the MRFP gave no context for the conference", "mrfp", p.Name, "error", err.Error())
		last = err
	}
	return Processor{}, agw.Context{}, last
}
