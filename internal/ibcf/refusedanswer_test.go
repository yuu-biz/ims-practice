package ibcf_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have a TrGW refuse the SDP of a response or of
// an ACK. Nobody can turn those down, so the IBCF releases the call, and
// it does not pass on an SDP that names the addresses of the side that
// sent it.

// responsesOf reads the responses to an INVITE up to its final one.
func responsesOf(t *testing.T, c *sip.OutgoingCall) (provisional []*sip.Message, final *sip.Message) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case resp, ok := <-c.Responses():
			if !ok {
				t.Fatalf("the INVITE got no final response: %v", c.Err())
			}
			if resp.StatusCode >= 200 {
				return provisional, resp
			}
			provisional = append(provisional, resp)
		case <-timeout:
			t.Fatal("no final response to the INVITE")
		}
	}
}

// The TrGW refuses the answer to the offer of the INVITE. The caller gets
// 500 in the place of the response with the answer, and the callee a
// CANCEL, or a BYE after its 200. This holds for an answer that enters a
// network and for one that leaves it.
func TestRefusedAnswerOfTheINVITEEndsTheCall(t *testing.T) {
	for _, c := range []struct {
		name     string
		refusing func(p *pair) *fakeTrGW
		side     ibcf.Side
		answer   int
	}{
		{"183 entering network A", func(p *pair) *fakeTrGW { return p.a.trgw }, ibcf.PeerSide, 183},
		{"183 leaving network B", func(p *pair) *fakeTrGW { return p.b.trgw }, ibcf.CoreSide, 183},
		{"200 leaving network B", func(p *pair) *fakeTrGW { return p.b.trgw }, ibcf.CoreSide, 200},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPair(t, nil)
			c.refusing(p).refuse(c.side, errRefusedModify)
			call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
			in := p.b.nextCall(t)
			answer := p.b.answer(t, in.Request)
			if c.answer == 183 {
				go in.Progress(183, answer)
			} else {
				go in.Accept(answer)
			}
			provisional, resp := responsesOf(t, call)
			if resp.StatusCode != 500 {
				t.Errorf("the INVITE got %d, want 500", resp.StatusCode)
			}
			for _, m := range provisional {
				if len(m.Body) > 0 {
					t.Errorf("the caller got a %d with the answer that the TrGW refused", m.StatusCode)
				}
			}
			waitDone(t, "the callee's call", in.Done())
			if c.answer == 183 && !errors.Is(in.Err(), sip.ErrCancelled) {
				t.Errorf("the callee's INVITE ended with %v, want a CANCEL", in.Err())
			}
			if m := in.EndedBy(); c.answer == 200 && (m == nil || m.Method != sip.BYE) {
				t.Errorf("the callee's call ended by %v, want a BYE", m)
			}
			p.waitReleased(t)
			assertHidden(t, p.a, planB)
			assertHidden(t, p.b, planA)
		})
	}
}

// The TrGW refuses the answer to an offer within the call. The request
// gets 500 in the place of the 2xx with the answer, and each UE a BYE.
// This holds for an answer that enters network A and for one that leaves
// it.
func TestRefusedAnswerWithinTheCallEndsTheCall(t *testing.T) {
	for _, c := range []struct {
		name     string
		side     ibcf.Side
		ofCallee bool
	}{
		{"answer to the caller's re-INVITE, entering network A", ibcf.PeerSide, false},
		{"answer to the callee's UPDATE, leaving network A", ibcf.CoreSide, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPair(t, nil)
			call, in, _, _ := p.answeredCall(t)
			p.a.trgw.refuse(c.side, errRefusedModify)
			// The callee's UPDATE crosses network B's IBCF on its way, and
			// that IBCF may take the BYE of network A's before the 500 that
			// went ahead of it. It then answers the UPDATE 487, as a request
			// that the end of its dialog left pending (RFC 3261 15.1.2).
			want := []int{500}
			if c.ofCallee {
				want = append(want, 487)
			}
			if resp, err := p.requestWithOffer(t, call, in, c.ofCallee, 2); err != nil || !slices.Contains(want, resp.StatusCode) {
				t.Errorf("the request got %v %v, want one of %v in the place of the 2xx", resp, err, want)
			}
			endedByBYE(t, "the caller's call", call.Done(), call.EndedBy)
			endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
			p.waitReleased(t)
			assertHidden(t, p.a, planB)
			assertHidden(t, p.b, planA)
		})
	}
}

// An INVITE without an offer: the 200 has the offer, and the TrGW refuses
// the answer in the caller's ACK. The callee's 200 gets an ACK without
// that answer, and each UE a BYE.
func TestRefusedAnswerInTheACKEndsTheCall(t *testing.T) {
	p := newPair(t, nil)
	p.a.trgw.refuse(ibcf.CoreSide, errRefusedModify)
	call := p.a.invite(t, p.b.sipURI(), sip.Body{}, nil)
	in := p.b.nextCall(t)
	go in.Accept(p.b.offer(t, sdp.VoLTECodecs(), 1, ""))
	ok := final(t, call)
	if ok.StatusCode != 200 {
		t.Fatalf("the call got %d", ok.StatusCode)
	}
	if err := call.Ack(p.a.answer(t, ok)); err != nil {
		t.Fatalf("ACK with the answer: %v", err)
	}
	endedByBYE(t, "the caller's call", call.Done(), call.EndedBy)
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
	// The BYE may reach the callee before the ACK that went ahead of it,
	// so the ACK is read from what the callee received.
	acks := p.b.rec.requests("ue", sip.ACK)
	if len(acks) == 0 {
		t.Error("the callee's 200 got no ACK")
	}
	for _, ack := range acks {
		if len(ack.Body) != 0 {
			t.Errorf("the callee's 200 got an ACK with %d bytes of body, want none", len(ack.Body))
		}
	}
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
}
