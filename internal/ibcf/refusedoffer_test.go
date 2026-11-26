package ibcf_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have network A's TrGW refuse the SDP of a
// request: the IBCF answers the request itself and does not pass on an
// SDP that names the addresses of the side that sent it.

// answeredCall has network A's UE call network B's UE, which answers at
// once. callerGot and calleeGot receive the requests within the dialog
// that reach each UE, which answers them (network.answerInDialog). It
// returns once the ACK has reached the callee.
func (p *pair) answeredCall(t *testing.T) (call *sip.OutgoingCall, in *sip.IncomingCall, callerGot, calleeGot chan *sip.Message) {
	t.Helper()
	callerGot, calleeGot = make(chan *sip.Message, 4), make(chan *sip.Message, 4)
	call = p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), callerGot)
	in = p.b.nextCall(t)
	in.Handle(p.b.answerInDialog(calleeGot))
	accepted := make(chan error, 1)
	go func() {
		_, err := in.Accept(p.b.answer(t, in.Request))
		accepted <- err
	}()
	if ok := final(t, call); ok.StatusCode != 200 {
		t.Fatalf("the call got %d", ok.StatusCode)
	}
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("no ACK at the callee: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no ACK at the callee")
	}
	return call, in, callerGot, calleeGot
}

// originVersion is the version of the o= line of the SDP in m.
func originVersion(t *testing.T, m *sip.Message) uint64 {
	t.Helper()
	s, err := sdp.Parse(m.Body)
	if err != nil {
		t.Fatalf("the %s has no SDP: %v", m.Method, err)
	}
	return s.Origin.Version
}

// endedByBYE waits until a call has ended, and checks that a BYE ended
// it.
func endedByBYE(t *testing.T, what string, done <-chan struct{}, endedBy func() *sip.Message) {
	t.Helper()
	waitDone(t, what, done)
	if m := endedBy(); m == nil || m.Method != sip.BYE {
		t.Errorf("%s ended by %v, want a BYE", what, m)
	}
}

// noneCame checks that ch delivered nothing.
func noneCame(t *testing.T, what string, ch <-chan *sip.Message) {
	t.Helper()
	select {
	case m := <-ch:
		t.Errorf("%s got the %s that the IBCF refused", what, m.Method)
	default:
	}
}

// errRefusedModify is how a TrGW answers a Modify that it turns down,
// having changed nothing.
var errRefusedModify = fmt.Errorf("%w: %w", agw.ErrRejected, agw.ErrBadRequest)

// requestWithOffer is a request with a new offer, of version, within the
// call: a re-INVITE of the caller, or an UPDATE of the callee.
func (p *pair) requestWithOffer(t *testing.T, call *sip.OutgoingCall, in *sip.IncomingCall, ofCallee bool, version uint64) (*sip.Message, error) {
	t.Helper()
	if ofCallee {
		return in.Update(p.b.offer(t, sdp.VoLTECodecs(), version, ""))
	}
	return call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), version, ""))
}

// The TrGW turns down the offer of the INVITE: the caller gets 500, the
// INVITE goes no further, and the context goes.
func TestRefusedOfferOfTheINVITEIsRejected(t *testing.T) {
	p := newPair(t, nil)
	p.a.trgw.refuse(ibcf.CoreSide, errRefusedModify)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	if resp := final(t, call); resp.StatusCode != 500 {
		t.Errorf("the INVITE got %d, want 500", resp.StatusCode)
	}
	if got := p.b.rec.requests("ibcf1-external", sip.INVITE); len(got) != 0 {
		t.Errorf("network B received %d INVITEs, want none", len(got))
	}
	if reservedA, _ := p.waitReleased(t); reservedA != 1 {
		t.Errorf("TrGW A reserved %d contexts, want 1", reservedA)
	}
}

// The TrGW turns down the offer of a request within the call, and
// changes nothing. The request gets 500 and goes no further, and the
// call stays on the last offer and answer: once the TrGW takes the SDP
// again, the next offer crosses, and it is the first one that reaches
// the other UE. This holds for an offer that leaves network A and for
// one that enters it.
func TestRefusedOfferWithinTheCallIsRejected(t *testing.T) {
	for _, c := range []struct {
		name     string
		side     ibcf.Side
		ofCallee bool
	}{
		{"re-INVITE of the caller, leaving network A", ibcf.CoreSide, false},
		{"UPDATE of the callee, entering network A", ibcf.PeerSide, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPair(t, nil)
			call, in, callerGot, calleeGot := p.answeredCall(t)
			got := calleeGot
			if c.ofCallee {
				got = callerGot
			}
			p.a.trgw.refuse(c.side, errRefusedModify)
			if resp, err := p.requestWithOffer(t, call, in, c.ofCallee, 2); err != nil || resp.StatusCode != 500 {
				t.Errorf("the request with the refused offer got %v %v, want 500", resp, err)
			}
			p.a.trgw.refuse(c.side, nil)
			if resp, err := p.requestWithOffer(t, call, in, c.ofCallee, 3); err != nil || resp.StatusCode != 200 {
				t.Fatalf("the next request got %v %v, want 200", resp, err)
			}
			select {
			case m := <-got:
				if v := originVersion(t, m); v != 3 {
					t.Errorf("the other UE got the offer of version %d first, want 3", v)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the next request did not reach the other UE")
			}
			if err := call.Bye(); err != nil {
				t.Fatalf("BYE: %v", err)
			}
			waitDone(t, "the callee's call after the caller's BYE", in.Done())
			p.waitReleased(t)
			assertHidden(t, p.a, planB)
			assertHidden(t, p.b, planA)
		})
	}
}

// When the TrGW may have carried the change out, because it did not
// answer in time, or has lost the context, the request gets 500 and the
// IBCF releases the call: each UE gets a BYE, and both contexts go.
func TestOfferWithinTheCallEndsTheCallWhenTheTrGWMayHaveTakenIt(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"no answer in time", context.DeadlineExceeded},
		{"context lost", fmt.Errorf("%w: %w", agw.ErrRejected, agw.ErrNoContext)},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPair(t, nil)
			call, in, _, calleeGot := p.answeredCall(t)
			p.a.trgw.refuse(ibcf.CoreSide, c.err)
			if resp, err := call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 2, "")); err != nil || resp.StatusCode != 500 {
				t.Errorf("the re-INVITE got %v %v, want 500", resp, err)
			}
			endedByBYE(t, "the caller's call", call.Done(), call.EndedBy)
			endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
			noneCame(t, "the callee", calleeGot)
			p.waitReleased(t)
			assertHidden(t, p.b, planA)
		})
	}
}

// An INVITE without an offer: the callee's reliable 183 has the offer,
// and the TrGW turns down the answer in the caller's PRACK. The PRACK
// gets 500, and the IBCF ends the INVITE whatever the TrGW answered,
// since the reliable 183 would stay without its PRACK: the caller's
// INVITE gets 500, and the callee a CANCEL.
func TestRefusedSDPOfAPRACKEndsTheINVITE(t *testing.T) {
	p := newPair(t, nil)
	p.a.trgw.refuse(ibcf.CoreSide, errRefusedModify)
	call := p.a.invite(t, p.b.sipURI(), sip.Body{}, nil)
	in := p.b.nextCall(t)
	pracked := make(chan *sip.Message, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		pracked <- r.Request
		r.Accept(sip.Body{})
	})
	go in.Progress(183, p.b.offer(t, sdp.VoLTECodecs(), 1, ""))
	var offer *sip.Message
	select {
	case offer = <-call.Responses():
	case <-time.After(10 * time.Second):
		t.Fatal("no 183 reached the caller")
	}
	if resp, err := call.PRACK(offer, p.a.answer(t, offer)); err != nil || resp.StatusCode != 500 {
		t.Errorf("the PRACK got %v %v, want 500", resp, err)
	}
	if resp := final(t, call); resp.StatusCode != 500 {
		t.Errorf("the INVITE got %d, want 500", resp.StatusCode)
	}
	waitDone(t, "the callee's INVITE", in.Done())
	if !errors.Is(in.Err(), sip.ErrCancelled) {
		t.Errorf("the callee's INVITE ended with %v, want a CANCEL", in.Err())
	}
	noneCame(t, "the callee", pracked)
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
}
