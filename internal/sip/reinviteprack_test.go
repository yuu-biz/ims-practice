package sip

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
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

// acceptReInvite answers the re-INVITE with 200 and body, and returns
// the ACK.
func acceptReInvite(t *testing.T, p *rawCallee, reinvite *Message, body Body) *Message {
	t.Helper()
	cseq, _ := ParseCSeq(reinvite.Get("CSeq"))
	p.respond(reinvite, 200, body, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	ack := readDatagram(t, p.peer)
	if want := (CSeq{Number: cseq.Number, Method: ACK}).String(); ack.Method != ACK || ack.Get("CSeq") != want {
		t.Fatalf("got %s with CSeq %q, want the ACK with %s", startLine(ack), ack.Get("CSeq"), want)
	}
	return ack
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

// RFC 3262 5: the answer to the offer of a re-INVITE may come in a
// reliable 183, as with preconditions, whose UAs support PRACK and
// UPDATE (RFC 3312 11). The offer is then answered, so the UAC may
// offer again in an UPDATE while the re-INVITE goes on (RFC 3311 5.1),
// and the 2xx to the re-INVITE carries no session description.
// That 2xx leaves the session as the 183 and the UPDATE made it: the
// offer of the UPDATE is still open when it comes.
func TestReINVITEAnsweredInAReliable183(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	got := make(chan *Message, 2)
	done := reInviteWith(out, ReInvitation{Body: holdOffer, OnProvisional: func(resp *Message) { got <- resp }})
	reinvite := readDatagram(t, p.peer)
	progress := reliable(reinvite, 183, 1, p.target)
	holdAnswer.addTo(progress)
	send(t, p.peer, progress, callerUA.Stack())
	p.respond(readPRACK(t, p, "1 2 INVITE", "3 PRACK"), 200, Body{})
	if m := handed(t, got, 1)[0]; string(m.Body) != string(holdAnswer.Content) {
		t.Errorf("OnProvisional got %q, want the 183 with the answer", m.Body)
	}

	updated := make(chan reInviteDone, 1)
	go func() {
		resp, err := out.Update(offer2)
		updated <- reInviteDone{resp, err}
	}()
	update := readDatagram(t, p.peer)
	if update.Method != UPDATE || string(update.Body) != string(offer2.Content) {
		t.Fatalf("got %s with body %q, want the UPDATE with the new offer", startLine(update), update.Body)
	}
	if ack := acceptReInvite(t, p, reinvite, Body{}); len(ack.Body) > 0 {
		t.Errorf("ACK carries %q, want no body", ack.Body)
	}
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInviteWith returned %v, %v; want the 200", r.resp, r.err)
	}
	second := make(chan error, 1)
	go func() {
		_, err := out.Update(offer2)
		second <- err
	}()
	select {
	case err := <-second:
		if !errors.Is(err, ErrOfferPending) {
			t.Errorf("second Update while the UPDATE's offer is open: %v, want ErrOfferPending", err)
		}
	case <-time.After(time.Second):
		t.Error("a second offer went out while the offer of the UPDATE was open")
	}
	p.respond(update, 200, answer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if r := finalOf(t, updated); r.err != nil || r.resp.StatusCode != 200 {
		t.Errorf("Update returned %v, %v; want the 200", r.resp, r.err)
	}
}

// RFC 3262 5: a re-INVITE without an offer may get the offer in its
// first reliable response. Its PRACK carries the answer, which only the
// user has; the 2xx and the ACK then carry none.
func TestReINVITEWithoutAnOfferGetsItInAReliable183(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	got := make(chan *Message, 2)
	done := reInviteWith(out, ReInvitation{OnProvisional: func(resp *Message) { got <- resp }})
	reinvite := readDatagram(t, p.peer)
	if len(reinvite.Body) > 0 {
		t.Fatalf("re-INVITE carries %q, want no offer", reinvite.Body)
	}
	progress := reliable(reinvite, 183, 1, p.target)
	offer2.addTo(progress)
	send(t, p.peer, progress, callerUA.Stack())
	expectSilence(t, p.peer, 3*slowTimers.T1, "no PRACK without the answer")

	m := handed(t, got, 1)[0]
	if _, err := out.PRACK(m, Body{}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("PRACK without a body: %v, want ErrNoAnswer", err)
	}
	pracked := prackOf(out, m, answer2)
	prack := readPRACK(t, p, "1 2 INVITE", "3 PRACK")
	if string(prack.Body) != string(answer2.Content) {
		t.Errorf("PRACK carries %q, want the answer", prack.Body)
	}
	p.respond(prack, 200, Body{})
	if r := <-pracked; r.err != nil || r.final.StatusCode != 200 {
		t.Fatalf("PRACK returned %v, %v; want the 200", r.final, r.err)
	}
	if ack := acceptReInvite(t, p, reinvite, Body{}); len(ack.Body) > 0 {
		t.Errorf("ACK carries %q, want no body", ack.Body)
	}
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInviteWith returned %v, %v; want the 200", r.resp, r.err)
	}
}

// RFC 6141 4.7: the Contact of a reliable provisional response to a
// re-INVITE is the remote target from then on, so the PRACK goes there;
// that of a provisional response that is not reliable is not.
func TestReliableResponseToAReINVITERefreshesTheRemoteTarget(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	unreliable := "sip:unreliable@" + addrOf(p.peer).String()
	moved := "sip:moved@" + addrOf(p.peer).String()
	done := reInvite(out, holdOffer)
	reinvite := readDatagram(t, p.peer)
	ringing := NewResponse(reinvite, 180, "")
	ringing.Add("Contact", "<"+unreliable+">")
	send(t, p.peer, ringing, callerUA.Stack())
	progress := reliable(reinvite, 183, 1, p.target)
	progress.Remove("Contact")
	send(t, p.peer, progress, callerUA.Stack())
	prack := readPRACK(t, p, "1 2 INVITE", "3 PRACK")
	if prack.RequestURI != p.target {
		t.Errorf("PRACK after the unreliable 180 goes to %s, want %s still", prack.RequestURI, p.target)
	}
	p.respond(prack, 200, Body{})

	send(t, p.peer, reliable(reinvite, 180, 2, moved), callerUA.Stack())
	prack = readPRACK(t, p, "2 2 INVITE", "4 PRACK")
	if prack.RequestURI != moved {
		t.Errorf("PRACK after the reliable 180 goes to %s, want its Contact %s", prack.RequestURI, moved)
	}
	p.respond(prack, 200, Body{})
	p.target = moved
	acceptReInvite(t, p, reinvite, holdAnswer)
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, %v; want the 200", r.resp, r.err)
	}
}

// RFC 3261 14.1: when the re-INVITE fails before the user has answered
// the offer of its reliable 183, that offer goes with it, and the UA
// may offer itself.
func TestFailedReINVITEDropsTheOfferOfItsReliable183(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := reInviteWith(out, ReInvitation{OnProvisional: func(*Message) {}})
	reinvite := readDatagram(t, p.peer)
	progress := reliable(reinvite, 183, 1, p.target)
	offer2.addTo(progress)
	send(t, p.peer, progress, callerUA.Stack())
	p.respond(reinvite, 500, Body{})
	if r := finalOf(t, done); r.resp == nil || r.resp.StatusCode != 500 {
		t.Fatalf("ReInviteWith returned %v, %v; want the 500", r.resp, r.err)
	}
	updated := make(chan error, 1)
	go func() {
		_, err := out.Update(offer2)
		updated <- err
	}()
	for {
		// The ACK of the 500 may come first (RFC 3261 17.1.1.3).
		if m := readDatagram(t, p.peer); m.Method == UPDATE {
			p.respond(m, 200, answer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
			break
		}
	}
	if err := <-updated; err != nil {
		t.Errorf("Update after the failed re-INVITE: %v", err)
	}
}

// updatedReInvite is a re-INVITE of the UA whose offer a reliable 183
// has answered, after which the peer has sent an UPDATE with an offer of
// its own (RFC 3311 5.1). The user of the call holds that UPDATE and has
// not answered it yet.
type updatedReInvite struct {
	ua       *UA
	out      *OutgoingCall
	p        *rawCallee
	done     <-chan reInviteDone
	reinvite *Message
	update   *InDialogRequest
}

func reInviteThenUPDATE(t *testing.T) *updatedReInvite {
	t.Helper()
	updates := make(chan *InDialogRequest, 1)
	ua := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, ua, Invitation{OnRequest: func(r *InDialogRequest) { updates <- r }})
	u := &updatedReInvite{ua: ua, out: out, p: p, done: reInvite(out, holdOffer)}
	u.reinvite = readDatagram(t, p.peer)
	progress := reliable(u.reinvite, 183, 1, p.target)
	holdAnswer.addTo(progress)
	send(t, p.peer, progress, ua.Stack())
	p.respond(readPRACK(t, p, "1 2 INVITE", "3 PRACK"), 200, Body{})
	p.request(UPDATE, offer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	select {
	case u.update = <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("the user of the call got no UPDATE within 2s")
	}
	return u
}

// acceptUPDATE answers the UPDATE of the peer, and reads its 200 at the
// peer.
func (u *updatedReInvite) acceptUPDATE(t *testing.T) {
	t.Helper()
	if _, err := u.update.Accept(answer2); err != nil {
		t.Fatalf("Accept of the UPDATE: %v", err)
	}
	for {
		if m := readDatagram(t, u.p.peer); m.StatusCode == 200 && m.Get("CSeq") == u.update.Request.Get("CSeq") {
			return
		}
	}
}

// RFC 6141 3.3: the offer and answer of a reliable 183 stay done when
// the re-INVITE then fails, and so does what came after them. The offer
// of the peer's UPDATE still waits for its answer, so the UA may not
// offer (RFC 3311 5.1), and the user answers it as before.
func TestFailedReINVITEKeepsTheOfferOfAnUPDATEAfterItsReliable183(t *testing.T) {
	u := reInviteThenUPDATE(t)
	u.p.respond(u.reinvite, 500, Body{})
	if r := finalOf(t, u.done); r.resp == nil || r.resp.StatusCode != 500 {
		t.Fatalf("ReInvite returned %v, %v; want the 500", r.resp, r.err)
	}
	offered := make(chan error, 1)
	go func() {
		_, err := u.out.Update(offer2)
		offered <- err
	}()
	select {
	case err := <-offered:
		if !errors.Is(err, ErrOfferUnanswered) {
			t.Errorf("Update while the offer of the peer's UPDATE is open: %v, want ErrOfferUnanswered", err)
		}
	case <-time.After(time.Second):
		t.Error("an offer of the UA went out while the offer of the peer's UPDATE was open")
	}
	u.acceptUPDATE(t)
}

// RFC 3262 4: a reliable provisional response without an offer gets its
// PRACK from the UA, also while an offer of the peer's UPDATE waits for
// the user's answer: that offer is not one of the response.
func TestReliableResponseAfterAnUPDATEOfThePeerGetsItsPRACK(t *testing.T) {
	u := reInviteThenUPDATE(t)
	send(t, u.p.peer, reliable(u.reinvite, 180, 2, u.p.target), u.ua.Stack())
	u.p.respond(readPRACK(t, u.p, "2 2 INVITE", "4 PRACK"), 200, Body{})
	u.acceptUPDATE(t)
	acceptReInvite(t, u.p, u.reinvite, Body{})
	if r := finalOf(t, u.done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, %v; want the 200", r.resp, r.err)
	}
}

// RFC 3261 13.2.2.4: the 2xx to a re-INVITE whose offer a reliable 183
// answered carries no offer, so the UA sends its ACK at once, also while
// an offer of the peer's UPDATE waits for the user's answer.
func TestReINVITEAnsweredInAReliable183IsAcknowledgedWhileAnUPDATEWaits(t *testing.T) {
	u := reInviteThenUPDATE(t)
	if ack := acceptReInvite(t, u.p, u.reinvite, Body{}); len(ack.Body) > 0 {
		t.Errorf("ACK carries %q, want no body", ack.Body)
	}
	if r := finalOf(t, u.done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, %v; want the 200", r.resp, r.err)
	}
	u.acceptUPDATE(t)
}

// RFC 3261 12: the To tag of a response is the remote tag of its dialog,
// so a response to a re-INVITE with another tag is not of the dialog. A
// reliable one gets no PRACK within the dialog, and neither changes its
// remote target nor reaches the user.
func TestReliableResponseWithAnotherTagIsNotOfTheDialog(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	got := make(chan *Message, 1)
	done := reInviteWith(out, ReInvitation{Body: holdOffer, OnProvisional: func(resp *Message) { got <- resp }})
	reinvite := readDatagram(t, p.peer)
	foreign := reliable(reinvite, 183, 1, "sip:moved@"+addrOf(p.peer).String())
	foreign.Set("To", strings.Replace(foreign.Get("To"), "tag=callee", "tag=other", 1))
	holdAnswer.addTo(foreign)
	send(t, p.peer, foreign, callerUA.Stack())
	expectSilence(t, p.peer, 3*slowTimers.T1, "no PRACK for a response of another dialog")
	if len(got) > 0 {
		t.Errorf("OnProvisional got the %d with the tag %q", (<-got).StatusCode, tagOf(foreign.Get("To")))
	}
	acceptReInvite(t, p, reinvite, holdAnswer)
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInviteWith returned %v, %v; want the 200", r.resp, r.err)
	}
}

// RFC 3261 13.2.1: only a session description is an offer or an answer.
// A reliable 183 whose body is of another type, such as one that a
// service puts there, carries no offer for a re-INVITE without one: it
// gets its PRACK from the UA, and the offer comes in the 2xx.
func TestReliableResponseWithABodyThatIsNotSDPCarriesNoOffer(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := reInvite(out, Body{})
	reinvite := readDatagram(t, p.peer)
	progress := reliable(reinvite, 183, 1, p.target)
	Body{Type: "text/plain", Content: []byte("connecting")}.addTo(progress)
	send(t, p.peer, progress, callerUA.Stack())
	p.respond(readPRACK(t, p, "1 2 INVITE", "3 PRACK"), 200, Body{})

	p.respond(reinvite, 200, offer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, %v; want the 200", r.resp, r.err)
	}
	if err := out.Ack(answer2); err != nil {
		t.Fatalf("Ack with the answer to the offer of the 2xx: %v", err)
	}
	if ack := readDatagram(t, p.peer); ack.Method != ACK || string(ack.Body) != string(answer2.Content) {
		t.Errorf("got %s with %q, want the ACK with the answer", startLine(ack), ack.Body)
	}
}

// RFC 3262 5: the PRACK of a reliable response with an offer carries the
// answer, which only the user has. A user of ReInvite takes no
// provisional responses, so that PRACK cannot go out. The UA does not
// make an answer up, and says in its log why the UAS gets no PRACK.
func TestOfferInAReliableResponseThatTheUserDoesNotTakeIsLogged(t *testing.T) {
	var mu sync.Mutex
	var logs bytes.Buffer
	tr := listenLayer(t, testLogger(t))
	callerUA, err := NewUA(tr, UAConfig{SentBy: tr.LocalAddr().String(), Timers: slowTimers, OnCall: noCalls,
		Contact: "sip:001010000000001@" + tr.LocalAddr().String()}, slog.New(slog.NewTextHandler(lockedWriter{&mu, &logs}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callerUA.Close)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := reInvite(out, Body{})
	reinvite := readDatagram(t, p.peer)
	progress := reliable(reinvite, 183, 1, p.target)
	offer2.addTo(progress)
	send(t, p.peer, progress, callerUA.Stack())
	expectSilence(t, p.peer, 3*slowTimers.T1, "no PRACK without the answer")
	mu.Lock()
	logged := strings.Contains(logs.String(), "nobody takes")
	mu.Unlock()
	if !logged {
		t.Error("the UA did not log that the offer of the 183 has nobody to answer it")
	}
	p.respond(reinvite, 500, Body{})
	if r := finalOf(t, done); r.resp == nil || r.resp.StatusCode != 500 {
		t.Fatalf("ReInvite returned %v, %v; want the 500", r.resp, r.err)
	}
}

// RFC 3262 3 and 4: a UAS sends no new reliable provisional response
// after the final response, and the UAC keeps the RSeq numbers of a
// re-INVITE only until then. A reliable 183 that comes after the 200
// gets no PRACK.
func TestReliableResponseAfterTheFinalResponseOfAReINVITEGetsNoPRACK(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	done := reInvite(out, holdOffer)
	reinvite := readDatagram(t, p.peer)
	acceptReInvite(t, p, reinvite, holdAnswer)
	if r := finalOf(t, done); r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, %v; want the 200", r.resp, r.err)
	}
	send(t, p.peer, reliable(reinvite, 183, 1, p.target), callerUA.Stack())
	expectSilence(t, p.peer, 3*slowTimers.T1, "no PRACK for a reliable response after the 200")
}

// RFC 6141 4.3: the remote target that a reliable provisional response
// refreshed stays refreshed when the re-INVITE then fails, unlike the
// rest of what the re-INVITE asked to change. The next request within
// the dialog goes to the new target.
func TestTargetOfAReliableResponseStaysAfterTheReINVITEFails(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	moved := "sip:moved@" + addrOf(p.peer).String()
	done := reInvite(out, holdOffer)
	reinvite := readDatagram(t, p.peer)
	send(t, p.peer, reliable(reinvite, 180, 1, moved), callerUA.Stack())
	p.respond(readPRACK(t, p, "1 2 INVITE", "3 PRACK"), 200, Body{})
	p.respond(reinvite, 500, Body{})
	if r := finalOf(t, done); r.resp == nil || r.resp.StatusCode != 500 {
		t.Fatalf("ReInvite returned %v, %v; want the 500", r.resp, r.err)
	}
	updated := make(chan error, 1)
	go func() {
		_, err := out.Update(Body{})
		updated <- err
	}()
	for {
		// The ACK of the 500 may come first (RFC 3261 17.1.1.3).
		if m := readDatagram(t, p.peer); m.Method == UPDATE {
			if m.RequestURI != moved {
				t.Errorf("UPDATE after the failed re-INVITE goes to %s, want %s", m.RequestURI, moved)
			}
			p.respond(m, 200, Body{}, HeaderField{Name: "Contact", Value: "<" + moved + ">"})
			break
		}
	}
	if err := <-updated; err != nil {
		t.Errorf("Update after the failed re-INVITE: %v", err)
	}
}
