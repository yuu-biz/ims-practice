package b2bua_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

var conferenceEvent = sip.HeaderField{Name: "Event", Value: "conference"}

// relayedSubscription is a call through a B2BUA in whose dialogs the
// caller has subscribed to the callee, and has then hung up: the B2BUA
// has sent the callee its BYE. Both ends keep their dialog for the
// subscription until the test ends their usages.
type relayedSubscription struct {
	b2b, callee    *sip.UA
	in             *sip.IncomingCall
	callerDialog   sip.Dialog
	calleeDialog   sip.Dialog
	atCaller       <-chan *sip.InDialogRequest
	subscriberEnds func()
	notifierEnds   func()
}

func subscribeThroughTheB2BUAAndHangUp(t *testing.T, h *hooks) relayedSubscription {
	t.Helper()
	h.crossing = true
	calls := make(chan *sip.IncomingCall, 1)
	notifierEnds := make(chan func(), 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		in.Handle(func(r *sip.InDialogRequest) {
			if r.Request.Method != sip.SUBSCRIBE {
				r.Reject(405)
				return
			}
			end, err := r.Dialog().AddSubscriptionUsage()
			if err != nil {
				t.Errorf("AddSubscriptionUsage at the notifier: %v", err)
				r.Reject(500)
				return
			}
			notifierEnds <- end
			r.Accept(sip.Body{}, sip.HeaderField{Name: "Expires", Value: "600"})
		})
		calls <- in
		in.Accept(answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	atCaller := make(chan *sip.InDialogRequest, 4)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, func(r *sip.InDialogRequest) { atCaller <- r }))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	in := within(t, "INVITE at the callee", calls)

	subscriberEnds, err := out.AddSubscriptionUsage()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(subscriberEnds)
	if resp, err := out.Send(sip.SUBSCRIBE, sip.Body{}, conferenceEvent, sip.HeaderField{Name: "Expires", Value: "600"}); err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within the dialog got %v, %v; want 200", resp, err)
	}
	endAtTheNotifier := within(t, "SUBSCRIBE at the callee", notifierEnds)
	t.Cleanup(endAtTheNotifier)

	callerDialog, _ := out.Dialog()
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, in.Done(), "BYE of the B2BUA at the callee")
	calleeDialog, _ := in.Dialog()
	return relayedSubscription{b2b: b2b, callee: callee, in: in, callerDialog: callerDialog, calleeDialog: calleeDialog,
		atCaller: atCaller, subscriberEnds: subscriberEnds, notifierEnds: endAtTheNotifier}
}

// notify sends a NOTIFY of the subscription from the callee with
// Subscription-State state, and returns where its final response comes.
func (s relayedSubscription) notify(t *testing.T, state string) <-chan *sip.Message {
	notified := make(chan *sip.Message, 1)
	go func() {
		resp, err := s.in.Send(sip.NOTIFY, sip.Body{}, conferenceEvent, sip.HeaderField{Name: "Subscription-State", Value: state})
		if err != nil {
			t.Errorf("the NOTIFY after the BYE: %v", err)
		}
		notified <- resp
	}()
	return notified
}

// forgotten checks that the B2BUA no longer knows the callee's dialog: a
// request within it gets 481 (RFC 3261 12.2.2).
func (s relayedSubscription) forgotten(t *testing.T) {
	t.Helper()
	if !until(5*time.Second, func() bool { return optionsStatus(t, s.callee, s.calleeDialog, s.b2b) == 481 }) {
		t.Error("a request within the callee's dialog still finds it at the B2BUA once the subscription has ended, want 481")
	}
}

// TestASubscriptionWithinTheDialogsOutlivesTheBYE is RFC 5057 5.5 and
// RFC 6665 4.5.2 at a B2BUA: a subscription that a SUBSCRIBE within the
// dialogs created shares both dialogs with the session, and the BYE of
// the caller ends the session only. The B2BUA sends the callee the BYE
// as ever, but keeps both legs, so that the NOTIFY that ends the
// subscription still reaches the caller, in its dialog, and the
// caller's 200 the callee. Once that NOTIFY has its response, the B2BUA
// no longer knows the dialogs (RFC 5057 4): a request within the
// callee's gets 481 (RFC 3261 12.2.2).
func TestASubscriptionWithinTheDialogsOutlivesTheBYE(t *testing.T) {
	s := subscribeThroughTheB2BUAAndHangUp(t, &hooks{})
	notified := s.notify(t, "terminated;reason=noresource")
	n := within(t, "NOTIFY at the caller after the BYE", s.atCaller)
	if n.Request.Method != sip.NOTIFY || n.Request.Get("Call-ID") != s.callerDialog.ID.CallID || n.Request.Get("Subscription-State") != "terminated;reason=noresource" {
		t.Errorf("the caller got %s in Call-ID %q with Subscription-State %q, want the NOTIFY that ends the subscription in %q",
			n.Request.Method, n.Request.Get("Call-ID"), n.Request.Get("Subscription-State"), s.callerDialog.ID.CallID)
	}
	if _, err := n.Accept(sip.Body{}); err != nil {
		t.Fatal(err)
	}
	if resp := within(t, "response to the NOTIFY at the callee", notified); resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the NOTIFY after the BYE got %v, want the caller's 200", resp)
	}
	s.notifierEnds()
	s.subscriberEnds()
	s.forgotten(t)
}

// TestA481ToANOTIFYEndsTheRelayedSubscription is RFC 6665 4.2.2 with
// RFC 5057 5.1 at a B2BUA: a subscriber that answers a NOTIFY with 481
// has lost the subscription, and the B2BUA, which passes the 481 on to
// the notifier, lets both legs go with it.
func TestA481ToANOTIFYEndsTheRelayedSubscription(t *testing.T) {
	s := subscribeThroughTheB2BUAAndHangUp(t, &hooks{})
	notified := s.notify(t, "active;expires=600")
	within(t, "NOTIFY at the caller after the BYE", s.atCaller).Reject(481)
	if resp := within(t, "response to the NOTIFY at the callee", notified); resp == nil || resp.StatusCode != 481 {
		t.Fatalf("the NOTIFY got %v, want the caller's 481", resp)
	}
	s.notifierEnds()
	s.forgotten(t)
}

// TestARefusedLastNOTIFYStillEndsTheRelayedSubscription: a NOTIFY that
// says terminated ends the subscription at the notifier, whatever its
// response (RFC 6665 4.2.2), so the B2BUA lets both legs go even when
// its role refuses to pass that NOTIFY on.
func TestARefusedLastNOTIFYStillEndsTheRelayedSubscription(t *testing.T) {
	h := &hooks{request: func(req *sip.Message, _ b2bua.Direction) *b2bua.Refusal {
		if req.Method == sip.NOTIFY {
			return &b2bua.Refusal{Status: 500}
		}
		return nil
	}}
	s := subscribeThroughTheB2BUAAndHangUp(t, h)
	notified := s.notify(t, "terminated;reason=noresource")
	if resp := within(t, "response to the NOTIFY at the callee", notified); resp == nil || resp.StatusCode != 500 {
		t.Fatalf("the NOTIFY got %v, want the role's 500", resp)
	}
	s.notifierEnds()
	s.forgotten(t)
}

// TestARefusedSubscriptionKeepsNoLeg: a SUBSCRIBE within the dialogs
// that the other end refuses creates no subscription (RFC 6665 4.1.2.1),
// so the BYE ends both dialogs at the B2BUA as it does without one.
func TestARefusedSubscriptionKeepsNoLeg(t *testing.T) {
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		in.Handle(func(r *sip.InDialogRequest) { r.Reject(489) })
		calls <- in
		in.Accept(answer)
	})
	b2b := newB2BUA(t, &hooks{}, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	in := within(t, "INVITE at the callee", calls)
	if resp, err := out.Send(sip.SUBSCRIBE, sip.Body{}, conferenceEvent); err != nil || resp.StatusCode != 489 {
		t.Fatalf("SUBSCRIBE within the dialog got %v, %v; want 489", resp, err)
	}
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, in.Done(), "BYE of the B2BUA at the callee")
	calleeDialog, _ := in.Dialog()
	if !until(5*time.Second, func() bool { return optionsStatus(t, callee, calleeDialog, b2b) == 481 }) {
		t.Error("a request within the callee's dialog still finds it at the B2BUA after the BYE, want 481")
	}
}

// optionsStatus sends an OPTIONS within d, a dialog of ua with the
// B2BUA, and returns the status of its final response, 0 for none. An
// OPTIONS is answered within any dialog that the B2BUA knows (RFC 3261
// 11.2), and gets 481 in one that it does not (12.2.2).
func optionsStatus(t *testing.T, ua *sip.UA, d sip.Dialog, b2b *sip.UA) int {
	t.Helper()
	ct, err := ua.Stack().SendRequest(d.NewRequest(sip.OPTIONS), sip.Destination{Transport: sip.UDP, Addr: b2b.Stack().LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	status := 0
	for resp := range ct.Responses() {
		status = resp.StatusCode
	}
	return status
}

// waitClosed waits for done.
func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
	}
}
