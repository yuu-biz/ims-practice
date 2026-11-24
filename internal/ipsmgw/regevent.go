package ipsmgw

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
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
// The IP-SM-GW runs no timer for it.
//
// SIMPLIFIED: RFC 6665 4.1.2.4. Timer N does not run: a SUBSCRIBE that
// no NOTIFY follows leaves a subscription without a dialog, which the
// next third-party REGISTER replaces with a new one and the Store
// forgets when it expires. The S-CSCF follows every 200 to a SUBSCRIBE
// with a NOTIFY at once (RFC 6665 4.2.1.2), so the timer would only
// shorten the wait for a NOTIFY that was lost.
//
// The REGISTER has been answered when this runs. When the Store fails,
// the IP-SM-GW sends no SUBSCRIBE and logs the failure; the third-party
// REGISTER of the next reregistration tries again.
func (g *IPSMGW) watchRegistration(publicID, msisdn string, registered time.Duration, log *slog.Logger) {
	ctx := context.Background()
	unlock, err := g.users.Lock(ctx, publicID)
	if err != nil {
		log.Warn("reg event subscription not read: the lock of the user was not taken", "error", err.Error())
		return
	}
	defer unlock()
	expires := registered + ims.RegEventMargin
	sub, ok, err := g.store.RegSubscriptionOf(ctx, publicID)
	if err != nil {
		log.Warn("reg event subscription not read: the store failed", "error", err.Error())
		return
	}
	if ok && sub.Dialog.ID.RemoteTag != "" {
		sub.MSISDN = msisdn
		g.refreshRegEvent(sub, expires, log)
		return
	}
	if ok {
		if err := g.store.DeleteRegSubscription(ctx, sub.Dialog.ID.CallID); err != nil {
			log.Warn("reg event subscription without a dialog not removed: the store failed", "error", err.Error())
			return
		}
	}
	g.subscribeRegEvent(publicID, msisdn, expires, log)
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
	// In the Store before the SUBSCRIBE goes out: the first NOTIFY may
	// come before the 200, and to another IP-SM-GW (RFC 6665 4.1.2.4).
	ctx := context.Background()
	log = log.With("subscription_call_id", d.ID.CallID)
	if err := g.store.PutRegSubscription(ctx, RegSubscription{PublicIdentity: publicID, MSISDN: msisdn, Dialog: d, ICID: icid,
		Expires: time.Now().Add(expires)}); err != nil {
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
// goes to the S-CSCF, the remote target. The lock of the user is held.
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
	sub.Expires = time.Now().Add(expires)
	callID := sub.Dialog.ID.CallID
	log = log.With("subscription_call_id", callID)
	// The CSeq of the refresh is in the Store before the refresh goes
	// out. When the Store fails, the subscription runs out at its old
	// expiry at both ends, and the next third-party REGISTER subscribes
	// anew.
	ctx := context.Background()
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
			return
		case final == nil || !sip.EndsSubscription(final.StatusCode):
			// RFC 6665 4.1.2.2: the subscription stays until it expires.
			log.Warn("reg event refresh failed", "status", statusOf(final), "error", errorText(err))
			return
		}
		// The notifier no longer has the subscription. The IP-SM-GW no
		// longer hears of the user, so it no longer claims the user's
		// short messages; the next third-party REGISTER subscribes anew.
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
		if err := g.store.DeleteRegSubscription(ctx, callID); err != nil {
			// The subscription stays until it expires; the HSS learns all
			// the same that the user takes no short messages here.
			log.Warn("ended reg event subscription not removed: the store failed", "error", err.Error())
		}
		if current.Available {
			g.updateHSS(&current, false, log)
		}
	}()
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

// reachableAgain is TS 23.204 6.5b step 2 with TS 23.040 3.2.6 item 2c:
// a request has come from a user whose short message this IP-SM-GW could
// not deliver, so the UE is there again and the HSS hears that the user
// can take short messages, which makes it alert the Service Centres that
// are waiting.
//
// Step 2d has the IP-SM-GW discard the notification where the UE is not
// registered in the IMS. The subscription to the user's registration
// state is what tells it: without one that says the user has a contact
// taking short messages, a short message would not get through now
// either.
//
// A notification the HSS would not take puts the flag back, so that the
// user's next sign of life tells it again. Nothing is lost if that never
// comes: the HSS keeps UNRI of its own, which the next registration of
// this IP-SM-GW for the user clears.
func (g *IPSMGW) reachableAgain(publicID string, log *slog.Logger) {
	ctx := context.Background()
	sub, ok, err := g.store.RegSubscriptionOf(ctx, publicID)
	switch {
	case err != nil:
		log.Warn("the registration state of the user was not read: the store failed", "error", err.Error())
		return
	case !ok || !sub.Available:
		// The notification is discarded, as step 2d has it.
		return
	}
	if !g.takeNotReachable(ctx, publicID, log) {
		return
	}
	log = log.With("msisdn", sub.MSISDN)
	if err := g.hss.NotifyReadyForSM(ctx, shIdentity(sub.PublicIdentity, sub.MSISDN), g.cfg.Number, sh.MSPresent); err != nil {
		log.Warn("the return of the user was not passed to the HSS", "error", err.Error())
		if err := g.store.PutNotReachable(ctx, publicID, time.Now().Add(notReachableLifetime)); err != nil {
			log.Warn("the unreachable flag of the user was not put back", "error", err.Error())
		}
		return
	}
	log.Info("the user can take short messages again; the HSS is told")
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
