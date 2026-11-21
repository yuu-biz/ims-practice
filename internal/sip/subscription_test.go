package sip

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var fastTimers = Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}

// testNotifier answers SUBSCRIBEs on a stack of its own with the
// notifier's side of this package, the way an S-CSCF does.
type testNotifier struct {
	t     *testing.T
	stack *Stack
	// subscribes gets every SUBSCRIBE that reaches the notifier.
	subscribes chan *Message

	// Set before the first SUBSCRIBE.
	recordRoute string // a Record-Route that a proxy would add to each NOTIFY
	notifyFirst bool   // send the first NOTIFY before the 200
	noNotify    bool   // answer 200 and never NOTIFY
	refuse      int    // answer the initial SUBSCRIBE with this code

	mu  sync.Mutex
	sub NotifierSubscription
}

const notifierContact = "sip:notifier.example.org"

var state = Body{Type: "application/test+xml", Content: []byte("<state/>")}

func newTestNotifier(t *testing.T) *testNotifier {
	n := &testNotifier{t: t, subscribes: make(chan *Message, 8)}
	n.stack = newStack(t, fastTimers, n.receive)
	return n
}

func (n *testNotifier) receive(st *ServerTransaction) {
	req := st.Request
	if req.Method != SUBSCRIBE {
		st.Respond(NewResponse(req, 405, NewTag()))
		return
	}
	n.subscribes <- req
	expires, err := SubscribeExpires(req, time.Hour)
	if err != nil {
		st.Respond(NewResponse(req, 400, NewTag()))
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var ok *Message
	if tagOf(req.Get("To")) == "" {
		if n.refuse != 0 {
			st.Respond(NewResponse(req, n.refuse, NewTag()))
			return
		}
		ok, n.sub, err = AcceptSubscription(req, expires, notifierContact, time.Now())
	} else {
		ok, err = n.sub.ReceiveSubscribe(req, expires, time.Now())
	}
	if err != nil {
		n.t.Errorf("SUBSCRIBE refused: %v", err)
		st.Respond(NewResponse(req, 500, NewTag()))
		return
	}
	if n.notifyFirst {
		// The answer to the NOTIFY shows that the subscriber has handled
		// it before the 200 to the SUBSCRIBE leaves.
		for range n.notifyLocked().Responses() {
		}
	}
	st.Respond(ok)
	if !n.notifyFirst && !n.noNotify {
		n.notifyLocked()
	}
	n.notifyFirst = false
}

// notify sends a NOTIFY with the subscription's state as it is.
func (n *testNotifier) notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notifyLocked()
}

func (n *testNotifier) terminate(reason string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sub.Terminate(reason)
	n.notifyLocked()
}

func (n *testNotifier) notifyLocked() *ClientTransaction {
	req := n.sub.NewNotify(state, time.Now())
	if n.recordRoute != "" {
		req.Add("Record-Route", n.recordRoute)
	}
	hop, err := NextHop(req)
	if err != nil {
		n.t.Fatal(err)
	}
	dst, err := locateIPAddress(hop)
	if err != nil {
		n.t.Fatal(err)
	}
	ct, err := n.stack.SendRequest(req, dst)
	if err != nil {
		n.t.Fatal(err)
	}
	return ct
}

// subscriberUA runs a UA whose requests go to n, and sets the OnNotify
// of r to collect what it sees.
func subscriberUA(t *testing.T, n *testNotifier, r *SubscribeRequest) (*UA, chan Notification) {
	t.Helper()
	tr := listenLayer(t, testLogger(t))
	notifier := n.stack.LocalAddr()
	ua, err := NewUA(tr, UAConfig{
		SentBy:  tr.LocalAddr().String(),
		Timers:  fastTimers,
		Contact: "sip:subscriber@" + tr.LocalAddr().String(),
		OnCall:  noCalls,
		// Every request goes to the notifier, whatever its Route says,
		// as if one proxy stood in between.
		Locate: func(URI) (Destination, error) { return Destination{Transport: UDP, Addr: notifier}, nil },
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	got := make(chan Notification, 8)
	r.OnNotify = func(n Notification, resp *Message) { got <- n }
	return ua, got
}

func subscribeRequest() SubscribeRequest {
	return SubscribeRequest{
		RequestURI: "sip:resource@example.org",
		From:       "sip:subscriber@example.org",
		Event:      "reg",
		Expires:    time.Hour,
	}
}

func nextNotification(t *testing.T, got <-chan Notification) Notification {
	t.Helper()
	select {
	case n := <-got:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no NOTIFY reached the subscriber")
		return Notification{}
	}
}

func nextSubscribe(t *testing.T, n *testNotifier) *Message {
	t.Helper()
	select {
	case m := <-n.subscribes:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no SUBSCRIBE reached the notifier")
		return nil
	}
}

func waitEnded(t *testing.T, sub *Subscription) {
	t.Helper()
	select {
	case <-sub.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription never ended")
	}
}

func TestSubscriptionIsRefreshedThroughTheRouteOfTheNOTIFYAndEnds(t *testing.T) {
	n := newTestNotifier(t)
	n.recordRoute = "<sip:proxy.example.org;lr>"
	r := subscribeRequest()
	r.Expires = 2 * time.Second
	r.RefreshAfter = func(d time.Duration) time.Duration { return d / 4 }
	ua, got := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}

	initial := nextSubscribe(t, n)
	if initial.Get("Event") != "reg" || initial.Get("Expires") != "2" {
		t.Errorf("SUBSCRIBE Event %q, Expires %q", initial.Get("Event"), initial.Get("Expires"))
	}
	first := nextNotification(t, got)
	if first.State.State != SubscriptionActive || string(first.Body.Content) != "<state/>" {
		t.Fatalf("first NOTIFY: %+v", first.State)
	}

	refresh := nextSubscribe(t, n)
	if tagOf(refresh.Get("To")) == "" || refresh.Get("Call-ID") != initial.Get("Call-ID") {
		t.Errorf("refresh is not within the dialog: To %q", refresh.Get("To"))
	}
	if refresh.Get("Route") != "<sip:proxy.example.org;lr>" {
		t.Errorf("refresh Route = %q, want the Record-Route of the NOTIFY", refresh.Get("Route"))
	}
	if c, _ := ParseCSeq(refresh.Get("CSeq")); c.Number != 2 {
		t.Errorf("refresh CSeq = %q", refresh.Get("CSeq"))
	}
	if again := nextNotification(t, got); again.State.State != SubscriptionActive {
		t.Errorf("NOTIFY after the refresh: %+v", again.State)
	}

	n.terminate(ReasonNoResource)
	last := nextNotification(t, got)
	if last.State.State != SubscriptionTerminated || last.State.Reason != ReasonNoResource {
		t.Errorf("last NOTIFY: %+v", last.State)
	}
	waitEnded(t, sub)
	if err := sub.Err(); err != nil {
		t.Errorf("Err = %v, want nil after a NOTIFY that says terminated", err)
	}
}

func TestNotifyThatComesBeforeThe200CreatesTheSubscription(t *testing.T) {
	n := newTestNotifier(t)
	n.notifyFirst = true
	r := subscribeRequest()
	ua, got := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}
	if first := nextNotification(t, got); first.State.State != SubscriptionActive {
		t.Fatalf("first NOTIFY: %+v", first.State)
	}
	// Past the 200 and well past Timer N, the subscription still holds.
	time.Sleep(64*fastTimers.T1 + 200*time.Millisecond)
	select {
	case <-sub.Done():
		t.Fatalf("subscription ended: %v", sub.Err())
	default:
	}
	n.notify()
	if again := nextNotification(t, got); again.State.State != SubscriptionActive {
		t.Errorf("second NOTIFY: %+v", again.State)
	}
}

func TestSubscriptionWithoutANotifyEndsWithTimerN(t *testing.T) {
	n := newTestNotifier(t)
	n.noNotify = true
	r := subscribeRequest()
	ua, _ := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}
	waitEnded(t, sub)
	if !errors.Is(sub.Err(), ErrNoNotify) {
		t.Errorf("Err = %v, want ErrNoNotify", sub.Err())
	}
}

func TestRefusedSubscribeEndsTheSubscription(t *testing.T) {
	n := newTestNotifier(t)
	n.refuse = 489
	r := subscribeRequest()
	ua, _ := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}
	waitEnded(t, sub)
	if sub.Err() == nil {
		t.Error("Err = nil after a 489")
	}
}

func TestUnsubscribeEndsWithTheLastNotify(t *testing.T) {
	n := newTestNotifier(t)
	r := subscribeRequest()
	ua, got := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}
	nextSubscribe(t, n)
	nextNotification(t, got)
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if m := nextSubscribe(t, n); m.Get("Expires") != "0" {
		t.Errorf("unsubscribe Expires = %q, want 0", m.Get("Expires"))
	}
	last := nextNotification(t, got)
	if last.State.State != SubscriptionTerminated || last.State.Reason != ReasonTimeout {
		t.Errorf("last NOTIFY: %+v, want terminated with reason timeout (RFC 6665 4.2.1.4)", last.State)
	}
	waitEnded(t, sub)
}

func TestNotifyForNoSubscriptionGets481(t *testing.T) {
	ua := newUA(t, fastTimers, "subscriber", noCalls)
	peer := newStack(t, fastTimers, ignoreRequests)
	for _, to := range []string{"<sip:subscriber@example.org>", "<sip:subscriber@example.org>;tag=unknown"} {
		req := &Message{Method: NOTIFY, RequestURI: ua.cfg.Contact}
		req.Add("From", "<sip:notifier@example.org>;tag=n")
		req.Add("To", to)
		req.Add("Call-ID", NewCallID())
		req.Add("CSeq", "1 NOTIFY")
		req.Add("Event", "reg")
		req.Add("Subscription-State", "active;expires=60")
		ct, err := peer.SendRequest(req, Destination{Transport: UDP, Addr: ua.Stack().LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		if final := next(t, ct); final.StatusCode != 481 {
			t.Errorf("To %s: got %d, want 481 (RFC 6665 4.1.3)", to, final.StatusCode)
		}
	}
}

func TestSubscriptionState(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want SubscriptionState
		out  string
	}{
		{"active;expires=600000", SubscriptionState{State: SubscriptionActive, Expires: 600000 * time.Second}, "active;expires=600000"},
		{"Pending ; expires = 30", SubscriptionState{State: SubscriptionPending, Expires: 30 * time.Second}, "pending;expires=30"},
		{"terminated;reason=noresource", SubscriptionState{State: SubscriptionTerminated, Reason: ReasonNoResource}, "terminated;reason=noresource"},
		// RFC 6665 4.1.3: expires means nothing once terminated.
		{"terminated;reason=probation;retry-after=20;expires=5", SubscriptionState{State: SubscriptionTerminated, Reason: "probation", RetryAfter: 20 * time.Second}, "terminated;reason=probation;retry-after=20"},
	} {
		got, err := ParseSubscriptionState(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
			continue
		}
		if s := got.String(); s != tc.out {
			t.Errorf("String() = %q, want %q", s, tc.out)
		}
	}
	for _, bad := range []string{"", "waiting", "active;expires=soon"} {
		if _, err := ParseSubscriptionState(bad); err == nil {
			t.Errorf("Parse(%q) succeeded", bad)
		}
	}
}

func TestSameEvent(t *testing.T) {
	// The examples of RFC 6665 8.2.1.
	if !sameEvent("foo; id=1234", "foo; param=abcd; id=1234") {
		t.Error(`"foo; id=1234" should match "foo; param=abcd; id=1234"`)
	}
	for _, other := range []string{"foo", "Foo; id=1234"} {
		if sameEvent("foo; id=1234", other) {
			t.Errorf(`"foo; id=1234" should not match %q`, other)
		}
	}
}

func TestNotifierSubscriptionBuildsTheDialogAndItsNotifies(t *testing.T) {
	subscribe := &Message{Method: SUBSCRIBE, RequestURI: "sip:resource@example.org"}
	subscribe.Add("Via", "SIP/2.0/UDP 192.0.2.10;branch=z9hG4bK1")
	subscribe.Add("Record-Route", "<sip:proxy.example.org;lr>")
	subscribe.Add("From", "<sip:subscriber@example.org>;tag=s")
	subscribe.Add("To", "<sip:resource@example.org>")
	subscribe.Add("Call-ID", "c1")
	subscribe.Add("CSeq", "7 SUBSCRIBE")
	subscribe.Add("Contact", "<sip:subscriber@192.0.2.10>")
	subscribe.Add("Event", "reg")
	now := time.Now()

	ok, sub, err := AcceptSubscription(subscribe, 60*time.Second, notifierContact, now)
	if err != nil {
		t.Fatal(err)
	}
	if ok.Get("Expires") != "60" || ok.Get("Record-Route") != "<sip:proxy.example.org;lr>" || ok.Get("Contact") != "<"+notifierContact+">" {
		t.Errorf("200: Expires %q, Record-Route %q, Contact %q", ok.Get("Expires"), ok.Get("Record-Route"), ok.Get("Contact"))
	}
	notify := sub.NewNotify(state, now.Add(10*time.Second))
	if notify.RequestURI != "sip:subscriber@192.0.2.10" || notify.Get("Route") != "<sip:proxy.example.org;lr>" {
		t.Errorf("NOTIFY Request-URI %q, Route %q", notify.RequestURI, notify.Get("Route"))
	}
	if notify.Get("Subscription-State") != "active;expires=50" || notify.Get("Event") != "reg" || notify.Get("CSeq") != "1 NOTIFY" {
		t.Errorf("NOTIFY Subscription-State %q, Event %q, CSeq %q", notify.Get("Subscription-State"), notify.Get("Event"), notify.Get("CSeq"))
	}
	if tagOf(notify.Get("From")) != tagOf(ok.Get("To")) || tagOf(notify.Get("To")) != "s" {
		t.Errorf("NOTIFY From %q, To %q", notify.Get("From"), notify.Get("To"))
	}

	refresh := subscribe.Clone()
	refresh.Set("To", ok.Get("To"))
	refresh.Set("CSeq", "8 SUBSCRIBE")
	refresh.Set("Event", "presence")
	if _, err := sub.ReceiveSubscribe(refresh, 0, now); !errors.Is(err, ErrBadEvent) {
		t.Errorf("SUBSCRIBE for another event: %v, want ErrBadEvent", err)
	}
	refresh.Set("Event", "reg")
	if _, err := sub.ReceiveSubscribe(refresh, 0, now); err != nil {
		t.Fatal(err)
	}
	if last := sub.NewNotify(Body{}, now); last.Get("Subscription-State") != "terminated;reason=timeout" {
		t.Errorf("NOTIFY after an unsubscribe: %q", last.Get("Subscription-State"))
	}
}

func TestEndsSubscription(t *testing.T) {
	for _, code := range []int{404, 405, 410, 416, 480, 481, 485, 489, 501, 604} {
		if !EndsSubscription(code) {
			t.Errorf("%d should end the subscription", code)
		}
	}
	for _, code := range []int{408, 486, 500, 503} {
		if EndsSubscription(code) {
			t.Errorf("%d should leave the subscription to expire", code)
		}
	}
}

// A NOTIFY that says active with expires 0 ends the subscription: the
// expires is the time left (RFC 6665 4.1.3), not a missing value that
// the 2xx would fill in.
func TestNotifyWithNoTimeLeftEndsTheSubscription(t *testing.T) {
	n := newTestNotifier(t)
	r := subscribeRequest()
	ua, got := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}
	nextNotification(t, got)
	n.mu.Lock()
	n.sub.Expires = time.Now().Add(300 * time.Millisecond)
	n.notifyLocked()
	n.mu.Unlock()
	if last := nextNotification(t, got); last.State.State != SubscriptionActive || last.State.Expires != 0 {
		t.Fatalf("NOTIFY says %+v, want active with expires 0", last.State)
	}
	waitEnded(t, sub)
	if !errors.Is(sub.Err(), ErrSubscriptionExpired) {
		t.Errorf("Err = %v, want ErrSubscriptionExpired", sub.Err())
	}
}

// A NOTIFY built once the subscription has expired is the one that
// ends it (RFC 6665 4.2.1.4), even when no timer of the notifier has
// ended it yet: that NOTIFY never says active with no time left.
func TestNotifyAfterTheExpiryEndsTheSubscription(t *testing.T) {
	subscribe := &Message{Method: SUBSCRIBE, RequestURI: "sip:resource@example.org"}
	subscribe.Add("Via", "SIP/2.0/UDP 192.0.2.10;branch=z9hG4bK1")
	subscribe.Add("From", "<sip:subscriber@example.org>;tag=s")
	subscribe.Add("To", "<sip:resource@example.org>")
	subscribe.Add("Call-ID", "c1")
	subscribe.Add("CSeq", "1 SUBSCRIBE")
	subscribe.Add("Contact", "<sip:subscriber@192.0.2.10>")
	subscribe.Add("Event", "reg")
	now := time.Now()
	_, sub, err := AcceptSubscription(subscribe, 60*time.Second, notifierContact, now)
	if err != nil {
		t.Fatal(err)
	}
	notify := sub.NewNotify(state, now.Add(60*time.Second))
	if got := notify.Get("Subscription-State"); got != "terminated;reason=timeout" {
		t.Errorf("NOTIFY at the expiry says %q, want terminated;reason=timeout", got)
	}
	if sub.State != SubscriptionTerminated || sub.Reason != ReasonTimeout {
		t.Errorf("subscription is %s %q after that NOTIFY, want terminated by timeout", sub.State, sub.Reason)
	}
}
