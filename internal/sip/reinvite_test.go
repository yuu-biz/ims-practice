package sip

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	holdOffer  = Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 2 IN IP4 127.0.0.1\r\na=sendonly\r\n")}
	holdAnswer = Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 2 IN IP4 127.0.0.1\r\na=recvonly\r\n")}
)

type reinviteResult struct {
	ack *Message
	err error
}

// TestReINVITEPutsTheCallOnHold is the HOLD of TS 24.610 as the UA sees
// it: a re-INVITE whose offer makes the media sendonly, the answer in
// the 200, and the ACK (RFC 3261 14). Either side may send one.
func TestReINVITEPutsTheCallOnHold(t *testing.T) {
	got := make(chan *InDialogRequest, 2)
	acks := make(chan reinviteResult, 2)
	user := func(r *InDialogRequest) {
		got <- r
		ack, err := r.Accept(holdAnswer)
		acks <- reinviteResult{ack, err}
	}
	e := establishWith(t, user, user)

	resp, err := e.out.ReInvite(holdOffer)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.Body) != string(holdAnswer.Content) || resp.Get("Contact") != "<"+e.calleeUA.cfg.Contact+">" {
		t.Errorf("re-INVITE got %s with body %q and Contact %q, want 200 with the answer and the callee's Contact",
			startLine(resp), resp.Body, resp.Get("Contact"))
	}
	r := <-got
	if r.Request.Method != INVITE || !r.Offer || string(r.Request.Body) != string(holdOffer.Content) ||
		r.Request.Get("CSeq") != "2 INVITE" || r.Request.Get("Contact") != "<"+e.callerUA.cfg.Contact+">" {
		t.Errorf("callee's user got Offer %v and\n%s", r.Offer, r.Request)
	}
	// RFC 3261 13.2.2.4: the ACK has the CSeq number of its INVITE.
	if a := <-acks; a.err != nil || a.ack == nil || a.ack.Get("CSeq") != "2 ACK" {
		t.Fatalf("Accept returned %v, %v; want the ACK with 2 ACK", a.ack, a.err)
	}

	// The callee takes its turn, which shows that the first re-INVITE
	// is over on both sides.
	resp, err = e.in.ReInvite(holdOffer)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("re-INVITE of the callee got %v, %v; want 200", resp, err)
	}
	if r := <-got; r.Request.Get("CSeq") != "1 INVITE" {
		t.Errorf("caller's user got CSeq %q, want 1 INVITE", r.Request.Get("CSeq"))
	}
	if a := <-acks; a.err != nil || a.ack.Get("CSeq") != "1 ACK" {
		t.Errorf("Accept returned %v, %v; want the ACK with 1 ACK", a.ack, a.err)
	}
	if err := e.out.Bye(); err != nil {
		t.Errorf("Bye after the re-INVITEs: %v", err)
	}
}

// RFC 3261 14.1: a re-INVITE without a session description asks the
// peer for an offer, which comes in the 2xx; the ACK carries the answer.
// A B2BUA gets such a re-INVITE from an AS that controls the call.
func TestReINVITEWithoutAnOffer(t *testing.T) {
	acks := make(chan reinviteResult, 1)
	e := establishWith(t, nil, func(r *InDialogRequest) {
		if r.Offer {
			t.Error("a re-INVITE without a body is marked as an offer")
		}
		if _, err := r.Accept(Body{}); err == nil {
			t.Error("Accept without the offer succeeded")
		}
		ack, err := r.Accept(answer2)
		acks <- reinviteResult{ack, err}
	})
	resp, err := e.out.ReInvite(Body{})
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(answer2.Content) {
		t.Fatalf("re-INVITE got %v, %v; want 200 with the callee's offer", resp, err)
	}
	if _, err := e.out.Update(offer2); !errors.Is(err, ErrOfferUnanswered) {
		t.Errorf("Update with an offer before the ACK: %v, want ErrOfferUnanswered", err)
	}
	if err := e.out.Ack(offer2); err != nil {
		t.Fatal(err)
	}
	if a := <-acks; a.err != nil || string(a.ack.Body) != string(offer2.Content) {
		t.Errorf("Accept returned %v, %v; want the ACK with the answer", a.ack, a.err)
	}
}

// RFC 3261 14.2: a call whose user takes no requests cannot change its
// session. The re-INVITE gets 488, and the call goes on as it was.
func TestReINVITEWithoutAHandlerGets488(t *testing.T) {
	e := establish(t)
	resp, err := e.out.ReInvite(holdOffer)
	if err != nil || resp.StatusCode != 488 || !strings.HasPrefix(resp.Get("Warning"), "399 ") {
		t.Fatalf("re-INVITE got %v, %v; want 488 with a Warning", resp, err)
	}
	// RFC 3261 14.1: the offer is gone with the 488.
	if resp, err := e.out.ReInvite(holdOffer); err != nil || resp.StatusCode != 488 {
		t.Errorf("second re-INVITE got %v, %v; want 488 again", resp, err)
	}
	if err := e.out.Bye(); err != nil {
		t.Errorf("Bye after the refused re-INVITE: %v", err)
	}
	waitDone(t, e.in.Done(), "callee")
}

// RFC 3261 14.1 and 14.2: one INVITE transaction at a time within a
// dialog. A re-INVITE that crosses the UA's own gets 491, one that
// comes before the last one is answered gets 500 with Retry-After, and
// a retransmitted 2xx gets its ACK again.
func TestReINVITEGlare(t *testing.T) {
	arrived := make(chan *InDialogRequest, 1)
	release := make(chan struct{})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) {
		arrived <- r
		<-release
		r.Accept(holdAnswer)
	}})

	done := make(chan *Message, 1)
	go func() {
		resp, _ := out.ReInvite(holdOffer)
		done <- resp
	}()
	own := readDatagram(t, p.peer)
	if own.Method != INVITE || own.Get("CSeq") != "2 INVITE" || tagOf(own.Get("To")) != "callee" {
		t.Fatalf("got %s with CSeq %q, want the re-INVITE with 2 INVITE", startLine(own), own.Get("CSeq"))
	}
	if _, err := out.ReInvite(holdOffer); !errors.Is(err, ErrInviteInProgress) {
		t.Errorf("second ReInvite: %v, want ErrInviteInProgress", err)
	}
	crossing := p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	pending := readSkippingTrying(t, p.peer)
	if pending.StatusCode != 491 {
		t.Fatalf("crossing re-INVITE got %s, want 491", startLine(pending))
	}
	// The ACK stops the retransmissions of the 491 (RFC 3261 17.1.1.3).
	send(t, p.peer, newMatchingRequest(crossing, ACK, pending.Get("To")), callerUA.Stack())
	// Now the peer answers the UA's re-INVITE, twice.
	ok := NewResponse(own, 200, "")
	ok.Add("Contact", "<"+p.target+">")
	holdAnswer.addTo(ok)
	send(t, p.peer, ok, callerUA.Stack())
	ack := readDatagram(t, p.peer)
	if ack.Method != ACK || ack.Get("CSeq") != "2 ACK" {
		t.Fatalf("got %s with CSeq %q, want ACK with 2 ACK", startLine(ack), ack.Get("CSeq"))
	}
	send(t, p.peer, ok, callerUA.Stack())
	if again := readDatagram(t, p.peer); again.Method != ACK || again.Get("CSeq") != "2 ACK" {
		t.Fatalf("got %s with CSeq %q for the retransmitted 200, want the ACK again", startLine(again), again.Get("CSeq"))
	}
	if resp := <-done; resp == nil || resp.StatusCode != 200 {
		t.Fatalf("ReInvite returned %v, want the 200", resp)
	}

	// The peer's own re-INVITE is taken now; a second one before its
	// final response is too early.
	first := p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	<-arrived
	p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	var busy *Message
	for busy == nil {
		if m := readDatagram(t, p.peer); m.StatusCode == 500 {
			busy = m
		}
	}
	if seconds, err := strconv.Atoi(busy.Get("Retry-After")); err != nil || seconds < 0 || seconds > 10 {
		t.Errorf("500 has Retry-After %q, want 0 to 10 seconds", busy.Get("Retry-After"))
	}
	if _, err := out.ReInvite(holdOffer); !errors.Is(err, ErrInviteInProgress) {
		t.Errorf("ReInvite while the peer's is in progress: %v, want ErrInviteInProgress", err)
	}
	close(release)
	var accepted *Message
	for accepted == nil {
		if m := readDatagram(t, p.peer); m.StatusCode == 200 && m.Get("CSeq") == first.Get("CSeq") {
			accepted = m
		}
	}
	if string(accepted.Body) != string(holdAnswer.Content) {
		t.Errorf("200 to the peer's re-INVITE has body %q, want the answer", accepted.Body)
	}
}

// RFC 3261 14.2: a 2xx to a re-INVITE that gets no ACK ends the dialog
// with a BYE.
func TestReINVITEWithoutAnACKEndsTheCall(t *testing.T) {
	timers := Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}
	results := make(chan reinviteResult, 1)
	callerUA := newUA(t, timers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) {
		ack, err := r.Accept(holdAnswer)
		results <- reinviteResult{ack, err}
	}})
	p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	var bye *Message
	for bye == nil {
		if m := readDatagram(t, p.peer); m.Method == BYE {
			bye = m
		}
	}
	p.respond(bye, 200, Body{})
	if r := <-results; !errors.Is(r.err, ErrNoACK) {
		t.Errorf("Accept returned %v, %v; want ErrNoACK", r.ack, r.err)
	}
	waitDone(t, out.Done(), "caller")
	if !errors.Is(out.Err(), ErrNoACK) {
		t.Errorf("call ended with %v, want ErrNoACK", out.Err())
	}
}

// RFC 3261 9.2: a CANCEL for a re-INVITE that has no final response
// yet gets the re-INVITE a 487, and the session stays as it was.
func TestCancelledReINVITEGets487(t *testing.T) {
	arrived := make(chan *InDialogRequest, 1)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) { arrived <- r }})
	reinvite := p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	r := <-arrived
	send(t, p.peer, newMatchingRequest(reinvite, CANCEL, reinvite.Get("To")), callerUA.Stack())
	codes := map[string]int{}
	for len(codes) < 2 {
		if m := readDatagram(t, p.peer); m.StatusCode >= 200 {
			codes[m.Get("CSeq")] = m.StatusCode
		}
	}
	if codes["1 CANCEL"] != 200 || codes["1 INVITE"] != 487 {
		t.Errorf("got %v, want 200 for the CANCEL and 487 for the re-INVITE", codes)
	}
	if _, err := r.Accept(holdAnswer); !errors.Is(err, ErrRequestAnswered) {
		t.Errorf("Accept after the CANCEL: %v, want ErrRequestAnswered", err)
	}
	// The offer is gone with the 487, so the UA may offer itself.
	done := make(chan *Message, 1)
	go func() {
		resp, _ := out.ReInvite(holdOffer)
		done <- resp
	}()
	var own *Message
	for own == nil {
		if m := readDatagram(t, p.peer); m.Method == INVITE {
			own = m
		}
	}
	p.respond(own, 200, holdAnswer)
	select {
	case resp := <-done:
		if resp == nil || resp.StatusCode != 200 {
			t.Errorf("ReInvite after the 487 got %v, want 200", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReInvite after the 487 never returned")
	}
}

// RFC 3261 14.1 lets a UAC send a re-INVITE once the client transaction
// of the INVITE before it has ended, which a 2xx does. The ACK of that
// 2xx goes end to end, on its own path, so it may come after the
// re-INVITE. 14.2 asks for a 500 only while the earlier INVITE has no
// final response; after its 2xx the UAS takes the re-INVITE, and still
// gets each ACK to the 2xx it acknowledges.
func TestReINVITEBeforeTheACKOfThe2xx(t *testing.T) {
	acks := make(chan accepted, 3)
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		calls <- c
		c.Handle(func(r *InDialogRequest) {
			ack, err := r.Accept(holdAnswer)
			acks <- accepted{ack, err}
		})
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	offer.addTo(invite)
	send(t, peer, invite, calleeUA.Stack())
	ok := readSkippingTrying(t, peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	d, err := NewUACDialog(invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	// reinvite sends a re-INVITE before the ACK of the last 2xx, and
	// returns it with its final response.
	reinvite := func() (*Message, *Message) {
		t.Helper()
		req := d.NewRequest(INVITE)
		via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
		req.Prepend("Via", via.String())
		req.Add("Contact", "<sip:001010000000001@"+addrOf(peer).String()+">")
		holdOffer.addTo(req)
		send(t, peer, req, calleeUA.Stack())
		for {
			// Skip the 100 and the retransmissions of the earlier 2xx.
			if m := readDatagram(t, peer); m.StatusCode >= 200 && m.Get("CSeq") == req.Get("CSeq") {
				return req, m
			}
		}
	}
	second, secondOK := reinvite()
	third, thirdOK := reinvite()
	for _, resp := range []*Message{secondOK, thirdOK} {
		if resp.StatusCode != 200 {
			t.Fatalf("re-INVITE %q before the ACK got %s, want 200", resp.Get("CSeq"), startLine(resp))
		}
	}
	// 14.1 still keeps the UAS from sending an INVITE of its own before
	// the ACKs.
	if _, err := (<-calls).ReInvite(holdOffer); !errors.Is(err, ErrInviteInProgress) {
		t.Errorf("ReInvite of the UAS before the ACKs: %v, want ErrInviteInProgress", err)
	}
	// The ACKs come last, and out of order.
	send(t, peer, rawACK(peer, third, thirdOK), calleeUA.Stack())
	send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
	send(t, peer, rawACK(peer, second, secondOK), calleeUA.Stack())
	got := map[string]bool{}
	for range 3 {
		a := <-acks
		if a.err != nil {
			t.Fatalf("Accept returned %v, want the ACK", a.err)
		}
		got[a.ack.Get("CSeq")] = true
	}
	for _, cseq := range []string{"1 ACK", "2 ACK", "3 ACK"} {
		if !got[cseq] {
			t.Errorf("Accepts returned the ACKs %v, want one with %s", got, cseq)
		}
	}
}
