package sip

import (
	"errors"
	"testing"
	"time"
)

// conferenceEvent is the Event of the subscriptions in these tests,
// which share the dialog of a call (RFC 6665 4.5.2).
var conferenceEvent = HeaderField{Name: "Event", Value: "conference"}

// nextWithin waits for the next request that the user of a call got
// within its dialog.
func nextWithin(t *testing.T, requests <-chan *InDialogRequest, what string) *InDialogRequest {
	t.Helper()
	select {
	case r := <-requests:
		return r
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
		return nil
	}
}

// subscribedCall is a call in whose dialog the caller has subscribed to
// the callee, and both have told their UA that the subscription shares
// the dialog. atCaller and atCallee take the requests within the dialog
// at each end.
type subscribedCall struct {
	established
	atCaller, atCallee     <-chan *InDialogRequest
	callerEnds, calleeEnds func()
}

func subscribeWithinTheCall(t *testing.T) subscribedCall {
	t.Helper()
	atCaller := make(chan *InDialogRequest, 4)
	atCallee := make(chan *InDialogRequest, 4)
	calleeEnds := make(chan func(), 1)
	e := establishWith(t, func(r *InDialogRequest) { atCaller <- r }, func(r *InDialogRequest) {
		if r.Request.Method != SUBSCRIBE {
			atCallee <- r
			return
		}
		end, err := r.Dialog().AddSubscriptionUsage()
		if err != nil {
			t.Errorf("AddSubscriptionUsage at the notifier: %v", err)
			r.Reject(500)
			return
		}
		calleeEnds <- end
		r.Accept(Body{}, HeaderField{Name: "Expires", Value: "600"})
	})
	resp, err := e.out.Send(SUBSCRIBE, Body{}, conferenceEvent, HeaderField{Name: "Expires", Value: "600"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within the dialog got %v, %v", resp, err)
	}
	callerEnds, err := e.out.AddSubscriptionUsage()
	if err != nil {
		t.Fatalf("AddSubscriptionUsage at the subscriber: %v", err)
	}
	return subscribedCall{established: e, atCaller: atCaller, atCallee: atCallee, callerEnds: callerEnds, calleeEnds: <-calleeEnds}
}

// notifyTerminated sends the NOTIFY that ends the subscription from the
// callee, as the notifier does once the session is over.
func (s subscribedCall) notifyTerminated() (*Message, error) {
	return s.in.Send(NOTIFY, Body{}, conferenceEvent, HeaderField{Name: "Subscription-State", Value: "terminated;reason=noresource"})
}

// TestASubscriptionKeepsTheDialogPastTheBYE is RFC 5057 5.5 and RFC 6665
// 4.5.2: a BYE ends the session, not a subscription that shares its
// dialog. Both calls end with the BYE, but the dialog stays for the
// subscription: the notifier ends it with a NOTIFY in the same dialog,
// with the next CSeq, and the dialog ends with the last usage (RFC 5057
// 4). Requests of the session find no dialog in between, at either end,
// and a REFER, which would start a subscription of its own, is declined.
func TestASubscriptionKeepsTheDialogPastTheBYE(t *testing.T) {
	s := subscribeWithinTheCall(t)
	before, _ := s.in.Dialog()
	if err := s.out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s.out.Done(), "caller")
	waitDone(t, s.in.Done(), "callee")

	if _, err := s.in.Update(Body{}); !errors.Is(err, ErrCallEnded) {
		t.Errorf("UPDATE after the BYE: %v, want ErrCallEnded", err)
	}
	if _, err := s.in.ReInvite(answer); !errors.Is(err, ErrCallEnded) {
		t.Errorf("re-INVITE after the BYE: %v, want ErrCallEnded", err)
	}
	if _, err := s.in.Send("INFO", Body{}); !errors.Is(err, ErrCallEnded) {
		t.Errorf("INFO after the BYE: %v, want ErrCallEnded", err)
	}
	if err := s.in.Bye(); !errors.Is(err, ErrCallEnded) {
		t.Errorf("second BYE: %v, want ErrCallEnded", err)
	}
	if _, err := s.in.AddSubscriptionUsage(); !errors.Is(err, ErrCallEnded) {
		t.Errorf("AddSubscriptionUsage after the BYE: %v, want ErrCallEnded", err)
	}
	uac, _ := s.out.Dialog()
	if resp := sendInDialog(t, s.established, uac.NewRequest("INFO")); resp.StatusCode != 481 {
		t.Errorf("INFO of the ended session got %s, want 481", startLine(resp))
	}
	// RFC 5057 5.6: a REFER would add a usage, which 603 refuses
	// without ending the dialog.
	if resp := sendInDialog(t, s.established, uac.NewRequest(REFER)); resp.StatusCode != 603 {
		t.Errorf("REFER after the BYE got %s, want 603", startLine(resp))
	}

	notified := make(chan error, 1)
	go func() {
		resp, err := s.notifyTerminated()
		if err == nil && resp.StatusCode != 200 {
			err = errors.New(startLine(resp))
		}
		notified <- err
	}()
	n := nextWithin(t, s.atCaller, "NOTIFY after the BYE")
	cseq, _ := ParseCSeq(n.Request.Get("CSeq"))
	if n.Request.Method != NOTIFY || ReceivedDialogID(n.Request) != uac.ID || cseq.Number != before.LocalSeq+1 {
		t.Errorf("caller got %s in dialog %+v with CSeq %q, want a NOTIFY in %+v with CSeq %d",
			n.Request.Method, ReceivedDialogID(n.Request), n.Request.Get("CSeq"), uac.ID, before.LocalSeq+1)
	}
	if _, err := n.Accept(Body{}); err != nil {
		t.Fatal(err)
	}
	if err := <-notified; err != nil {
		t.Fatalf("the NOTIFY after the BYE: %v", err)
	}

	s.calleeEnds()
	s.callerEnds()
	if _, err := s.notifyTerminated(); !errors.Is(err, ErrCallEnded) {
		t.Errorf("NOTIFY once the subscription has ended: %v, want ErrCallEnded", err)
	}
	if d, _ := s.in.Dialog(); d.State != DialogTerminated {
		t.Errorf("callee's dialog is %s once its last usage ended, want Terminated", d.State)
	}
	uac, _ = s.out.Dialog()
	if resp := sendInDialog(t, s.established, uac.NewRequest(SUBSCRIBE)); resp.StatusCode != 481 {
		t.Errorf("SUBSCRIBE in the ended dialog got %s, want 481", startLine(resp))
	}
}

// TestWithoutASubscriptionTheBYEEndsTheDialog: a dialog that no
// subscription shares ends with its BYE (RFC 3261 15), and nothing goes
// out in it after that.
func TestWithoutASubscriptionTheBYEEndsTheDialog(t *testing.T) {
	e := establish(t)
	if err := e.out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, e.in.Done(), "callee")
	if _, err := e.in.Send(NOTIFY, Body{}, conferenceEvent, HeaderField{Name: "Subscription-State", Value: "terminated"}); !errors.Is(err, ErrCallEnded) {
		t.Errorf("NOTIFY after the BYE: %v, want ErrCallEnded", err)
	}
	if d, _ := e.in.Dialog(); d.State != DialogTerminated {
		t.Errorf("callee's dialog is %s after the BYE, want Terminated", d.State)
	}
}

// TestAFailedNOTIFYAfterTheBYEEndsOnlyItsSubscription is RFC 5057 5.1
// and 5.2: a 481, a 408 or no answer at all to a NOTIFY ends the
// subscription that it was of, not the dialog. The dialog of the
// notifier stays until the notifier ends that subscription, as for any
// other end of it.
func TestAFailedNOTIFYAfterTheBYEEndsOnlyItsSubscription(t *testing.T) {
	for _, tc := range []struct {
		name string
		// subscriber has the caller answer the NOTIFY, or end its own
		// usage before the BYE so that its dialog is gone (481).
		lost   bool
		answer func(*InDialogRequest)
		want   int
	}{
		{name: "481", lost: true, want: 481},
		{name: "408", answer: func(r *InDialogRequest) { r.Reject(408) }, want: 408},
		{name: "no answer", answer: func(*InDialogRequest) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := subscribeWithinTheCall(t)
			if tc.lost {
				s.callerEnds()
			}
			if err := s.out.Bye(); err != nil {
				t.Fatal(err)
			}
			waitDone(t, s.in.Done(), "callee")
			if tc.answer != nil {
				go func() { tc.answer(nextWithin(t, s.atCaller, "NOTIFY after the BYE")) }()
			}
			resp, err := s.notifyTerminated()
			switch {
			case tc.want == 0 && !errors.Is(err, ErrTimeout):
				t.Fatalf("unanswered NOTIFY got %v, %v; want ErrTimeout", resp, err)
			case tc.want != 0 && (err != nil || resp.StatusCode != tc.want):
				t.Fatalf("NOTIFY got %v, %v; want %d", resp, err, tc.want)
			}
			if d, _ := s.in.Dialog(); d.State != DialogConfirmed {
				t.Errorf("callee's dialog is %s after the NOTIFY failed, want Confirmed until its subscription ends", d.State)
			}
			s.calleeEnds()
			if d, _ := s.in.Dialog(); d.State != DialogTerminated {
				t.Errorf("callee's dialog is %s once its subscription ended, want Terminated", d.State)
			}
			s.callerEnds()
		})
	}
}

// TestAFailedNOTIFYLeavesTheSessionAlone is RFC 5057 5.1: a 481 to the
// NOTIFY of a subscription that shares the dialog ends that
// subscription, while the session goes on, with no BYE.
func TestAFailedNOTIFYLeavesTheSessionAlone(t *testing.T) {
	s := subscribeWithinTheCall(t)
	go func() { nextWithin(t, s.atCaller, "NOTIFY").Reject(481) }()
	resp, err := s.in.Send(NOTIFY, Body{}, conferenceEvent, HeaderField{Name: "Subscription-State", Value: "active;expires=600"})
	if err != nil || resp.StatusCode != 481 {
		t.Fatalf("NOTIFY got %v, %v; want 481", resp, err)
	}
	s.calleeEnds()
	go func() { nextWithin(t, s.atCaller, "UPDATE").Accept(Body{}) }()
	if resp, err := s.in.Update(Body{}); err != nil || resp.StatusCode != 200 {
		t.Fatalf("UPDATE of the session after the 481 got %v, %v; want 200", resp, err)
	}
	select {
	case <-s.in.Done():
		t.Fatal("the 481 to the NOTIFY ended the callee's call")
	case <-s.out.Done():
		t.Fatal("the 481 to the NOTIFY ended the caller's call")
	default:
	}
	s.callerEnds()
}

// TestALostSessionLeavesItsSubscription is RFC 3261 12.2.1.2 with RFC
// 5057 5.1: a 481 to a request of the session has the UA end the
// session with a BYE, and the call with ErrDialogLost, while a
// subscription that shares the dialog goes on until its NOTIFY ends it.
func TestALostSessionLeavesItsSubscription(t *testing.T) {
	s := subscribeWithinTheCall(t)
	go func() { nextWithin(t, s.atCallee, "UPDATE").Reject(481) }()
	if resp, err := s.out.Update(Body{}); err != nil || resp.StatusCode != 481 {
		t.Fatalf("UPDATE got %v, %v; want 481", resp, err)
	}
	waitDone(t, s.out.Done(), "caller")
	if err := s.out.Err(); !errors.Is(err, ErrDialogLost) {
		t.Errorf("caller's call ended with %v, want ErrDialogLost", err)
	}
	waitDone(t, s.in.Done(), "callee")
	go func() { nextWithin(t, s.atCaller, "NOTIFY after the BYE").Accept(Body{}) }()
	if resp, err := s.notifyTerminated(); err != nil || resp.StatusCode != 200 {
		t.Fatalf("NOTIFY after the lost session got %v, %v; want 200", resp, err)
	}
	s.calleeEnds()
	s.callerEnds()
	if d, _ := s.out.Dialog(); d.State != DialogTerminated {
		t.Errorf("caller's dialog is %s once its subscription ended, want Terminated", d.State)
	}
}

// TestReplacesOfASessionThatEndedWhileASubscriptionKeepsTheDialog is
// RFC 3891 3 with RFC 5057 5.7: Replaces takes the place of a session,
// and one that a BYE has ended gets 603 although its dialog is still
// there.
func TestReplacesOfASessionThatEndedWhileASubscriptionKeepsTheDialog(t *testing.T) {
	u := newReplacingUAs(t)
	end, err := u.in.AddSubscriptionUsage()
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	ended := u.replacesTo().String()
	if err := u.in.Bye(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, u.out.Done(), "caller")
	c := u.inviteWith(t, ended)
	if d, refusal := c.ReplacedDialog(); d != nil || refusal != 603 {
		t.Errorf("ReplacedDialog = %p, %d; want 603", d, refusal)
	}
	c.Reject(603)
}
