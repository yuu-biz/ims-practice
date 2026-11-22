package pcscf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the P-CSCF as a subscriber of the reg event package
// (TS 24.229 5.2.3): it learns from the S-CSCF when a registration ends,
// even one that the UE did not end itself, and then forgets the UE
// (5.2.5.2).
//
// The subscription is in the Store, and the Contact of the SUBSCRIBE is
// the host and port that every instance of the role answers at: a
// P-CSCF made again at that address takes the NOTIFYs of a subscription
// that an instance before it sent the SUBSCRIBE for, and refreshes it at
// the next REGISTER of the user. That is why the P-CSCF does not use
// sip.Subscriber, which keeps its subscriptions and their timers in the
// memory of one process.

const regEventPackage = "reg"

// contactURI is the P-CSCF's URI in the From and Contact of its own
// SUBSCRIBEs, where the S-CSCF sends the NOTIFYs.
func (p *PCSCF) contactURI() string { return fmt.Sprintf("sip:%s:%d", p.cfg.Host, p.cfg.Port) }

func (p *PCSCF) locate(u sip.URI) (sip.Destination, error) {
	return ims.Destination(context.Background(), p.locator, u)
}

// watchRegEvent subscribes to the registration state of the user that a
// REGISTER registered for registered, unless that user already has a
// subscription. The subscription asks for the duration of the
// registration and ims.RegEventMargin more, so that it does not end
// first (5.2.3 item e).
//
// A reregistration sends nothing: the subscription of the user holds,
// and the instance that took its last NOTIFY refreshes it at the time
// 5.2.3 gives (timeRegEventRefresh), which is counted from the duration
// the subscription has and not from the REGISTERs of the UE.
//
// SIMPLIFIED: TS 24.229 5.2.3. The timer for that refresh runs in the
// process that took the subscription's last NOTIFY, so a P-CSCF made
// again at the same address holds none for a subscription an earlier
// process was watching: it finds the subscription in the Store and
// leaves it. A NOTIFY of that subscription hands it over, timer and all,
// but the S-CSCF sends one only when the registration changes, so
// usually none comes and the subscription runs out at both ends. The
// user's next REGISTER after that starts a new one, and in between the
// P-CSCF hears of no change. Carrying the refresh over without a NOTIFY
// would need the timers themselves outside the process, and only the
// subscription is kept there.
//
// Only the instance that a REGISTER of a user reaches writes the
// subscription of that user, as with the IP association, so the P-CSCF
// takes no lock for it. The REGISTER has been answered when this runs.
// When the Store fails, no SUBSCRIBE goes out and the next REGISTER
// tries again.
func (p *PCSCF) watchRegEvent(a Association, registered time.Duration, log *slog.Logger) {
	if len(a.PublicIdentities) == 0 {
		log.Warn("no public identity to subscribe to the reg event package of")
		return
	}
	ctx := context.Background()
	sub, ok, err := p.store.RegSubscription(ctx, a.PrivateIdentity)
	if err != nil {
		log.Warn("reg event subscription not read: the store failed; the next REGISTER subscribes", "error", err.Error())
		return
	}
	switch {
	case ok && (sub.Dialog.ID.RemoteTag != "" || time.Since(sub.Subscribed) < p.notifyWait):
		// One subscription per private identity (5.2.3 NOTE). One that is
		// still within Timer N counts as well: it may yet get its first
		// NOTIFY, and the instance that sent its SUBSCRIBE ends it if it
		// does not.
		return
	case ok:
		// A SUBSCRIBE that no NOTIFY followed within Timer N, whose
		// instance is gone: no subscription came about (RFC 6665 4.1.2.1),
		// and nothing is left to end it. Take it away and subscribe again.
		if err := p.store.DeleteRegSubscription(ctx, a.PrivateIdentity); err != nil {
			log.Warn("reg event subscription without a NOTIFY not removed: the store failed", "error", err.Error())
			return
		}
		log.Info("reg event subscription without a NOTIFY removed", "subscription_call_id", sub.Dialog.ID.CallID)
	}
	p.subscribeRegEvent(a, registered+ims.RegEventMargin, log)
}

// subscribeRegEvent is TS 24.229 5.2.3 after the 200 to the first
// initial REGISTER of a private identity: a SUBSCRIBE to the user's
// default public identity, which goes to the I-CSCF of the home network
// (item 4). The subscription is made once per private identity (NOTE);
// a new one follows only once the old one has ended.
func (p *PCSCF) subscribeRegEvent(a Association, expires time.Duration, log *slog.Logger) {
	stack, err := p.stack.Get()
	if err != nil {
		return
	}
	// Item f: the URI that the P-CSCF put in Path.
	path, err := p.pathURIOf(a)
	if err != nil {
		log.Warn("reg event SUBSCRIBE not sent: the store failed; the next REGISTER subscribes", "error", err.Error())
		return
	}
	defaultID := a.PublicIdentities[0]
	// Items a to e.
	m := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: defaultID}
	m.Add("Max-Forwards", "70")
	m.Add("From", sip.NameAddr{URI: p.contactURI()}.String()+";tag="+sip.NewTag())
	m.Add("To", sip.NameAddr{URI: defaultID}.String())
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 SUBSCRIBE")
	m.Add("P-Asserted-Identity", path)
	// Item g.
	m.Add("P-Charging-Vector", sip.ChargingVector{ICID: sip.NewCallID(), OrigIOI: p.cfg.VisitedNetworkID}.String())
	// RFC 6665 4.1.2.1: where the NOTIFYs go.
	m.Add("Contact", sip.NameAddr{URI: p.contactURI()}.String())
	m.Add("Event", regEventPackage)
	m.Add("Expires", strconv.Itoa(int(expires/time.Second)))

	d, err := sip.NewSubscriberDialog(m)
	if err != nil {
		log.Warn("reg event SUBSCRIBE not built", "error", err.Error())
		return
	}
	dst, err := p.regEventHop(m)
	if err != nil {
		log.Warn("reg event SUBSCRIBE not sent", "error", err.Error())
		return
	}
	ctx, privateID := context.Background(), a.PrivateIdentity
	// In the Store before the SUBSCRIBE goes out: the first NOTIFY may
	// come before the 200, and to another instance (RFC 6665 4.1.2.4).
	// Only the instance whose write wins subscribes, so that one user
	// gets one subscription however many REGISTERs are answered at once.
	now := time.Now()
	created, err := p.store.CreateRegSubscription(ctx, RegSubscription{PrivateIdentity: privateID, PublicIdentity: defaultID,
		Dialog: d, Requested: expires, Subscribed: now, Expires: now.Add(expires)})
	if err != nil {
		log.Warn("reg event SUBSCRIBE not sent: the store failed; the next REGISTER subscribes", "error", err.Error())
		return
	}
	if !created {
		log.Info("reg event SUBSCRIBE not sent: the user was subscribed to first", "public_identity", defaultID)
		return
	}
	log.Info("reg event subscribing", "public_identity", defaultID)
	p.timeRegEventNotify(privateID, d, log)
	go func() {
		final, err := p.sendRegEvent(stack, m, dst)
		if err == nil && final.StatusCode < 300 {
			p.grantRegEvent(privateID, d.ID, final, log)
			return
		}
		// RFC 6665 4.1.2.1: the SUBSCRIBE failed, and no subscription was
		// made. One that a NOTIFY has already confirmed stays, as does one
		// the Store could not be told about; the next REGISTER replaces
		// that one.
		log.Warn("reg event SUBSCRIBE failed", "status", statusOf(final), "error", errorText(err))
		current, ok, err := p.store.RegSubscription(ctx, privateID)
		if err == nil && ok && current.Dialog.ID == d.ID {
			err = p.store.DeleteRegSubscription(ctx, privateID)
		}
		if err != nil {
			log.Warn("failed reg event subscription not removed: the store failed", "error", err.Error())
		}
	}()
}

// refreshRegEvent sends a SUBSCRIBE within the dialog of sub, a
// subscription as the Store holds it and whose refresh has fallen due,
// with the duration its initial SUBSCRIBE asked for (RFC 6665 4.1.2.2).
// It goes to the S-CSCF, the remote target of the dialog, not through
// the I-CSCF.
//
// Taking the next CSeq number of the dialog is a compare and swap on the
// stored subscription, so that of several instances whose refresh falls
// due together exactly one sends one, and no two SUBSCRIBEs go out with
// the same CSeq. The expiry is not touched: a refresh that goes
// unanswered must leave the subscription to run out when the notifier
// drops it, and only the 2xx or a NOTIFY says a new one.
//
// SIMPLIFIED: TS 24.229 5.2.3, last paragraph. When a refresh fails with
// 481, the P-CSCF should start a new subscription at once. Here the
// subscription ends, and the next REGISTER of the user starts the new
// one.
func (p *PCSCF) refreshRegEvent(sub RegSubscription, log *slog.Logger) {
	stack, err := p.stack.Get()
	if err != nil {
		return
	}
	was, privateID := sub.Dialog, sub.PrivateIdentity
	// NewRequest takes the next CSeq number in this copy of the dialog;
	// the Store gets it only if the subscription is still the one read.
	d := sub.Dialog
	m := d.NewRequest(sip.SUBSCRIBE)
	m.Add("Contact", sip.NameAddr{URI: p.contactURI()}.String())
	m.Add("Event", regEventPackage)
	m.Add("Expires", strconv.Itoa(int(sub.Requested/time.Second)))
	dst, err := p.regEventHop(m)
	if err != nil {
		log.Warn("reg event refresh not sent", "error", err.Error())
		return
	}
	ctx := context.Background()
	_, mine, err := p.store.UpdateRegSubscription(ctx, privateID, func(s *RegSubscription) bool {
		if s.Dialog.ID != was.ID || s.Dialog.LocalSeq != was.LocalSeq {
			return false
		}
		s.Dialog.LocalSeq = d.LocalSeq
		return true
	})
	switch {
	case err != nil:
		// The subscription runs out at both ends, and the next REGISTER
		// subscribes anew.
		log.Warn("reg event refresh not sent: the store failed", "error", err.Error())
		return
	case !mine:
		// It has ended, or another instance took this refresh.
		return
	}
	log.Info("reg event refreshing", "public_identity", sub.PublicIdentity, "expires", int(sub.Requested/time.Second))
	p.timeRegEventNotify(privateID, d, log)
	go func() {
		final, err := p.sendRegEvent(stack, m, dst)
		switch {
		case err == nil && final.StatusCode < 300:
			p.grantRegEvent(privateID, d.ID, final, log)
			return
		case final == nil || !sip.EndsSubscription(final.StatusCode):
			// RFC 6665 4.1.2.2: the subscription stays until it expires.
			log.Warn("reg event refresh failed", "status", statusOf(final), "error", errorText(err))
			return
		}
		// The S-CSCF no longer has the subscription, so the P-CSCF no
		// longer hears of the user. The IP association stays until it
		// expires, since only the S-CSCF decides that the registration has
		// ended.
		log.Warn("reg event subscription ended by its refresh", "status", final.StatusCode)
		current, ok, err := p.store.RegSubscription(ctx, privateID)
		if err == nil && ok && current.Dialog.ID == d.ID {
			err = p.store.DeleteRegSubscription(ctx, privateID)
		}
		if err != nil {
			// The next REGISTER refreshes the subscription again, which
			// ends it again.
			log.Warn("ended reg event subscription not removed: the store failed", "error", err.Error())
		}
	}()
}

// timeRegEventNotify starts Timer N in the process that sent a
// SUBSCRIBE, initial or within the dialog: every SUBSCRIBE that succeeds
// is followed by a NOTIFY (RFC 6665 4.1.2.2, 4.1.2.4). sent is the
// dialog as that SUBSCRIBE left it.
func (p *PCSCF) timeRegEventNotify(privateID string, sent sip.Dialog, log *slog.Logger) {
	time.AfterFunc(p.notifyWait, func() { p.regEventNotifyWaitOver(privateID, sent, log) })
}

// grantRegEvent keeps the duration that a 2xx to a SUBSCRIBE granted
// (RFC 6665 3.1.1), which is what a NOTIFY that carries no expires
// stands for (TS 24.229 5.1.1.3 item 2). It does not move the expiry:
// the NOTIFY that follows every SUBSCRIBE says the time left, and that
// overrides the 2xx (RFC 6665 4.1.3).
func (p *PCSCF) grantRegEvent(privateID string, id sip.DialogID, final *sip.Message, log *slog.Logger) {
	seconds, err := sip.ParseDeltaSeconds(final.Get("Expires"))
	if err != nil || seconds <= 0 {
		return
	}
	granted := time.Duration(seconds) * time.Second
	_, _, err = p.store.UpdateRegSubscription(context.Background(), privateID, func(s *RegSubscription) bool {
		if s.Dialog.ID.CallID != id.CallID || s.Granted == granted {
			return false
		}
		s.Granted = granted
		return true
	})
	if err != nil {
		// A NOTIFY that carries expires, which the S-CSCF always sends,
		// needs none of this.
		log.Warn("the duration granted to the reg event subscription not stored: the store failed", "error", err.Error())
	}
}

// timeRegEventRefresh starts the timer for the next refresh of sub (TS
// 24.229 5.2.3), in the process that took the NOTIFY. The NOTIFY is what
// says how long the subscription has (RFC 6665 4.1.3), so the refresh is
// counted from that, not from the duration the SUBSCRIBE asked for and
// not from the REGISTERs of the UE.
//
// One timer runs per subscription in this process: the NOTIFYs of a
// subscription come one after another, and each replaces the timer the
// one before it set, so that two timers of one subscription cannot fall
// due together.
//
// Like Timer N, the timer reads the Store when it fires and does nothing
// unless the subscription is still the one it was started for, so a
// refresh that another instance sent in the meantime stands. A timer is
// not cancelled when the subscription ends elsewhere: it then finds
// none.
func (p *PCSCF) timeRegEventRefresh(sub RegSubscription, log *slog.Logger) {
	expires := time.Until(sub.Expires).Round(time.Second)
	after := p.regEventRefresh(expires)
	if after <= 0 || after >= expires {
		p.stopRegEventRefresh(sub.PrivateIdentity)
		return
	}
	key := privateKey(sub.PrivateIdentity)
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if t := p.refreshTimers[key]; t != nil {
		t.Stop()
	}
	p.refreshTimers[key] = time.AfterFunc(after, func() {
		p.refreshMu.Lock()
		delete(p.refreshTimers, key)
		p.refreshMu.Unlock()
		p.refreshRegEventDue(sub.PrivateIdentity, sub.Dialog, log)
	})
}

// stopRegEventRefresh stops the refresh timer of a subscription that has
// ended, so that the process keeps none for a user it no longer
// watches.
func (p *PCSCF) stopRegEventRefresh(privateID string) {
	key := privateKey(privateID)
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if t := p.refreshTimers[key]; t != nil {
		t.Stop()
		delete(p.refreshTimers, key)
	}
}

// regEventNotifyWaitOver is Timer N (RFC 6665 4.1.2.4): no NOTIFY
// followed the SUBSCRIBE, so the subscription never came about, or the
// one there was is gone at the notifier. The P-CSCF forgets it, and the
// user's next REGISTER subscribes again.
//
// The dialog of a subscription moves on with every NOTIFY, so a dialog
// that is still exactly the one the SUBSCRIBE left means that no NOTIFY
// has arrived, at this instance or at any other.
func (p *PCSCF) regEventNotifyWaitOver(privateID string, sent sip.Dialog, log *slog.Logger) {
	ctx := context.Background()
	sub, ok, err := p.store.RegSubscription(ctx, privateID)
	switch {
	case err != nil:
		// The subscription stays until it runs out, as one whose refresh
		// could not be sent does.
		log.Warn("reg event subscription without a NOTIFY not read: the store failed", "error", err.Error())
		return
	case !ok, sub.Dialog.ID != sent.ID, sub.Dialog.LocalSeq != sent.LocalSeq, sub.Dialog.RemoteSeq != sent.RemoteSeq:
		return
	}
	log.Info("reg event subscription ended: no NOTIFY for the SUBSCRIBE",
		"public_identity", sub.PublicIdentity, "subscription_call_id", sent.ID.CallID)
	if err := p.store.DeleteRegSubscription(ctx, privateID); err != nil {
		log.Warn("reg event subscription without a NOTIFY not removed: the store failed", "error", err.Error())
	}
}

// refreshRegEventDue is the refresh of TS 24.229 5.2.3 falling due at
// this instance. It stands down when the subscription has ended, or when
// another instance has refreshed it since and now holds the timer for the
// refresh after this one.
func (p *PCSCF) refreshRegEventDue(privateID string, sent sip.Dialog, log *slog.Logger) {
	sub, ok, err := p.store.RegSubscription(context.Background(), privateID)
	switch {
	case err != nil:
		// The subscription runs out at both ends, and the next REGISTER
		// subscribes anew.
		log.Warn("reg event refresh not sent: the store failed", "error", err.Error())
		return
	case !ok:
		// It has ended, or it ran out and the Store forgot it at its
		// Expires.
		return
	case sub.Dialog.ID.CallID != sent.ID.CallID || sub.Dialog.LocalSeq != sent.LocalSeq:
		return
	case sub.Dialog.ID.RemoteTag == "":
		// No NOTIFY has made a dialog to refresh in; Timer N ends it.
		return
	}
	p.refreshRegEvent(sub, log)
}

// regEventHop is where a SUBSCRIBE of the P-CSCF goes: the I-CSCF of the
// home network for an initial one, whose Request-URI is the user's
// public identity, and the remote target for one within the dialog.
func (p *PCSCF) regEventHop(m *sip.Message) (sip.Destination, error) {
	hop, err := sip.NextHop(m)
	if err != nil {
		return sip.Destination{}, err
	}
	return p.locate(hop)
}

// sendRegEvent sends a SUBSCRIBE of the P-CSCF and returns its final
// response. With no final response, the error says why.
func (p *PCSCF) sendRegEvent(stack *sip.Stack, m *sip.Message, dst sip.Destination) (*sip.Message, error) {
	ct, err := stack.SendRequest(m, dst)
	if err != nil {
		return nil, err
	}
	var final *sip.Message
	for r := range ct.Responses() {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		if err := ct.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("pcscf: no final response to the SUBSCRIBE")
	}
	return final, nil
}

// ownRegEventNotify reports whether req is a NOTIFY of one of the
// P-CSCF's own subscriptions rather than one it routes, such as a
// NOTIFY of the UE's own subscription: only the P-CSCF's own carry the
// P-CSCF's contact, with no user part, as their Request-URI (Screen).
func (p *PCSCF) ownRegEventNotify(req *sip.Message) bool {
	if req.Method != sip.NOTIFY {
		return false
	}
	target, err := sip.ParseURI(req.RequestURI)
	if err != nil || target.User != "" {
		return false
	}
	for _, own := range p.ownURIs() {
		if strings.EqualFold(target.Host, own.Host) && target.Port == own.Port {
			return true
		}
	}
	return false
}

// receiveRegEvent is a NOTIFY of a subscription of the P-CSCF (TS 24.229
// 5.2.3 and 5.2.5.2): it completes the dialog with the first one, checks
// the order of the others (RFC 3261 12.2.2), and for a registration that
// has ended, or a contact of the UE that the network removed, ends the
// IP association of the UE: the P-CSCF forgets what it stored for the
// user.
//
// SIMPLIFIED: RFC 3680 5.2. The version of the document is not checked.
// The S-CSCF sends the full state every time, and a NOTIFY that arrives
// after a later one is already refused for its CSeq (RFC 3261 12.2.2).
//
// The NOTIFYs of one subscription are not serialised here, as they were
// while a sip.Subscription held each subscription in one process. They
// do not need to be: a notifier sends the next NOTIFY of a subscription
// only once the one before it has been answered (RFC 6665 4.2.2), the
// CSeq of the dialog refuses one that arrives after a later one (RFC
// 3261 12.2.2), and each write keeps only what its own NOTIFY said.
//
// A NOTIFY whose subscription the P-CSCF cannot read or write gets 500
// (RFC 3261 21.5.1). The S-CSCF keeps the subscription then, since a 500
// is not one of the responses that end one (sip.EndsSubscription, RFC
// 6665 4.2.2), so what the NOTIFY said is lost only until the next one.
// A NOTIFY whose association the P-CSCF cannot read or end is answered
// 200 all the same: the association then stays until its KeepUntil, and
// with it the requests to the UE.
func (p *PCSCF) receiveRegEvent(st *sip.ServerTransaction) {
	req := st.Request
	id := sip.ReceivedDialogID(req)
	log := p.log.With("method", "NOTIFY", "call_id", id.CallID)
	ctx := context.Background()
	// 5.2.3: the response carries the icid-value and the orig-ioi of the
	// NOTIFY, and this network's term-ioi.
	notify, _ := sip.ChargingVectorOf(req)
	respond := func(code int) {
		resp := sip.NewResponse(req, code, sip.NewTag())
		p.chargeResponse(resp, notify)
		st.Respond(resp)
	}
	storeFailed := func(err error) {
		log.Warn("reg event NOTIFY refused: the store failed", "error", err.Error())
		respond(500)
	}

	sub, ok, err := p.store.RegSubscriptionOfDialog(ctx, id.CallID)
	if err != nil {
		storeFailed(err)
		return
	}
	if !ok || sub.Dialog.ID.LocalTag != id.LocalTag {
		respond(481)
		return
	}
	if sip.EventType(req.Get("Event")) != regEventPackage {
		// RFC 6665 4.1.3: the subscription is for another event package.
		respond(489)
		return
	}
	state, err := sip.ParseSubscriptionState(req.Get("Subscription-State"))
	if err != nil {
		respond(400)
		return
	}
	d := sub.Dialog
	switch {
	case d.ID.RemoteTag == "":
		// 4.4.1: the first NOTIFY creates the dialog.
		err = d.ReceiveFirstNotify(req)
	case id.RemoteTag != d.ID.RemoteTag:
		// RFC 3680 4.9 forbids forking a SUBSCRIBE of the reg event
		// package, so there is no second dialog for a NOTIFY to belong to.
		respond(481)
		return
	default:
		err = d.ReceiveRequest(req)
	}
	switch {
	case errors.Is(err, sip.ErrOutOfOrder):
		respond(500)
		return
	case err != nil:
		respond(400)
		return
	}

	// RFC 6665 4.1.3: the expires of the NOTIFY is the time left, so
	// active or pending with expires=0 ends the subscription as
	// terminated does.
	expires := sip.HasExpires(req.Get("Subscription-State"))
	ends := state.State == sip.SubscriptionTerminated || (expires && state.Expires == 0)
	if ends {
		// The subscription has ended, so the P-CSCF forgets it; a first
		// NOTIFY that says terminated creates no dialog at all (4.4.1).
		// The next REGISTER of the user starts a new subscription.
		if err := p.store.DeleteRegSubscription(ctx, sub.PrivateIdentity); err != nil {
			storeFailed(err)
			return
		}
		p.stopRegEventRefresh(sub.PrivateIdentity)
		p.endAssociationsOf(sub, req.Body, log.With("private_identity", sub.PrivateIdentity, "subscription_state", string(state.State)))
		respond(200)
		return
	}
	// Only what this NOTIFY says is written, so that the CSeq a refresh
	// took in the meantime stands (refreshRegEvent).
	sub, ok, err = p.store.UpdateRegSubscription(ctx, sub.PrivateIdentity, func(s *RegSubscription) bool {
		if s.Dialog.ID.CallID != d.ID.CallID {
			return false
		}
		s.Dialog.ID.RemoteTag = d.ID.RemoteTag
		s.Dialog.State = d.State
		s.Dialog.RemoteSeq = d.RemoteSeq
		s.Dialog.RemoteTarget = d.RemoteTarget
		s.Dialog.RouteSet = d.RouteSet
		// 4.1.3: the expires of the NOTIFY is the time left. Without it,
		// the duration is the one the 2xx granted, and failing that the one
		// the SUBSCRIBE asked for (TS 24.229 5.1.1.3 item 2, 5.2.3).
		left := state.Expires
		if !expires {
			left = orElse(s.Granted, s.Requested)
		}
		s.Expires = time.Now().Add(left)
		return true
	})
	switch {
	case err != nil:
		storeFailed(err)
		return
	case !ok:
		// It ended between the read and the write.
		respond(481)
		return
	}
	p.timeRegEventRefresh(sub, log)

	p.endAssociationsOf(sub, req.Body, log.With("private_identity", sub.PrivateIdentity, "subscription_state", string(state.State)))
	respond(200)
}

// endAssociationsOf ends the IP association of every contact of the user
// that body, the reginfo document of a NOTIFY, reports as removed by the
// network (TS 24.229 5.2.5.2). A NOTIFY without a document reports
// nothing but the state of the subscription.
func (p *PCSCF) endAssociationsOf(sub RegSubscription, body []byte, log *slog.Logger) {
	if len(body) == 0 {
		log.Info("reg event")
		return
	}
	doc, err := reginfo.Parse(body)
	if err != nil {
		log.Warn("reg event NOTIFY with a body that does not parse", "error", err.Error())
		return
	}
	for _, r := range doc.Registrations {
		for _, c := range r.Contacts {
			log.Info("reg event", "public_identity", r.AOR, "state", string(r.State), "contact", c.URI,
				"contact_state", string(c.State), "contact_event", string(c.Event))
			if !removedByTheNetwork(r, c) {
				continue
			}
			a, ok, err := p.associationOfContact(c.URI)
			if err == nil && ok && strings.EqualFold(a.PrivateIdentity, sub.PrivateIdentity) {
				if err = p.endAssociation(a, log); err == nil {
					log.Info("reg event ended the IP association", "public_identity", r.AOR, "source", a.Source.String())
				}
			}
			if err != nil {
				log.Warn("the IP association stays until it expires: the store failed", "contact", c.URI, "error", err.Error())
			}
		}
	}
}

// orElse returns a unless it is zero, and then b.
func orElse(a, b time.Duration) time.Duration {
	if a != 0 {
		return a
	}
	return b
}

// removedByTheNetwork is the test of TS 24.229 5.2.5.2: the registration
// has ended, or the contact has, by an event that removed it.
func removedByTheNetwork(r reginfo.Registration, c reginfo.Contact) bool {
	if r.State == reginfo.Terminated {
		return true
	}
	if c.State != reginfo.Terminated {
		return false
	}
	switch c.Event {
	case reginfo.Unregistered, reginfo.Rejected, reginfo.Deactivated:
		return true
	}
	return false
}

func statusOf(m *sip.Message) int {
	if m == nil {
		return 0
	}
	return m.StatusCode
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
