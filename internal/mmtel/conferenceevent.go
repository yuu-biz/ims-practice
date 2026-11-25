package mmtel

import (
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the conference notification service of the AS (TS
// 24.147 5.3.3): the notifier of the conference event package (RFC
// 4575). GSMA IR.92 2.3.3 has the UE subscribe outside the dialog of its
// conference session, and the network take a SUBSCRIBE within that
// dialog too, from UEs of older versions. Every NOTIFY carries the full
// state of the conference (confinfo.Conference.FullState).
//
// SIMPLIFIED: RFC 4575 3.9. The focus sends each change at once, as
// IR.92 2.3.3 asks (within 5 s), and does not hold NOTIFYs back to one
// every 5 seconds per subscriber, which RFC 4575 recommends. A
// conference of a few users changes seldom.

// conferenceExpires is the longest subscription that the focus grants,
// and the duration of one that asks for none: the default of RFC 4575
// 3.3.
const conferenceExpires = time.Hour

// inDialogSubscription is a subscription within the dialog of a
// participant's conference session, which shares that dialog (RFC 6665
// 4.5.2). Its NOTIFYs go within the dialog, so it lives with the dialog.
type inDialogSubscription struct {
	event   string
	expires time.Time
	version uint32
}

// subscribe is the AS's UAConfig.OnSubscribe: a SUBSCRIBE outside the
// dialog of any call, to the conference URI (TS 24.147 5.3.3.2), or one
// within the dialog of such a subscription.
func (as *AS) subscribe(st *sip.ServerTransaction) {
	req := st.Request
	f := as.conferenceAt(req.RequestURI)
	switch {
	case f == nil:
		// 5.3.3.2 step 1: no such conference here.
		st.Respond(sip.NewResponse(req, 404, sip.NewTag()))
	case sip.EventType(req.Get("Event")) != confinfo.EventPackage:
		// RFC 6665 4.2.1.1: the focus is the notifier of one package.
		st.Respond(sip.NewResponse(req, 489, sip.NewTag()))
	default:
		f.subscribe(st)
	}
}

// subscribe establishes, refreshes or ends a subscription outside the
// dialog of a conference session (TS 24.147 5.3.3.2 step 3, RFC 6665
// 4.2.1), and sends its NOTIFY at once (4.2.1.2).
//
// SIMPLIFIED: TS 24.147 5.3.3.2 step 2 and RFC 4575 3.5. The focus takes
// every subscriber whose identity the network asserts: it has no
// conference policy that would allow only the participants.
func (f *conference) subscribe(st *sip.ServerTransaction) {
	req := st.Request
	expires, err := sip.SubscribeExpires(req, conferenceExpires)
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	expires = min(expires, conferenceExpires)
	now := time.Now()
	f.mu.Lock()
	if f.ended {
		f.mu.Unlock()
		st.Respond(sip.NewResponse(req, 404, sip.NewTag()))
		return
	}
	var id sip.DialogID
	if sip.ReceivedDialogID(req).LocalTag == "" {
		ok, sub, err := sip.AcceptSubscription(req, expires, f.record.URI, now)
		if err != nil {
			f.mu.Unlock()
			st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
			return
		}
		// The record holds the subscription before the 200 goes, so that
		// the 200 is never sent for a subscription that no instance
		// would find after this one stops.
		f.record.Subscriptions = append(f.record.Subscriptions, ConferenceSubscription{NotifierSubscription: sub, Subscriber: assertedUser(req, "")})
		if err := f.putRecord(); err != nil {
			f.record.Subscriptions = f.record.Subscriptions[:len(f.record.Subscriptions)-1]
			f.mu.Unlock()
			f.subscriptionNotStored(st, err)
			return
		}
		id = sub.Dialog.ID
		if sub.State == sip.SubscriptionActive {
			f.watchExpiry(id, sub.Expires)
		}
		f.mu.Unlock()
		st.Respond(ok)
		f.log.Info("subscribed to the conference", "subscriber", assertedUser(req, ""), "expires", int(expires/time.Second))
	} else {
		id = sip.ReceivedDialogID(req)
		i := f.subscriptionIndex(id)
		if i < 0 {
			f.mu.Unlock()
			st.Respond(sip.NewResponse(req, 481, ""))
			return
		}
		s := &f.record.Subscriptions[i]
		before := *s
		ok, err := s.ReceiveSubscribe(req, expires, now)
		if err == nil {
			// As for a new subscription, the refresh or the end of it is
			// in the record before the 200 goes.
			if err = f.putRecord(); err != nil {
				*s = before
				f.mu.Unlock()
				f.subscriptionNotStored(st, err)
				return
			}
			if s.State == sip.SubscriptionActive {
				f.watchExpiry(id, s.Expires)
			}
		}
		f.mu.Unlock()
		switch {
		case err == sip.ErrBadEvent:
			st.Respond(sip.NewResponse(req, 489, ""))
			return
		case err != nil:
			st.Respond(sip.NewResponse(req, 500, ""))
			return
		}
		st.Respond(ok)
	}
	f.notify(&id)
}

// subscriptionNotStored answers a SUBSCRIBE whose subscription the
// record could not take with 500 (RFC 3261 21.5.1): the subscription
// stays as it was, and the subscriber may try again.
func (f *conference) subscriptionNotStored(st *sip.ServerTransaction, err error) {
	f.log.Warn("conference subscription not stored", "error", err.Error())
	st.Respond(sip.NewResponse(st.Request, 500, ""))
}

// subscriptionIndex is the index of the subscription with dialog id in
// the record, -1 when there is none. f.mu is held.
func (f *conference) subscriptionIndex(id sip.DialogID) int {
	for i, s := range f.record.Subscriptions {
		if s.Dialog.ID == id {
			return i
		}
	}
	return -1
}

// subscribeInDialog takes a SUBSCRIBE within the dialog of a
// participant's conference session (GSMA IR.92 2.3.3, TS 24.147
// 5.3.1.2), whose NOTIFYs then go within that dialog.
func (f *conference) subscribeInDialog(r *sip.InDialogRequest, p *party) {
	req := r.Request
	if sip.EventType(req.Get("Event")) != confinfo.EventPackage {
		r.Reject(489)
		return
	}
	expires, err := sip.SubscribeExpires(req, conferenceExpires)
	if err != nil {
		r.Reject(400)
		return
	}
	expires = min(expires, conferenceExpires)
	f.mu.Lock()
	if f.ended {
		f.mu.Unlock()
		r.Reject(481)
		return
	}
	if expires == 0 {
		p.inDialog = nil
	} else if p.inDialog == nil {
		p.inDialog = &inDialogSubscription{event: req.Get("Event")}
	}
	if p.inDialog != nil {
		p.inDialog.expires = time.Now().Add(expires)
		f.watchExpiry(p.dialog, p.inDialog.expires)
	}
	f.mu.Unlock()
	// RFC 6665 3.1: SUBSCRIBE is a target refresh request, so its 2xx
	// carries Contact; and Expires, the duration granted (4.2.1.1).
	r.Accept(sip.Body{}, sip.HeaderField{Name: "Contact", Value: f.contact.String()},
		sip.HeaderField{Name: "Expires", Value: strconv.Itoa(int(expires / time.Second))})
	if expires == 0 {
		// RFC 6665 4.2.1.4: an unsubscribe ends like an expiry.
		f.notifyInDialog(p, &inDialogSubscription{event: req.Get("Event")}, sip.ReasonTimeout)
		return
	}
	f.notify(&p.dialog)
}

// watchExpiry starts the timer that ends the subscription in dialog id
// when it expires at at (RFC 6665 4.2.1.4), in place of the timer it had.
// A subscription within the dialog of a participant has the ID of that
// dialog. f.mu is held.
//
// The timer runs in this instance, as the dialogs of the conference do:
// only the instance that hosts a conference sends its NOTIFYs. When it
// fires, expired checks the subscription again, so that a refresh in
// between is never undone.
func (f *conference) watchExpiry(id sip.DialogID, at time.Time) {
	if f.expiry == nil {
		f.expiry = map[sip.DialogID]func() bool{}
	}
	if stop, ok := f.expiry[id]; ok {
		stop()
	}
	f.expiry[id] = f.as.cfg.AfterFunc(time.Until(at), func() { f.expired(id, at) })
}

// expired ends the subscription in dialog id if it still expires at at:
// the notifier removes a subscription that was not refreshed, and its
// last NOTIFY says terminated with reason timeout (RFC 6665 4.2.1.4).
func (f *conference) expired(id sip.DialogID, at time.Time) {
	f.mu.Lock()
	if i := f.subscriptionIndex(id); i >= 0 {
		s := &f.record.Subscriptions[i]
		if s.State != sip.SubscriptionActive || !s.Expires.Equal(at) {
			f.mu.Unlock()
			return
		}
		s.Terminate(sip.ReasonTimeout)
		delete(f.expiry, id)
		// s points into the record, whose subscriptions move up when one
		// before them is removed: past f.mu, s may be another one.
		subscriber := s.Subscriber
		f.mu.Unlock()
		f.log.Info("conference subscription expired", "subscriber", subscriber)
		f.notify(&id)
		return
	}
	for _, p := range f.parties {
		if p.dialog == id && p.inDialog != nil && p.inDialog.expires.Equal(at) {
			sub := p.inDialog
			delete(f.expiry, id)
			f.mu.Unlock()
			f.log.Info("conference subscription within the dialog expired")
			f.notifyInDialog(p, sub, sip.ReasonTimeout)
			return
		}
	}
	f.mu.Unlock()
}

// notify sends the full state of the conference to its subscribers:
// to the subscription with dialog only, when it is not nil, and to all
// of them otherwise. A subscriber who has left the conference gets its
// last NOTIFY with Subscription-State terminated (TS 24.147 5.3.3.3),
// and so does everyone when the conference has ended (5.3.3.4), with
// reason noresource (RFC 4575 3.3). A subscription past its expiry ends
// with reason timeout (RFC 6665 4.2.1.4). A NOTIFY that times out, or
// whose response says that the subscriber has lost the subscription,
// removes it without a last NOTIFY (RFC 6665 4.2.2).
func (f *conference) notify(only *sip.DialogID) {
	f.sending.Lock()
	defer f.sending.Unlock()
	now := time.Now()
	f.mu.Lock()
	info := f.record.info()
	gone := map[string]bool{}
	for _, p := range f.record.Participants {
		// A participant that asked for privacy may have come in with no
		// identity at all (TS 24.229 5.7.1.4 a). The focus cannot tell
		// which subscriber that is, and ends no subscription for it.
		if p.Status == confinfo.Disconnected && p.User != "" {
			gone[ims.PublicIdentityKey(p.User)] = true
		}
	}
	type outgoing struct {
		dialog sip.DialogID
		req    *sip.Message
		// last is a NOTIFY that ends the subscription.
		last bool
	}
	var out []outgoing
	for i := range f.record.Subscriptions {
		s := &f.record.Subscriptions[i]
		if only != nil && s.Dialog.ID != *only {
			continue
		}
		if f.ended || gone[ims.PublicIdentityKey(s.Subscriber)] {
			s.Terminate(sip.ReasonNoResource)
		}
		v, err := confinfo.NextVersion(s.Version)
		if err != nil {
			s.Terminate(sip.ReasonNoResource)
		}
		s.Version = v
		body, err := info.FullState(v).Marshal()
		if err != nil {
			f.log.Warn("conference document not built", "error", err.Error())
			continue
		}
		req := s.NewNotify(sip.Body{Type: confinfo.ContentType, Content: body}, now)
		out = append(out, outgoing{s.Dialog.ID, req, s.State == sip.SubscriptionTerminated})
	}
	type inDialog struct {
		p      *party
		sub    *inDialogSubscription
		reason string
	}
	var within []inDialog
	for _, p := range f.parties {
		if p.inDialog == nil || (only != nil && p.dialog != *only) {
			continue
		}
		// A participant who has left has no dialog to send in.
		if gone[ims.PublicIdentityKey(f.userOf(p))] {
			continue
		}
		w := inDialog{p: p, sub: p.inDialog}
		switch {
		case f.ended:
			w.reason = sip.ReasonNoResource
		case !now.Before(p.inDialog.expires):
			w.reason = sip.ReasonTimeout
		}
		within = append(within, w)
	}
	f.writeRecord()
	f.mu.Unlock()

	ua := f.as.ua.Load()
	for _, o := range out {
		final, err := f.send(ua, o.req)
		failed := sip.NotifyEndsSubscription(final, err)
		f.mu.Lock()
		if i := f.subscriptionIndex(o.dialog); i >= 0 {
			// The subscription goes with the NOTIFY that ended it, not
			// with one that was on its way when it ended: that one
			// still has to be sent.
			if o.last || failed {
				f.record.Subscriptions = append(f.record.Subscriptions[:i], f.record.Subscriptions[i+1:]...)
				f.writeRecord()
			}
		}
		f.mu.Unlock()
		if failed {
			f.log.Info("conference subscription removed after a failed NOTIFY", "status", statusCode(final), "error", errText(err))
		}
	}
	for _, w := range within {
		f.notifyInDialog(w.p, w.sub, w.reason)
	}
}

// send sends a NOTIFY outside any call and returns its final response,
// or nil and why none came.
func (f *conference) send(ua *sip.UA, req *sip.Message) (*sip.Message, error) {
	if ua == nil {
		return nil, errNotListening
	}
	ct, err := ua.SendRequest(req)
	if err != nil {
		f.log.Warn("NOTIFY not sent", "error", err.Error())
		return nil, err
	}
	var final *sip.Message
	for resp := range ct.Responses() {
		if resp.StatusCode >= 200 {
			final = resp
		}
	}
	if final == nil {
		return nil, ct.Err()
	}
	return final, nil
}

// notifyInDialog sends the full state in a NOTIFY within the dialog of
// participant p, for its subscription sub. A reason ends the
// subscription with that reason; empty keeps it active.
func (f *conference) notifyInDialog(p *party, sub *inDialogSubscription, reason string) {
	f.mu.Lock()
	v, err := confinfo.NextVersion(sub.version)
	if err != nil && reason == "" {
		reason = sip.ReasonNoResource
	}
	sub.version = v
	info := f.record.info()
	leg := p.leg
	state := sip.SubscriptionState{State: sip.SubscriptionActive, Expires: max(time.Until(sub.expires).Round(time.Second), 0)}
	if reason != "" {
		state = sip.SubscriptionState{State: sip.SubscriptionTerminated, Reason: reason}
		if p.inDialog == sub {
			p.inDialog = nil
		}
	}
	f.mu.Unlock()
	body, err := info.FullState(v).Marshal()
	if err != nil || leg == nil {
		return
	}
	_, err = leg.Send(sip.NOTIFY, sip.Body{Type: confinfo.ContentType, Content: body},
		sip.HeaderField{Name: "Event", Value: sub.event},
		sip.HeaderField{Name: "Subscription-State", Value: state.String()},
		// NOTIFY is a target refresh request (RFC 6665 3.2).
		sip.HeaderField{Name: "Contact", Value: f.contact.String()})
	if err != nil {
		f.log.Info("NOTIFY within the dialog failed", "error", err.Error())
	}
}

// userOf is the user of party p in the record. f.mu is held.
func (f *conference) userOf(p *party) string {
	if e := f.entry(p); e != nil {
		return e.User
	}
	return ""
}
