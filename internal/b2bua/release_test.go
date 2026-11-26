package b2bua_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have the role end a call that it cannot let go
// on: with Refusal.Release, or by declining a response or an ACK
// (Hooks).

// endedByBYE waits until the leg whose Done is done has ended, and
// checks that a BYE of the B2BUA ended it.
func endedByBYE(t *testing.T, what string, done <-chan struct{}, endedBy func() *sip.Message) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not end", what)
	}
	if m := endedBy(); m == nil || m.Method != sip.BYE {
		t.Errorf("%s ended by %v, want a BYE of the B2BUA", what, m)
	}
}

// cancelled waits until the callee's INVITE has ended, and checks that
// a CANCEL ended it.
func cancelled(t *testing.T, in *sip.IncomingCall) {
	t.Helper()
	select {
	case <-in.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the callee's INVITE did not end")
	}
	if !errors.Is(in.Err(), sip.ErrCancelled) {
		t.Errorf("the callee's INVITE ended with %v, want a CANCEL", in.Err())
	}
}

// releaseINFO refuses each INFO with 500 and releases the call.
func releaseINFO(req *sip.Message, _ b2bua.Direction) *b2bua.Refusal {
	if req.Method == "INFO" {
		return &b2bua.Refusal{Status: 500, Release: true}
	}
	return nil
}

// A request that the role refuses with Release gets the refusal, and
// then each side of the answered call gets a BYE.
func TestRefusalReleasesTheAnsweredCall(t *testing.T) {
	h := &hooks{request: releaseINFO}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		in.Accept(answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	in := within(t, "call at the callee", calls)
	if resp, err := out.Send("INFO", sip.Body{}); err != nil || resp.StatusCode != 500 {
		t.Errorf("the INFO got %v %v, want the role's 500", resp, err)
	}
	endedByBYE(t, "the caller's call", out.Done(), out.EndedBy)
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
}

// Before the callee has answered, a release ends the caller's INVITE
// with 500 and cancels the one toward the callee.
func TestRefusalReleasesTheEarlyCall(t *testing.T) {
	h := &hooks{request: releaseINFO}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		in.Progress(180, sip.Body{})
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "call at the callee", calls)
	if resp := within(t, "180 at the caller", out.Responses()); resp.StatusCode != 180 {
		t.Fatalf("the caller got %d, want 180", resp.StatusCode)
	}
	if resp, err := out.Send("INFO", sip.Body{}); err != nil || resp.StatusCode != 500 {
		t.Errorf("the INFO got %v %v, want the role's 500", resp, err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 500 {
		t.Errorf("the INVITE got %d, want 500", final.StatusCode)
	}
	cancelled(t, in)
}

// A provisional response that the role declines does not reach the
// caller: the caller's INVITE gets 500, and the callee a CANCEL.
func TestDeclinedProvisionalResponseReleasesTheCall(t *testing.T) {
	h := &hooks{decline: func(m *sip.Message, _ b2bua.Direction) bool { return m.StatusCode == 183 }}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		in.Progress(183, answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "call at the callee", calls)
	provisional, final := finalOf(t, out)
	if final.StatusCode != 500 {
		t.Errorf("the INVITE got %d, want 500", final.StatusCode)
	}
	for _, resp := range provisional {
		if resp.StatusCode == 183 {
			t.Error("the caller got the 183 that the role declined")
		}
	}
	cancelled(t, in)
}

// A 2xx that the role declines does not reach the caller, which gets 500
// in its place; the callee's 2xx is acknowledged, and the callee gets a
// BYE.
func TestDeclined2xxReleasesTheCall(t *testing.T) {
	h := &hooks{decline: func(m *sip.Message, _ b2bua.Direction) bool { return m.StatusCode == 200 }}
	calls := make(chan *sip.IncomingCall, 1)
	acked := make(chan error, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		_, err := in.Accept(answer)
		acked <- err
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "call at the callee", calls)
	if _, final := finalOf(t, out); final.StatusCode != 500 {
		t.Errorf("the INVITE got %d, want 500", final.StatusCode)
	}
	if err := within(t, "ACK at the callee", acked); err != nil {
		t.Errorf("the callee's 2xx got no ACK: %v", err)
	}
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
}

// A 2xx to a request within the dialog that the role declines: the
// request gets 500 in its place, and both sides get a BYE.
func TestDeclined2xxOfARequestReleasesTheCall(t *testing.T) {
	var armed atomic.Bool
	h := &hooks{decline: func(m *sip.Message, d b2bua.Direction) bool {
		return armed.Load() && m.StatusCode == 200 && d == b2bua.ToCaller
	}}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		in.Handle(func(r *sip.InDialogRequest) { r.Accept(answer) })
		in.Accept(answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	in := within(t, "call at the callee", calls)
	armed.Store(true)
	if resp, err := out.ReInvite(offer); err != nil || resp.StatusCode != 500 {
		t.Errorf("the re-INVITE got %v %v, want 500 in the place of the declined 2xx", resp, err)
	}
	endedByBYE(t, "the caller's call", out.Done(), out.EndedBy)
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
}

// An answer in the ACK that the role declines does not go on: the
// callee's 2xx gets an ACK without it, and both sides get a BYE.
func TestDeclinedAckReleasesTheCall(t *testing.T) {
	h := &hooks{decline: func(m *sip.Message, _ b2bua.Direction) bool { return m.Method == sip.ACK }}
	calls := make(chan *sip.IncomingCall, 1)
	w := newWire()
	callee := startUA(t, "callee", timers, w, func(in *sip.IncomingCall) {
		calls <- in
		in.Accept(offer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, nil)
	inv.Body = sip.Body{}
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	if err := out.Ack(answer); err != nil {
		t.Fatalf("ACK: %v", err)
	}
	in := within(t, "call at the callee", calls)
	endedByBYE(t, "the caller's call", out.Done(), out.EndedBy)
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
	if bodies := w.bodies(sip.ACK); len(bodies) != 1 || bodies[0] != "" {
		t.Errorf("the callee got ACKs with %q, want one without the declined answer", bodies)
	}
}

// A release that comes after the callee's 2xx, while the Call prepares
// that 2xx for the caller: the caller has no final response yet, so it
// gets 500 and not the 2xx, and the callee gets a BYE.
func TestReleaseBeforeTheCallerHasThe2xx(t *testing.T) {
	h := &hooks{request: releaseINFO}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		in.Progress(180, sip.Body{})
		in.Accept(answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	outs := make(chan *sip.OutgoingCall, 1)
	finals := make(chan *sip.Message, 1)
	h.answer = func() {
		// The caller's INFO in its early dialog crosses the callee's 2xx,
		// and the role releases the call for it. The 2xx waits here until
		// the caller's INVITE has its final response, or for a while if it
		// gets none.
		out := <-outs
		if resp, err := out.Send("INFO", sip.Body{}); err != nil || resp.StatusCode != 500 {
			t.Errorf("the caller's INFO got %v %v, want the role's 500", resp, err)
		}
		select {
		case f := <-finals:
			finals <- f
		case <-time.After(2 * time.Second):
		}
	}
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	outs <- out
	go func() {
		for resp := range out.Responses() {
			if resp.StatusCode >= 200 {
				finals <- resp
				return
			}
		}
	}()
	in := within(t, "call at the callee", calls)
	if final := within(t, "final response at the caller", finals); final.StatusCode != 500 {
		t.Errorf("the INVITE got %d, want 500", final.StatusCode)
	}
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
}
