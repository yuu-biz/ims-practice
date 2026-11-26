package mmtel

import (
	"slices"
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
// 4.5.2). Its NOTIFYs go within the dialog, which a BYE of the session
// does not end while the subscription goes on (RFC 5057 5.5): end tells
// the SIP stack that the subscription is over, after its last NOTIFY
// (sip.CallDialog.AddSubscriptionUsage).
type inDialogSubscription struct {
	event   string
	expires time.Time
	version uint32
	end     func()
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
	sub := p.inDialog
	switch {
	case expires == 0:
		p.inDialog = nil
	case sub == nil:
		end, err := p.leg.AddSubscriptionUsage()
		if err != nil {
			// A BYE has ended the session: the participant has left, and
			// its dialog takes no new subscription. 603 refuses the new
			// usage without ending the dialog (RFC 5057 5.6).
			f.mu.Unlock()
			r.Reject(603)
			return
		}
		sub = &inDialogSubscription{event: req.Get("Event"), end: end}
		p.inDialog = sub
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
		if sub == nil {
			sub = &inDialogSubscription{event: req.Get("Event")}
		}
		f.mu.Lock()
		f.notifyInDialog(p, sub, sip.ReasonTimeout)
		f.mu.Unlock()
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
			delete(f.expiry, id)
			f.notifyInDialog(p, p.inDialog, sip.ReasonTimeout)
			f.mu.Unlock()
			f.log.Info("conference subscription within the dialog expired")
			return
		}
	}
	f.mu.Unlock()
}

// notify sends the full state of the conference to its subscribers:
// to the subscription with dialog only, when it is not nil, and to all
// of them otherwise. A subscriber who has left the conference gets its
// last NOTIFY with Subscription-State terminated (TS 24.147 5.3.3.3),
// with reason noresource: one outside a dialog, and one within the
// dialog of a participant that has left, which the BYE of the
// participant has not ended (RFC 5057 5.5). When the conference has
// ended, every subscription that is left gets its last NOTIFY
// (5.3.3.4), with reason noresource (RFC 4575 3.3). A subscription past
// its expiry ends with reason timeout (RFC 6665 4.2.1.4). A NOTIFY that
// times out, or whose response says that the subscriber has lost the
// subscription, removes it without a last NOTIFY (RFC 6665 4.2.2).
//
// notify builds the NOTIFYs and leaves them in the outbox of each
// subscription: it returns before any is sent, and so holds up no
// answer to a user who joins or leaves.
func (f *conference) notify(only *sip.DialogID) {
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
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
	ua := f.as.ua.Load()
	// last are the subscriptions that this round ends. Each goes from the
	// record with the NOTIFY that ends it, which is still to be sent,
	// after the ones before it.
	var last []sip.DialogID
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
		f.post(f.outboxOf(s.Dialog.ID), outgoing{send: f.sendOutside(ua, s.Dialog.ID, req)})
		if s.State == sip.SubscriptionTerminated {
			last = append(last, s.Dialog.ID)
		}
	}
	f.record.Subscriptions = slices.DeleteFunc(f.record.Subscriptions, func(s ConferenceSubscription) bool {
		return slices.Contains(last, s.Dialog.ID)
	})
	for _, id := range last {
		delete(f.outboxes, id)
	}
	for _, p := range f.parties {
		if p.inDialog == nil || (only != nil && p.dialog != *only) {
			continue
		}
		reason := ""
		switch {
		case f.ended || f.entry(p).Status == confinfo.Disconnected:
			reason = sip.ReasonNoResource
		case !now.Before(p.inDialog.expires):
			reason = sip.ReasonTimeout
		}
		f.notifyInDialog(p, p.inDialog, reason)
	}
	f.writeRecord()
}

// outbox holds NOTIFYs on their way within one dialog, in the order in
// which the focus built them, each as a function that sends it and
// waits for its final response. One goroutine at a time sends them,
// each once the one before has its final response, so that the
// subscriber gets them in the order of their CSeq numbers (RFC 3261
// 12.2.2) and of their versions (RFC 4575 4.6). Each subscription
// outside a dialog has an outbox of its own, and so has the dialog of
// each participant, for the subscription within it, which also takes the
// BYE that ends the dialog at the end of the conference (end): a
// subscriber that does not answer holds up its own NOTIFYs only, until
// Timer F. The fields are guarded by the mu of the conference.
type outbox struct {
	waiting []outgoing
	sending bool
}

// outgoing is one message in an outbox. send sends it, and reports
// whether it ended what the outbox is for, after which those behind it
// go nowhere: dropped, when it is not nil, is then called in the place
// of send, for what has to be done all the same.
type outgoing struct {
	send    func() (ended bool)
	dropped func()
}

// outboxOf is the outbox of the subscription in dialog id outside any
// call. f.mu is held.
func (f *conference) outboxOf(id sip.DialogID) *outbox {
	if f.outboxes == nil {
		f.outboxes = map[sip.DialogID]*outbox{}
	}
	o, ok := f.outboxes[id]
	if !ok {
		o = &outbox{}
		f.outboxes[id] = o
	}
	return o
}

// post puts m last in o, and starts the goroutine that sends what o
// holds unless it runs already. f.mu is held.
func (f *conference) post(o *outbox, m outgoing) {
	o.waiting = append(o.waiting, m)
	if !o.sending {
		o.sending = true
		go f.deliver(o)
	}
}

// deliver sends the NOTIFYs of o until none is left. Once one has ended
// its subscription, those behind it go nowhere.
func (f *conference) deliver(o *outbox) {
	for {
		f.mu.Lock()
		if len(o.waiting) == 0 {
			o.sending = false
			f.mu.Unlock()
			return
		}
		m := o.waiting[0]
		o.waiting = o.waiting[1:]
		f.mu.Unlock()
		if !m.send() {
			continue
		}
		f.mu.Lock()
		left := o.waiting
		o.waiting = nil
		f.mu.Unlock()
		for _, m := range left {
			if m.dropped != nil {
				m.dropped()
			}
		}
	}
}

// sendOutside is the sending of req, a NOTIFY of the subscription in
// dialog id outside any call. It reports whether the subscription ended
// with it: a NOTIFY that times out, or whose response says that the
// subscriber has lost the subscription, removes it (RFC 6665 4.2.2).
func (f *conference) sendOutside(ua *sip.UA, id sip.DialogID, req *sip.Message) func() bool {
	return func() bool {
		final, err := f.send(ua, req)
		if !sip.NotifyEndsSubscription(final, err) {
			return false
		}
		f.mu.Lock()
		// A NOTIFY that ended the subscription took it from the record
		// as it was built, which leaves nothing to remove here.
		i := f.subscriptionIndex(id)
		if i >= 0 {
			f.record.Subscriptions = slices.Delete(f.record.Subscriptions, i, i+1)
			f.writeRecord()
		}
		delete(f.outboxes, id)
		f.mu.Unlock()
		if i >= 0 {
			f.log.Info("conference subscription removed after a failed NOTIFY", "status", statusCode(final), "error", errText(err))
		}
		return true
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

// notifyInDialog leaves the full state in a NOTIFY within the dialog of
// participant p, for its subscription sub, in the outbox of that
// dialog. A reason ends the subscription with that reason; empty keeps
// it active. f.mu is held.
//
// A NOTIFY that times out, or gets 408 or 481, ends the subscription
// without a last NOTIFY (RFC 6665 4.2.2), and nothing else: the session
// of the participant shares the dialog, and goes on (RFC 5057 5.1,
// 5.2). Once the subscription has ended, by its last NOTIFY or by such
// a failure, the SIP stack no longer keeps the dialog for it: a dialog
// whose session a BYE has ended then ends too. So it does for a NOTIFY
// that never goes, behind the BYE that ends the conference (end).
func (f *conference) notifyInDialog(p *party, sub *inDialogSubscription, reason string) {
	v, err := confinfo.NextVersion(sub.version)
	if err != nil && reason == "" {
		reason = sip.ReasonNoResource
	}
	sub.version = v
	state := sip.SubscriptionState{State: sip.SubscriptionActive, Expires: max(time.Until(sub.expires).Round(time.Second), 0)}
	// ended is what ends the usage of the subscription in the dialog,
	// once the subscription is over; nil while it goes on.
	var ended func()
	if reason != "" {
		state = sip.SubscriptionState{State: sip.SubscriptionTerminated, Reason: reason}
		f.forgetInDialog(p, sub)
		ended = sub.end
	}
	body, err := f.record.info().FullState(v).Marshal()
	leg := p.leg
	if err != nil || leg == nil {
		if ended != nil {
			ended()
		}
		return
	}
	f.post(&p.outbox, outgoing{send: func() bool {
		final, err := leg.Send(sip.NOTIFY, sip.Body{Type: confinfo.ContentType, Content: body},
			sip.HeaderField{Name: "Event", Value: sub.event},
			sip.HeaderField{Name: "Subscription-State", Value: state.String()},
			// NOTIFY is a target refresh request (RFC 6665 3.2).
			sip.HeaderField{Name: "Contact", Value: f.contact.String()})
		if err != nil {
			f.log.Info("NOTIFY within the dialog failed", "error", err.Error())
		}
		if reason == "" && sip.NotifyEndsSubscription(final, err) {
			f.mu.Lock()
			f.forgetInDialog(p, sub)
			f.mu.Unlock()
			f.log.Info("conference subscription within the dialog removed", "status", statusOf(final))
			ended = sub.end
		}
		if ended != nil {
			ended()
		}
		// The BYE of end may wait behind this NOTIFY, and must go: a
		// NOTIFY that fails drops nothing from the outbox of the dialog.
		return false
	}, dropped: ended})
}

// forgetInDialog takes sub, a subscription within the dialog of p, out
// of the conference, with its expiry timer, unless a newer one has taken
// its place. f.mu is held.
func (f *conference) forgetInDialog(p *party, sub *inDialogSubscription) {
	if p.inDialog != sub {
		return
	}
	p.inDialog = nil
	if stop, ok := f.expiry[p.dialog]; ok {
		stop()
		delete(f.expiry, p.dialog)
	}
}
