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
