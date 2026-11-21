package sip

import (
	"errors"
	"reflect"
	"testing"
)

// RFC 6228: a 199 tells the caller that one early dialog is over while
// the INVITE goes on. An AS that turns from a callee who does not
// answer to the next one sends it, and the responses that follow start
// a new early dialog with a To tag of its own.
func TestEarlyDialogTerminatedBetweenTwoUAs(t *testing.T) {
	errs := make(chan error, 3)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		errs <- c.EarlyDialogTerminated(480)
		c.Progress(180, Body{})
		if err := c.Progress(199, Body{}); err == nil {
			t.Error("Progress sent a 199")
		}
		errs <- c.EarlyDialogTerminated(408, PEarlyMedia{Directions: []string{EarlyMediaInactive}}.Header())
		c.Progress(183, answer)
		_, err := c.Accept(Body{})
		errs <- err
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6228 4: the caller says that it takes a 199.
	if !out.Request.Supports(Option199) {
		t.Errorf("INVITE has Supported %q, want 199 in it", out.Request.GetAll("Supported"))
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{180, 199, 183, 200}) {
		t.Fatalf("caller got %v, want 180, 199, 183 and 200", codes(got))
	}
	if err := <-errs; err == nil {
		t.Error("EarlyDialogTerminated before any early dialog succeeded")
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}

	gone := got[1]
	// RFC 6228 5: the To tag of the dialog that ended, a Reason with the
	// status code that ended it, and no 100rel for a UAC that only
	// supports it.
	want := []Reason{{Protocol: "SIP", Cause: 408, HasCause: true, Text: "Request Timeout"}}
	if tagOf(gone.Get("To")) != tagOf(got[0].Get("To")) || !reflect.DeepEqual(Reasons(gone), want) ||
		gone.Requires(Option100rel) || gone.Supports(Option199) || gone.Get("P-Early-Media") != "inactive" {
		t.Errorf("199 is\n%s\nwant the tag of the 180, Reason SIP;cause=408 and no option tags", gone)
	}
	if tagOf(got[2].Get("To")) == tagOf(got[0].Get("To")) || tagOf(got[3].Get("To")) != tagOf(got[2].Get("To")) {
		t.Errorf("To tags are %q, %q and %q; want a new one for the 183 and the same again for the 200",
			tagOf(got[0].Get("To")), tagOf(got[2].Get("To")), tagOf(got[3].Get("To")))
	}

	dialogs := out.Dialogs()
	if len(dialogs) != 2 {
		t.Fatalf("the call has %d dialogs, want 2", len(dialogs))
	}
	first, _ := dialogs[0].Dialog()
	second, _ := dialogs[1].Dialog()
	if first.State != DialogTerminated || second.State != DialogConfirmed || out.DialogOf(gone) != dialogs[0] {
		t.Errorf("dialogs are %s and %s, want the one of the 199 Terminated and the other Confirmed", first.State, second.State)
	}
	if d, _ := out.Dialog(); d.ID != second.ID {
		t.Errorf("the call goes on in the dialog with tag %q, want that of the 200", d.ID.RemoteTag)
	}
	if err := out.Bye(); err != nil {
		t.Errorf("Bye: %v", err)
	}
}

// RFC 6228 4: before the 2xx, the call goes on in the next early dialog
// once a 199 has ended the first. A 199 for a dialog that the UAC never
// heard of is discarded.
func TestUACTakesA199(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	at := addrOf(peer).String()
	first, second := "sip:first@"+at, "sip:second@"+at
	out, err := callerUA.Invite(Invitation{RequestURI: "sip:" + callee + "@" + at, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)
	terminated := func(tag, contact string) *Message {
		resp := forked(invite, 199, tag, contact, 0)
		resp.Add("Reason", NewReason(ReasonSIP, 486, "Busy Here").String())
		return resp
	}

	send(t, peer, terminated("unknown", first), callerUA.Stack())
	send(t, peer, forked(invite, 180, "first", first, 0), callerUA.Stack())
	send(t, peer, forked(invite, 180, "second", second, 0), callerUA.Stack())
	send(t, peer, terminated("first", first), callerUA.Stack())
	// A response that is late for the dialog that has ended.
	send(t, peer, forked(invite, 183, "first", first, 0), callerUA.Stack())

	var got []int
	for range 3 {
		got = append(got, (<-out.Responses()).StatusCode)
	}
	if !reflect.DeepEqual(got, []int{180, 180, 199}) {
		t.Fatalf("caller got %v, want 180, 180 and the 199 of the first dialog", got)
	}
	if d, _ := out.Dialog(); d.ID.RemoteTag != "second" || d.State != DialogEarly {
		t.Errorf("the call goes on in the dialog with tag %q in state %s, want second and Early", d.ID.RemoteTag, d.State)
	}
	if ds := out.Dialogs(); len(ds) != 2 {
		t.Errorf("the call has %d dialogs, want 2: the unknown 199 creates none", len(ds))
	}
	send(t, peer, forked(invite, 486, "second", second, 0), callerUA.Stack())
	if rest := codes(responses(t, out)); !reflect.DeepEqual(rest, []int{486}) {
		t.Errorf("caller got %v after the 199, want 486 alone", rest)
	}
}

// RFC 6228 5: a UAS must not send a 199 to a caller that does not list
// it in Supported.
func TestNo199WithoutSupport(t *testing.T) {
	errs := make(chan error, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Progress(180, Body{})
		errs <- c.EarlyDialogTerminated(480)
		c.Reject(480)
	})
	peer := rawSocket(t)
	send(t, peer, rawINVITE(peer), calleeUA.Stack())
	if err := <-errs; !errors.Is(err, ErrNo199) {
		t.Errorf("EarlyDialogTerminated: %v, want ErrNo199", err)
	}
	for {
		m := readDatagram(t, peer)
		if m.StatusCode == 199 {
			t.Fatal("a 199 went to a caller that does not support it")
		}
		if m.StatusCode == 480 {
			break
		}
	}
}

// A request of the caller that is still with the callee's user when a
// 199 ends its early dialog gets its answer, and leaves the dialog that
// follows alone.
func TestRequestOutlivesItsEarlyDialog(t *testing.T) {
	arrived := make(chan *InDialogRequest, 1)
	ended := make(chan struct{})
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) { arrived <- r })
		c.Progress(183, answer)
		r := <-arrived
		if err := c.EarlyDialogTerminated(480); err != nil {
			t.Error(err)
		}
		if _, err := r.Accept(answer2); err != nil {
			t.Error(err)
		}
		close(ended)
		c.Progress(183, answer)
		c.Accept(Body{})
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	<-out.Responses()
	if resp, err := out.Update(offer2); err != nil || resp.StatusCode != 200 {
		t.Fatalf("UPDATE got %v, %v; want the 200 that came after the 199", resp, err)
	}
	<-ended
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{199, 183, 200}) {
		t.Errorf("caller got %v after the UPDATE, want 199, 183 and 200", got)
	}
}
