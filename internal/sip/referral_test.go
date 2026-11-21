package sip

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// statusesOf reads a Referral until its Statuses close.
func statusesOf(t *testing.T, r *Referral) []ReferStatus {
	t.Helper()
	var got []ReferStatus
	timeout := time.After(5 * time.Second)
	for {
		select {
		case s, ok := <-r.Statuses():
			if !ok {
				return got
			}
			got = append(got, s)
		case <-timeout:
			t.Fatalf("Statuses still open after %d NOTIFYs", len(got))
		}
	}
}

func referCodes(statuses []ReferStatus) []int {
	var codes []int
	for _, s := range statuses {
		codes = append(codes, s.StatusCode)
	}
	return codes
}

// TestReferWithinADialog is RFC 3515 2.4: the REFER gets 200, not the
// 202 that RFC 7647 5 forbids, the first NOTIFY says 100 at once, and
// the last one the final status, with Subscription-State terminated and
// reason noresource.
func TestReferWithinADialog(t *testing.T) {
	target := ReferTo{URI: "sip:+12025550103@" + imsDomain + ";user=phone",
		Replaces: &Replaces{CallID: "a@b", ToTag: "x", FromTag: "y"}}
	referred := make(chan *InDialogRequest, 1)
	notified := make(chan struct{})
	e := establishWith(t, nil, func(r *InDialogRequest) {
		defer close(notified)
		referred <- r
		s, err := r.AcceptRefer(time.Minute)
		if err != nil {
			t.Error(err)
			return
		}
		if err := s.Notify(200); err != nil {
			t.Error(err)
		}
		if err := s.Notify(200); !errors.Is(err, ErrReferEnded) {
			t.Errorf("Notify after the final status: %v, want ErrReferEnded", err)
		}
	})
	r, err := e.out.Refer(target, HeaderField{Name: "Referred-By", Value: "<" + caller + ">"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Response.StatusCode != 200 || r.Response.Get("Contact") == "" {
		t.Errorf("REFER got %d with Contact %q, want 200 with one", r.Response.StatusCode, r.Response.Get("Contact"))
	}
	got := statusesOf(t, r)
	if codes := referCodes(got); len(codes) != 2 || codes[0] != 100 || codes[1] != 200 {
		t.Fatalf("NOTIFYs reported %v, want 100 then 200", codes)
	}
	last := got[1]
	if last.State.State != SubscriptionTerminated || last.State.Reason != ReasonNoResource ||
		last.Request.Get("Content-Type") != SipfragType || !strings.HasPrefix(last.Request.Get("Event"), "refer;id=") {
		t.Errorf("last NOTIFY has Subscription-State %q, Content-Type %q, Event %q", last.Request.Get("Subscription-State"),
			last.Request.Get("Content-Type"), last.Request.Get("Event"))
	}
	if r.Err() != nil {
		t.Errorf("Err = %v, want nil after a NOTIFY that said terminated", r.Err())
	}
	<-notified
	req := (<-referred).Request
	rt, err := ParseReferTo(req.Get("Refer-To"))
	if err != nil || rt.URI != target.URI || rt.Replaces == nil || *rt.Replaces != *target.Replaces || req.Get("Referred-By") == "" {
		t.Errorf("REFER has Refer-To %q (%v), Referred-By %q", req.Get("Refer-To"), err, req.Get("Referred-By"))
	}
}

// TestTwoReferralsInOneDialog is RFC 3515 2.4.6: each NOTIFY names its
// REFER by the id of Event, so the second subscription in the dialog is
// told apart from the first.
func TestTwoReferralsInOneDialog(t *testing.T) {
	subs := make(chan *ReferSubscription, 2)
	e := establishWith(t, nil, func(r *InDialogRequest) {
		s, err := r.AcceptRefer(time.Minute)
		if err != nil {
			t.Error(err)
		}
		subs <- s
	})
	first, err := e.out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	firstSub := <-subs
	second, err := e.out.Refer(ReferTo{URI: "sip:b@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	secondSub := <-subs
	if err := secondSub.Notify(486); err != nil {
		t.Error(err)
	}
	if err := firstSub.Notify(200); err != nil {
		t.Error(err)
	}
	if codes := referCodes(statusesOf(t, first)); len(codes) != 2 || codes[1] != 200 {
		t.Errorf("first REFER got %v, want 100 then 200", codes)
	}
	if codes := referCodes(statusesOf(t, second)); len(codes) != 2 || codes[1] != 486 {
		t.Errorf("second REFER got %v, want 100 then 486", codes)
	}
}

// TestReferAcceptedWith202 is RFC 7647 5 on the referrer's side: a
// notifier of RFC 3515 still answers 202, which the referrer takes as
// it takes 200. The subscription is there, and its NOTIFYs are read.
func TestReferAcceptedWith202(t *testing.T) {
	notified := make(chan struct{})
	e := establishWith(t, nil, func(r *InDialogRequest) {
		defer close(notified)
		resp := NewResponse(r.Request, 202, "")
		resp.Add("Contact", "<sip:"+callee+"@"+imsDomain+">")
		if err := r.respond(resp, true); err != nil {
			t.Error(err)
			return
		}
		notifyRefer(t, r, "terminated;reason=noresource")
	})
	r, err := e.out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	if r.Response.StatusCode != 202 {
		t.Fatalf("REFER got %d, want the 202 of the notifier", r.Response.StatusCode)
	}
	if codes := referCodes(statusesOf(t, r)); len(codes) != 1 || codes[0] != 100 {
		t.Errorf("NOTIFYs reported %v, want the 100 of the notifier", codes)
	}
	if r.Err() != nil {
		t.Errorf("Err = %v, want nil after a NOTIFY that said terminated", r.Err())
	}
	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Error("the notifier got no response to its NOTIFY within 5 s")
	}
}

// TestRefusedRefer: a REFER that gets a final response other than 2xx
// creates no subscription (RFC 3515 2.4.2).
func TestRefusedRefer(t *testing.T) {
	e := establishWith(t, nil, func(r *InDialogRequest) { r.Reject(403) })
	r, err := e.out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	if r.Response.StatusCode != 403 || len(statusesOf(t, r)) != 0 {
		t.Errorf("REFER got %d, want 403 and no NOTIFY", r.Response.StatusCode)
	}
}

// TestReferWithoutReferTo is RFC 3515 2.4.2: a REFER needs exactly one
// Refer-To, or the UA answers 400 without asking its user.
func TestReferWithoutReferTo(t *testing.T) {
	e := establishWith(t, nil, func(r *InDialogRequest) { t.Error("the user got a REFER without Refer-To") })
	resp, err := e.out.Send(REFER, Body{})
	if err != nil || resp.StatusCode != 400 {
		t.Errorf("REFER without Refer-To got %v, %v; want 400", resp, err)
	}
}

// TestReferralEndsWithTheDialog: a subscription that shares the dialog
// of a call ends when the call does.
func TestReferralEndsWithTheDialog(t *testing.T) {
	e := establishWith(t, nil, func(r *InDialogRequest) { r.AcceptRefer(time.Minute) })
	r, err := e.out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	<-r.Statuses() // the 100
	if err := e.in.Bye(); err != nil {
		t.Fatal(err)
	}
	statusesOf(t, r)
	if !errors.Is(r.Err(), ErrCallEnded) {
		t.Errorf("Err = %v, want ErrCallEnded", r.Err())
	}
}

func TestParseSipfragStatus(t *testing.T) {
	code, reason, err := ParseSipfragStatus([]byte("SIP/2.0 603 Declined\r\nWarning: 399 x \"y\"\r\n"))
	if err != nil || code != 603 || reason != "Declined" {
		t.Errorf("got %d %q %v", code, reason, err)
	}
	if _, _, err := ParseSipfragStatus([]byte("INVITE sip:a@b SIP/2.0\r\n")); err == nil {
		t.Error("a request line parsed as a status line")
	}
}

// notifyRefer sends, within the dialog of r, a NOTIFY of the refer
// event of the REFER r with subscriptionState and a 100 in its body, as
// a notifier written by others might.
func notifyRefer(t *testing.T, r *InDialogRequest, subscriptionState string) {
	t.Helper()
	cseq, _ := ParseCSeq(r.Request.Get("CSeq"))
	resp, err := r.Dialog().Send(NOTIFY, Body{Type: SipfragType, Content: []byte("SIP/2.0 100 Trying\r\n")},
		HeaderField{Name: "Event", Value: EventRefer + ";id=" + strconv.FormatUint(uint64(cseq.Number), 10)},
		HeaderField{Name: "Subscription-State", Value: subscriptionState},
		HeaderField{Name: "Contact", Value: "<sip:" + callee + "@" + imsDomain + ">"})
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("NOTIFY got %v, %v; want 200", resp, err)
	}
}

// TestReferralEndsWithNoTimeLeft is RFC 6665 4.1.3: a NOTIFY that says
// active with expires=0 has left the subscription no time, so it ends
// although no NOTIFY said terminated.
func TestReferralEndsWithNoTimeLeft(t *testing.T) {
	// The referrer ends its subscription when the NOTIFY arrives, which is
	// before the notifier has the 200 to it: the test waits for both.
	notified := make(chan struct{})
	e := establishWith(t, nil, func(r *InDialogRequest) {
		if _, err := r.Accept(Body{}, HeaderField{Name: "Contact", Value: "<sip:" + callee + "@" + imsDomain + ">"}); err != nil {
			t.Error(err)
			close(notified)
			return
		}
		go func() {
			defer close(notified)
			notifyRefer(t, r, "active;expires=0")
		}()
	})
	defer func() {
		select {
		case <-notified:
		case <-time.After(5 * time.Second):
			t.Error("the notifier got no response to its NOTIFY within 5 s")
		}
	}()
	r, err := e.out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	if got := referCodes(statusesOf(t, r)); len(got) != 1 || got[0] != 100 {
		t.Errorf("statuses %v, want the one 100", got)
	}
	if !errors.Is(r.Err(), ErrSubscriptionExpired) {
		t.Errorf("Err = %v, want ErrSubscriptionExpired", r.Err())
	}
}

// TestReferralWithoutANotify is RFC 6665 4.1.2.4, which RFC 3515 2.4.4
// carries over to the subscription of a REFER: without a NOTIFY within
// 64*T1 of the REFER, Timer N ends the subscription.
func TestReferralWithoutANotify(t *testing.T) {
	e := establishWith(t, nil, func(r *InDialogRequest) {
		r.Accept(Body{}, HeaderField{Name: "Contact", Value: "<sip:" + callee + "@" + imsDomain + ">"})
	})
	r, err := e.out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	if got := statusesOf(t, r); len(got) != 0 {
		t.Errorf("statuses %v, want none", referCodes(got))
	}
	if !errors.Is(r.Err(), ErrNoNotify) {
		t.Errorf("Err = %v, want ErrNoNotify", r.Err())
	}
}

// TestReferSubscriptionExpires is RFC 6665 4.2.1.4: when the duration
// that the notifier chose runs out before a final status, the notifier
// removes the subscription and says so in a NOTIFY of
// terminated;reason=timeout, which repeats the last status. Both UAs
// run their timers by hand: the notifier tells the whole seconds left
// and keeps no more (4.2.2), and the referrer takes that as the
// duration (4.1.3).
func TestReferSubscriptionExpires(t *testing.T) {
	callerTimers, calleeTimers := &handTimers{}, &handTimers{}
	subs := make(chan *ReferSubscription, 1)
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUAWith(t, UAConfig{Timers: testTimers, AfterFunc: calleeTimers.afterFunc, OnCall: func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			s, err := r.AcceptRefer(1500 * time.Millisecond)
			if err != nil {
				t.Error(err)
			}
			subs <- s
		})
		if _, err := c.Accept(answer); err != nil {
			t.Error(err)
		}
		calls <- c
	}}, callee)
	callerUA := newUAWith(t, UAConfig{Timers: testTimers, AfterFunc: callerTimers.afterFunc, OnCall: noCalls}, "001010000000001")
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	responses(t, out)
	<-calls
	r, err := out.Refer(ReferTo{URI: "sip:a@" + imsDomain})
	if err != nil {
		t.Fatal(err)
	}
	first := <-r.Statuses()
	if first.StatusCode != 100 || first.State.State != SubscriptionActive || first.State.Expires != time.Second {
		t.Errorf("first NOTIFY %d %s, want 100 active;expires=1", first.StatusCode, first.State)
	}
	if got := callerTimers.durations(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("the referrer runs timers of %v, want the one second of the NOTIFY", got)
	}
	s := <-subs
	calleeTimers.fire(t, time.Second)
	rest := statusesOf(t, r)
	if len(rest) != 1 || rest[0].StatusCode != 100 || rest[0].State.State != SubscriptionTerminated || rest[0].State.Reason != ReasonTimeout {
		t.Errorf("NOTIFYs after the expiry %+v, want 100 terminated;reason=timeout", rest)
	}
	if r.Err() != nil {
		t.Errorf("Err = %v, want nil after the NOTIFY that said terminated", r.Err())
	}
	if err := s.Notify(200); !errors.Is(err, ErrReferEnded) {
		t.Errorf("Notify after the expiry: %v, want ErrReferEnded", err)
	}
}
