package sip

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUAAsNotifier: with OnSubscribe a UA takes an initial SUBSCRIBE
// and the SUBSCRIBE that ends the subscription within its dialog, lists
// SUBSCRIBE in Allow, and sends its NOTIFYs with SendRequest (RFC 6665
// 4.2.1).
func TestUAAsNotifier(t *testing.T) {
	var mu sync.Mutex
	var sub *NotifierSubscription
	received := make(chan *Message, 2)
	var notifier *UA
	notify := func(n *NotifierSubscription) {
		req := n.NewNotify(Body{Type: "text/plain", Content: []byte("state")}, time.Now())
		ct, err := notifier.SendRequest(req)
		if err != nil {
			t.Error(err)
			return
		}
		for range ct.Responses() {
		}
	}
	notifier = newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls, OnSubscribe: func(st *ServerTransaction) {
		received <- st.Request
		mu.Lock()
		defer mu.Unlock()
		if sub == nil {
			ok, s, err := AcceptSubscription(st.Request, time.Minute, notifier.cfg.Contact, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			sub = &s
			st.Respond(ok)
			notify(sub)
			return
		}
		ok, err := sub.ReceiveSubscribe(st.Request, 0, time.Now())
		if err != nil {
			t.Error(err)
			return
		}
		st.Respond(ok)
		notify(sub)
	}}, "conference")
	subscriberUA := newUA(t, testTimers, "001010000000001", noCalls)
	notifications := make(chan Notification, 2)
	s, err := subscriberUA.Subscribe(SubscribeRequest{RequestURI: notifier.cfg.Contact, From: caller, Event: "conference",
		Expires: time.Minute, OnNotify: func(n Notification, _ *Message) { notifications <- n }})
	if err != nil {
		t.Fatal(err)
	}
	if n := <-notifications; n.State.State != SubscriptionActive || string(n.Body.Content) != "state" {
		t.Fatalf("first NOTIFY has state %v and body %q", n.State, n.Body.Content)
	}
	if err := s.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if n := <-notifications; n.State.State != SubscriptionTerminated {
		t.Errorf("NOTIFY after the unsubscribe has state %v, want terminated", n.State)
	}
	waitDone(t, s.Done(), "subscriber")
	if first, second := <-received, <-received; tagOf(first.Get("To")) != "" || tagOf(second.Get("To")) == "" {
		t.Errorf("OnSubscribe got To %q and %q, want an initial SUBSCRIBE and then one within the dialog", first.Get("To"), second.Get("To"))
	}
	if !strings.Contains(notifier.allowHeader(), "SUBSCRIBE") {
		t.Errorf("Allow is %q, want SUBSCRIBE in it", notifier.allowHeader())
	}
}
