package sip

import (
	"errors"
	"testing"
	"time"
)

// ownHeaders is a UAConfig.OwnHeaders that names, in X-Own, what it was
// asked for: the method, whether a request or a response, and for a BYE
// why the UA ends the call.
func ownHeaders(method Method, response bool, reason error) []HeaderField {
	v := string(method) + " request"
	if response {
		v = string(method) + " response"
	}
	switch {
	case errors.Is(reason, ErrSessionExpired):
		v += " session expired"
	case reason != nil:
		v += " " + reason.Error()
	}
	return []HeaderField{{Name: "X-Own", Value: v}}
}

// What the UA sends on its own in a call carries OwnHeaders, as a UE
// needs: User-Agent or Server in each of them (GSMA IR.92 2.6), and
// P-Access-Network-Info in each request and response within a dialog
// (TS 24.229 5.1.2A.1.1).
func TestOwnHeadersGoIntoWhatTheUASendsByItself(t *testing.T) {
	timers := &handTimers{}
	callerUA := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls, AfterFunc: timers.afterFunc,
		SessionTimer: SessionTimerPolicy{Interval: 90 * time.Second}, OwnHeaders: ownHeaders}, "001010000000001")
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)
	if got := invite.Get("X-Own"); got != "" {
		t.Errorf("the INVITE of the user has X-Own %q, want none", got)
	}
	ok := forked(invite, 200, "callee", target, 0)
	ok.Add("Allow", "INVITE, ACK, BYE, UPDATE")
	ok.Add(sessionHeader, "90;refresher=uac")
	answer.addTo(ok)
	send(t, peer, ok, callerUA.Stack())
	expect := func(method Method, want string) *Message {
		t.Helper()
		for {
			if m := readDatagram(t, peer); m.Method == method {
				if got := m.Get("X-Own"); got != want {
					t.Errorf("%s has X-Own %q, want %q", method, got, want)
				}
				return m
			}
		}
	}
	expect(ACK, "ACK request")
	responses(t, out)

	resp := finalTo(t, callerUA, options(callerUA))
	if got := resp.Get("X-Own"); got != "OPTIONS response" {
		t.Errorf("answer to OPTIONS has X-Own %q, want %q", got, "OPTIONS response")
	}

	// RFC 4028 10: the refresh half way through the interval, and the BYE
	// when the session expires without one.
	// fire returns once the timer's function has, which for the refresh
	// is after its 200.
	refreshed := make(chan struct{})
	go func() {
		timers.fire(t, 45*time.Second)
		close(refreshed)
	}()
	refresh := expect(UPDATE, "UPDATE request")
	ok = NewResponse(refresh, 200, "")
	ok.Add(sessionHeader, "90;refresher=uac")
	send(t, peer, ok, callerUA.Stack())
	<-refreshed
	expired := make(chan struct{})
	go func() {
		timers.fire(t, 60*time.Second)
		close(expired)
	}()
	bye := expect(BYE, "BYE request session expired")
	send(t, peer, NewResponse(bye, 200, ""), callerUA.Stack())
	<-expired
	waitDone(t, out.Done(), "caller")
}

// The 200 that the UA sends for a BYE carries OwnHeaders too.
func TestOwnHeadersGoIntoTheAnswerToABye(t *testing.T) {
	calleeUA := newUAWith(t, UAConfig{Timers: testTimers, OwnHeaders: ownHeaders,
		OnCall: func(c *IncomingCall) { c.Accept(answer) }}, callee)
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	offer.addTo(invite)
	send(t, peer, invite, calleeUA.Stack())
	ok := readSkippingTrying(t, peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
	d, err := NewUACDialog(invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	bye := d.NewRequest(BYE)
	via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	bye.Prepend("Via", via.String())
	send(t, peer, bye, calleeUA.Stack())
	for {
		if m := readDatagram(t, peer); m.Get("CSeq") == bye.Get("CSeq") {
			if got := m.Get("X-Own"); m.StatusCode != 200 || got != "BYE response" {
				t.Errorf("BYE got %s with X-Own %q, want 200 with %q", startLine(m), got, "BYE response")
			}
			return
		}
	}
}
