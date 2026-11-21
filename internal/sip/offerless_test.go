package sip

import (
	"errors"
	"reflect"
	"testing"
)

// RFC 3262 5: an INVITE without an offer gets the offer in the first
// reliable provisional response, and the PRACK carries the answer. IR.92
// asks a UE to take such an INVITE.
func TestOfferInAReliable183AndAnswerInThePRACK(t *testing.T) {
	pracks := make(chan *InDialogRequest, 1)
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			pracks <- r
			r.Accept(Body{})
		})
		// The callee's session description is the offer here.
		if err := c.Progress(183, answer); err != nil {
			t.Error(err)
		}
		ack, err := c.Accept(Body{})
		acks <- accepted{ack, err}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.Body = Body{}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	progress := <-out.Responses()
	if progress.StatusCode != 183 || string(progress.Body) != string(answer.Content) {
		t.Fatalf("got %s with body %q, want 183 with the callee's offer", startLine(progress), progress.Body)
	}
	// The UA leaves this PRACK to the user: it has to carry the answer.
	if _, err := out.PRACK(progress, Body{}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("PRACK without a body: %v, want ErrNoAnswer", err)
	}
	resp, err := out.PRACK(progress, offer)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PRACK got %v, %v; want 200", resp, err)
	}
	if _, err := out.PRACK(progress, offer); err == nil {
		t.Error("a second PRACK for the same response went out")
	}
	r := <-pracks
	if r.Offer || r.Request.Method != PRACK || string(r.Request.Body) != string(offer.Content) {
		t.Errorf("callee's user got Offer %v and\n%s\nwant the PRACK with the answer", r.Offer, r.Request)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Errorf("caller got %v, want 200", got)
	}
	// The exchange is complete, so the UA sends the ACK itself.
	if a := <-acks; a.err != nil || len(a.ack.Body) != 0 {
		t.Errorf("Accept returned %v, %v; want an ACK without a body", a.ack, a.err)
	}
}

// RFC 3261 13.2.1 and 13.2.2.4: an INVITE without an offer may get the
// offer in the 2xx; then the ACK carries the answer, and the user of
// the call sends it.
func TestOfferInThe2xxAndAnswerInTheACK(t *testing.T) {
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.Body = Body{}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{200}) || string(got[0].Body) != string(answer.Content) {
		t.Fatalf("caller got %v, want a 200 with the callee's offer", codes(got))
	}
	select {
	case a := <-acks:
		t.Fatalf("Accept returned %v, %v before the user sent the ACK", a.ack, a.err)
	default:
	}
	if err := out.Ack(Body{}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Ack without a body: %v, want ErrNoAnswer", err)
	}
	if err := out.Ack(offer); err != nil {
		t.Fatal(err)
	}
	a := <-acks
	if a.err != nil || string(a.ack.Body) != string(offer.Content) || a.ack.Get("Content-Type") != offer.Type {
		t.Errorf("Accept returned %v, %v; want the ACK with the answer", a.ack, a.err)
	}
	if err := out.Ack(offer); err == nil {
		t.Error("a second Ack succeeded")
	}
}

// RFC 3262 5: after the answer in a reliable provisional response, the
// PRACK may carry a new offer, and the 2xx to the PRACK answers it. A
// B2BUA relays such a PRACK from one leg to the other.
func TestNewOfferInThePRACK(t *testing.T) {
	pracks := make(chan *InDialogRequest, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			pracks <- r
			r.Accept(answer2)
		})
		if err := c.Progress(183, answer); err != nil {
			t.Error(err)
		}
		c.Accept(Body{})
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.ManualPRACK = true
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	progress := <-out.Responses()
	resp, err := out.PRACK(progress, offer2)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(answer2.Content) {
		t.Fatalf("PRACK got %v, %v; want 200 with the second answer", resp, err)
	}
	if r := <-pracks; !r.Offer || string(r.Request.Body) != string(offer2.Content) {
		t.Errorf("callee's user got Offer %v and body %q, want the second offer", r.Offer, r.Request.Body)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Errorf("caller got %v, want 200", got)
	}
}
