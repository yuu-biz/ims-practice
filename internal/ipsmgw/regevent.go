package ipsmgw

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the rest of TS 24.341 5.3.3.2 after the 200 to a
// third-party REGISTER: the IP-SM-GW subscribes to the reg event
// package of the user (TS 24.229 5.7.1.1, RFC 3680), and each NOTIFY
// tells it whether the user has a contact registered with
// "+g.3gpp.smsip", the feature tag of an SM-over-IP receiver (5.3.2.2).
// When that changes, the IP-SM-GW tells the HSS over Sh.
//
// The subscription is in the Store, and the Contact of the SUBSCRIBE is
// the IP-SM-GW's name in DNS, not one node: whichever IP-SM-GW the
// NOTIFY reaches takes it. That is why the IP-SM-GW does not use
// sip.Subscriber, which keeps its subscriptions and their timers in the
// memory of one process.

const regEventPackage = "reg"

// featureSMSOverIP is the feature tag that a UE registers to say that it
// takes short messages over IP (TS 24.341 5.3.2.2, RFC 3840).
const featureSMSOverIP = "+g.3gpp.smsip"

// watchRegistration keeps the subscription to the registration state of
// a user whom a third-party REGISTER registered for registered: it
// starts one, or refreshes the one there is within its dialog.
//
// The subscription asks for the duration of the registration and
// ims.RegEventMargin more, as the P-CSCF's does. The S-CSCF sends a
// third-party REGISTER for every reregistration, so the refresh comes
// before the subscription would end, and the S-CSCF ends the
// subscription itself when the registration ends (TS 24.229 5.4.2.1.2).
// The IP-SM-GW runs no refresh timer for it. It does run Timer N after
// each SUBSCRIBE (awaitNotify).
//
// The REGISTER has been answered when this runs. When the Store fails,
// the IP-SM-GW sends no SUBSCRIBE and logs the failure; the third-party
// REGISTER of the next reregistration tries again.
//
// It returns the subscription that it refreshed, and whether there was
// one: the REGISTER was then a reregistration of a user whose state the
// IP-SM-GW already hears of.
func (g *IPSMGW) watchRegistration(publicID, msisdn string, registered time.Duration, log *slog.Logger) (RegSubscription, bool) {
	ctx := context.Background()
	unlock, err := g.users.Lock(ctx, publicID)
	if err != nil {
		log.Warn("reg event subscription not read: the lock of the user was not taken", "error", err.Error())
		return RegSubscription{}, false
	}
	defer unlock()
	expires := registered + ims.RegEventMargin
	sub, ok, err := g.store.RegSubscriptionOf(ctx, publicID)
	if err != nil {
		log.Warn("reg event subscription not read: the store failed", "error", err.Error())
		return RegSubscription{}, false
	}
	if ok && sub.Dialog.ID.RemoteTag != "" {
		sub.MSISDN = msisdn
		sub.Expires = time.Now().Add(expires)
		g.refreshRegEvent(sub, expires, log)
		return sub, true
	}
	if ok {
		if err := g.store.DeleteRegSubscription(ctx, sub.Dialog.ID.CallID); err != nil {
			log.Warn("reg event subscription without a dialog not removed: the store failed", "error", err.Error())
			return RegSubscription{}, false
		}
	}
	g.subscribeRegEvent(publicID, msisdn, expires, log)
	return RegSubscription{}, false
}

// subscribeRegEvent sends the SUBSCRIBE of TS 24.229 5.7.1.1 items a to
// f. Like every request of the IP-SM-GW, it goes to the entry point of
// the home network, where the I-CSCF finds the S-CSCF of the user
// (5.3.2.1). The lock of the user is held.
func (g *IPSMGW) subscribeRegEvent(publicID, msisdn string, expires time.Duration, log *slog.Logger) {
	icid := sip.NewCallID()
	m := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: publicID} // item a
	m.Add("Max-Forwards", "70")
	m.Add("From", sip.NameAddr{URI: g.cfg.URI}.String()+";tag="+sip.NewTag()) // item b
	m.Add("To", sip.NameAddr{URI: publicID}.String())                         // item c
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 SUBSCRIBE")
	m.Add("Event", regEventPackage) // item d
	m.Add("Expires", strconv.Itoa(int(expires/time.Second)))
	// The S-CSCF lets an application server of the user's filter
	// criteria subscribe, by this URI (item e, NOTE 3).
	m.Add("P-Asserted-Identity", sip.NameAddr{URI: g.cfg.URI}.String())
	m.Add("P-Charging-Vector", sip.ChargingVector{ICID: icid, OrigIOI: g.cfg.HomeDomain}.String()) // item f
	// RFC 6665 4.1.2.1: where the NOTIFYs go.
	m.Add("Contact", sip.NameAddr{URI: g.cfg.URI}.String())
	d, err := sip.NewSubscriberDialog(m)
	if err != nil {
		log.Warn("reg event SUBSCRIBE not built", "error", err.Error())
		return
	}
	deadline, err := g.notifyDeadline()
	if err != nil {
		log.Warn("reg event SUBSCRIBE not sent", "error", err.Error())
		return
	}
	// In the Store before the SUBSCRIBE goes out: the first NOTIFY may
	// come before the 200, and to another IP-SM-GW (RFC 6665 4.1.2.4).
	ctx := context.Background()
	log = log.With("subscription_call_id", d.ID.CallID)
	if err := g.store.PutRegSubscription(ctx, RegSubscription{PublicIdentity: publicID, MSISDN: msisdn, Dialog: d, ICID: icid,
		NotifyDeadline: deadline, Expires: time.Now().Add(expires)}); err != nil {
		log.Warn("reg event SUBSCRIBE not sent: the store failed", "error", err.Error())
		return
	}
	log.Info("reg event subscribing", "expires", int(expires/time.Second))
	// The SUBSCRIBE is a user of the IP-SM-GW until it has its final
	// response, so that a shut down does not leave a subscription
	// without a dialog behind (maintenance.go).
	g.userArrived()
	go func() {
		defer g.userQuit()
		final, err := g.send(m, g.entryPoint())
		if err == nil && final.StatusCode < 300 {
			g.awaitNotify(publicID, d.ID.CallID, deadline, log)
			return
		}
		if errors.Is(err, sip.ErrStackClosed) {
			// The stop of this instance's stack is no answer of the home
			// network, which may have made the subscription and send its
			// NOTIFY to another IP-SM-GW.
			log.Info("reg event SUBSCRIBE cut short by the stop of the stack; the subscription stays")
			return
		}
		// RFC 6665 4.1.2.1: the SUBSCRIBE failed, and no subscription was
		// made. One that a NOTIFY has already confirmed stays.
		log.Warn("reg event SUBSCRIBE failed", "status", statusOf(final), "error", errorText(err))
		// When the Store fails, the subscription without a dialog stays
		// until it expires, or the next third-party REGISTER replaces it.
		unlock, err := g.users.Lock(ctx, publicID)
		if err != nil {
			log.Warn("failed reg event subscription not removed: the lock of the user was not taken", "error", err.Error())
			return
		}
		defer unlock()
		sub, ok, err := g.store.RegSubscription(ctx, d.ID.CallID)
		if err == nil && ok && sub.Dialog.ID.RemoteTag == "" {
			err = g.store.DeleteRegSubscription(ctx, d.ID.CallID)
		}
		if err != nil {
			log.Warn("failed reg event subscription not removed: the store failed", "error", err.Error())
		}
	}()
}

// refreshRegEvent sends a SUBSCRIBE within the dialog of sub (RFC 6665
// 4.1.2.2), with the icid-value of the first one (TS 24.229 5.7.3). It
// goes to the S-CSCF, the remote target. sub has the Expires that the
// refresh asks for, expires from now. The lock of the user is held.
func (g *IPSMGW) refreshRegEvent(sub RegSubscription, expires time.Duration, log *slog.Logger) {
	m := sub.Dialog.NewRequest(sip.SUBSCRIBE)
	m.Add("Event", regEventPackage)
	m.Add("Expires", strconv.Itoa(int(expires/time.Second)))
	m.Add("P-Charging-Vector", sip.ChargingVector{ICID: sub.ICID, OrigIOI: g.cfg.HomeDomain}.String())
	m.Add("Contact", sip.NameAddr{URI: g.cfg.URI}.String())
	hop, err := sip.NextHop(m)
	if err != nil {
		log.Warn("reg event refresh not sent", "error", err.Error())
		return
	}
	deadline, err := g.notifyDeadline()
	if err != nil {
		log.Warn("reg event refresh not sent", "error", err.Error())
		return
	}
	callID := sub.Dialog.ID.CallID
	log = log.With("subscription_call_id", callID)
	// The CSeq of the refresh is in the Store before the refresh goes
	// out. When the Store fails, the subscription runs out at its old
	// expiry at both ends, and the next third-party REGISTER subscribes
	// anew.
	ctx := context.Background()
	sub.NotifyDeadline = deadline
	if err := g.store.PutRegSubscription(ctx, sub); err != nil {
		log.Warn("reg event refresh not sent: the store failed", "error", err.Error())
		return
	}
	log.Info("reg event refreshing", "expires", int(expires/time.Second))
	// The refresh is a user of the IP-SM-GW until it has its final
	// response, as the first SUBSCRIBE is.
	g.userArrived()
	go func() {
		defer g.userQuit()
		final, err := g.send(m, hop)
		switch {
		case err == nil && final.StatusCode < 300:
			g.awaitNotify(sub.PublicIdentity, callID, deadline, log)
			return
		case final == nil || !sip.EndsSubscription(final.StatusCode):
			// RFC 6665 4.1.2.2: the subscription stays until it expires.
			log.Warn("reg event refresh failed", "status", statusOf(final), "error", errorText(err))
			return
		}
		// The notifier no longer has the subscription.
		log.Warn("reg event subscription ended by its refresh", "status", final.StatusCode)
		unlock, err := g.users.Lock(ctx, sub.PublicIdentity)
		if err != nil {
			log.Warn("ended reg event subscription not read: the lock of the user was not taken", "error", err.Error())
			return
		}
		defer unlock()
		current, ok, err := g.store.RegSubscription(ctx, callID)
		if err != nil {
			// The next third-party REGISTER refreshes the subscription
			// again, which ends it again.
			log.Warn("ended reg event subscription not read: the store failed", "error", err.Error())
			return
		}
		if !ok {
			return
		}
		g.endRegSubscription(ctx, current, log)
	}()
}

// endRegSubscription forgets sub, a subscription that the notifier no
// longer has. The IP-SM-GW no longer hears of the user, so it no longer
// claims the user's short messages; the next third-party REGISTER
// subscribes anew. The lock of the user is held.
func (g *IPSMGW) endRegSubscription(ctx context.Context, sub RegSubscription, log *slog.Logger) {
	if err := g.store.DeleteRegSubscription(ctx, sub.Dialog.ID.CallID); err != nil {
		// The subscription stays until it expires; the HSS learns all
		// the same that the user takes no short messages here.
		log.Warn("ended reg event subscription not removed: the store failed", "error", err.Error())
	}
	if sub.Available {
		g.updateHSS(&sub, false, log)
	}
}

// notifyDeadline is when Timer N runs out for a SUBSCRIBE that goes out
// now: 64*T1 of the stack that sends it (RFC 6665 4.1.2.4).
func (g *IPSMGW) notifyDeadline() (time.Time, error) {
	stack, err := g.stack.Get()
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(64 * stack.Timers().T1), nil
}

// awaitNotify runs Timer N for a SUBSCRIBE that the S-CSCF has accepted
// with a 2xx, until deadline, the NotifyDeadline that the SUBSCRIBE
// wrote. A SUBSCRIBE that the S-CSCF refused needs none: no NOTIFY
// follows a final response other than 2xx (RFC 6665 4.1.2.1), and the
// refresh that fails keeps or ends the subscription by its response
// (4.1.2.2).
//
// The NOTIFY may reach another instance, so it is the Store that says
// whether one came. The wait is no user of the IP-SM-GW
// (maintenance.go): a shut down does not wait for it. A subscription
// whose instance stops before Timer N runs out stays as it is until it
// expires, or until the next third-party REGISTER replaces it, or
// refreshes it with a Timer N of its own.
func (g *IPSMGW) awaitNotify(publicID, callID string, deadline time.Time, log *slog.Logger) {
	time.AfterFunc(time.Until(deadline), func() { g.notifyWaitOver(publicID, callID, deadline, log) })
}

// notifyWaitOver is Timer N running out. Under the lock of the user,
// every NOTIFY clears the NotifyDeadline, at whichever instance it
// arrives, and every later SUBSCRIBE writes a deadline of its own. So
// only a subscription that still holds deadline got no NOTIFY after this
// SUBSCRIBE. It has failed, if this was the first SUBSCRIBE (RFC 6665
// 4.1.2.4), or ended, if this was a refresh (4.1.2.2), and the IP-SM-GW
// forgets it either way. A NOTIFY that comes later then gets 481. When
// the lock or the Store fails, the subscription stays as if this
// instance had stopped (awaitNotify).
func (g *IPSMGW) notifyWaitOver(publicID, callID string, deadline time.Time, log *slog.Logger) {
	ctx := context.Background()
	unlock, err := g.users.Lock(ctx, publicID)
	if err != nil {
		log.Warn("reg event subscription after Timer N not read: the lock of the user was not taken", "error", err.Error())
		return
	}
	defer unlock()
	sub, ok, err := g.store.RegSubscription(ctx, callID)
	switch {
	case err != nil:
		log.Warn("reg event subscription after Timer N not read: the store failed", "error", err.Error())
		return
	case !ok || !sub.NotifyDeadline.Equal(deadline):
		return
	}
	log.Warn("reg event subscription ended: no NOTIFY followed the SUBSCRIBE within Timer N")
	g.endRegSubscription(ctx, sub, log)
}

// receiveRegEvent is a NOTIFY of a subscription of the IP-SM-GW (TS
// 24.229 5.7.1.1): it completes the dialog with the first one, checks
// the order of the others (RFC 3261 12.2.2), answers 200, and tells the
// HSS when the availability of the user for short messages has changed
// (TS 24.341 5.3.3.2). A NOTIFY that ends the subscription ends it here
// too (5.7.1.1 NOTE 5); the user then takes no short messages through
// this subscription, whatever the document says.
//
// A NOTIFY whose subscription the IP-SM-GW cannot lock, read or write
// gets 500 (RFC 3261 21.5.1), which does not end the subscription at
// the notifier. What the NOTIFY said is then lost until the next one.
func (g *IPSMGW) receiveRegEvent(st *sip.ServerTransaction) {
	req := st.Request
	id := sip.ReceivedDialogID(req)
	log := g.log.With("method", "NOTIFY", "call_id", id.CallID)
	ctx := context.Background()
	storeFailed := func(err error) {
		log.Warn("reg event NOTIFY refused: the store failed", "error", err.Error())
		g.respond(st, 500, nil)
	}
	sub, ok, err := g.store.RegSubscription(ctx, id.CallID)
	if err != nil {
		storeFailed(err)
		return
	}
	if !ok || sub.Dialog.ID.LocalTag != id.LocalTag {
		g.respond(st, 481, nil)
		return
	}
	unlock, err := g.users.Lock(ctx, sub.PublicIdentity)
	if err != nil {
		log.Warn("reg event NOTIFY refused: the lock of the user was not taken", "error", err.Error())
		g.respond(st, 500, nil)
		return
	}
	defer unlock()
	sub, ok, err = g.store.RegSubscription(ctx, id.CallID)
	if err != nil {
		storeFailed(err)
		return
	}
	if !ok {
		g.respond(st, 481, nil)
		return
	}
	if sip.EventType(req.Get("Event")) != regEventPackage {
		// RFC 6665 4.1.3.
		g.respond(st, 489, nil)
		return
	}
	state, err := sip.ParseSubscriptionState(req.Get("Subscription-State"))
	if err != nil {
		g.respond(st, 400, nil)
		return
	}
	d := sub.Dialog
	switch {
	case d.ID.RemoteTag == "":
		err = d.ReceiveFirstNotify(req)
	case id.RemoteTag != d.ID.RemoteTag:
		// RFC 3680 4.9 forbids forking a SUBSCRIBE of the reg event
		// package, so there is no second dialog for a NOTIFY to belong to.
		g.respond(st, 481, nil)
		return
	default:
		err = d.ReceiveRequest(req)
	}
	switch {
	case errors.Is(err, sip.ErrOutOfOrder):
		g.respond(st, 500, nil)
		return
	case err != nil:
		g.respond(st, 400, nil)
		return
	}

	log = log.With("public_identity", sub.PublicIdentity, "subscription_state", string(state.State))
	// RFC 6665 4.1.3: the expires of the NOTIFY is the time left, so
	// active or pending with expires=0 ends the subscription as
	// terminated does.
	expires := sip.HasExpires(req.Get("Subscription-State"))
	ends := state.State == sip.SubscriptionTerminated || (expires && state.Expires == 0)
	available := sub.Available
	var stored error
	switch {
	case ends:
		available = false
		stored = g.store.DeleteRegSubscription(ctx, id.CallID)
	default:
		doc, err := reginfo.Parse(req.Body)
		if err != nil {
			// The availability is unknown; what the HSS was told stands.
			log.Warn("reg event NOTIFY with a body that does not parse", "error", err.Error())
		} else {
			available = takesShortMessages(doc, sub.PublicIdentity)
		}
		sub.Dialog = d
		// The NOTIFY that Timer N waits for is here (notifyWaitOver).
		sub.NotifyDeadline = time.Time{}
		if expires {
			// 5.7.1.1: the expiration time is that of Subscription-State.
			sub.Expires = time.Now().Add(state.Expires)
		}
		stored = g.store.PutRegSubscription(ctx, sub)
	}
	if stored != nil {
		storeFailed(stored)
		return
	}
	// 5.7.1.1: the response repeats the icid-value and the orig-ioi of
	// the NOTIFY, with a type 3 term-ioi.
	g.respond(st, 200, nil)

	log.Info("reg event", "sms_over_ip", available)
	if available == sub.Available {
		return
	}
	if g.updateHSS(&sub, available, log) && !ends {
		if available {
			// TS 23.040 3.2.6 item 2c: the registration that the PUR just
			// made is itself the news that the user is reachable for short
			// messages over IP, so the HSS clears its own UNRI and alerts
			// the Service Centres that are waiting. The IP-SM-GW's own flag
			// goes with it, and a notification beside the PUR would only ask
			// for the same alerts a second time.
			g.takeNotReachable(ctx, sub.PublicIdentity, log)
		}
		sub.Available = available
		if err := g.store.PutRegSubscription(ctx, sub); err != nil {
			// The NOTIFY has been answered and the HSS told. The Store
			// keeps what the HSS was told before, so the next NOTIFY
			// tells the HSS again.
			log.Warn("what the HSS was told not stored: the store failed", "error", err.Error())
		}
	}
}

// takesShortMessages is the check of TS 24.341 5.3.3.2: whether the
// public identity has an active contact registered with the feature
// tag of SMS over IP. The S-CSCF lists a feature tag of a contact as an
// <unknown-param> (TS 24.229 5.4.2.1.2 step 4 e).
func takesShortMessages(doc reginfo.Document, publicID string) bool {
	for _, r := range doc.Registrations {
		if r.State != reginfo.Active || !ims.SamePublicIdentity(r.AOR, publicID) {
			continue
		}
		for _, c := range r.Contacts {
			if c.State != reginfo.Active {
				continue
			}
			for _, p := range c.UnknownParams {
				if strings.EqualFold(p.Name, featureSMSOverIP) {
					return true
				}
			}
		}
	}
	return false
}

// updateHSS is the data update of TS 24.341 5.3.3.2, over Sh (TS 23.204
// 6.1): a PUR of SMSRegistrationInfo (TS 29.328 6.1.2) with the
// number of the IP-SM-GW when the user can take short messages, and
// without it when the user no longer can. The PUR names the user by the
// MSISDN that TS 24.341 B.3 step 9 uses.
//
// A registration reads the IMSI of the user over Sh first (TS 29.328
// 7.6.24) and stores it in sub, so that a TFR, which names the
// recipient by IMSI, finds the user (sgd.go). Both come before the PUR
// because the PUR is what makes the HSS name this IP-SM-GW to an
// SMS-GMSC: a TFR could arrive before the IP-SM-GW has the answer. The HSS has no
// IMSI for a subscription whose private identity is not derived from
// one, and the IP-SM-GW registers itself for that user all the same;
// only SGd cannot reach it (sgd.go).
//
// SIMPLIFIED: TS 24.341 5.3.3.2. Without an MSISDN in service-info, the
// IP-SM-GW would take the IMSI from the private identity of the UE's
// REGISTER, which a third-party REGISTER can carry in a message/sip
// body. The S-CSCF of this project sends no such body, so the PUR names
// the user by the public identity, which Sh accepts as well (TS 29.328
// Table 7.6.1).
func (g *IPSMGW) updateHSS(sub *RegSubscription, available bool, log *slog.Logger) bool {
	id := shIdentity(sub.PublicIdentity, sub.MSISDN)
	number := ""
	if available {
		number = g.cfg.Number
	}
	log = log.With("msisdn", id.MSISDN)
	ctx := context.Background()
	if available {
		imsi, err := g.hss.IMSI(ctx, sub.PublicIdentity)
		if err != nil {
			log.Warn("the IMSI of the user was not read", "error", err.Error())
			return false
		}
		sub.IMSI = imsi
		log = log.With("imsi", imsi)
		// The subscription says that the user takes short messages, with
		// the IMSI, before the PUR goes: a TFR that the HSS sets off from
		// the PUR, through the alert of TS 23.040 3.2.6 item 2c, may come
		// before the IP-SM-GW has the answer, and finds a user it can
		// deliver to then. A PUR that fails takes that back, so that the
		// next NOTIFY tells the HSS again.
		sub.Available = true
		if err := g.store.PutRegSubscription(ctx, *sub); err != nil {
			log.Warn("the IMSI of the user was not stored: the store failed", "error", err.Error())
			sub.Available = false
			return false
		}
	}
	if err := g.hss.UpdateSMSRegistrationInfo(ctx, id, number); err != nil {
		log.Warn("SMS registration in the HSS failed", "error", err.Error())
		if available {
			sub.Available = false
			if err := g.store.PutRegSubscription(ctx, *sub); err != nil {
				log.Warn("the registration that failed was not taken back: the store failed", "error", err.Error())
			}
		}
		return false
	}
	if number == "" {
		log.Info("SMS registration withdrawn from the HSS")
	} else {
		log.Info("SMS registration sent to the HSS", "ipsmgw_number", number)
	}
	return true
}

// takeNotReachable takes the IP-SM-GW's own UNRI for a user and reports
// whether it was set: whether a short message of this network is waiting
// for the user to come back (sgd.go). Only one instance gets it, so only
// one acts on the user's return (store.go). A Store that fails reports
// no flag, and the user's next sign of life tries again.
func (g *IPSMGW) takeNotReachable(ctx context.Context, publicID string, log *slog.Logger) bool {
	set, err := g.store.TakeNotReachable(ctx, publicID)
	if err != nil {
		log.Warn("the unreachable flag of the user was not read: the store failed", "error", err.Error())
		return false
	}
	return set
}

// reachableAgain is TS 23.204 6.5b step 2 for a request that has come
// from a user: whatever it carries, it shows that the UE is there
// (tellReachable). Without a subscription to the sender's registration
// state, the sender is no user this IP-SM-GW serves.
func (g *IPSMGW) reachableAgain(req *sip.Message, log *slog.Logger) {
	ctx := context.Background()
	sub, ok, err := g.subscriptionOfSender(ctx, req)
	switch {
	case err != nil:
		log.Warn("the registration state of the user was not read: the store failed", "error", err.Error())
		return
	case !ok:
		return
	}
	g.tellReachable(ctx, sub, req, log)
}

// tellReachable is TS 23.204 6.5b step 2 with TS 23.040 3.2.6 item 2c:
// the UE of sub has shown that it is there, by req, a request of its
// own, or by registering again (step 2a), where req is nil. Where this
// IP-SM-GW could not deliver a short message to the user, the HSS hears
// in a Ready-For-SM with UE_PRESENT that the user can take short
// messages again, which makes it clear its own UNRI and alert the
// Service Centres that are waiting.
//
// Step 2d has the IP-SM-GW discard the notification where the UE is not
// registered in the IMS. The subscription to the user's registration
// state is what tells it: without one that says the user has a contact
// taking short messages, a short message would not get through now
// either.
//
// The flag is taken only once the notification can go: Ready-For-SM
// names the user by MSISDN (numberOf), and a user for whom no number is
// found keeps the flag, so that a later request or reregistration tells
// the HSS. A notification the HSS would not take puts the flag back for
// the same reason. A flag that the Store loses leaves the HSS with its
// UNRI until this IP-SM-GW registers itself for the user anew, which
// the next registration after a deregistration does.
func (g *IPSMGW) tellReachable(ctx context.Context, sub RegSubscription, req *sip.Message, log *slog.Logger) {
	if !sub.Available {
		// The notification is discarded, as step 2d has it.
		return
	}
	msisdn, err := g.numberOf(ctx, &sub, req)
	switch {
	case err != nil:
		log.Warn("the return of the user is not passed to the HSS: the MSISDN was not read", "error", err.Error())
		return
	case msisdn == "":
		log.Info("the return of the user is not passed to the HSS: the user has no MSISDN")
		return
	}
	// The flag is kept under the public identity of the subscription
	// (sgd.go), whichever identity the request asserted.
	publicID := sub.PublicIdentity
	if !g.takeNotReachable(ctx, publicID, log) {
		return
	}
	log = log.With("msisdn", msisdn)
	result, err := g.readyForSM(ctx, msisdn, sgd.AlertReasonUEPresent)
	if err != nil || !result.Success() {
		log.Warn("the return of the user was not passed to the HSS", "result", result.String(), "error", errorText(err))
		if err := g.store.PutNotReachable(ctx, publicID, notReachableUntil(sub)); err != nil {
			log.Warn("the unreachable flag of the user was not put back", "error", err.Error())
		}
		return
	}
	log.Info("the user can take short messages again; the HSS is told")
}

// numberOf is the MSISDN, as digits, by which a Ready-For-SM names the
// user of sub: the one that the third-party REGISTER carried in
// service-info, which sub holds; else the number of the tel URI that req,
// a request from the user, asserts (TS 24.341 5.3.3.4.1 step d); else
// the MSISDN that the HSS keeps for the user (TS 29.328 7.6.9). Either
// of sub and req may be nil. It is "" for a user that has no number.
func (g *IPSMGW) numberOf(ctx context.Context, sub *RegSubscription, req *sip.Message) (string, error) {
	if sub != nil && sub.MSISDN != "" {
		return sub.MSISDN, nil
	}
	if req != nil {
		if n, ok := telNumber(req); ok {
			return n.Digits, nil
		}
	}
	if sub == nil {
		return "", nil
	}
	return g.hss.MSISDN(ctx, sub.PublicIdentity)
}

// readyForSM sends the HSS the Ready-For-SM of package sgd for the user
// of msisdn, and returns the result of its answer. The request names
// this IP-SM-GW by its number, which every instance has, and the HSS by
// the realm of the home network.
func (g *IPSMGW) readyForSM(ctx context.Context, msisdn string, reason sgd.AlertReason) (diameter.Result, error) {
	a, err := g.hss.ReadyForSM(ctx, sgd.ReadyForSMRequest{
		Header:         diameter.Header{DestinationRealm: g.cfg.HomeDomain},
		UserIdentifier: sgd.UserIdentifier{MSISDN: msisdn},
		IPSMGWNumber:   g.cfg.Number,
		AlertReason:    reason,
	})
	return a.Result, err
}

// subscriptionOfSender finds the subscription of the user who sent req,
// by the identities in its P-Asserted-Identity. The UE may assert any
// identity it registered (TS 24.229 5.2.6.3.1), and the S-CSCF adds the
// alias of that one (5.4.3.2 step 9), while the subscription is to the
// public identity of the third-party REGISTER. Each asserted identity
// is looked for as that public identity first, and one that names a
// telephone number as the MSISDN of the subscription after that.
func (g *IPSMGW) subscriptionOfSender(ctx context.Context, req *sip.Message) (RegSubscription, bool, error) {
	var ids []string
	for _, v := range req.GetAll("P-Asserted-Identity") {
		if na, err := sip.ParseNameAddr(v); err == nil {
			ids = append(ids, na.URI)
		}
	}
	for _, id := range ids {
		if sub, ok, err := g.store.RegSubscriptionOf(ctx, id); err != nil || ok {
			return sub, ok, err
		}
	}
	for _, id := range ids {
		n, ok := ims.ParseTelephoneNumber(id)
		if !ok {
			continue
		}
		canonical, err := ims.CanonicalURI("tel:" + n.Number)
		msisdn, global := strings.CutPrefix(canonical, "tel:+")
		if err != nil || !global {
			continue
		}
		if sub, ok, err := g.store.RegSubscriptionOfMSISDN(ctx, msisdn); err != nil || ok {
			return sub, ok, err
		}
	}
	return RegSubscription{}, false, nil
}

// shIdentity names a user in an Sh request: by the MSISDN that the
// third-party REGISTER carried, which TS 24.341 B.3 step 9 uses, and by
// the public identity where there is none, which Sh accepts as well (TS
// 29.328 Table 7.6.1).
func shIdentity(publicID, msisdn string) sh.UserIdentity {
	if msisdn != "" {
		return sh.UserIdentity{MSISDN: msisdn}
	}
	return sh.UserIdentity{PublicIdentity: publicID}
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
