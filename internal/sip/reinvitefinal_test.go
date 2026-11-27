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

// RFC 3262 3: only a 2xx waits for the PRACK of a reliable provisional
// response, and only for one that carried a session description. A user
// that ends a cancelled re-INVITE with 487 while its reliable 183 waits
// for a PRACK that never comes gets the 487 out at once, and the
// Progress that waited returns.
func TestRejectOfAReINVITEDoesNotWaitForThePRACK(t *testing.T) {
	progressed := make(chan error, 1)
	rejected := make(chan error, 1)
	_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		go func() { progressed <- r.Progress(183, holdAnswer) }()
		select {
		case <-r.Cancelled():
		case <-time.After(5 * time.Second):
			rejected <- errors.New("Cancelled was not closed within 5s of the CANCEL")
			return
		}
		rejected <- r.Reject(487)
	})
	reinvite := reInviteFrom(p, holdOffer, supports100rel)
	readProvisional(t, p)
	start := time.Now()
	send(t, p.peer, newMatchingRequest(reinvite, CANCEL, reinvite.Get("To")), p.ua.Stack())
	if final := readFinal(t, p, reinvite.Get("CSeq")); final.StatusCode != 487 {
		t.Fatalf("re-INVITE got %s, want the 487 of the user", startLine(final))
	}
	if elapsed := time.Since(start); elapsed > 16*slowTimers.T1 {
		t.Errorf("487 after %v, want it at once", elapsed)
	}
	ackNonOK(t, p, reinvite)
	if err := returned(t, rejected, "Reject"); err != nil {
		t.Errorf("Reject: %v", err)
	}
	if err := returned(t, progressed, "Progress"); !errors.Is(err, ErrRequestAnswered) {
		t.Errorf("Progress: %v, want ErrRequestAnswered", err)
	}
}

// RFC 3262 3: a reliable provisional response that has no PRACK when its
// re-INVITE fails still waits for one after the next re-INVITE has come,
// as each INVITE has responses of its own. Its PRACK, found by the CSeq
// in its RAck, gets 200, and the re-INVITE in progress goes on.
func TestPRACKOfAnEarlierReINVITEAfterTheNextOne(t *testing.T) {
	reject := make(chan struct{})
	next := make(chan *InDialogRequest, 1)
	_, p := rawCall(t, slowTimers, func(r *InDialogRequest) {
		if r.Request.Get("CSeq") != "2 INVITE" {
			next <- r
			return
		}
		go r.Progress(180, Body{})
		<-reject
		r.Reject(488)
	})
	first := reInviteFrom(p, holdOffer, supports100rel)
	prov := readProvisional(t, p)
	close(reject)
	if final := readFinal(t, p, first.Get("CSeq")); final.StatusCode != 488 {
		t.Fatalf("first re-INVITE got %s, want 488", startLine(final))
	}
	ackNonOK(t, p, first)
	second := reInviteFrom(p, offer2, supports100rel)
	var r *InDialogRequest
	select {
	case r = <-next:
	case <-time.After(2 * time.Second):
		t.Fatal("the user did not get the second re-INVITE")
	}

	prack := prackFrom(p, prov, Body{})
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 200 {
		t.Errorf("PRACK of the 180 to the first re-INVITE got %s, want 200", startLine(resp))
	}
	accepted := make(chan reinviteResult, 1)
	go func() {
		ack, err := r.Accept(answer2)
		accepted <- reinviteResult{ack, err}
	}()
	if ok := readFinal(t, p, second.Get("CSeq")); ok.StatusCode != 200 || string(ok.Body) != string(answer2.Content) {
		t.Fatalf("second re-INVITE got %s with body %q, want 200 with the answer", startLine(ok), ok.Body)
	}
	ackFrom(p, second)
	if a := acceptedWithin(t, accepted); a.err != nil || a.ack == nil {
		t.Errorf("Accept returned %v, %v; want the ACK", a.ack, a.err)
	}
}
