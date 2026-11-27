package sip

import (
	"errors"
	"testing"
	"time"
)

// noUPDATEWithin reads what the raw peer gets for d, and fails on an
// UPDATE: retransmissions of a reliable response may come meanwhile.
func noUPDATEWithin(t *testing.T, p *rawCallee, d time.Duration, why string) {
	t.Helper()
	for until := time.Now().Add(d); time.Now().Before(until); {
		if m, ok := readWithin(p.peer, time.Until(until)); ok && m.Method == UPDATE {
			t.Fatalf("%s, but the UPDATE went", why)
		}
	}
}

// RFC 3262 5 and RFC 6337 4.2: once this UA has answered the offer of a
// re-INVITE in a reliable 183, the PRACK of the 183 may carry a new
// offer. An offer of this UA waits for that PRACK, so the two cannot
// cross: the offer of the PRACK goes to the user, and while the user has
// not answered it, the offer that waited finds an offer of the peer open
// (RFC 3311 5.1).
func TestOfferWaitsForThePRACKOfAReliableResponseWithAnAnswer(t *testing.T) {
	pracks := make(chan *InDialogRequest, 1)
	in, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		if r.Request.Method == PRACK {
			pracks <- r
			return
		}
		r.Progress(183, holdAnswer)
	})
	reInviteFrom(p, holdOffer, supports100rel)
	prov := readProvisional(t, p)
	waited := make(chan error, 1)
	go func() {
		_, err := in.Update(offer2)
		waited <- err
	}()
	noUPDATEWithin(t, p, 3*slowTimers.T1, "the answer of the 183 waits for its PRACK")

	prack := prackFrom(p, prov, offer2)
	var r *InDialogRequest
	select {
	case r = <-pracks:
		if !r.Offer {
			t.Error("the user got the PRACK without its offer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the user did not get the PRACK with the offer")
	}
	if err := returned(t, waited, "the Update that waited"); !errors.Is(err, ErrOfferUnanswered) {
		t.Errorf("the Update that waited for the PRACK: %v, want ErrOfferUnanswered", err)
	}
	if _, err := r.Accept(answer2); err != nil {
		t.Fatalf("Accept of the PRACK: %v", err)
	}
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 || string(resp.Body) != string(answer2.Content) {
		t.Errorf("PRACK got %s with body %q, want 200 with the answer of the user", startLine(resp), resp.Body)
	}
	updated := make(chan reInviteDone, 1)
	go func() {
		resp, err := in.Update(offer2)
		updated <- reInviteDone{resp, err}
	}()
	update := readRequest(t, p, UPDATE)
	p.respond(update, 200, answer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if u := finalOf(t, updated); u.err != nil || u.resp.StatusCode != 200 {
		t.Errorf("Update after the exchange of the PRACK returned %v, %v; want the 200", u.resp, u.err)
	}
}

// An offer that waits for the PRACK of a reliable 183 goes once the
// re-INVITE has its final response without that PRACK: the 500 after
// 64*T1 (RFC 3262 3) ends the exchange that the PRACK could follow.
func TestOfferThatWaitsForAPRACKGoesWhenTheReINVITEEnds(t *testing.T) {
	in, p := rawCall(t, testTimers, func(r *InDialogRequest) { r.Progress(183, holdAnswer) })
	reinvite := reInviteFrom(p, holdOffer, supports100rel)
	readProvisional(t, p)
	updated := make(chan reInviteDone, 1)
	go func() {
		resp, err := in.Update(offer2)
		updated <- reInviteDone{resp, err}
	}()
	if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 500 {
		t.Fatalf("re-INVITE got %s, want 500", startLine(final))
	}
	ackNonOK(t, p, reinvite)
	update := readRequest(t, p, UPDATE)
	p.respond(update, 200, answer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if u := finalOf(t, updated); u.err != nil || u.resp.StatusCode != 200 {
		t.Errorf("Update after the 500 returned %v, %v; want the 200", u.resp, u.err)
	}
}

// RFC 3311 5.2: an offer that meets an offer of this UA still waiting for
// its answer gets 491. Here the offer is in the PRACK of a reliable 180
// without a session description, which RFC 3262 5 does not let it be:
// the offer of the UPDATE that this UA sent after the earlier exchange
// stays open, and the 180 waits for a PRACK that it accepts.
func TestOfferInAPRACKThatCrossesAnOfferOfThisUAGets491(t *testing.T) {
	ringing := make(chan struct{})
	in, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		if r.Request.Method == PRACK {
			r.Accept(Body{})
			return
		}
		r.Progress(183, holdAnswer)
		<-ringing
		r.Progress(180, Body{})
	})
	reInviteFrom(p, holdOffer, supports100rel)
	progress := prackFrom(p, readProvisional(t, p), Body{})
	if resp := readFinal(t, p, progress.Get("CSeq")); resp.StatusCode != 200 {
		t.Fatalf("PRACK of the 183 got %s, want 200", startLine(resp))
	}
	updated := make(chan reInviteDone, 1)
	go func() {
		resp, err := in.Update(offer2)
		updated <- reInviteDone{resp, err}
	}()
	update := readRequest(t, p, UPDATE)

	close(ringing)
	prov := readProvisional(t, p)
	crossing := prackFrom(p, prov, holdOffer)
	if resp := readFinal(t, p, crossing.Get("CSeq")); resp.StatusCode != 491 {
		t.Errorf("PRACK with an offer that crosses the UPDATE got %s, want 491", startLine(resp))
	}
	third := make(chan error, 1)
	go func() {
		_, err := in.Update(holdOffer)
		third <- err
	}()
	select {
	case err := <-third:
		if !errors.Is(err, ErrOfferPending) {
			t.Errorf("Update while the offer of the first UPDATE is open: %v, want ErrOfferPending", err)
		}
	case <-time.After(time.Second):
		t.Error("an offer went out while the offer of the UPDATE was open")
	}
	if resp := readFinal(t, p, prackFrom(p, prov, Body{}).Get("CSeq")); resp.StatusCode != 200 {
		t.Errorf("PRACK of the 180 without a body got %s, want 200", startLine(resp))
	}
	p.respond(update, 200, answer2, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	if r := finalOf(t, updated); r.err != nil || r.resp.StatusCode != 200 {
		t.Errorf("Update returned %v, %v; want the 200", r.resp, r.err)
	}
}
