package b2bua_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

var conferenceEvent = sip.HeaderField{Name: "Event", Value: "conference"}

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
	b2b := newB2BUA(t, &hooks{crossing: true}, contactOf(t, callee))
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
	defer subscriberEnds()
	if resp, err := out.Send(sip.SUBSCRIBE, sip.Body{}, conferenceEvent, sip.HeaderField{Name: "Expires", Value: "600"}); err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within the dialog got %v, %v; want 200", resp, err)
	}
	endAtTheNotifier := within(t, "SUBSCRIBE at the callee", notifierEnds)
	defer endAtTheNotifier()

	d, _ := out.Dialog()
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, in.Done(), "BYE of the B2BUA at the callee")
	calleeDialog, _ := in.Dialog()

	notified := make(chan *sip.Message, 1)
	go func() {
		resp, err := in.Send(sip.NOTIFY, sip.Body{}, conferenceEvent,
			sip.HeaderField{Name: "Subscription-State", Value: "terminated;reason=noresource"})
		if err != nil {
			t.Errorf("the NOTIFY after the BYE: %v", err)
		}
		notified <- resp
	}()
	n := within(t, "NOTIFY at the caller after the BYE", atCaller)
	if n.Request.Method != sip.NOTIFY || n.Request.Get("Call-ID") != d.ID.CallID || n.Request.Get("Subscription-State") != "terminated;reason=noresource" {
		t.Errorf("the caller got %s in Call-ID %q with Subscription-State %q, want the NOTIFY that ends the subscription in %q",
			n.Request.Method, n.Request.Get("Call-ID"), n.Request.Get("Subscription-State"), d.ID.CallID)
	}
	if _, err := n.Accept(sip.Body{}); err != nil {
		t.Fatal(err)
	}
	if resp := within(t, "response to the NOTIFY at the callee", notified); resp == nil || resp.StatusCode != 200 {
		t.Fatalf("the NOTIFY after the BYE got %v, want the caller's 200", resp)
	}
	endAtTheNotifier()
	subscriberEnds()

	if !until(5*time.Second, func() bool { return optionsStatus(t, callee, calleeDialog, b2b) == 481 }) {
		t.Error("a request within the callee's dialog still finds it at the B2BUA once the subscription has ended, want 481")
	}
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
