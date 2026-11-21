package sip

import (
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestReliableProvisionalResponsesBetweenTwoUAs(t *testing.T) {
	progress := make(chan error, 2)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		progress <- c.Progress(183, answer)
		progress <- c.Progress(180, Body{})
		c.Accept(answer)
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Request.Get("Supported"); got != "100rel" {
		t.Errorf("INVITE Supported = %q, want 100rel (RFC 3262 4)", got)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{183, 180, 200}) {
		t.Fatalf("caller got %v, want 183, 180 and 200", codes(got))
	}
	// Progress returns once the PRACK has come, for each response.
	for range 2 {
		if err := <-progress; err != nil {
			t.Errorf("Progress: %v", err)
		}
	}
	first, err := ParseRSeq(got[0].Get("RSeq"))
	if err != nil || got[0].Get("Require") != "100rel" {
		t.Fatalf("183 has Require %q and RSeq %q, want 100rel and a number", got[0].Get("Require"), got[0].Get("RSeq"))
	}
	// RFC 3262 3.
	if first >= 1<<31 {
		t.Errorf("first RSeq %d is not below 2**31", first)
	}
	if second, _ := ParseRSeq(got[1].Get("RSeq")); second != first+1 {
		t.Errorf("RSeq of the 180 is %d, want %d", second, first+1)
	}
	if string(got[0].Body) != string(answer.Content) {
		t.Errorf("183 carries %q, want the answer for early media", got[0].Body)
	}
}

// rawPRACK is the PRACK a raw caller sends for a reliable provisional
// response, built from the early dialog that the response creates.
func rawPRACK(t *testing.T, peer *net.UDPConn, invite, prov *Message, cseq uint32) *Message {
	t.Helper()
	d, err := NewUACDialog(invite, prov)
	if err != nil {
		t.Fatal(err)
	}
	d.LocalSeq = cseq - 1
	prack := d.NewRequest(PRACK)
	prack.Add("RAck", rackFor(prov).String())
	via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	prack.Prepend("Via", via.String())
	return prack
}

func TestUASRetransmitsAReliableProvisionalUntilThePRACK(t *testing.T) {
	progress := make(chan error, 1)
	calleeUA := newUA(t, slowTimers, callee, func(c *IncomingCall) {
		progress <- c.Progress(183, answer)
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	invite.Add("Supported", "100rel")
	send(t, peer, invite, calleeUA.Stack())

	prov := readSkippingTrying(t, peer)
	if prov.StatusCode != 183 || prov.Get("Require") != "100rel" || prov.Get("RSeq") == "" {
		t.Fatalf("got %s with Require %q and RSeq %q, want a reliable 183", startLine(prov), prov.Get("Require"), prov.Get("RSeq"))
	}
	// RFC 3262 3: without a PRACK, the 183 comes again after T1, then
	// after 2*T1.
	start := time.Now()
	var gaps []time.Duration
	for range 2 {
		again := readDatagram(t, peer)
		if again.StatusCode != 183 || again.Get("RSeq") != prov.Get("RSeq") {
			t.Fatalf("got %s with RSeq %q, want the 183 again", startLine(again), again.Get("RSeq"))
		}
		gaps = append(gaps, time.Since(start))
		start = time.Now()
	}
	if gaps[0] < slowTimers.T1/2 || gaps[1] < slowTimers.T1+slowTimers.T1/2 {
		t.Errorf("183 retransmitted after %v, want about T1 and then 2*T1 (T1 = %v)", gaps, slowTimers.T1)
	}

	wrong := rawPRACK(t, peer, invite, prov, 3)
	rseq, _ := ParseRSeq(prov.Get("RSeq"))
	wrong.Set("RAck", RAck{RSeq: rseq + 1, CSeq: CSeq{Number: 1, Method: INVITE}}.String())
	send(t, peer, wrong, calleeUA.Stack())
	if resp := readSkipping183(t, peer); resp.StatusCode != 481 || resp.Get("CSeq") != "3 PRACK" {
		t.Fatalf("PRACK for another RSeq got %s with CSeq %q, want 481 (RFC 3262 3)", startLine(resp), resp.Get("CSeq"))
	}

	// RFC 3261 12.2.2: the rejected PRACK left the remote sequence
	// number at the INVITE's, so a lower CSeq than its 3 is in order.
	send(t, peer, rawPRACK(t, peer, invite, prov, 2), calleeUA.Stack())
	if resp := readSkipping183(t, peer); resp.StatusCode != 200 || resp.Get("CSeq") != "2 PRACK" {
		t.Fatalf("PRACK got %s with CSeq %q, want 200", startLine(resp), resp.Get("CSeq"))
	}
	select {
	case err := <-progress:
		if err != nil {
			t.Errorf("Progress: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Progress did not return after the PRACK")
	}
	expectSilence(t, peer, 6*slowTimers.T1, "the PRACK should stop the retransmissions")
}

// readSkipping183 reads the next datagram that is not a retransmission
// of the 183, which may cross a PRACK.
func readSkipping183(t *testing.T, peer *net.UDPConn) *Message {
	t.Helper()
	for {
		if m := readDatagram(t, peer); m.StatusCode != 183 {
			return m
		}
	}
}

func TestUASRejectsTheINVITEWithoutAPRACK(t *testing.T) {
	timers := Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}
	progress := make(chan error, 1)
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, timers, callee, func(c *IncomingCall) {
		calls <- c
		progress <- c.Progress(180, Body{})
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	invite.Add("Require", "100rel")
	start := time.Now()
	send(t, peer, invite, calleeUA.Stack())
	for {
		m := readDatagram(t, peer)
		if m.StatusCode == 500 {
			break
		}
		if m.StatusCode != 180 && m.StatusCode != 100 {
			t.Fatalf("got %s, want 180s and then 500", startLine(m))
		}
	}
	// RFC 3262 3: after 64*T1 the INVITE gets a 5xx.
	if elapsed := time.Since(start); elapsed < 64*timers.T1 {
		t.Errorf("500 after %v, before 64*T1", elapsed)
	}
	if err := <-progress; !errors.Is(err, ErrNoPRACK) {
		t.Errorf("Progress: %v, want ErrNoPRACK", err)
	}
	c := <-calls
	waitDone(t, c.Done(), "callee")
	if !errors.Is(c.Err(), ErrNoPRACK) {
		t.Errorf("call ended with %v, want ErrNoPRACK", c.Err())
	}
}

func TestProvisionalResponsesAreUnreliableWithout100rel(t *testing.T) {
	progress := make(chan error, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		progress <- c.Progress(180, Body{})
	})
	peer := rawSocket(t)
	send(t, peer, rawINVITE(peer), calleeUA.Stack())
	ringing := readSkippingTrying(t, peer)
	// RFC 3262 3: MUST NOT send it reliably.
	if ringing.StatusCode != 180 || ringing.Get("Require") != "" || ringing.Get("RSeq") != "" {
		t.Errorf("got %s with Require %q and RSeq %q, want a plain 180", startLine(ringing), ringing.Get("Require"), ringing.Get("RSeq"))
	}
	if err := <-progress; err != nil {
		t.Errorf("Progress: %v", err)
	}
}

// reliable is a reliable provisional response from a raw callee.
func reliable(invite *Message, code int, rseq uint32, target string) *Message {
	resp := NewResponse(invite, code, "callee")
	resp.Add("Contact", "<"+target+">")
	resp.Add("Require", "100rel")
	resp.Add("RSeq", strconv.FormatUint(uint64(rseq), 10))
	return resp
}

func TestUACAcknowledgesReliableProvisionalResponsesInOrder(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)

	progress := reliable(invite, 183, 5, target)
	send(t, peer, progress, callerUA.Stack())
	prack := readDatagram(t, peer)
	// RFC 3262 4 and 7.2: the PRACK goes within the early dialog.
	if prack.Method != PRACK || prack.RequestURI != target || prack.Get("RAck") != "5 1 INVITE" || prack.Get("CSeq") != "2 PRACK" {
		t.Fatalf("got %s %s with RAck %q and CSeq %q, want PRACK %s with 5 1 INVITE and 2 PRACK",
			startLine(prack), prack.RequestURI, prack.Get("RAck"), prack.Get("CSeq"), target)
	}
	if tagOf(prack.Get("To")) != "callee" {
		t.Errorf("PRACK To = %q, want the callee's tag", prack.Get("To"))
	}
	send(t, peer, NewResponse(prack, 200, ""), callerUA.Stack())

	// RFC 3262 4: a retransmission, and a response that skips an RSeq,
	// get no PRACK.
	send(t, peer, progress, callerUA.Stack())
	send(t, peer, reliable(invite, 180, 7, target), callerUA.Stack())
	expectSilence(t, peer, 3*slowTimers.T1, "no PRACK for a retransmission or a gap")

	send(t, peer, reliable(invite, 180, 6, target), callerUA.Stack())
	prack = readDatagram(t, peer)
	if prack.Method != PRACK || prack.Get("RAck") != "6 1 INVITE" || prack.Get("CSeq") != "3 PRACK" {
		t.Fatalf("got %s with RAck %q and CSeq %q, want PRACK with 6 1 INVITE and 3 PRACK",
			startLine(prack), prack.Get("RAck"), prack.Get("CSeq"))
	}
	send(t, peer, NewResponse(prack, 200, ""), callerUA.Stack())

	ok := NewResponse(invite, 200, "callee")
	ok.Add("Contact", "<"+target+">")
	send(t, peer, ok, callerUA.Stack())
	// RFC 3261 13.2.2.4: the ACK keeps the INVITE's number after two
	// PRACKs.
	if ack := readDatagram(t, peer); ack.Method != ACK || ack.Get("CSeq") != "1 ACK" {
		t.Errorf("got %s with CSeq %q, want ACK with 1 ACK", startLine(ack), ack.Get("CSeq"))
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{183, 180, 200}) {
		t.Errorf("caller got %v, want 183, the 180 with RSeq 6, and 200", got)
	}
}
