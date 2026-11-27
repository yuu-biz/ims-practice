package pcscf

import (
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have the other party of a call turn down an
// offer within the dialog that the relay has taken. The P-CSCF told the
// termination that faces the party that offered to send where the offer
// said before it passed the offer on. A final response other than 2xx
// leaves the session as it was before the request (RFC 3261 14.1, RFC
// 3311 5.3), so the P-CSCF has the termination send where the last offer
// and answer said again.

// TestRejectedOfferPutsTheRelayBack: a party of a call that is up offers
// a new port in an UPDATE or a re-INVITE, and the other party turns the
// offer down with 488 (Not Acceptable Here). The party that offered
// still takes its media at the old port, and the termination that faces
// it sends there again. The call stays up.
func TestRejectedOfferPutsTheRelayBack(t *testing.T) {
	for _, request := range []string{"UPDATE", "re-INVITE"} {
		for _, sender := range offerSenders {
			t.Run(request+" "+sender.name, func(t *testing.T) {
				h, pcrf, relay := newPolicyHarness(t)
				c := newUpCall(t, h)
				// The far end turns every offer down, and the UE, which has
				// no handler here, answers each one 488 itself.
				c.in.Handle(func(r *sip.InDialogRequest) { r.Reject(488) })
				party, peer, peerGot := sender.of(h, c)
				facing := sender.refused
				before := relay.remote("1", facing)

				resp := reoffer(t, party, request, moved)
				if resp.StatusCode != 488 {
					t.Fatalf("the %s got %d, want the 488 of %s", request, resp.StatusCode, peer)
				}
				wantForwarded(t, peer, peerGot, resp)
				if got := relay.remote("1", facing); got != before {
					t.Errorf("the %s termination sends to %v after the 488, want %v again", facing, got, before)
				}
				wantCallUp(t, h, pcrf, relay, c.key)
				hangUp(t, h, c.out, pcrf, relay, c.key)
			})
		}
	}
}

// TestRejectedOfferGoesBackToTheLastOfferAndAnswer: a re-INVITE of the
// UE moves its media, and the far end answers from a new port too. A
// later offer of each party is turned down, and each termination goes
// back where that offer and answer said, not where the INVITE and its
// answer did.
func TestRejectedOfferGoesBackToTheLastOfferAndAnswer(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	c := newUpCall(t, h)
	farMoved := netip.MustParseAddrPort("192.0.2.99:6004")
	c.in.Handle(func(r *sip.InDialogRequest) { r.Accept(sdpBody(sdp.Offer("3", farMoved))) })
	if resp := reoffer(t, c.out, "re-INVITE", moved); resp.StatusCode != 200 {
		t.Fatalf("the re-INVITE of the UE got %d, want 200", resp.StatusCode)
	}

	c.in.Handle(func(r *sip.InDialogRequest) { r.Reject(488) })
	if resp := reoffer(t, c.out, "re-INVITE", netip.MustParseAddrPort("192.0.2.50:4004")); resp.StatusCode != 488 {
		t.Fatalf("the second re-INVITE of the UE got %d, want 488", resp.StatusCode)
	}
	if got := relay.remote("1", agw.Access); got != moved {
		t.Errorf("the access termination sends to %v after the 488, want %v of the last offer", got, moved)
	}
	if resp := reoffer(t, c.in, "re-INVITE", netip.MustParseAddrPort("192.0.2.99:6006")); resp.StatusCode != 488 {
		t.Fatalf("the re-INVITE of the far end got %d, want the 488 of the UE", resp.StatusCode)
	}
	if got := relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v after the 488, want %v of the last answer", got, farMoved)
	}
	wantCallUp(t, h, pcrf, relay, c.key)
	hangUp(t, h, c.out, pcrf, relay, c.key)
}

// TestRejectedOfferEndsTheCallWhenTheRelayDoesNotGoBack: the relay takes
// the offer, but not the change back after the 488. The termination
// keeps sending where the party that offered no longer takes its media,
// so the P-CSCF releases the dialog, as when the relay does not take an
// answer: the 488 goes on, and each party then gets a BYE (TS 24.229
// 5.2.8.1.2). The policy session and the relay context go.
func TestRejectedOfferEndsTheCallWhenTheRelayDoesNotGoBack(t *testing.T) {
	for _, request := range []string{"UPDATE", "re-INVITE"} {
		for _, sender := range offerSenders {
			t.Run(request+" "+sender.name, func(t *testing.T) {
				h, pcrf, relay := newPolicyHarness(t)
				c := newUpCall(t, h)
				c.in.Handle(func(r *sip.InDialogRequest) { r.Reject(488) })
				party, _, _ := sender.of(h, c)
				relay.refuseRemote(relay.remote("1", sender.refused))

				if resp := reoffer(t, party, request, moved); resp.StatusCode != 488 {
					t.Fatalf("the %s got %d, want 488", request, resp.StatusCode)
				}
				endedByBYE(t, "the UE's call", c.out)
				endedByBYE(t, "the far end's call", c.in)
				pcrf.waitReleased(t, "session-1")
				relay.waitHeld(t, 0, time.Second)
				h.waitNoDialog(c.key)
			})
		}
	}
}

// TestRejectedOfferAfterAPreviewPutsBothSidesBack: the far end shows its
// answer to a re-INVITE of the UE in a 183 that is not reliable, and then
// turns the offer down with 488. Only an answer in a 2xx or a reliable
// provisional response is the answer (RFC 3261 13.2.1), so the session
// is as it was before the re-INVITE (14.1). The relay sent to each party
// where the offer and the 183 said, and both terminations send where the
// INVITE and its answer said again.
func TestRejectedOfferAfterAPreviewPutsBothSidesBack(t *testing.T) {
	c := newRawINVITE(t)
	ok := c.response(200)
	c.st.Respond(ok)
	if r := final(t, c.ct.Responses(), 3*time.Second); r.StatusCode != 200 {
		t.Fatalf("the caller got %d, want the 200", r.StatusCode)
	}
	access, core := c.relay.remote("1", agw.Access), c.relay.remote("1", agw.Core)

	reinvite := &sip.Message{Method: sip.INVITE, RequestURI: c.farContact}
	reinvite.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	reinvite.Add("Max-Forwards", "70")
	reinvite.Add("From", c.st.Request.Get("From"))
	reinvite.Add("To", ok.Get("To"))
	reinvite.Add("Call-ID", c.st.Request.Get("Call-ID"))
	reinvite.Add("CSeq", "2 INVITE")
	reinvite.Add("Contact", c.st.Request.Get("Contact"))
	reinvite.Add("Content-Type", "application/sdp")
	reinvite.Body = sdp.Offer("2", moved).Bytes()
	ct, err := c.ue.SendRequest(reinvite, sip.Destination{Transport: sip.UDP, Addr: c.pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var st *sip.ServerTransaction
	select {
	case st = <-c.invites:
	case <-time.After(3 * time.Second):
		t.Fatal("the far end got no re-INVITE")
	}
	preview := netip.MustParseAddrPort("192.0.2.99:6008")
	progress := sip.NewResponse(st.Request, 183, "")
	progress.Add("Contact", "<"+c.farContact+">")
	progress.Add("Content-Type", "application/sdp")
	progress.Body = sdp.Offer("3", preview).Bytes()
	st.Respond(progress)
	if r := <-ct.Responses(); r.StatusCode != 183 {
		t.Fatalf("the UE got %d, want the 183", r.StatusCode)
	}
	if got := c.relay.remote("1", agw.Core); got != preview {
		t.Fatalf("the core termination sends to %v for the 183, want %v", got, preview)
	}
	st.Respond(sip.NewResponse(st.Request, 488, ""))
	if r := final(t, ct.Responses(), 3*time.Second); r.StatusCode != 488 {
		t.Fatalf("the re-INVITE got %d, want the 488 of the far end", r.StatusCode)
	}
	if got := c.relay.remote("1", agw.Access); got != access {
		t.Errorf("the access termination sends to %v after the 488, want %v of the INVITE again", got, access)
	}
	if got := c.relay.remote("1", agw.Core); got != core {
		t.Errorf("the core termination sends to %v after the 488, want %v of its answer again", got, core)
	}
	if n := c.relay.held(); n != 1 {
		t.Errorf("the relay holds %d contexts after the 488, want the one of the call", n)
	}
}
