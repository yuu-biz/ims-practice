package sip

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file holds the subscriber's side of a subscription (RFC 6665
// 4.1): the SUBSCRIBE that asks for it, the NOTIFYs that report the
// state, the refreshes that keep it, and its end. A Subscriber keeps the
// subscriptions of one node. A UA has one for its user (UA.Subscribe);
// a proxy that subscribes on its own behalf, such as a P-CSCF, makes
// one on its own stack.

// Subscriber keeps the subscriptions that one node made, and finds the
// one that a NOTIFY belongs to.
type Subscriber struct {
	contact string
	locate  func(URI) ([]Destination, error)
	log     *slog.Logger

	mu   sync.Mutex
	subs map[subscriptionKey]*Subscription
}

// subscriptionKey matches a NOTIFY to its subscription: the Call-ID and
// the From tag of the SUBSCRIBE, which come back as the Call-ID and the
// To tag of the NOTIFY (RFC 6665 4.4.1). The From tag of the NOTIFY is
// not part of it, since the first NOTIFY may come before the 200 that
// carries that tag (4.1.2.4).
type subscriptionKey struct {
	callID   string
	localTag string
}

// NewSubscriber makes a Subscriber. contact is the SIP URI it writes in
// Contact, where the NOTIFYs come. locate gives the targets of a
// request for the URI that NextHop picks from it, as UAConfig.Locate
// does; nil means that the host of that URI is an IP address.
func NewSubscriber(contact string, locate func(URI) ([]Destination, error), log *slog.Logger) *Subscriber {
	if locate == nil {
		locate = locateIPAddress
	}
	return &Subscriber{contact: contact, locate: locate, log: log, subs: map[subscriptionKey]*Subscription{}}
}

// SubscribeRequest is what a subscriber puts in an initial SUBSCRIBE
// (RFC 6665 4.1.2.1), and how it keeps the subscription.
type SubscribeRequest struct {
	// RequestURI is the resource, such as the public identity whose
	// registration state is wanted.
	RequestURI string
	// From is the subscriber's URI, and To the resource's; an empty To
	// means RequestURI.
	From string
	To   string
	// Route is the preloaded route set, top first, as in Invitation.
	Route []string
	// Event names the event package, such as "reg" (RFC 6665 3.1.2).
	Event   string
	Expires time.Duration
	// Accept is the body type the NOTIFYs are to use. Empty leaves it to
	// the default of the event package (RFC 6665 3.1.3).
	Accept string
	// Headers are further header fields, such as P-Asserted-Identity.
	Headers []HeaderField
	// RefreshAfter gives, for the duration a subscription has, when to
	// refresh it. Nil means never: the subscription ends when it
	// expires. RFC 6665 4.1.2.2 leaves the time to the subscriber.
	RefreshAfter func(time.Duration) time.Duration
	// OnNotify is called for every NOTIFY of the subscription, the last
	// one included, before the 200 to it goes out. It may add header
	// fields to resp, that 200. It must return quickly: a subscriber
	// MUST NOT wait for a user before it answers (RFC 6665 4.1.3).
	OnNotify func(n Notification, resp *Message)
}

// Notification is one NOTIFY as the subscriber sees it.
type Notification struct {
	Request *Message
	State   SubscriptionState
	Body    Body
}

// Errors with which a subscription ends without a NOTIFY that says
// terminated.
var (
	// ErrNoNotify: Timer N fired, 64*T1 after a SUBSCRIBE, before any
	// NOTIFY came (RFC 6665 4.1.2.2, 4.1.2.4).
	ErrNoNotify = errors.New("sip: no NOTIFY for the SUBSCRIBE")
	// ErrSubscriptionExpired: the subscription was not refreshed in time.
	ErrSubscriptionExpired = errors.New("sip: the subscription expired")
)

// SubscribeRefused is how a subscription ends whose SUBSCRIBE got a
// final response from 300 to 699: StatusCode tells the subscriber what
// to do next, such as subscribe within a dialog after a 403 (TS 24.147
// 5.3.1.2).
type SubscribeRefused struct {
	StatusCode int
	Reason     string
}

func (e *SubscribeRefused) Error() string {
	return fmt.Sprintf("sip: SUBSCRIBE answered %d %s", e.StatusCode, e.Reason)
}

// Subscription is a subscription this node made, from the subscriber's
// side (RFC 6665 4.1.2). Its methods may be called from any goroutine.
type Subscription struct {
	subscriber *Subscriber
	stack      *Stack
	req        SubscribeRequest
	key        subscriptionKey
	done       chan struct{}

	// notifying lets one NOTIFY at a time through OnNotify, so that they
	// reach the user in the order they arrived.
	notifying sync.Mutex

	// Guarded by mu.
	mu sync.Mutex
	// subscribe is the initial SUBSCRIBE, which the dialog is built from.
	subscribe *Message
	// dialog is nil until the first NOTIFY creates it (RFC 6665 4.4.1).
	dialog *Dialog
	state  SubState
	// granted is the Expires of the last 2xx to a SUBSCRIBE, for a
	// NOTIFY that carries no expires (TS 24.229 5.1.1.3 item 2).
	granted time.Duration
	// timerN, refresh and expiry are nil while they are not running.
	timerN  *time.Timer
	refresh *time.Timer
	expiry  *time.Timer
	err     error
}

// Subscribe sends an initial SUBSCRIBE on stack. The subscription waits
// in notify_wait for the first NOTIFY; OnNotify reports it and every
// later one.
func (s *Subscriber) Subscribe(stack *Stack, r SubscribeRequest) (*Subscription, error) {
	to := r.To
	if to == "" {
		to = r.RequestURI
	}
	req := &Message{Method: SUBSCRIBE, RequestURI: r.RequestURI}
	for _, route := range r.Route {
		req.Add("Route", route)
	}
	req.Add("Max-Forwards", "70")
	req.Add("From", withTag(r.From, NewTag()))
	req.Add("To", withTag(to, ""))
	req.Add("Call-ID", NewCallID())
	req.Add("CSeq", CSeq{Number: 1, Method: SUBSCRIBE}.String())
	for _, h := range r.Headers {
		req.Add(h.Name, h.Value)
	}

	sub := &Subscription{
		subscriber: s,
		stack:      stack,
		req:        r,
		key:        subscriptionKey{callID: req.Get("Call-ID"), localTag: tagOf(req.Get("From"))},
		done:       make(chan struct{}),
		subscribe:  req,
		state:      SubscriptionInit,
	}
	s.mu.Lock()
	s.subs[sub.key] = sub
	s.mu.Unlock()

	sub.mu.Lock()
	defer sub.mu.Unlock()
	sub.moveTo(SubscriptionNotifyWait, "SUBSCRIBE sent")
	if err := sub.send(req, r.Expires, true); err != nil {
		sub.end("SUBSCRIBE not sent", err)
		return nil, err
	}
	return sub, nil
}

// ReceiveRequest takes a NOTIFY for one of the subscriptions, answers it
// and reports true. For any other request it reports false and leaves
// it to the caller.
func (s *Subscriber) ReceiveRequest(st *ServerTransaction) bool {
	sub := s.find(st.Request)
	if sub == nil {
		return false
	}
	sub.receiveNotify(st)
	return true
}

// find is the subscription that req, a NOTIFY, belongs to, or nil.
func (s *Subscriber) find(req *Message) *Subscription {
	if req.Method != NOTIFY {
		return nil
	}
	key := subscriptionKey{callID: req.Get("Call-ID"), localTag: tagOf(req.Get("To"))}
	if key.localTag == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subs[key]
}

// Done is closed when the subscription ends.
func (sub *Subscription) Done() <-chan struct{} { return sub.done }

// Err tells why the subscription ended, once Done is closed. It is nil
// when a NOTIFY said terminated, which is how a subscription normally
// ends, and when it was ended by Unsubscribe before any NOTIFY came.
// Otherwise it is ErrNoNotify, ErrSubscriptionExpired, or the failure of
// the initial SUBSCRIBE or of a refresh.
func (sub *Subscription) Err() error {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return sub.err
}

// Unsubscribe sends a SUBSCRIBE with Expires 0 within the dialog (RFC
// 6665 4.1.2.3). The subscription ends with the NOTIFY that answers it.
// Before the first NOTIFY there is no dialog to send it in, and the
// subscription ends here; a NOTIFY that comes later gets 481.
func (sub *Subscription) Unsubscribe() error {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	select {
	case <-sub.done:
		return nil
	default:
	}
	if sub.dialog == nil {
		sub.end("unsubscribed before any NOTIFY", nil)
		return nil
	}
	stop(&sub.refresh)
	sub.moveTo(sub.state, "unsubscribe sent")
	return sub.send(sub.dialog.NewRequest(SUBSCRIBE), 0, false)
}

// send completes req, an initial SUBSCRIBE or one within the dialog,
// and sends it. Timer N starts with it: every SUBSCRIBE that succeeds is
// followed by a NOTIFY (RFC 6665 4.1.2.2, 4.1.2.4). sub.mu is held.
func (sub *Subscription) send(req *Message, expires time.Duration, initial bool) error {
	req.Add("Contact", NameAddr{URI: sub.subscriber.contact}.String())
	req.Add("Event", sub.req.Event)
	req.Add("Expires", strconv.Itoa(int(expires/time.Second)))
	if sub.req.Accept != "" {
		req.Add("Accept", sub.req.Accept)
	}
	hop, err := NextHop(req)
	if err != nil {
		return err
	}
	targets, err := sub.subscriber.locate(hop)
	if err != nil {
		return err
	}
	ct, err := sub.stack.SendRequest(req, targets...)
	if err != nil {
		return err
	}
	sub.start(&sub.timerN, 64*sub.stack.cfg.Timers.T1, func() { sub.end("Timer N fired", ErrNoNotify) })
	go sub.readAnswer(ct, initial)
	return nil
}

// readAnswer reads the final response to a SUBSCRIBE (RFC 6665 4.1.2.1,
// 4.1.2.2).
func (sub *Subscription) readAnswer(ct *ClientTransaction, initial bool) {
	var final *Message
	for resp := range ct.Responses() {
		if resp.StatusCode >= 200 {
			final = resp
		}
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	select {
	case <-sub.done:
		return
	default:
	}
	var failure error
	switch {
	case final == nil:
		failure = fmt.Errorf("sip: no answer to SUBSCRIBE: %w", ct.Err())
	case final.StatusCode >= 300:
		failure = &SubscribeRefused{StatusCode: final.StatusCode, Reason: final.Reason}
	default:
		if n, err := ParseDeltaSeconds(final.Get("Expires")); err == nil {
			// 3.1.1: the Expires of the 2xx is the duration. A NOTIFY that
			// says otherwise overrides it (4.1.3).
			sub.granted = time.Duration(n) * time.Second
			if sub.dialog != nil && sub.granted > 0 {
				sub.schedule(sub.granted)
			}
		}
		return
	}
	switch {
	case initial:
		// 4.1.2.1: no subscription was created.
		sub.end(fmt.Sprintf("%d received", statusOf(final)), failure)
	case final != nil && EndsSubscription(final.StatusCode):
		sub.end(fmt.Sprintf("%d to the refresh", final.StatusCode), failure)
	default:
		// 4.1.2.2: the subscription stays valid until it expires.
		stop(&sub.timerN)
		sub.subscriber.log.Warn("subscription refresh failed", "call_id", sub.key.callID, "event", sub.req.Event, "error", failure.Error())
	}
}

func statusOf(resp *Message) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// receiveNotify is RFC 6665 4.1.3 for a NOTIFY of this subscription.
func (sub *Subscription) receiveNotify(st *ServerTransaction) {
	sub.notifying.Lock()
	defer sub.notifying.Unlock()
	sub.mu.Lock()
	n, resp, ends, ok := sub.acceptNotify(st)
	sub.mu.Unlock()
	if !ok {
		return
	}
	if sub.req.OnNotify != nil {
		sub.req.OnNotify(n, resp)
	}
	st.Respond(resp)
	if ends {
		sub.mu.Lock()
		sub.end("NOTIFY terminated received", nil)
		sub.mu.Unlock()
	}
}

// acceptNotify checks a NOTIFY and moves the subscription on. It
// answers a NOTIFY it refuses itself and reports ok false; otherwise it
// returns the 200 to send, and ends reports a NOTIFY that ends the
// subscription. sub.mu is held.
func (sub *Subscription) acceptNotify(st *ServerTransaction) (n Notification, resp *Message, ends, ok bool) {
	req := st.Request
	refuse := func(code int) (Notification, *Message, bool, bool) {
		st.Respond(NewResponse(req, code, ""))
		return Notification{}, nil, false, false
	}
	if sub.state == SubscriptionTerminated {
		return refuse(481)
	}
	if !sameEvent(req.Get("Event"), sub.req.Event) {
		// 4.1.3: the subscription is for another event package.
		return refuse(489)
	}
	state, err := ParseSubscriptionState(req.Get("Subscription-State"))
	if err != nil {
		return refuse(400)
	}
	if sub.dialog == nil {
		d, err := NewSubscriberDialog(sub.subscribe)
		if err == nil {
			err = d.ReceiveFirstNotify(req)
		}
		if err != nil {
			return refuse(400)
		}
		// 4.4.1: a NOTIFY that says terminated creates no dialog.
		if state.State != SubscriptionTerminated {
			sub.dialog = &d
			sub.subscriber.log.Info("sip dialog", "call_id", d.ID.CallID, "local_tag", d.ID.LocalTag, "remote_tag", d.ID.RemoteTag,
				"event", "NOTIFY received", "from", "", "to", d.State.String())
		}
	} else {
		if tagOf(req.Get("From")) != sub.dialog.ID.RemoteTag {
			// SIMPLIFIED: RFC 6665 4.1.4 and 5.4.9. A forked SUBSCRIBE may
			// be accepted by several notifiers, each with a dialog of its
			// own. The reg event package forbids that (RFC 3680 4.9), and
			// it is the only package here, so a NOTIFY from a second
			// dialog gets 481.
			return refuse(481)
		}
		before := *sub.dialog
		if err := sub.dialog.ReceiveRequest(req); err != nil {
			*sub.dialog = before
			if errors.Is(err, ErrOutOfOrder) {
				return refuse(500)
			}
			return refuse(400)
		}
	}
	stop(&sub.timerN)
	switch state.State {
	case SubscriptionActive, SubscriptionPending:
		if sub.state != state.State {
			sub.moveTo(state.State, "NOTIFY received")
		}
		// 4.1.3: the expires of the NOTIFY is the time left, and 0 means
		// that the subscription ends now.
		duration := state.Expires
		if !HasExpires(req.Get("Subscription-State")) {
			// TS 24.229 5.1.1.3 item 2 and 5.2.3: without expires, the
			// 2xx tells the duration, and failing that the request.
			duration = orElse(sub.granted, sub.req.Expires)
		}
		sub.schedule(duration)
	case SubscriptionTerminated:
		ends = true
	}
	n = Notification{Request: req, State: state, Body: Body{Type: req.Get("Content-Type"), Content: req.Body}}
	return n, NewResponse(req, 200, ""), ends, true
}

// HasExpires reports whether a Subscription-State value has the
// expires parameter. SubscriptionState.Expires is zero both without it
// and with expires=0, which ends the subscription (RFC 6665 4.1.3).
func HasExpires(subscriptionState string) bool {
	_, rest, _ := strings.Cut(subscriptionState, ";")
	_, ok := parseParams(rest).Get("expires")
	return ok
}

// orElse returns a unless it is zero, and then b.
func orElse(a, b time.Duration) time.Duration {
	if a != 0 {
		return a
	}
	return b
}

// schedule sets when the subscription expires, and when to refresh it
// before that. sub.mu is held.
func (sub *Subscription) schedule(duration time.Duration) {
	sub.start(&sub.expiry, duration, func() { sub.end("expired", ErrSubscriptionExpired) })
	stop(&sub.refresh)
	if sub.req.RefreshAfter == nil || sub.dialog == nil {
		return
	}
	if after := sub.req.RefreshAfter(duration); after < duration {
		sub.start(&sub.refresh, after, sub.sendRefresh)
	}
}

// sendRefresh is RFC 6665 4.1.2.2: a SUBSCRIBE within the dialog with
// the duration of the initial one. sub.mu is held.
func (sub *Subscription) sendRefresh() {
	sub.moveTo(sub.state, "refresh sent")
	if err := sub.send(sub.dialog.NewRequest(SUBSCRIBE), sub.req.Expires, false); err != nil {
		sub.subscriber.log.Warn("subscription refresh not sent", "call_id", sub.key.callID, "event", sub.req.Event, "error", err.Error())
	}
}

// start runs f with sub.mu held after d, unless the timer in slot was
// stopped or replaced first, or the subscription ended. sub.mu is held.
func (sub *Subscription) start(slot **time.Timer, d time.Duration, f func()) {
	stop(slot)
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		sub.mu.Lock()
		defer sub.mu.Unlock()
		if *slot != t || sub.state == SubscriptionTerminated {
			return
		}
		*slot = nil
		f()
	})
	*slot = t
}

func stop(slot **time.Timer) {
	if *slot != nil {
		(*slot).Stop()
		*slot = nil
	}
}

// end ends the subscription with err; only the first end counts. sub.mu
// is held.
func (sub *Subscription) end(event string, err error) {
	select {
	case <-sub.done:
		return
	default:
	}
	sub.moveTo(SubscriptionTerminated, event)
	stop(&sub.timerN)
	stop(&sub.refresh)
	stop(&sub.expiry)
	sub.subscriber.mu.Lock()
	delete(sub.subscriber.subs, sub.key)
	sub.subscriber.mu.Unlock()
	sub.err = err
	if err != nil {
		sub.subscriber.log.Info("subscription failed", "call_id", sub.key.callID, "event_package", sub.req.Event, "error", err.Error())
	}
	close(sub.done)
}

// moveTo puts the subscription in state to, after what happened in
// event, and logs the step; to may be the state it is in. sub.mu is
// held.
func (sub *Subscription) moveTo(to SubState, event string) {
	sub.subscriber.log.Info("sip subscription", "call_id", sub.key.callID, "event_package", sub.req.Event,
		"event", event, "from", string(sub.state), "to", string(to))
	sub.state = to
}
