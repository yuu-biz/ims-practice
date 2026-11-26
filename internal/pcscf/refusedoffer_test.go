package pcscf

import (
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have a media relay that does not take the SDP
// offer of a request within a dialog, as one that restarts while a call
// is up. The P-CSCF then has no offer that names the relay to pass on,
// and answers the request 500 itself.

// offerer is a party of a call that sends a new offer within its
// dialog: the caller's *sip.OutgoingCall or the callee's
// *sip.IncomingCall.
type offerer interface {
	Update(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	ReInvite(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
}

// reoffer sends a new offer from party in request, "UPDATE" or
// "re-INVITE", and returns the final response to it.
func reoffer(t *testing.T, party offerer, request string, media netip.AddrPort) *sip.Message {
	t.Helper()
	send := party.Update
	if request == "re-INVITE" {
		send = party.ReInvite
	}
	resp, err := send(sdpBody(sdp.Offer("2", media)))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// wantNotForwarded checks that got, what a party has received, holds no
// request with the CSeq of resp.
func wantNotForwarded(t *testing.T, what string, got *received, resp *sip.Message) {
	t.Helper()
	for _, m := range got.all() {
		if m.Method != "" && m.Get("CSeq") == resp.Get("CSeq") {
			t.Errorf("%s got the %s whose offer the relay did not take", what, m.Method)
		}
	}
}

// wantCallUp checks that the P-CSCF holds the call of key as it was
// before the refused offer: its Dialog, the relay context, and the one
// authorisation of the media, with nothing released and no BYE sent.
func wantCallUp(t *testing.T, h *callHarness, pcrf *fakePolicy, relay *fakeRelay, key DialogKey) {
	t.Helper()
	if d, ok := h.dialog(key); !ok || d.Released || d.ReleasePending {
		t.Errorf("dialog = %+v, %v; want it kept, and not released", d, ok)
	}
	if n := relay.held(); n != 1 {
		t.Errorf("the relay holds %d contexts, want the one of the call", n)
	}
	if n := len(pcrf.mediaRequests()); n != 1 {
		t.Errorf("%d requests to authorise media, want only the one of the call's first answer", n)
	}
	if released := pcrf.releases(); len(released) != 0 {
		t.Errorf("the policy sessions %v were released, want none", released)
	}
	for _, got := range []*received{h.ueGot, h.coreGot} {
		for _, m := range got.all() {
			if m.Method == sip.BYE {
				t.Errorf("a party got a BYE while the call stays up: %v", m)
			}
		}
	}
}

// hangUp ends the call of key with the UE's BYE, and checks that what
// the call held goes with it (TS 24.229 5.2.8.2, TS 29.214 4.4.4).
func hangUp(t *testing.T, h *callHarness, out *sip.OutgoingCall, pcrf *fakePolicy, relay *fakeRelay, key DialogKey) {
	t.Helper()
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	pcrf.waitReleased(t, "session-1")
	relay.waitHeld(t, 0, time.Second)
	h.waitNoDialog(key)
}

// TestRefusedOfferWithinTheDialogIsRejected: the relay does not take the
// offer of an UPDATE, or of a re-INVITE such as the one of a hold, in a
// call that is up, from either party. The P-CSCF cannot make the offer
// that TS 24.229 6.7.2.1 asks of it, so it forwards nothing and answers
// the request 500 (RFC 3261 21.5.1), as TS 24.229 6.2 has it answer a
// request whose offer it cannot let through. A non-2xx leaves the
// session as it was (RFC 3261 14.1, RFC 3311 5.3): the call stays up on
// the media of the last offer and answer, through the relay, and the
// PCRF keeps the authorisation it has.
func TestRefusedOfferWithinTheDialogIsRejected(t *testing.T) {
	moved := netip.MustParseAddrPort("192.0.2.50:4002")
	for _, request := range []string{"UPDATE", "re-INVITE"} {
		for _, sender := range []struct {
			name   string
			fromUE bool
		}{{"from the UE", true}, {"to the UE", false}} {
			t.Run(request+" "+sender.name, func(t *testing.T) {
				h, pcrf, relay := newPolicyHarness(t)
				out, in := h.call()
				answerOffers(in)
				acked := accept(in, answerTo(t, in.Request, coreMedia))
				answered(t, out, 200)
				waitACK(t, acked)
				ueDialog, ok := out.Dialog()
				key := keyOf(t, ueDialog, ok)

				// The offer of the UE tells the access termination where the
				// UE now takes its media; that of the far end, the core one.
				var party offerer = out
				refused, peer, peerGot := agw.Access, "the far end", h.coreGot
				if !sender.fromUE {
					party, refused, peer, peerGot = in, agw.Core, "the UE", h.ueGot
				}
				relay.refuseModify(refused)
				resp := reoffer(t, party, request, moved)
				if resp.StatusCode != 500 {
					t.Fatalf("the %s got %d, want 500 from the P-CSCF", request, resp.StatusCode)
				}
				wantNotForwarded(t, peer, peerGot, resp)
				wantCallUp(t, h, pcrf, relay, key)

				relay.refuseModify("")
				hangUp(t, h, out, pcrf, relay, key)
			})
		}
	}
}

// TestRefusedOfferOfAnUPDATEInTheEarlyDialogKeepsTheCall: the UPDATE
// comes before the 200 to the INVITE, as when a VoLTE UE tells that its
// resources are reserved, and the relay does not take its offer. The
// UPDATE gets 500 and goes no further, as within a confirmed dialog;
// the INVITE goes on, and the callee can still answer it.
func TestRefusedOfferOfAnUPDATEInTheEarlyDialogKeepsTheCall(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	out, in := h.call()
	answerOffers(in)
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	ueDialog, ok := out.Dialog()
	key := keyOf(t, ueDialog, ok)

	relay.refuseModify(agw.Access)
	resp := reoffer(t, out, "UPDATE", netip.MustParseAddrPort("192.0.2.50:4002"))
	if resp.StatusCode != 500 {
		t.Fatalf("the UPDATE got %d, want 500 from the P-CSCF", resp.StatusCode)
	}
	wantNotForwarded(t, "the callee", h.coreGot, resp)
	wantCallUp(t, h, pcrf, relay, key)

	relay.refuseModify("")
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	waitACK(t, acked)
	hangUp(t, h, out, pcrf, relay, key)
}
