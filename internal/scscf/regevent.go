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
	sub, unlock, ok, err := s.lockSubscription(context.Background(), sip.ReceivedDialogID(req))
	if ok {
		defer unlock()
		s.resubscribe(st, sub)
		return true
	}
	if !s.toNotifier(req.RequestURI) {
		return false
	}
	if err != nil {
		s.storeFailed(st, err, s.log.With("call_id", req.Get("Call-ID")))
		return true
	}
	// The subscription has ended, so its dialog is gone (RFC 3261
	// 12.2.2). RFC 6665 4.1.2.2 has the subscriber start anew.
	s.respond(st, 481, nil)
	return true
}

// toNotifier reports whether a request within a dialog is for the
// S-CSCF as the notifier: its Request-URI is the contact of the 200 and
// the NOTIFYs.
func (s *SCSCF) toNotifier(requestURI string) bool {
	u, err := sip.ParseURI(requestURI)
	return err == nil && strings.EqualFold(u.Host, s.cfg.Host) && (u.Port == s.cfg.Port || u.Port == 0 && s.cfg.Port == 5060)
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
	ctx := context.Background()
	// Step 0.
	reg, ok, err := s.store.RegistrationOf(ctx, resource)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if ok {
		unlock, err := s.users.Lock(ctx, reg.PrivateIdentity)
		if err != nil {
			s.lockFailed(st, err, log)
			return
		}
		defer unlock()
		// The binding may have changed while this waited for the lock.
		if reg, ok, err = s.store.RegistrationOf(ctx, resource); err != nil {
			s.storeFailed(st, err, log)
			return
		}
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
	// Steps 1B and 4.
	sub := Subscription{NotifierSubscription: accepted, PrivateIdentity: reg.PrivateIdentity, ICID: icidOf(req)}
	n, ok := s.acceptSubscribe(ctx, st, ok200, sub, regInfo(reg, reginfo.Active, reg.ContactEvent, time.Now()), log)
	if !ok {
		return
	}
	log.Info("reg event subscribed", "subscriber", subscriber, "public_identity", resource, "expires", int(expires/time.Second))
	s.sendNotify(n, log)
}

// acceptSubscribe answers a SUBSCRIBE with ok200 once the store holds
// sub as the NOTIFY with doc that follows the 200 leaves it, and returns
// that NOTIFY. A SUBSCRIBE whose subscription the store cannot keep gets
// 500 instead (RFC 3261 21.5.1), rather than a 200 whose NOTIFYs no
// instance could send; acceptSubscribe reports false then.
func (s *SCSCF) acceptSubscribe(ctx context.Context, st *sip.ServerTransaction, ok200 *sip.Message, sub Subscription, doc reginfo.Document, log *slog.Logger) (notification, bool) {
	n, err := s.nextNotify(sub, doc)
	if err != nil {
		log.Warn("reg event document not built", "error", err.Error())
		s.respond(st, 500, nil)
		return notification{}, false
	}
	if err := s.keepSubscription(ctx, n.sub); err != nil {
		s.storeFailed(st, err, log)
		return notification{}, false
	}
	s.addChargingVector(ok200, st.Request)
	st.Respond(ok200)
	return n, true
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
	ctx := context.Background()
	reg, ok, err := s.store.Registration(ctx, sub.PrivateIdentity)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	doc := reginfo.Document{}
	if ok {
		doc = regInfo(reg, reginfo.Active, reg.ContactEvent, time.Now())
	} else {
		sub.Terminate(sip.ReasonNoResource)
	}
	n, accepted := s.acceptSubscribe(ctx, st, ok200, sub, doc, log)
	if !accepted {
		return
	}
	if ok {
		log.Info("reg event subscription refreshed", "subscriber", sub.Dialog.RemoteURI, "expires", int(expires/time.Second))
	}
	s.sendNotify(n, log)
}

// notifyRegistration sends the new state of a registration that a
// REGISTER created or refreshed to every subscription to it (TS 24.229
// 5.4.2.1.2). A binding it replaced shows up once more, terminated.
func (s *SCSCF) notifyRegistration(ctx context.Context, reg Registration, replaced *Registration, log *slog.Logger) {
	now := time.Now()
	doc := regInfo(reg, reginfo.Active, reg.ContactEvent, now)
	if replaced != nil {
		old := contactOf(*replaced, reginfo.Terminated, reginfo.Unregistered, now)
		for i := range doc.Registrations {
			doc.Registrations[i].Contacts = append(doc.Registrations[i].Contacts, old)
		}
	}
	subs, err := s.store.SubscriptionsOf(ctx, reg.PrivateIdentity)
	if err != nil {
		log.Warn("reg event NOTIFYs not sent: the store failed; the subscribers learn the state from the next NOTIFY", "error", err.Error())
		return
	}
	for _, sub := range subs {
		s.notify(ctx, sub, doc, log)
	}
}

// endRegistration tells every subscription to a registration that has
// ended, with the event that ended its contact, and ends the
// subscriptions: the last NOTIFY of TS 24.229 5.4.2.1.2 has every
// registration terminated, and with it the subscription. Its reason is
// noresource: the UE subscribes anew after its next initial
// registration (5.1.1.3), not before (RFC 6665 4.1.3).
func (s *SCSCF) endRegistration(ctx context.Context, reg Registration, event reginfo.Event, log *slog.Logger) {
	doc := regInfo(reg, reginfo.Terminated, event, time.Now())
	subs, err := s.store.SubscriptionsOf(ctx, reg.PrivateIdentity)
	if err != nil {
		log.Warn("reg event NOTIFYs not sent: the store failed; the subscriptions end when they expire", "error", err.Error())
		return
	}
	for _, sub := range subs {
		sub.Terminate(sip.ReasonNoResource)
		s.notify(ctx, sub, doc, log)
	}
}

// subscriptionExpired ends a subscription that was not refreshed (RFC
// 6665 4.2.1.4). It returns the failure of the store or of the lock that
// left the subscription where it was, so that the expiry is handled
// again (handleExpiry).
func (s *SCSCF) subscriptionExpired(ctx context.Context, id sip.DialogID) error {
	log := s.log.With("call_id", id.CallID)
	sub, unlock, ok, err := s.lockSubscription(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer unlock()
	// Under the lock, the subscriber may have refreshed the subscription
	// since its expiry came up.
	if time.Now().Before(sub.Expires) {
		return nil
	}
	reg, registered, err := s.store.Registration(ctx, sub.PrivateIdentity)
	if err != nil {
		return err
	}
	sub.Terminate(sip.ReasonTimeout)
	doc := reginfo.Document{}
	if registered {
		doc = regInfo(reg, reginfo.Active, reg.ContactEvent, time.Now())
	}
	return s.notify(ctx, sub, doc, log)
}

// notify sends sub the next NOTIFY with doc, once the store holds what
// the NOTIFY changes. The lock of the user is held. It returns the
// failure of the store that stopped it from sending; a caller that sends
// to every subscription of a user goes on to the next one.
func (s *SCSCF) notify(ctx context.Context, sub Subscription, doc reginfo.Document, log *slog.Logger) error {
	n, err := s.nextNotify(sub, doc)
	if err != nil {
		log.Warn("reg event document not built", "error", err.Error())
		return nil
	}
	if err := s.keepSubscription(ctx, n.sub); err != nil {
		// Sent, the NOTIFY would leave the next one with its CSeq, which
		// goes up by one with each request of the dialog (RFC 3261
		// 12.2.1.1).
		log.Warn("reg event NOTIFY not sent: the store failed; the subscription stays as it was until the next event or its expiry",
			"subscriber", sub.Dialog.RemoteURI, "error", err.Error())
		return err
	}
	s.sendNotify(n, log)
	return nil
}

// notification is a NOTIFY of the reg event package, with the
// subscription as the NOTIFY leaves it and the document it carries.
type notification struct {
	req *sip.Message
	sub Subscription
	doc reginfo.Document
}

// nextNotify is the next NOTIFY of sub with doc (TS 24.229 5.4.2.1.2
// steps 1 to 5). It moves on the CSeq of the dialog and the version of
// the document.
func (s *SCSCF) nextNotify(sub Subscription, doc reginfo.Document) (notification, error) {
	doc.Version, doc.State = sub.Version, reginfo.Full
	sub.Version++
	body, err := doc.Marshal()
	if err != nil {
		return notification{}, err
	}
	req := sub.NewNotify(sip.Body{Type: reginfo.ContentType, Content: body}, time.Now())
	// Step 5: the icid-value of the SUBSCRIBE and this network's
	// orig-ioi.
	if sub.ICID != "" {
		req.Add("P-Charging-Vector", sip.ChargingVector{ICID: sub.ICID, OrigIOI: s.cfg.HomeDomain}.String())
	}
	return notification{req: req, sub: sub, doc: doc}, nil
}

// keepSubscription stores sub as a NOTIFY leaves it, and removes it
// when the NOTIFY ends it. Its expiry goes with it, in the store.
func (s *SCSCF) keepSubscription(ctx context.Context, sub Subscription) error {
	id := sub.Dialog.ID
	if sub.State == sip.SubscriptionTerminated {
		return s.store.DeleteSubscription(ctx, id)
	}
	return s.store.PutSubscription(ctx, sub)
}

// sendNotify sends a NOTIFY whose subscription the store holds as the
// NOTIFY leaves it.
func (s *SCSCF) sendNotify(n notification, log *slog.Logger) {
	state, contactState, event := registrationStateOf(n.doc)
	log.Info("reg event", "subscriber", n.sub.Dialog.RemoteURI, "public_identity", aorOf(n.doc), "state", string(state),
		"contact_state", string(contactState), "contact_event", string(event), "subscription_state", string(n.sub.State), "version", n.doc.Version)

	stack, err := s.stack.Get()
	if err != nil {
		return
	}
	targets, err := s.nextHop(n.req)
	if err != nil {
		log.Warn("cannot route the NOTIFY", "subscriber", n.sub.Dialog.RemoteURI, "error", err.Error())
		return
	}
	ct, err := stack.SendRequest(n.req, targets...)
	if err != nil {
		log.Warn("NOTIFY not sent", "subscriber", n.sub.Dialog.RemoteURI, "error", err.Error())
		return
	}
	go s.readNotifyAnswer(ct, n.sub.Dialog.ID, log)
}

// readNotifyAnswer removes the subscription when its NOTIFY fails the
// way RFC 6665 4.2.2 lists: no answer within Timer F, or a response
// that says the subscriber has lost it.
//
// SIMPLIFIED: TS 24.229 5.4.2.1.2. The term-ioi of the response is not
// stored: it is for the charging records of the S-CSCF, which no node
// of this project writes.
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
	ctx := context.Background()
	_, unlock, ok, err := s.lockSubscription(ctx, id)
	if err != nil {
		log.Warn("reg event subscription not removed after a failed NOTIFY: the store failed; it stays until it expires", "status", status, "error", err.Error())
		return
	}
	if !ok {
		return
	}
	defer unlock()
	if err := s.store.DeleteSubscription(ctx, id); err != nil {
		log.Warn("reg event subscription not removed after a failed NOTIFY: the store failed; it stays until it expires", "status", status, "error", err.Error())
		return
	}
	log.Info("reg event subscription removed after a failed NOTIFY", "status", status, "error", fmt.Sprint(ct.Err()))
}

// lockSubscription takes the lock of the user that a subscription is
// to, and reads the subscription again under it, since it may have
// changed or ended in the meantime. ok is false, and nothing is locked,
// when there is no such subscription, the lock was not taken or the
// store failed.
func (s *SCSCF) lockSubscription(ctx context.Context, id sip.DialogID) (sub Subscription, unlock func(), ok bool, err error) {
	sub, ok, err = s.store.Subscription(ctx, id)
	if err != nil || !ok {
		return Subscription{}, nil, false, err
	}
	if unlock, err = s.users.Lock(ctx, sub.PrivateIdentity); err != nil {
		return Subscription{}, nil, false, err
	}
	if sub, ok, err = s.store.Subscription(ctx, id); err != nil || !ok {
		unlock()
		return Subscription{}, nil, false, err
	}
	return sub, unlock, true, nil
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
