package pcscf

import (
	"context"
	"fmt"
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

// moved is where a party that offers anew takes its media.
var moved = netip.MustParseAddrPort("192.0.2.50:4002")

// upCall is a call from the UE through the P-CSCF that is up: its 200 has
// had its ACK, and the far end answers each offer within the dialog.
type upCall struct {
	out *sip.OutgoingCall
	in  *sip.IncomingCall
	key DialogKey
}

func newUpCall(t *testing.T, h *callHarness) upCall {
	t.Helper()
	out, in := h.call()
	answerOffers(in)
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	waitACK(t, acked)
	d, ok := out.Dialog()
	return upCall{out: out, in: in, key: keyOf(t, d, ok)}
}

// offerSenders are the two parties of a call from the UE that make a new
// offer. The offer of the UE tells the access termination where the UE
// now takes its media; that of the far end, the core one.
var offerSenders = []struct {
	name    string
	refused agw.TerminationID
	of      func(*callHarness, upCall) (party offerer, peer string, peerGot *received)
}{
	{"from the UE", agw.Access, func(h *callHarness, c upCall) (offerer, string, *received) {
		return c.out, "the far end", h.coreGot
	}},
	{"to the UE", agw.Core, func(h *callHarness, c upCall) (offerer, string, *received) {
		return c.in, "the UE", h.ueGot
	}},
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

// wantForwarded checks that got, what a party has received, holds the
// request that resp answers: the P-CSCF passed it on.
func wantForwarded(t *testing.T, what string, got *received, resp *sip.Message) {
	t.Helper()
	got.wait(t, what+" getting the request of "+resp.Get("CSeq"), func(m *sip.Message) bool {
		return m.Method != "" && m.Get("CSeq") == resp.Get("CSeq")
	})
}

// wantCallUp checks, for watch, that the P-CSCF holds the call of key
// as it was before the refused offer: its Dialog, the relay context, and
// the one authorisation of the media, with nothing released and no BYE
// sent. The caller has had a positive sign first, such as a 200 that
// shows that the dialog serves requests, so that a release that would
// come after the response has had its time.
func wantCallUp(t *testing.T, h *callHarness, pcrf *fakePolicy, relay *fakeRelay, key DialogKey) {
	t.Helper()
	for deadline := time.Now().Add(watch); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, got := range []*received{h.ueGot, h.coreGot} {
			for _, m := range got.all() {
				if m.Method == sip.BYE {
					t.Fatalf("a party got a BYE while the call stays up: %v", m)
				}
			}
		}
		if d, ok := h.dialog(key); !ok || d.Released || d.ReleasePending {
			t.Fatalf("dialog = %+v, %v; want it kept, and not released", d, ok)
		}
	}
	if n := relay.held(); n != 1 {
		t.Errorf("the relay holds %d contexts, want the one of the call", n)
	}
	if released := pcrf.releases(); len(released) != 0 {
		t.Errorf("the policy sessions %v were released, want none", released)
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

// TestRefusedOfferWithinTheDialogIsRejected: the relay turns down the
// Modify for the offer of an UPDATE, or of a re-INVITE such as the one
// of a hold, in a call that is up, from either party, and changes
// nothing. The P-CSCF cannot make the offer that TS 24.229 6.7.2.1 asks
// of it, so it forwards nothing and answers the request 500 (RFC 3261
// 21.5.1). A non-2xx leaves the session as it was (RFC 3261 14.1, RFC
// 3311 5.3): the call stays up on the media of the last offer and
// answer, through the relay, the termination still sends where it did,
// and the PCRF keeps the authorisation it has. A later offer that the
// relay takes goes through.
func TestRefusedOfferWithinTheDialogIsRejected(t *testing.T) {
	for _, request := range []string{"UPDATE", "re-INVITE"} {
		for _, sender := range offerSenders {
			t.Run(request+" "+sender.name, func(t *testing.T) {
				h, pcrf, relay := newPolicyHarness(t)
				c := newUpCall(t, h)
				party, peer, peerGot := sender.of(h, c)
				before := relay.remote("1", sender.refused)

				relay.refuseModify(sender.refused)
				resp := reoffer(t, party, request, moved)
				if resp.StatusCode != 500 {
					t.Fatalf("the %s got %d, want 500 from the P-CSCF", request, resp.StatusCode)
				}
				wantNotForwarded(t, peer, peerGot, resp)
				if got := relay.remote("1", sender.refused); got != before {
					t.Errorf("the %s termination sends to %v after the refused offer, want %v as before", sender.refused, got, before)
				}

				// The far end accepts the offer, and the UE, which has no
				// handler for it here, answers 488: either way the request
				// went through the dialog.
				relay.refuseModify("")
				wantForwarded(t, peer, peerGot, reoffer(t, party, request, moved))
				wantCallUp(t, h, pcrf, relay, c.key)
				hangUp(t, h, c.out, pcrf, relay, c.key)
			})
		}
	}
}

// TestOfferWithinTheDialogEndsTheCallWhenTheRelayMayHaveTakenIt: the
// relay does not answer the Modify for the offer in time, though it
// carried it out, or answers that it has lost the context of the call,
// as after a restart that lost its store. In the first case the
// termination may now send where the refused offer said, which the
// party that offered no longer takes after the non-2xx; in the second
// the media of the call are gone, and every later offer would fail the
// same way. The P-CSCF answers the request 500 and releases the dialog
// with a BYE to each party (TS 24.229 5.2.8.1.2), as when the relay
// does not take an answer, and the relay context and the policy session
// go.
func TestOfferWithinTheDialogEndsTheCallWhenTheRelayMayHaveTakenIt(t *testing.T) {
	for _, failure := range []struct {
		name    string
		err     error
		applied bool
	}{
		{"no answer in time", fmt.Errorf("agw: PUT: %w", context.DeadlineExceeded), true},
		{"the context is lost", fmt.Errorf("%w: %w", agw.ErrRejected, agw.ErrNoContext), false},
	} {
		for _, request := range []string{"UPDATE", "re-INVITE"} {
			t.Run(failure.name+" "+request, func(t *testing.T) {
				h, pcrf, relay := newPolicyHarness(t)
				c := newUpCall(t, h)

				relay.breakModify(agw.Access, failure.err, failure.applied)
				resp := reoffer(t, c.out, request, moved)
				if resp.StatusCode != 500 {
					t.Fatalf("the %s got %d, want 500 from the P-CSCF", request, resp.StatusCode)
				}
				wantNotForwarded(t, "the far end", h.coreGot, resp)
				endedByBYE(t, "the UE's call", c.out)
				endedByBYE(t, "the far end's call", c.in)
				pcrf.waitReleased(t, "session-1")
				relay.waitHeld(t, 0, time.Second)
				h.waitNoDialog(c.key)
			})
		}
	}
}

// TestMalformedOfferWithinTheDialogGets400: the SDP of an UPDATE cannot
// be read. The P-CSCF answers 400 (RFC 3261 21.4.1) and asks the relay
// nothing, so the call stays up as it was.
func TestMalformedOfferWithinTheDialogGets400(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	c := newUpCall(t, h)
	resp, err := c.out.Update(sip.Body{Type: "application/sdp", Content: []byte("not sdp")})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("the UPDATE got %d, want 400 from the P-CSCF", resp.StatusCode)
	}
	wantNotForwarded(t, "the far end", h.coreGot, resp)
	wantForwarded(t, "the far end", h.coreGot, reoffer(t, c.out, "UPDATE", moved))
	wantCallUp(t, h, pcrf, relay, c.key)
	hangUp(t, h, c.out, pcrf, relay, c.key)
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

