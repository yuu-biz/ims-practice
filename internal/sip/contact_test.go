package sip

import (
	"reflect"
	"testing"
)

// focusContact is the Contact of a conference focus: a URI that names
// no host of the test, so a request of the peer reaches the UA only
// along the Record-Route that the UA put in.
var focusContact = NameAddr{URI: "sip:conf-1@conf.example.invalid", Params: Params{{Name: "isfocus"}}}

// TestInviteWithTheContactOfAnotherLeg is TS 24.229 5.7.5.1 at the UAC
// side: a B2BUA passes on a Contact with isfocus and record-routes
// itself. The callee keeps that Contact as the remote target and the
// B2BUA as its route set; the B2BUA's own route set leaves it out, and
// the callee's BYE reaches it.
func TestInviteWithTheContactOfAnotherLeg(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		calls <- c
		c.Accept(answer)
	})
	b2bua := newUA(t, testTimers, "mmtel", noCalls)
	inv := invitation(calleeUA)
	inv.Contact, inv.RecordRoute = focusContact, true
	out, err := b2bua.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("caller got %v, want 200", got)
	}
	in := <-calls
	if got := in.Request.Get("Contact"); got != focusContact.String() {
		t.Errorf("INVITE has Contact %q, want %q", got, focusContact.String())
	}
	d, _ := in.Dialog()
	own := b2bua.ownRoute()
	na, _ := ParseNameAddr(own)
	if d.RemoteTarget != focusContact.URI || !reflect.DeepEqual(d.RouteSet, []string{na.URI}) {
		t.Errorf("callee's dialog has remote target %q, route set %q; want %q and the B2BUA", d.RemoteTarget, d.RouteSet, focusContact.URI)
	}
	if mine, _ := out.Dialog(); len(mine.RouteSet) != 0 {
		t.Errorf("B2BUA's route set is %q, want it empty", mine.RouteSet)
	}
	if err := in.Bye(); err != nil {
		t.Fatalf("callee's BYE: %v", err)
	}
	waitDone(t, out.Done(), "B2BUA")
}

// TestAnswerWithTheContactOfAnotherLeg is the same at the UAS side
// (IncomingCall.SetContact): the 200 carries the given Contact and the
// UA's own Record-Route on top, the caller's BYE comes back along it,
// and a re-INVITE's 200 carries the same Contact again.
func TestAnswerWithTheContactOfAnotherLeg(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	focusUA := newUA(t, testTimers, "mmtel", func(c *IncomingCall) {
		c.SetContact(focusContact, true)
		c.Handle(func(r *InDialogRequest) { r.Accept(answer) })
		c.Accept(answer)
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(focusUA))
	if err != nil {
		t.Fatal(err)
	}
	resps := responses(t, out)
	ok := resps[len(resps)-1]
	if ok.StatusCode != 200 || ok.Get("Contact") != focusContact.String() || ok.Get("Record-Route") != focusUA.ownRoute() {
		t.Fatalf("caller got %d with Contact %q, Record-Route %q", ok.StatusCode, ok.Get("Contact"), ok.Get("Record-Route"))
	}
	in := <-calls
	if d, _ := in.Dialog(); len(d.RouteSet) != 0 {
		t.Errorf("focus's route set is %q, want it empty", d.RouteSet)
	}
	resp, err := out.ReInvite(offer)
	if err != nil || resp.StatusCode != 200 || resp.Get("Contact") != focusContact.String() {
		t.Fatalf("re-INVITE got %v, %v; want 200 with the focus's Contact", resp, err)
	}
	if err := out.Bye(); err != nil {
		t.Fatalf("caller's BYE: %v", err)
	}
	waitDone(t, in.Done(), "focus")
}
