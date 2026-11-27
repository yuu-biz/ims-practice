package sip

import (
	"errors"
	"testing"
	"time"
)

// RFC 3262 3: a reliable 183 that got no PRACK before the final response
// of its re-INVITE still waits for one, and a PRACK that comes later gets
// 200. That final response ended the offer and answer of the re-INVITE
// (RFC 3261 14.1), so the session description in the PRACK is neither
// the answer to an offer that this UA made since nor a new offer of the
// peer, and nothing of it reaches the user.
func TestPRACKAfterTheFinalResponseOfAReINVITEChangesNoSession(t *testing.T) {
	t.Run("answer", func(t *testing.T) {
		// The UPDATE of this UA has to outlast the 64*T1 of its
		// transaction until the end of the test.
		in, p := rawCall(t, testTimers, func(r *InDialogRequest) {
			if r.Request.Method == PRACK {
				r.Accept(Body{})
				return
			}
			r.Progress(183, offer2)
		})
		reinvite := reInviteFrom(p, Body{}, supports100rel)
		prov := readProvisional(t, p)
		if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 500 {
			t.Fatalf("re-INVITE got %s, want 500", startLine(final))
		}
		ackNonOK(t, p, reinvite)
		updated := make(chan reInviteDone, 1)
		go func() {
			resp, err := in.Update(holdOffer)
			updated <- reInviteDone{resp, err}
		}()
		update := readRequest(t, p, UPDATE)

		prack := prackFrom(p, prov, answer2)
		if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 || len(resp.Body) > 0 {
			t.Errorf("late PRACK got %s with body %q, want 200 without one", startLine(resp), resp.Body)
		}
		third := make(chan error, 1)
		go func() {
			_, err := in.Update(offer2)
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
		p.respond(update, 200, holdAnswer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
		if r := finalOf(t, updated); r.err != nil || r.resp.StatusCode != 200 {
			t.Errorf("Update returned %v, %v; want the 200", r.resp, r.err)
		}
	})
	t.Run("offer", func(t *testing.T) {
		pracks := make(chan *InDialogRequest, 1)
		in, p := rawCall(t, fastTimers, func(r *InDialogRequest) {
			if r.Request.Method == PRACK {
				pracks <- r
				r.Accept(answer2)
				return
			}
			r.Progress(183, holdAnswer)
		})
		reinvite := reInviteFrom(p, holdOffer, supports100rel)
		prov := readProvisional(t, p)
		if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 500 {
			t.Fatalf("re-INVITE got %s, want 500", startLine(final))
		}
		ackNonOK(t, p, reinvite)

		prack := prackFrom(p, prov, offer2)
		if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 || len(resp.Body) > 0 {
			t.Errorf("late PRACK got %s with body %q, want 200 without one", startLine(resp), resp.Body)
		}
		if len(pracks) > 0 {
			t.Error("the user got the late PRACK as a request with an offer")
		}
		updated := make(chan reInviteDone, 1)
		go func() {
			resp, err := in.Update(holdOffer)
			updated <- reInviteDone{resp, err}
		}()
		select {
		case r := <-updated:
			t.Fatalf("Update after the late PRACK returned %v, %v; want the UPDATE to go out", r.resp, r.err)
		case <-time.After(10 * fastTimers.T1):
		}
		update := readRequest(t, p, UPDATE)
		p.respond(update, 200, holdAnswer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
		if r := finalOf(t, updated); r.err != nil || r.resp.StatusCode != 200 {
			t.Errorf("Update returned %v, %v; want the 200", r.resp, r.err)
		}
	})
}

// acceptedWithin waits for what Accept returned.
func acceptedWithin(t *testing.T, acks <-chan reinviteResult) reinviteResult {
	t.Helper()
	select {
	case a := <-acks:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return within 5s")
		return reinviteResult{}
	}
}

// RFC 3262 3 lets a 2xx pass a reliable provisional response without a
// session description that waits for its PRACK. The PRACK that comes
// after the 2xx still gets its 200, and as the 2xx ended the offer and
// answer of the re-INVITE, a session description in it changes nothing.
func TestAcceptOfAReINVITEDoesNotWaitForThePRACKOfAResponseWithoutSDP(t *testing.T) {
	progressed := make(chan error, 1)
	accept := make(chan struct{})
	acks := make(chan reinviteResult, 1)
	pracks := make(chan *InDialogRequest, 1)
	_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		if r.Request.Method == PRACK {
			pracks <- r
			r.Accept(answer2)
			return
		}
		go func() { progressed <- r.Progress(180, Body{}) }()
		<-accept
		ack, err := r.Accept(holdAnswer)
		acks <- reinviteResult{ack, err}
	})
	reinvite := reInviteFrom(p, holdOffer, supports100rel)
	prov := readProvisional(t, p)
	start := time.Now()
	close(accept)
	ok := readFinal(t, p, reinvite.Get("CSeq"))
	if ok.StatusCode != 200 || string(ok.Body) != string(holdAnswer.Content) {
		t.Fatalf("re-INVITE got %s with body %q, want 200 with the answer", startLine(ok), ok.Body)
	}
	if elapsed := time.Since(start); elapsed > 16*slowTimers.T1 {
		t.Errorf("200 after %v, want it without the PRACK", elapsed)
	}
	ackFrom(p, reinvite)
	if a := acceptedWithin(t, acks); a.err != nil || a.ack == nil {
		t.Errorf("Accept returned %v, %v; want the ACK", a.ack, a.err)
	}
	prack := prackFrom(p, prov, offer2)
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 || len(resp.Body) > 0 {
		t.Errorf("PRACK after the 2xx got %s with body %q, want 200 without one", startLine(resp), resp.Body)
	}
	if len(pracks) > 0 {
		t.Error("the user got the PRACK that came after the 2xx")
	}
	if err := returned(t, progressed, "Progress"); !errors.Is(err, ErrRequestAnswered) {
		t.Errorf("Progress: %v, want ErrRequestAnswered", err)
	}
}

// While Accept waits for the ACK of its 2xx to a re-INVITE, another
// answer of the user to the same re-INVITE returns ErrRequestAnswered at
// once: the re-INVITE has its final response.
func TestAnswersAfterTheAcceptOfAReINVITEReturnAtOnce(t *testing.T) {
	accepting := make(chan *InDialogRequest, 1)
	acks := make(chan reinviteResult, 1)
	_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		accepting <- r
		ack, err := r.Accept(holdAnswer)
		acks <- reinviteResult{ack, err}
	})
	reinvite := reInviteFrom(p, holdOffer)
	if ok := readFinal(t, p, reinvite.Get("CSeq")); ok.StatusCode != 200 {
		t.Fatalf("re-INVITE got %s, want 200", startLine(ok))
	}
	r := <-accepting
	answers := make(chan error, 3)
	go func() { answers <- r.Reject(488) }()
	go func() { answers <- r.Progress(180, Body{}) }()
	go func() {
		_, err := r.Accept(holdAnswer)
		answers <- err
	}()
	for range 3 {
		select {
		case err := <-answers:
			if !errors.Is(err, ErrRequestAnswered) {
				t.Errorf("an answer while Accept waits for the ACK: %v, want ErrRequestAnswered", err)
			}
		case <-time.After(8 * slowTimers.T1):
			t.Fatal("an answer while Accept waits for the ACK did not return at once")
		}
	}
	ackFrom(p, reinvite)
	if a := acceptedWithin(t, acks); a.err != nil || a.ack == nil {
		t.Errorf("Accept returned %v, %v; want the ACK", a.ack, a.err)
	}
}
