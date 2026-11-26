package ibcf_test

import (
	"errors"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file give an IBCF an SDP body that it cannot read.
// It cannot point such an SDP at its TrGW, so the SDP does not cross:
// the IBCF answers a request with it 488 (Not Acceptable Here), as the
// border does, and releases a call whose response has it.

// unreadable is a body that says it is SDP and is not.
var unreadable = sip.Body{Type: "application/sdp", Content: []byte("this is not SDP\r\n")}

// noCodecLists has neither network's profile of the other list codecs,
// so that the border does not read the SDP, and the IBCF is the first to.
func noCodecLists(ofB, ofA *interconnect.Profile) { ofB.Codecs, ofA.Codecs = nil, nil }

// The first SDP of the call, in the INVITE: the caller gets 488, and the
// INVITE goes no further.
func TestUnreadableSDPOfTheINVITEIsRejected(t *testing.T) {
	p := newPair(t, noCodecLists)
	call := p.a.invite(t, p.b.sipURI(), unreadable, nil)
	if resp := final(t, call); resp.StatusCode != 488 {
		t.Errorf("the INVITE got %d, want 488", resp.StatusCode)
	}
	if got := p.b.rec.requests("ibcf1-external", sip.INVITE); len(got) != 0 {
		t.Errorf("network B received %d INVITEs, want none", len(got))
	}
	if reservedA, _ := p.waitReleased(t); reservedA != 0 {
		t.Errorf("TrGW A reserved %d contexts, want none", reservedA)
	}
}

// The first SDP of the call, as the offer in the response to an INVITE
// without one: the caller gets 500, and the callee a CANCEL after its
// 183, or a BYE after its 200.
func TestUnreadableOfferInAResponseEndsTheCall(t *testing.T) {
	for _, status := range []int{183, 200} {
		t.Run(sip.NewResponse(&sip.Message{Method: sip.INVITE}, status, "").Reason, func(t *testing.T) {
			p := newPair(t, nil)
			call := p.a.invite(t, p.b.sipURI(), sip.Body{}, nil)
			in := p.b.nextCall(t)
			if status == 183 {
				go in.Progress(183, unreadable)
			} else {
				go in.Accept(unreadable)
			}
			provisional, resp := responsesOf(t, call)
			if resp.StatusCode != 500 {
				t.Errorf("the INVITE got %d, want 500", resp.StatusCode)
			}
			for _, m := range provisional {
				if len(m.Body) > 0 {
					t.Errorf("the caller got a %d with the SDP that the IBCF cannot read", m.StatusCode)
				}
			}
			waitDone(t, "the callee's call", in.Done())
			if status == 183 && !errors.Is(in.Err(), sip.ErrCancelled) {
				t.Errorf("the callee's INVITE ended with %v, want a CANCEL", in.Err())
			}
			if m := in.EndedBy(); status == 200 && (m == nil || m.Method != sip.BYE) {
				t.Errorf("the callee's call ended by %v, want a BYE", m)
			}
			if got := p.a.rec.requests("ue", sip.ACK); status == 200 && len(got) != 0 {
				t.Errorf("the caller got %d ACKs, want none", len(got))
			}
			p.waitReleased(t)
		})
	}
}

// An SDP within the call that the IBCF cannot read: the request gets
// 488 and goes no further, and the call stays.
func TestUnreadableSDPWithinTheCallGets488(t *testing.T) {
	p := newPair(t, noCodecLists)
	call, in, _, calleeGot := p.answeredCall(t)
	if resp, err := call.ReInvite(unreadable); err != nil || resp.StatusCode != 488 {
		t.Errorf("the re-INVITE got %v %v, want 488", resp, err)
	}
	if got := p.b.rec.requests("ibcf1-external", sip.INVITE); len(got) != 1 {
		t.Errorf("network B received %d INVITEs, want the first one only", len(got))
	}
	if resp, err := call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 3, "")); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the next re-INVITE got %v %v, want 200", resp, err)
	}
	select {
	case m := <-calleeGot:
		if v := originVersion(t, m); v != 3 {
			t.Errorf("the callee got the offer of version %d first, want 3", v)
		}
	default:
		t.Error("the next re-INVITE did not reach the callee")
	}
	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the callee's call after the caller's BYE", in.Done())
	p.waitReleased(t)
}
