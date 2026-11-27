package sip

import (
	"errors"
	"net"
	"testing"
	"time"
)

// This file is CancelReInvite: the UA gives up a change of the session
// that it asked for with a re-INVITE (RFC 3261 9.1, RFC 6141 3.3). A
// raw peer answers, so that the test sees the CANCEL as it goes out and
// chooses what the re-INVITE gets.

// noRequest reads from the raw peer for d and fails when a request of
// method comes. The requests sent before it come again meanwhile, as a
// client retransmits over UDP (RFC 3261 17.1.1.2).
func noRequest(t *testing.T, peer *net.UDPConn, method Method, d time.Duration) {
	t.Helper()
	buf := make([]byte, 65535)
	deadline := time.Now().Add(d)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return
		}
		peer.SetReadDeadline(time.Now().Add(left))
		n, err := peer.Read(buf)
		if err != nil {
			return
		}
		if m, err := Parse(buf[:n]); err == nil && m.Method == method {
			t.Fatalf("got %s, want no %s yet", startLine(m), method)
		}
	}
}

// RFC 3261 9.1: the CANCEL waits for a provisional response to the
// re-INVITE, and then carries the Request-URI, Call-ID, To with its
// tag, From and CSeq number of the re-INVITE, with CANCEL as its
// method, and the top Via of the re-INVITE as its only Via. It takes
// the Reason that the user gave for giving the change up (RFC 3326 2).
// The 487 that follows leaves the session as it was, so the UA may
// offer again (RFC 3261 14.1).
func TestCancelOfAReInviteWaitsForAProvisionalResponse(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInvite(holdOffer)
		done <- reInviteDone{resp, err}
	}()
	reinvite := readRequest(t, p, INVITE)

	reason := NewReason(ReasonSIP, 487, "resources not reserved")
	if err := out.CancelReInvite(reason.Header()); err != nil {
		t.Fatalf("CancelReInvite: %v", err)
	}
	noRequest(t, p.peer, CANCEL, 5*slowTimers.T1)

	p.respond(reinvite, 180, Body{})
	cancel := readRequest(t, p, CANCEL)
	cseq, _ := ParseCSeq(reinvite.Get("CSeq"))
	switch {
	case cancel.RequestURI != reinvite.RequestURI:
		t.Errorf("the CANCEL goes to %q, want the Request-URI %q of the re-INVITE", cancel.RequestURI, reinvite.RequestURI)
	case cancel.Get("Call-ID") != reinvite.Get("Call-ID"):
		t.Errorf("the CANCEL has Call-ID %q, want %q of the re-INVITE", cancel.Get("Call-ID"), reinvite.Get("Call-ID"))
	case cancel.Get("To") != reinvite.Get("To"):
		t.Errorf("the CANCEL has To %q, want %q of the re-INVITE, tag and all", cancel.Get("To"), reinvite.Get("To"))
	case cancel.Get("From") != reinvite.Get("From"):
		t.Errorf("the CANCEL has From %q, want %q of the re-INVITE", cancel.Get("From"), reinvite.Get("From"))
	case cancel.Get("CSeq") != (CSeq{Number: cseq.Number, Method: CANCEL}).String():
		t.Errorf("the CANCEL has CSeq %q, want %d CANCEL", cancel.Get("CSeq"), cseq.Number)
	case len(cancel.GetAll("Via")) != 1 || cancel.Get("Via") != reinvite.Get("Via"):
		t.Errorf("the CANCEL has Via %q, want the one top Via %q of the re-INVITE", cancel.GetAll("Via"), reinvite.Get("Via"))
	case cancel.Get("Reason") != reason.String():
		t.Errorf("the CANCEL has Reason %q, want %q", cancel.Get("Reason"), reason.String())
	}

	p.respond(cancel, 200, Body{})
	p.respond(reinvite, 487, Body{})
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 487 {
		t.Fatalf("the re-INVITE returned %v, %v; want the 487", r.resp, r.err)
	}

	// The session is the one of the last offer and answer, and the UA
	// may offer again.
	updated := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.Update(offer2)
		updated <- reInviteDone{resp, err}
	}()
	update := readRequest(t, p, UPDATE)
	p.respond(update, 200, answer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if r := finalOf(t, updated); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("the UPDATE after the cancelled re-INVITE returned %v, %v; want the 200", r.resp, r.err)
	}
}

// ReInvitation.Cancelled cancels a re-INVITE that has not gone out
// yet, where CancelReInvite would find none to cancel: the re-INVITE
// waits for the final response of the request before it in the dialog
// (RFC 3261 12.2.1.1). The CANCEL goes once the re-INVITE has gone and
// a provisional response has come (RFC 3261 9.1), with the header
// fields that CancelHeaders gives then.
func TestAReInviteCancelledBeforeItGoesOut(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	refreshed := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.Update(Body{})
		refreshed <- reInviteDone{resp, err}
	}()
	update := readRequest(t, p, UPDATE)

	cancelled := make(chan struct{})
	close(cancelled)
	reason := NewReason(ReasonSIP, 487, "resources not reserved")
	done := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInviteWith(ReInvitation{
			Body:          holdOffer,
			Cancelled:     cancelled,
			CancelHeaders: func() []HeaderField { return []HeaderField{reason.Header()} },
		})
		done <- reInviteDone{resp, err}
	}()
	noRequest(t, p.peer, INVITE, 5*slowTimers.T1)

	p.respond(update, 200, Body{}, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if r := finalOf(t, refreshed); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("the UPDATE returned %v, %v; want the 200", r.resp, r.err)
	}
	reinvite := readRequest(t, p, INVITE)
	noRequest(t, p.peer, CANCEL, 5*slowTimers.T1)

	p.respond(reinvite, 180, Body{})
	cancel := readRequest(t, p, CANCEL)
	if cancel.Get("Reason") != reason.String() {
		t.Errorf("the CANCEL has Reason %q, want %q", cancel.Get("Reason"), reason.String())
	}
	p.respond(cancel, 200, Body{})
	p.respond(reinvite, 487, Body{})
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 487 {
		t.Fatalf("the re-INVITE returned %v, %v; want the 487", r.resp, r.err)
	}
}

// CancelReInvite has no re-INVITE to cancel before the UA sends one,
// and none once the re-INVITE has its final response (RFC 3261 9.1).
func TestCancelReInviteWithoutAReInviteInProgress(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	if err := out.CancelReInvite(); !errors.Is(err, ErrNoReInvite) {
		t.Errorf("CancelReInvite before any re-INVITE: %v, want ErrNoReInvite", err)
	}
	done := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInvite(holdOffer)
		done <- reInviteDone{resp, err}
	}()
	reinvite := readRequest(t, p, INVITE)
	p.respond(reinvite, 488, Body{})
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 488 {
		t.Fatalf("the re-INVITE returned %v, %v; want the 488", r.resp, r.err)
	}
	if err := out.CancelReInvite(); !errors.Is(err, ErrNoReInvite) {
		t.Errorf("CancelReInvite after the final response: %v, want ErrNoReInvite", err)
	}
}

// RFC 6141 3.3 and 3.8: a UAS that has made the change of the session
// answers the cancelled re-INVITE with a 2xx, not with an error. The UA
// takes that answer, acknowledges the 2xx and goes on with the session
// of that exchange, so that the two ends do not end up with different
// sessions.
func TestACancelledReInviteThatGetsA2xxKeepsTheNewSession(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInvite(holdOffer)
		done <- reInviteDone{resp, err}
	}()
	reinvite := readRequest(t, p, INVITE)
	p.respond(reinvite, 183, Body{})
	if err := out.CancelReInvite(); err != nil {
		t.Fatalf("CancelReInvite: %v", err)
	}
	cancel := readRequest(t, p, CANCEL)
	p.respond(cancel, 200, Body{})

	p.respond(reinvite, 200, holdAnswer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 || string(r.resp.Body) != string(holdAnswer.Content) {
		t.Fatalf("the cancelled re-INVITE returned %v, %v; want the 200 with the answer", r.resp, r.err)
	}
	cseq, _ := ParseCSeq(reinvite.Get("CSeq"))
	if ack := readRequest(t, p, ACK); ack.Get("CSeq") != (CSeq{Number: cseq.Number, Method: ACK}).String() {
		t.Errorf("the ACK has CSeq %q, want %d ACK for the re-INVITE", ack.Get("CSeq"), cseq.Number)
	}
}

// The CANCEL that a watcher of ReInvitation.Cancelled asks for belongs
// to the re-INVITE it watches. When that re-INVITE has its final
// response by the time the watcher acts, and the dialog has a later
// re-INVITE already, the later one goes on uncancelled (RFC 3261 9.1).
func TestTheCancelOfAReInviteThatIsOverLeavesTheNextOne(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	d, err := out.currentDialog()
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInvite(holdOffer)
		first <- reInviteDone{resp, err}
	}()
	reinvite := readRequest(t, p, INVITE)
	d.c.mu.Lock()
	watched := d.sentReInvite
	d.c.mu.Unlock()
	p.respond(reinvite, 200, holdAnswer)
	if r := finalOf(t, first); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("the first re-INVITE returned %v, %v; want the 200", r.resp, r.err)
	}

	second := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInvite(holdOffer)
		second <- reInviteDone{resp, err}
	}()
	next := readRequest(t, p, INVITE)
	p.respond(next, 180, Body{})
	if err := d.cancelReInvite(watched, nil); !errors.Is(err, ErrNoReInvite) {
		t.Errorf("the CANCEL of the first re-INVITE: %v, want ErrNoReInvite", err)
	}
	noRequest(t, p.peer, CANCEL, 5*slowTimers.T1)
	p.respond(next, 200, holdAnswer)
	if r := finalOf(t, second); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("the second re-INVITE returned %v, %v; want the 200", r.resp, r.err)
	}
}
