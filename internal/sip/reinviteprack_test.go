package sip

import (
	"errors"
	"testing"
	"time"
)

// reInviteDone is what ReInvite returned.
type reInviteDone struct {
	resp *Message
	err  error
}

// reInvite runs ReInvite in a goroutine of its own, so that the test can
// answer the re-INVITE from the raw callee meanwhile.
func reInvite(out *OutgoingCall, body Body) <-chan reInviteDone {
	done := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInvite(body)
		done <- reInviteDone{resp, err}
	}()
	return done
}

// finalOf waits for ReInvite to return.
func finalOf(t *testing.T, done <-chan reInviteDone) reInviteDone {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("ReInvite did not return within 5s of the final response")
		return reInviteDone{}
	}
}

// readPRACK reads the next datagram, which has to be a PRACK within the
// dialog with rack in RAck and the CSeq cseq.
func readPRACK(t *testing.T, p *rawCallee, rack, cseq string) *Message {
	t.Helper()
	prack := readDatagram(t, p.peer)
	if prack.Method != PRACK || prack.Get("RAck") != rack || prack.Get("CSeq") != cseq || tagOf(prack.Get("To")) != "callee" {
		t.Fatalf("got %s with RAck %q, CSeq %q and To %q; want a PRACK within the dialog with %s and %s",
			startLine(prack), prack.Get("RAck"), prack.Get("CSeq"), prack.Get("To"), rack, cseq)
	}
	return prack
}

// acceptReInvite answers the re-INVITE with 200 and body, and reads the
// ACK.
func acceptReInvite(t *testing.T, p *rawCallee, reinvite *Message, body Body) {
	t.Helper()
	cseq, _ := ParseCSeq(reinvite.Get("CSeq"))
	p.respond(reinvite, 200, body, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	ack := readDatagram(t, p.peer)
	if want := (CSeq{Number: cseq.Number, Method: ACK}).String(); ack.Method != ACK || ack.Get("CSeq") != want {
		t.Fatalf("got %s with CSeq %q, want the ACK with %s", startLine(ack), ack.Get("CSeq"), want)
	}
}

// RFC 3262 4: the re-INVITE lists 100rel in Supported, so the UAC
// acknowledges each reliable provisional response to it with a PRACK in
// the dialog, as it does for the INVITE of the call. A retransmission,
// and a response that skips an RSeq, get none.
func TestReINVITEAcknowledgesReliableProvisionalResponsesInOrder(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := reInvite(out, holdOffer)
	reinvite := readDatagram(t, p.peer)
	if reinvite.Method != INVITE || reinvite.Get("CSeq") != "2 INVITE" || reinvite.Get("Supported") != "100rel" {
		t.Fatalf("got %s with CSeq %q and Supported %q, want the re-INVITE with 2 INVITE and 100rel",
			startLine(reinvite), reinvite.Get("CSeq"), reinvite.Get("Supported"))
	}

	progress := reliable(reinvite, 183, 5, p.target)
	send(t, p.peer, progress, callerUA.Stack())
	prack := readPRACK(t, p, "5 2 INVITE", "3 PRACK")
	if prack.RequestURI != p.target || len(prack.Body) > 0 {
		t.Errorf("PRACK goes to %s with body %q, want %s without one", prack.RequestURI, prack.Body, p.target)
	}
	p.respond(prack, 200, Body{})

	send(t, p.peer, progress, callerUA.Stack())
	send(t, p.peer, reliable(reinvite, 180, 7, p.target), callerUA.Stack())
	expectSilence(t, p.peer, 3*slowTimers.T1, "no PRACK for a retransmission or a gap")

	send(t, p.peer, reliable(reinvite, 180, 6, p.target), callerUA.Stack())
	p.respond(readPRACK(t, p, "6 2 INVITE", "4 PRACK"), 200, Body{})

	acceptReInvite(t, p, reinvite, holdAnswer)
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, %v; want the 200", r.resp, r.err)
	}
}

// RFC 3262 3: the RSeq numbers count within one INVITE transaction, so
// the next re-INVITE of the dialog starts its own, which may be lower
// than the last one of the re-INVITE before it. Its responses are in
// order all the same.
func TestEachReINVITEHasRSeqNumbersOfItsOwn(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	for i, round := range []struct {
		rseq       uint32
		rack, cseq string
	}{
		{7, "7 2 INVITE", "3 PRACK"},
		{1, "1 4 INVITE", "5 PRACK"},
	} {
		done := reInvite(out, holdOffer)
		reinvite := readDatagram(t, p.peer)
		send(t, p.peer, reliable(reinvite, 183, round.rseq, p.target), callerUA.Stack())
		p.respond(readPRACK(t, p, round.rack, round.cseq), 200, Body{})
		acceptReInvite(t, p, reinvite, holdAnswer)
		if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
			t.Fatalf("re-INVITE %d returned %v, %v; want the 200", i+1, r.resp, r.err)
		}
	}
}

// RFC 3261 12.2.1.2: a 481 to the PRACK of a reliable response to a
// re-INVITE tells that the peer has lost the confirmed dialog, so the
// UA ends it with a BYE, as for any other request within it.
func TestPRACKOfAReINVITEThatGets481EndsTheCall(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := reInvite(out, holdOffer)
	reinvite := readDatagram(t, p.peer)
	send(t, p.peer, reliable(reinvite, 183, 1, p.target), callerUA.Stack())
	p.respond(readPRACK(t, p, "1 2 INVITE", "3 PRACK"), 481, Body{})
	bye := readDatagram(t, p.peer)
	if bye.Method != BYE || bye.Get("CSeq") != "4 BYE" {
		t.Fatalf("got %s with CSeq %q, want a BYE after the 481", startLine(bye), bye.Get("CSeq"))
	}
	p.respond(bye, 200, Body{})
	// RFC 3261 15.1.2: the peer answers its pending re-INVITE with 487.
	p.respond(reinvite, 487, Body{})
	if r := finalOf(t, done); r.resp == nil || r.resp.StatusCode != 487 {
		t.Errorf("ReInvite returned %v, %v; want the 487", r.resp, r.err)
	}
	waitDone(t, out.Done(), "caller")
	if !errors.Is(out.Err(), ErrDialogLost) {
		t.Errorf("call ended with %v, want ErrDialogLost", out.Err())
	}
}

// reInviteWith is reInvite for ReInviteWith.
func reInviteWith(out *OutgoingCall, re ReInvitation) <-chan reInviteDone {
	done := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.ReInviteWith(re)
		done <- reInviteDone{resp, err}
	}()
	return done
}

// prackResult is what PRACK returned.
type prackResult struct {
	final *Message
	err   error
}

// prackOf sends the PRACK of the user for resp in a goroutine of its
// own, so that the test can answer it from the raw callee meanwhile.
func prackOf(out *OutgoingCall, resp *Message, body Body, headers ...HeaderField) <-chan prackResult {
	pracked := make(chan prackResult, 1)
	go func() {
		final, err := out.PRACK(resp, body, headers...)
		pracked <- prackResult{final, err}
	}()
	return pracked
}

// handed reads n responses that OnProvisional passed on to got.
func handed(t *testing.T, got <-chan *Message, n int) []*Message {
	t.Helper()
	var ms []*Message
	for range n {
		select {
		case m := <-got:
			ms = append(ms, m)
		case <-time.After(2 * time.Second):
			t.Fatalf("OnProvisional got %d responses within 2s, want %d", len(ms), n)
		}
	}
	return ms
}

// A B2BUA passes the provisional responses to a re-INVITE on to its
// other leg, and the PRACK that comes back from there on to the UAS:
// OnProvisional gets each response that the UA takes, and ManualPRACK
// leaves the PRACK to the user.
func TestReINVITEHandsItsProvisionalResponsesToTheUser(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	got := make(chan *Message, 4)
	done := reInviteWith(out, ReInvitation{Body: holdOffer, ManualPRACK: true,
		OnProvisional: func(resp *Message) { got <- resp }})
	reinvite := readDatagram(t, p.peer)
	send(t, p.peer, NewResponse(reinvite, 180, ""), callerUA.Stack())
	progress := reliable(reinvite, 183, 1, p.target)
	send(t, p.peer, progress, callerUA.Stack())
	send(t, p.peer, progress, callerUA.Stack())
	expectSilence(t, p.peer, 3*slowTimers.T1, "no PRACK of the UA's own with ManualPRACK")

	ms := handed(t, got, 2)
	if ms[0].StatusCode != 180 || ms[1].StatusCode != 183 {
		t.Fatalf("OnProvisional got %d and %d, want 180 and 183", ms[0].StatusCode, ms[1].StatusCode)
	}
	pracked := prackOf(out, ms[1], Body{}, HeaderField{Name: "User-Agent", Value: "relay"})
	prack := readPRACK(t, p, "1 2 INVITE", "3 PRACK")
	if prack.Get("User-Agent") != "relay" {
		t.Errorf("PRACK has User-Agent %q, want the one the user gave", prack.Get("User-Agent"))
	}
	p.respond(prack, 200, Body{})
	if r := <-pracked; r.err != nil || r.final.StatusCode != 200 {
		t.Fatalf("PRACK returned %v, %v; want the 200", r.final, r.err)
	}
	if _, err := out.PRACK(ms[1], Body{}); err == nil {
		t.Error("a second PRACK for the same response went out")
	}

	acceptReInvite(t, p, reinvite, holdAnswer)
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInviteWith returned %v, %v; want the 200", r.resp, r.err)
	}
	if len(got) > 0 {
		t.Errorf("OnProvisional got %d more responses, such as the retransmitted 183", len(got))
	}
}
