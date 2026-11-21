package sip

import (
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
)

// This file proves that the UA lets a routeing B2BUA be written on top
// of it without reaching into the package: the MMTel AS is
// one. The B2BUA here is the smallest that
// relays a whole call. It answers the caller's INVITE as a UAS, calls
// the callee as a UAC with a Call-ID of its own, and passes what one
// leg receives to the other: provisional and final responses with
// their session descriptions, the CANCEL, the requests within the
// dialog whatever their method, the answer in the ACK of a re-INVITE
// without an offer, and the BYE. Each leg acknowledges its reliable
// provisional responses and its 2xx by itself.

// leg is what the two sides of a B2BUA have in common: both an
// IncomingCall and an OutgoingCall are one.
type leg interface {
	Update(body Body, headers ...HeaderField) (*Message, error)
	ReInvite(body Body, headers ...HeaderField) (*Message, error)
	Send(method Method, body Body, headers ...HeaderField) (*Message, error)
	Ack(body Body, headers ...HeaderField) error
	Bye(headers ...HeaderField) error
	Done() <-chan struct{}
	EndedBy() *Message
}

var (
	_ leg = (*IncomingCall)(nil)
	_ leg = (*OutgoingCall)(nil)
)

// reasonOf is the Reason header fields of the request that ended a leg,
// to pass on to the other leg (RFC 3326 2).
func reasonOf(ended *Message) []HeaderField {
	var headers []HeaderField
	if ended != nil {
		for _, r := range Reasons(ended) {
			headers = append(headers, r.Header())
		}
	}
	return headers
}

// relayRequest passes a request within the dialog of one leg on to the
// other leg, and its final response back.
func relayRequest(r *InDialogRequest, to leg) {
	var resp *Message
	var err error
	switch body := BodyOf(r.Request); r.Request.Method {
	case INVITE:
		resp, err = to.ReInvite(body)
	case UPDATE:
		resp, err = to.Update(body)
	default:
		resp, err = to.Send(r.Request.Method, body)
	}
	switch {
	case err != nil:
		r.Reject(500)
	case resp.StatusCode < 300:
		ack, err := r.Accept(BodyOf(resp))
		if err == nil && r.Request.Method == INVITE && !r.Offer {
			// A re-INVITE without an offer: the 2xx carried the offer of
			// the other leg, which waits for the answer in this leg's ACK
			// (RFC 3261 14.1, 13.2.2.4).
			to.Ack(BodyOf(ack))
		}
	default:
		r.Reject(resp.StatusCode)
	}
}

// newB2BUA runs a B2BUA that sends every call it receives on to target.
func newB2BUA(t *testing.T, target string) *UA {
	t.Helper()
	var self atomic.Pointer[UA]
	ua := newUA(t, testTimers, "b2bua", func(a *IncomingCall) {
		var b *OutgoingCall
		called := make(chan struct{})
		a.Handle(func(r *InDialogRequest) {
			<-called
			relayRequest(r, b)
		})
		from, _ := ParseNameAddr(a.Request.Get("From"))
		b, err := self.Load().Invite(Invitation{
			RequestURI: target, From: from.URI, FromDisplayName: from.DisplayName,
			Body:      BodyOf(a.Request),
			OnRequest: func(r *InDialogRequest) { relayRequest(r, a) },
		})
		if err != nil {
			a.Reject(500)
			return
		}
		close(called)

		// RFC 3261 9.1: the caller gives up, so the B2BUA cancels the
		// leg it started, for the same reason.
		answered := make(chan struct{})
		go func() {
			select {
			case <-a.Done():
				if errors.Is(a.Err(), ErrCancelled) {
					b.Cancel(reasonOf(a.EndedBy())...)
				}
			case <-answered:
			}
		}()
		for resp := range b.Responses() {
			switch code := resp.StatusCode; {
			case code < 200:
				a.Progress(code, BodyOf(resp))
			case code < 300:
				a.Accept(BodyOf(resp))
			default:
				a.Reject(code)
			}
		}
		close(answered)

		// RFC 3261 15: whoever hangs up, the other leg gets the BYE.
		select {
		case <-a.Done():
			b.Bye(reasonOf(a.EndedBy())...)
		case <-b.Done():
			a.Bye(reasonOf(b.EndedBy())...)
		}
	})
	self.Store(ua)
	return ua
}

func TestCallThroughAB2BUA(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	held := make(chan *InDialogRequest, 1)
	heldACKs := make(chan reinviteResult, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			held <- r
			ack, err := r.Accept(holdAnswer)
			heldACKs <- reinviteResult{ack, err}
		})
		if err := c.Progress(183, answer); err != nil {
			t.Error(err)
		}
		if err := c.Progress(180, Body{}); err != nil {
			t.Error(err)
		}
		if _, err := c.Accept(Body{}); err != nil {
			t.Error(err)
		}
		calls <- c
	})
	b2bua := newB2BUA(t, calleeUA.cfg.Contact)
	infos := make(chan *InDialogRequest, 1)
	offered := make(chan reinviteResult, 1)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(b2bua)
	inv.OnRequest = func(r *InDialogRequest) {
		if r.Request.Method == INVITE {
			// A re-INVITE without an offer asks for one.
			ack, err := r.Accept(offer2)
			offered <- reinviteResult{ack, err}
			return
		}
		infos <- r
		r.Accept(Body{})
	}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}

	// The reliable 183 with the answer, the 180 and the 200 come through,
	// and the caller's PRACKs and ACK end at the B2BUA.
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{183, 180, 200}) {
		t.Fatalf("caller got %v, want 183, 180 and 200", codes(got))
	}
	if string(got[0].Body) != string(answer.Content) || !got[0].Requires(Option100rel) || !got[1].Requires(Option100rel) {
		t.Errorf("183 has body %q and Require %q, 180 has Require %q; want the callee's answer and 100rel on both",
			got[0].Body, got[0].GetAll("Require"), got[1].GetAll("Require"))
	}
	in := <-calls

	// Two legs, two dialogs: the callee talks to the B2BUA, not to the
	// caller.
	uac, _ := out.Dialog()
	uas, _ := in.Dialog()
	if uac.ID.CallID == uas.ID.CallID || uac.RemoteTarget != b2bua.cfg.Contact || uas.RemoteTarget != b2bua.cfg.Contact {
		t.Errorf("caller's dialog %+v and callee's dialog %+v: want two Call-IDs and the B2BUA as the remote target of both", uac, uas)
	}
	if string(in.Request.Body) != string(offer.Content) || in.Request.Get("Contact") != "<"+b2bua.cfg.Contact+">" {
		t.Errorf("callee's INVITE has body %q and Contact %q, want the caller's offer from the B2BUA", in.Request.Body, in.Request.Get("Contact"))
	}

	// HOLD: the caller's re-INVITE reaches the callee, and the callee's
	// answer the caller.
	resp, err := out.ReInvite(holdOffer)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(holdAnswer.Content) {
		t.Fatalf("re-INVITE got %v, %v; want 200 with the callee's answer", resp, err)
	}
	if r := <-held; r.Request.Method != INVITE || string(r.Request.Body) != string(holdOffer.Content) {
		t.Errorf("callee's user got\n%s\nwant the re-INVITE with the caller's offer", r.Request)
	}
	if a := <-heldACKs; a.err != nil || a.ack == nil {
		t.Errorf("callee's Accept returned %v, %v; want the ACK of the B2BUA", a.ack, a.err)
	}

	// Any other method goes the same way, here from the callee.
	if resp, err := in.Send("INFO", Body{Type: "text/plain", Content: []byte("hello")}); err != nil || resp.StatusCode != 200 {
		t.Fatalf("INFO from the callee got %v, %v; want the caller's 200", resp, err)
	}
	if r := <-infos; r.Request.Method != "INFO" || string(r.Request.Body) != "hello" {
		t.Errorf("caller's user got\n%s\nwant the callee's INFO", r.Request)
	}

	// A re-INVITE without an offer, here from the callee: the caller's
	// offer comes back in the 2xx, and the callee's answer goes to the
	// caller in the ACK.
	if resp, err := in.ReInvite(Body{}); err != nil || resp.StatusCode != 200 || string(resp.Body) != string(offer2.Content) {
		t.Fatalf("re-INVITE without an offer got %v, %v; want 200 with the caller's offer", resp, err)
	}
	if err := in.Ack(answer2); err != nil {
		t.Fatal(err)
	}
	if a := <-offered; a.err != nil || a.ack == nil || string(a.ack.Body) != string(answer2.Content) {
		t.Errorf("caller's Accept returned %v, %v; want the ACK with the callee's answer", a.ack, a.err)
	}

	// The BYE, with its Reason.
	why := NewReason("RELEASE_CAUSE", 1, "User ends call")
	if err := out.Bye(why.Header()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, in.Done(), "callee")
	if bye := in.EndedBy(); bye == nil || bye.Method != BYE || !reflect.DeepEqual(Reasons(bye), []Reason{why}) {
		t.Errorf("callee's call was ended by %v, want a BYE with the caller's Reason", bye)
	}
}

func TestCancelThroughAB2BUA(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Progress(180, Body{})
		calls <- c
	})
	b2bua := newB2BUA(t, calleeUA.cfg.Contact)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(b2bua))
	if err != nil {
		t.Fatal(err)
	}
	if ringing := <-out.Responses(); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}
	why := NewReason(ReasonSIP, 487, "Caller gave up")
	if err := out.Cancel(why.Header()); err != nil {
		t.Fatal(err)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{487}) {
		t.Errorf("caller got %v after the CANCEL, want 487", got)
	}
	in := <-calls
	waitDone(t, in.Done(), "callee")
	cancel := in.EndedBy()
	if !errors.Is(in.Err(), ErrCancelled) || cancel == nil || cancel.Method != CANCEL || !reflect.DeepEqual(Reasons(cancel), []Reason{why}) {
		t.Errorf("callee's call ended with %v by %v, want ErrCancelled by a CANCEL with the caller's Reason", in.Err(), cancel)
	}
}

func TestRejectionThroughAB2BUA(t *testing.T) {
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) { c.Reject(486) })
	b2bua := newB2BUA(t, calleeUA.cfg.Contact)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(b2bua))
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{486}) {
		t.Errorf("caller got %v, want the callee's 486", got)
	}
}
