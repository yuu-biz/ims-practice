package scscf

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

// This file holds the S-CSCF as the notifier of the reg event package
// (TS 24.229 5.4.2.1, RFC 3680): it accepts the subscriptions of the UE
// and of its P-CSCF to the user's registration state, and sends each a
// NOTIFY with the full state whenever the registration changes.

const regEventPackage = "reg"

// regEventExpires is the duration of a subscription whose SUBSCRIBE asks
// for none (RFC 3680 4.4).
const regEventExpires = 3761 * time.Second

// contact is the S-CSCF's URI as the notifier, in the 200 to a
// SUBSCRIBE and in each NOTIFY.
func (s *SCSCF) contact() string { return fmt.Sprintf("sip:%s:%d", s.cfg.Host, s.cfg.Port) }

// receiveRegEvent takes a SUBSCRIBE to the reg event package that is for
// this S-CSCF: an initial one, or one within a subscription it holds. It
// reports false for any other request, which the S-CSCF routes.
func (s *SCSCF) receiveRegEvent(st *sip.ServerTransaction) bool {
	req := st.Request
	if req.Method != sip.SUBSCRIBE || sip.EventType(req.Get("Event")) != regEventPackage {
		return false
	}
	if !ims.IsInDialog(req) {
		s.subscribe(st)
		return true
	}
	if sub, unlock, ok := s.lockSubscription(sip.ReceivedDialogID(req)); ok {
		defer unlock()
		s.resubscribe(st, sub)
		return true
	}
	if u, err := sip.ParseURI(req.RequestURI); err == nil && strings.EqualFold(u.Host, s.cfg.Host) && (u.Port == s.cfg.Port || u.Port == 0 && s.cfg.Port == 5060) {
		// The subscription has ended, so its dialog is gone (RFC 3261
		// 12.2.2). RFC 6665 4.1.2.2 has the subscriber start anew.
		s.respond(st, 481, nil)
		return true
	}
	return false
}

// subscribe is TS 24.229 5.4.2.1.1 for an initial SUBSCRIBE.
func (s *SCSCF) subscribe(st *sip.ServerTransaction) {
	req := st.Request
	log := s.log.With("call_id", req.Get("Call-ID"))
	if !acceptsReginfo(req) {
		// RFC 3680 4.5: the NOTIFYs can only carry reginfo.
		s.respond(st, 406, nil)
		return
	}
	resource, err := ims.CanonicalURI(req.RequestURI)
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	// Step 0.
	reg, ok := s.store.RegistrationOf(resource)
	if ok {
		unlock, _ := s.users.Lock(context.Background(), reg.PrivateIdentity)
		defer unlock()
		// The binding may have changed while this waited for the lock.
		reg, ok = s.store.RegistrationOf(resource)
	}
	// A barred identity has no binding either (5.4.1.2.2 NOTE 9).
	if !ok || reg.Barred(resource) {
		log.Info("reg event subscription to a user with no binding", "public_identity", resource)
		s.respond(st, 480, nil)
		return
	}
	// Step 1.
	subscriber, ok := authorizedSubscriber(req, reg)
	if !ok {
		log.Info("reg event subscription refused", "public_identity", resource, "p_asserted_identity", req.Get("P-Asserted-Identity"))
		s.respond(st, 403, nil)
		return
	}
	expires, err := sip.SubscribeExpires(req, regEventExpires)
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	// Step 3: the S-CSCF keeps the duration the subscriber asked for.
	ok200, accepted, err := sip.AcceptSubscription(req, expires, s.contact(), time.Now())
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	s.addChargingVector(ok200, req)
	st.Respond(ok200)
	// Steps 1B and 4.
	sub := Subscription{NotifierSubscription: accepted, PrivateIdentity: reg.PrivateIdentity, ICID: icidOf(req)}
	log.Info("reg event subscribed", "subscriber", subscriber, "public_identity", resource, "expires", int(expires/time.Second))
	s.notify(sub, regInfo(reg, reginfo.Active, reg.ContactEvent, time.Now()), log)
}

// authorizedSubscriber is step 1 of TS 24.229 5.4.2.1.1. The subscriber
// is the identity in P-Asserted-Identity (NOTE 1): a public identity of
// the user, the P-CSCF that a Path of the registration names, or an
// application server of the user's criteria. Every application server
// of this project is in the trust domain. It returns that identity.
func authorizedSubscriber(req *sip.Message, reg Registration) (string, bool) {
	asserted, err := sip.ParseNameAddr(req.Get("P-Asserted-Identity"))
	if err != nil {
		return "", false
	}
	for _, own := range reg.NonBarredIdentities() {
		if ims.SamePublicIdentity(own, asserted.URI) {
			return asserted.URI, true
		}
	}
	for _, p := range reg.Path {
		if path, err := sip.ParseNameAddr(p); err == nil && sameURI(path.URI, asserted.URI) {
			return asserted.URI, true
		}
	}
	for _, c := range reg.InitialFilterCriteria {
		if sameURI(c.ApplicationServer.ServerName, asserted.URI) {
			return asserted.URI, true
		}
	}
	return "", false
}

// acceptsReginfo reports whether the NOTIFYs may carry reginfo: an
// Accept, when there is one, MUST list it (RFC 3680 4.5).
func acceptsReginfo(req *sip.Message) bool {
	types := req.GetAll("Accept")
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		mediaType, _, _ := strings.Cut(t, ";")
		if strings.EqualFold(strings.TrimSpace(mediaType), reginfo.ContentType) {
			return true
		}
	}
	return false
}

// icidOf is the icid-value of the request's P-Charging-Vector.
func icidOf(req *sip.Message) string {
	c, _ := sip.ChargingVectorOf(req)
	return c.ICID
}

// resubscribe is a SUBSCRIBE within a subscription: a refresh, or an
// unsubscribe with Expires 0 (RFC 6665 4.2.1.4). TS 24.229 5.4.2.1.1
// accepts a refresh from the UE whether or not the user is still
// registered; here a subscription ends with the registration, so one
// that is still held always has its registration.
func (s *SCSCF) resubscribe(st *sip.ServerTransaction, sub Subscription) {
	req := st.Request
	log := s.log.With("call_id", req.Get("Call-ID"))
	expires, err := sip.SubscribeExpires(req, regEventExpires)
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	ok200, err := sub.ReceiveSubscribe(req, expires, time.Now())
	switch {
	case errors.Is(err, sip.ErrBadEvent):
		s.respond(st, 489, nil)
		return
	case errors.Is(err, sip.ErrOutOfOrder):
		s.respond(st, 500, nil)
		return
	case err != nil:
		s.respond(st, 400, nil)
		return
	}
	s.addChargingVector(ok200, req)
	st.Respond(ok200)
	reg, ok := s.store.Registration(sub.PrivateIdentity)
	if !ok {
		sub.Terminate(sip.ReasonNoResource)
		s.notify(sub, reginfo.Document{}, log)
		return
	}
	log.Info("reg event subscription refreshed", "subscriber", sub.Dialog.RemoteURI, "expires", int(expires/time.Second))
	s.notify(sub, regInfo(reg, reginfo.Active, reg.ContactEvent, time.Now()), log)
}

// notifyRegistration sends the new state of a registration that a
// REGISTER created or refreshed to every subscription to it (TS 24.229
// 5.4.2.1.2). A binding it replaced shows up once more, terminated.
func (s *SCSCF) notifyRegistration(reg Registration, replaced *Registration, log *slog.Logger) {
	now := time.Now()
	doc := regInfo(reg, reginfo.Active, reg.ContactEvent, now)
	if replaced != nil {
		old := contactOf(*replaced, reginfo.Terminated, reginfo.Unregistered, now)
		for i := range doc.Registrations {
			doc.Registrations[i].Contacts = append(doc.Registrations[i].Contacts, old)
		}
	}
	for _, sub := range s.store.SubscriptionsOf(reg.PrivateIdentity) {
		s.notify(sub, doc, log)
	}
}

// endRegistration tells every subscription to a registration that has
// ended, with the event that ended its contact, and ends the
// subscriptions: the last NOTIFY of TS 24.229 5.4.2.1.2 has every
// registration terminated, and with it the subscription. Its reason is
// noresource: the UE subscribes anew after its next initial
// registration (5.1.1.3), not before (RFC 6665 4.1.3).
func (s *SCSCF) endRegistration(reg Registration, event reginfo.Event, log *slog.Logger) {
	doc := regInfo(reg, reginfo.Terminated, event, time.Now())
	for _, sub := range s.store.SubscriptionsOf(reg.PrivateIdentity) {
		sub.Terminate(sip.ReasonNoResource)
		s.notify(sub, doc, log)
	}
}

// subscriptionExpired ends a subscription that was not refreshed (RFC
// 6665 4.2.1.4).
func (s *SCSCF) subscriptionExpired(id sip.DialogID, expires time.Time) {
	sub, unlock, ok := s.lockSubscription(id)
	if !ok {
		return
	}
	defer unlock()
	if !sub.Expires.Equal(expires) {
		return
	}
	log := s.log.With("call_id", id.CallID)
	sub.Terminate(sip.ReasonTimeout)
	doc := reginfo.Document{}
	if reg, ok := s.store.Registration(sub.PrivateIdentity); ok {
		doc = regInfo(reg, reginfo.Active, reg.ContactEvent, time.Now())
	}
	s.notify(sub, doc, log)
}

// notify sends sub the next NOTIFY with doc (TS 24.229 5.4.2.1.2 steps 1
// to 5), and stores what the NOTIFY changed: the CSeq of the dialog and
// the version of the document. A subscription that the NOTIFY ends is
// removed. The lock of the user is held.
func (s *SCSCF) notify(sub Subscription, doc reginfo.Document, log *slog.Logger) {
	now := time.Now()
	doc.Version, doc.State = sub.Version, reginfo.Full
	sub.Version++
	body, err := doc.Marshal()
	if err != nil {
		log.Warn("reg event document not built", "error", err.Error())
		return
	}
	req := sub.NewNotify(sip.Body{Type: reginfo.ContentType, Content: body}, now)
	// Step 5: the icid-value of the SUBSCRIBE and this network's
	// orig-ioi.
	if sub.ICID != "" {
		req.Add("P-Charging-Vector", sip.ChargingVector{ICID: sub.ICID, OrigIOI: s.cfg.HomeDomain}.String())
	}
	id := sub.Dialog.ID
	if sub.State == sip.SubscriptionTerminated {
		s.store.DeleteSubscription(id)
		s.subscriptionTimers.stop(id)
	} else {
		s.store.PutSubscription(sub)
		s.subscriptionTimers.start(id, sub.Expires.Sub(now), func() { s.subscriptionExpired(id, sub.Expires) })
	}
	state, contactState, event := registrationStateOf(doc)
	log.Info("reg event", "subscriber", sub.Dialog.RemoteURI, "public_identity", aorOf(doc), "state", string(state),
		"contact_state", string(contactState), "contact_event", string(event), "subscription_state", string(sub.State), "version", doc.Version)

	stack, err := s.stack.Get()
	if err != nil {
		return
	}
	dst, err := s.nextHop(req)
	if err != nil {
		log.Warn("cannot route the NOTIFY", "subscriber", sub.Dialog.RemoteURI, "error", err.Error())
		return
	}
	ct, err := stack.SendRequest(req, dst)
	if err != nil {
		log.Warn("NOTIFY not sent", "subscriber", sub.Dialog.RemoteURI, "error", err.Error())
		return
	}
	go s.readNotifyAnswer(ct, id, log)
}

// readNotifyAnswer removes the subscription when its NOTIFY fails the
// way RFC 6665 4.2.2 lists: no answer within Timer F, or a response
// that says the subscriber has lost it.
func (s *SCSCF) readNotifyAnswer(ct *sip.ClientTransaction, id sip.DialogID, log *slog.Logger) {
	var final *sip.Message
	for resp := range ct.Responses() {
		final = resp
	}
	if final != nil && !sip.EndsSubscription(final.StatusCode) {
		return
	}
	status := 0
	if final != nil {
		status = final.StatusCode
	}
	_, unlock, ok := s.lockSubscription(id)
	if !ok {
		return
	}
	defer unlock()
	log.Info("reg event subscription removed after a failed NOTIFY", "status", status, "error", fmt.Sprint(ct.Err()))
	s.store.DeleteSubscription(id)
	s.subscriptionTimers.stop(id)
}

// lockSubscription takes the lock of the user that a subscription is
// to, and reads the subscription again under it, since it may have
// changed or ended in the meantime. ok is false, and nothing is locked,
// when there is no such subscription.
func (s *SCSCF) lockSubscription(id sip.DialogID) (sub Subscription, unlock func(), ok bool) {
	sub, ok = s.store.Subscription(id)
	if !ok {
		return Subscription{}, nil, false
	}
	unlock, _ = s.users.Lock(context.Background(), sub.PrivateIdentity)
	if sub, ok = s.store.Subscription(id); !ok {
		unlock()
		return Subscription{}, nil, false
	}
	return sub, unlock, true
}

// regInfo is the full state of a registration, which every NOTIFY of
// the S-CSCF carries (TS 24.229 5.4.2.1.2): one <registration> for each
// public identity of the implicit set that is not barred, all with the
// user's one contact.
// state is that of every registration, and event that of the contact.
func regInfo(reg Registration, state reginfo.State, event reginfo.Event, now time.Time) reginfo.Document {
	var doc reginfo.Document
	for i, id := range reg.NonBarredIdentities() {
		doc.Registrations = append(doc.Registrations, reginfo.Registration{
			AOR: id,
			// RFC 3680 5.1: the same id for the same address-of-record in
			// every NOTIFY. The implicit set keeps its order.
			ID:       "reg" + strconv.Itoa(i+1),
			State:    state,
			Contacts: []reginfo.Contact{contactOf(reg, state, event, now)},
		})
	}
	return doc
}

// contactOf is the <contact> of a registration's binding, in state with
// event. An active one says how many seconds it has left.
func contactOf(reg Registration, state reginfo.State, event reginfo.Event, now time.Time) reginfo.Contact {
	c := reginfo.Contact{ID: reg.ContactID, State: state, Event: event, CallID: reg.CallID, CSeq: reg.CSeq, URI: reg.Contact}
	// RFC 3680 5.1: q has an attribute of its own; every other parameter
	// that RFC 3261 does not define, such as a feature tag, is an
	// <unknown-param>, empty when the parameter has no value (TS 24.229
	// 5.4.2.1.2 step 4 e).
	for _, p := range reg.ContactParams {
		if strings.EqualFold(p.Name, "q") {
			c.Q = p.Value
			continue
		}
		c.UnknownParams = append(c.UnknownParams, reginfo.UnknownParam{Name: p.Name, Value: p.Value})
	}
	if state == reginfo.Active {
		c.Expires = uint64(max(reg.Expires.Sub(now).Round(time.Second), 0) / time.Second)
	}
	return c
}

// registrationStateOf is what the log tells of a document: the state of
// the first registration, and of its first contact.
func registrationStateOf(doc reginfo.Document) (reginfo.State, reginfo.State, reginfo.Event) {
	if len(doc.Registrations) == 0 {
		return "", "", ""
	}
	r := doc.Registrations[0]
	if len(r.Contacts) == 0 {
		return r.State, "", ""
	}
	return r.State, r.Contacts[0].State, r.Contacts[0].Event
}

func aorOf(doc reginfo.Document) string {
	if len(doc.Registrations) == 0 {
		return ""
	}
	return doc.Registrations[0].AOR
}
