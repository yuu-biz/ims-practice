package pcscf

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file are the re-INVITE without an offer, which asks
// the party it reaches for one: its 2xx carries the offer and the ACK
// the answer (RFC 3261 13.2.1, 14.1).

// reinviteWithoutOffer sends a re-INVITE of the UE of c with the CSeq
// number cseq and no offer, and returns the transaction of the UE and
// the one that the far end answers by hand.
func (c *rawINVITE) reinviteWithoutOffer(t *testing.T, cseq string) (*sip.ClientTransaction, *sip.ServerTransaction) {
	t.Helper()
	m := c.inDialog(sip.INVITE, cseq)
	m.Add("Contact", c.st.Request.Get("Contact"))
	return c.sendReINVITE(t, m)
}

// ackWithAnswer hands the P-CSCF the ACK of the UE for the 2xx with the
// CSeq number cseq, carrying the UE's answer at media to the offer of
// that 2xx (RFC 3261 13.2.2.4).
func (c *rawINVITE) ackWithAnswer(cseq, session string, media netip.AddrPort) {
	m := c.inDialog(sip.ACK, cseq)
	m.Add("Content-Type", "application/sdp")
	m.Body = sdp.Offer(session, media).Bytes()
	c.p.OnACK(m)
}

// offerIn2xx has the far end of c answer the re-INVITE of st with its
// offer at media (RFC 3261 14.1), and returns the 2xx as the UE got it.
func (c *rawINVITE) offerIn2xx(t *testing.T, ct *sip.ClientTransaction, st *sip.ServerTransaction, session string, media netip.AddrPort) *sip.Message {
	t.Helper()
	ok := sip.NewResponse(st.Request, 200, "")
	ok.Add("Contact", "<"+c.farContact+">")
	ok.Add("Content-Type", "application/sdp")
	ok.Body = sdp.Offer(session, media).Bytes()
	st.Respond(ok)
	resp := final(t, ct.Responses(), 3*time.Second)
	if resp.StatusCode != 200 {
		t.Fatalf("the re-INVITE without an offer got %d, want the 200 of the far end", resp.StatusCode)
	}
	return resp
}

// offerIn183 has the far end of c show its offer at media in a 183 that
// is not reliable, before the 2xx that carries it (RFC 3261 13.2.1),
// and waits for the UE to get that 183.
func (c *rawINVITE) offerIn183(t *testing.T, ct *sip.ClientTransaction, st *sip.ServerTransaction, session string, media netip.AddrPort) {
	t.Helper()
	progress := sip.NewResponse(st.Request, 183, "")
	progress.Add("Contact", "<"+c.farContact+">")
	progress.Add("Content-Type", "application/sdp")
	progress.Body = sdp.Offer(session, media).Bytes()
	st.Respond(progress)
	r := next(t, ct.Responses())
	for r.StatusCode == 100 {
		r = next(t, ct.Responses())
	}
	if r.StatusCode != 183 {
		t.Fatalf("the UE got %d, want the 183 of the far end", r.StatusCode)
	}
}

// TestOfferInThe2xxOfAReINVITEWithoutAnOfferIsRelayedAndAuthorised: the
// UE asks the far end for an offer with a re-INVITE without one, so the
// 2xx carries the offer and the ACK the answer (RFC 3261 13.2.1, 14.1).
// Both go through the relay context of the dialog, as the offer and the
// answer of a re-INVITE that offers do (TS 24.229 6.7.2.1), and the PCRF
// gets the service information of the media they agree on with the
// answer (TS 29.214 A.1).
func TestOfferInThe2xxOfAReINVITEWithoutAnOfferIsRelayedAndAuthorised(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	wantAuthorised(t, pcrf, "for the INVITE", ueMedia)
	before := len(pcrf.mediaRequests())

	ct, st := c.reinviteWithoutOffer(t, "2")
	if len(st.Request.Body) != 0 {
		t.Fatalf("the far end got a re-INVITE with %d bytes of body, want one without an offer", len(st.Request.Body))
	}
	farMoved := netip.MustParseAddrPort("192.0.2.99:6008")
	resp := c.offerIn2xx(t, ct, st, "3", farMoved)
	if got := audio(t, resp.Body); got != relayAccess {
		t.Errorf("the offer in the 2xx names %v at the UE, want the access termination %v", got, relayAccess)
	}
	if got := c.relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v, want the address of the offer %v", got, farMoved)
	}
	if n := len(pcrf.mediaRequests()); n != before {
		t.Errorf("the PCRF got %d service information for the offer of the 2xx, want none before the answer", n-before)
	}

	// The answer in the ACK moves the UE's media, so the access
	// termination is asked to latch onto the new source of it.
	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	c.ackWithAnswer("2", "4", ueMoved)
	ack := nextRequest(t, "the far end", c.farGot)
	if ack.Method != sip.ACK {
		t.Fatalf("the far end got %s, want the ACK with the answer", ack.Method)
	}
	if got := audio(t, ack.Body); got != relayCore {
		t.Errorf("the answer in the ACK names %v at the far end, want the core termination %v", got, relayCore)
	}
	if got := c.relay.remote("1", agw.Access); got != ueMoved {
		t.Errorf("the access termination sends to %v, want the address of the answer %v", got, ueMoved)
	}
	wantLatchings(t, c.relay, agw.Access, "", agw.NAPTRelatch)

	requests := pcrf.mediaRequests()
	if len(requests) != before+1 {
		t.Fatalf("the PCRF got %d service information for the re-INVITE, want one for the answer of the ACK", len(requests)-before)
	}
	r := requests[len(requests)-1]
	if r.Session != "session-1" || r.SDP.UEOffered || r.UE != c.ue.LocalAddr().Addr() {
		t.Errorf("request after the ACK = %+v; want session-1 for the UE's address with the offer it received", r)
	}
	if got := audio(t, r.SDP.Offer); got != relayAccess {
		t.Errorf("the offer the PCRF authorises names %v, want the access termination %v", got, relayAccess)
	}
	if got := audio(t, r.SDP.Answer); got != ueMoved {
		t.Errorf("the answer the PCRF authorises names %v, want the UE's new address %v", got, ueMoved)
	}
}

// TestACKWithTheAnswerSentAgainReachesThePCRFOnce: the far end sends the
// 2xx of a re-INVITE without an offer again until its ACK arrives, so
// the ACK with the answer comes again too. The answer goes through the
// relay each time, which moves nothing and so asks for no latching
// again, and reaches the PCRF once (TS 29.214 A.1).
func TestACKWithTheAnswerSentAgainReachesThePCRFOnce(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	ct, st := c.reinviteWithoutOffer(t, "2")
	c.offerIn2xx(t, ct, st, "3", netip.MustParseAddrPort("192.0.2.99:6008"))

	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	c.ackWithAnswer("2", "4", ueMoved)
	nextRequest(t, "the far end", c.farGot)
	authorised := len(pcrf.mediaRequests())

	c.ackWithAnswer("2", "4", ueMoved)
	again := nextRequest(t, "the far end", c.farGot)
	if got := audio(t, again.Body); got != relayCore {
		t.Errorf("the answer in the ACK sent again names %v at the far end, want the core termination %v", got, relayCore)
	}
	if n := len(pcrf.mediaRequests()); n != authorised {
		t.Errorf("the PCRF got %d service information for the ACK sent again, want none", n-authorised)
	}
	wantLatchings(t, c.relay, agw.Access, "", agw.NAPTRelatch, "")
}

// TestOfferInThe2xxSentAgainReachesThePCRFOnce: the far end sends the
// 2xx of a re-INVITE without an offer again (RFC 3261 13.3.1.4) after
// the ACK with the answer has passed, so the UE sends that ACK again
// (13.2.2.4). The 2xx carries the offer that the PCRF has the answer
// of already, and the ACK sent again reaches the PCRF no more (TS
// 29.214 A.1).
func TestOfferInThe2xxSentAgainReachesThePCRFOnce(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	ct, st := c.reinviteWithoutOffer(t, "2")
	ok := sip.NewResponse(st.Request, 200, "")
	ok.Add("Contact", "<"+c.farContact+">")
	ok.Add("Content-Type", "application/sdp")
	ok.Body = sdp.Offer("3", netip.MustParseAddrPort("192.0.2.99:6008")).Bytes()
	st.Respond(ok)
	first := final(t, ct.Responses(), 3*time.Second)

	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	c.ackWithAnswer("2", "4", ueMoved)
	nextRequest(t, "the far end", c.farGot)
	authorised := len(pcrf.mediaRequests())

	st.Respond(ok)
	again := final(t, ct.Responses(), 3*time.Second)
	if string(again.Body) != string(first.Body) {
		t.Errorf("the 2xx sent again carries\n%s\nwant the SDP of the first 2xx\n%s", again.Body, first.Body)
	}
	c.ackWithAnswer("2", "4", ueMoved)
	nextRequest(t, "the far end", c.farGot)
	if n := len(pcrf.mediaRequests()); n != authorised {
		t.Errorf("the PCRF got %d service information for the 2xx and the ACK sent again, want none", n-authorised)
	}
}

// TestACKOfAnotherRequestLeavesTheOfferOfThe2xxWaiting: the ACK of the
// INVITE comes again, as when the far end sent its 200 again, after the
// 2xx of a re-INVITE without an offer. It carries no answer and names
// the CSeq of the INVITE, so the offer of that 2xx goes on waiting for
// its own ACK (RFC 3261 13.2.2.4). So does it for an ACK with SDP from
// the far end that names the CSeq number of the re-INVITE: the far end
// numbers its own requests (12.2.1.1), so that ACK is not of the
// re-INVITE of the UE.
func TestACKOfAnotherRequestLeavesTheOfferOfThe2xxWaiting(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	before := len(pcrf.mediaRequests())
	ct, st := c.reinviteWithoutOffer(t, "2")
	c.offerIn2xx(t, ct, st, "3", netip.MustParseAddrPort("192.0.2.99:6008"))

	c.ack("1")
	if m := nextRequest(t, "the far end", c.farGot); m.Method != sip.ACK {
		t.Fatalf("the far end got %s, want the ACK of the INVITE sent again", m.Method)
	}
	stray := c.fromFarEnd(sip.ACK, "2")
	stray.Add("Content-Type", "application/sdp")
	strayMedia := netip.MustParseAddrPort("192.0.2.99:6010")
	stray.Body = sdp.Offer("5", strayMedia).Bytes()
	c.p.OnACK(stray)
	if got := c.relay.remote("1", agw.Core); got == strayMedia {
		t.Errorf("the core termination sends to %v of the ACK of the far end, want it left as the offer of the 2xx said", got)
	}

	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	c.ackWithAnswer("2", "4", ueMoved)
	ack := nextRequest(t, "the far end", c.farGot)
	if got := audio(t, ack.Body); got != relayCore {
		t.Errorf("the answer in the ACK names %v at the far end, want the core termination %v", got, relayCore)
	}
	if got := c.relay.remote("1", agw.Access); got != ueMoved {
		t.Errorf("the access termination sends to %v, want the address of the answer %v", got, ueMoved)
	}
	requests := pcrf.mediaRequests()
	if len(requests) != before+1 {
		t.Fatalf("the PCRF got %d service information for the re-INVITE, want one for the answer of the ACK", len(requests)-before)
	}
	if got := audio(t, requests[len(requests)-1].SDP.Answer); got != ueMoved {
		t.Errorf("the answer the PCRF authorises names %v, want the UE's new address %v", got, ueMoved)
	}
}

// TestRefusedOfferInThe2xxReleasesTheDialog: the relay does not take the
// offer that the 2xx of a re-INVITE without one carries, so the P-CSCF
// has no SDP with the relay's address to pass on (TS 24.229 6.7.2.1).
// The 2xx is the final response of the request and goes on as it came,
// and the P-CSCF releases the dialog (TS 24.229 5.2.8.1.2) once the ACK
// of that 2xx has passed, so that no BYE overtakes it (RFC 3261 15). The
// answer of that ACK reaches neither the relay nor the PCRF.
func TestRefusedOfferInThe2xxReleasesTheDialog(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	authorised := len(pcrf.mediaRequests())

	ct, st := c.reinviteWithoutOffer(t, "2")
	c.relay.refuseModify(agw.Core)
	c.offerIn2xx(t, ct, st, "3", netip.MustParseAddrPort("192.0.2.99:6008"))
	noRequest(t, "the far end before the ACK", c.farGot)

	c.ackWithAnswer("2", "4", netip.MustParseAddrPort("192.0.2.10:4006"))
	wantACKAndBYE(t, c.farGot)
	if m := nextRequest(t, "the UE", c.ueGot); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want the BYE of the release", m.Method)
	}
	if n := len(pcrf.mediaRequests()); n != authorised {
		t.Errorf("the PCRF got %d service information after the refused offer, want none", n-authorised)
	}
}

// TestRefusedOfferInThe2xxAfterAPreviewReleasesTheDialog: the far end
// shows its offer in a 183 that is not reliable, which the relay takes,
// and then the relay does not take the offer of the 2xx. Only the 2xx
// carries the offer (RFC 3261 13.2.1), so the P-CSCF has no offer with
// the relay's address to pass on, and releases the dialog as for any
// offer of a 2xx that the relay did not take (TS 24.229 5.2.8.1).
func TestRefusedOfferInThe2xxAfterAPreviewReleasesTheDialog(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	authorised := len(pcrf.mediaRequests())

	ct, st := c.reinviteWithoutOffer(t, "2")
	farMoved := netip.MustParseAddrPort("192.0.2.99:6008")
	c.offerIn183(t, ct, st, "3", farMoved)

	c.relay.refuseModify(agw.Core)
	c.offerIn2xx(t, ct, st, "3", farMoved)
	noRequest(t, "the far end before the ACK", c.farGot)

	c.ackWithAnswer("2", "4", netip.MustParseAddrPort("192.0.2.10:4006"))
	wantACKAndBYE(t, c.farGot)
	if m := nextRequest(t, "the UE", c.ueGot); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want the BYE of the release", m.Method)
	}
	if n := len(pcrf.mediaRequests()); n != authorised {
		t.Errorf("the PCRF got %d service information after the refused offer, want none", n-authorised)
	}
}

// TestFailedReINVITEWithoutAnOfferAfterAPreviewGoesBack: the far end
// shows its offer in a 183 that is not reliable, which moves the core
// termination, and then turns the re-INVITE down with 488. The 183
// carried no offer (RFC 3261 13.2.1), so the session is as it was
// before the re-INVITE (14.1), and the core termination sends where the
// INVITE and its answer said again. The call goes on.
func TestFailedReINVITEWithoutAnOfferAfterAPreviewGoesBack(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	core := c.relay.remote("1", agw.Core)
	authorised := len(pcrf.mediaRequests())

	ct, st := c.reinviteWithoutOffer(t, "2")
	preview := netip.MustParseAddrPort("192.0.2.99:6008")
	c.offerIn183(t, ct, st, "3", preview)
	if got := c.relay.remote("1", agw.Core); got != preview {
		t.Fatalf("the core termination sends to %v for the 183, want %v", got, preview)
	}
	st.Respond(sip.NewResponse(st.Request, 488, ""))
	if r := final(t, ct.Responses(), 3*time.Second); r.StatusCode != 488 {
		t.Fatalf("the re-INVITE got %d, want the 488 of the far end", r.StatusCode)
	}
	if got := c.relay.remote("1", agw.Core); got != core {
		t.Errorf("the core termination sends to %v after the 488, want %v of the INVITE's answer again", got, core)
	}
	noRequest(t, "the far end", c.farGot)
	noRequest(t, "the UE", c.ueGot)
	if n := len(pcrf.mediaRequests()); n != authorised {
		t.Errorf("the PCRF got %d service information for the failed re-INVITE, want none", n-authorised)
	}
}

// TestFailedReINVITEWithoutAnOfferEndsTheCallWhenTheRelayDoesNotGoBack:
// as above, but the relay does not take the core termination back
// after the 488. It keeps sending where the far end no longer takes the
// media, so the P-CSCF releases the dialog (TS 24.229 5.2.8.1.2): the
// 488 goes on, and each party then gets a BYE.
func TestFailedReINVITEWithoutAnOfferEndsTheCallWhenTheRelayDoesNotGoBack(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	core := c.relay.remote("1", agw.Core)

	ct, st := c.reinviteWithoutOffer(t, "2")
	c.offerIn183(t, ct, st, "3", netip.MustParseAddrPort("192.0.2.99:6008"))
	c.relay.refuseRemote(core)
	st.Respond(sip.NewResponse(st.Request, 488, ""))
	if r := final(t, ct.Responses(), 3*time.Second); r.StatusCode != 488 {
		t.Fatalf("the re-INVITE got %d, want the 488 of the far end", r.StatusCode)
	}
	if m := nextRequest(t, "the far end", c.farGot); m.Method != sip.BYE {
		t.Errorf("the far end got %s, want the BYE of the release", m.Method)
	}
	if m := nextRequest(t, "the UE", c.ueGot); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want the BYE of the release", m.Method)
	}
}

// TestRefusedAnswerInTheACKReleasesTheDialog: the relay takes the offer
// of the 2xx but not the answer of the ACK. The P-CSCF has no answer
// with the relay's address to pass on (TS 24.229 6.7.2.1), and an ACK
// takes no response, so the ACK goes no further and the
// P-CSCF releases the dialog (TS 24.229 5.2.8.1.2).
func TestRefusedAnswerInTheACKReleasesTheDialog(t *testing.T) {
	pcrf := &fakePolicy{}
	c := newRawINVITEWith(t, pcrf)
	c.upCall(t)
	authorised := len(pcrf.mediaRequests())

	ct, st := c.reinviteWithoutOffer(t, "2")
	c.offerIn2xx(t, ct, st, "3", netip.MustParseAddrPort("192.0.2.99:6008"))
	c.relay.refuseModify(agw.Access)
	c.ackWithAnswer("2", "4", netip.MustParseAddrPort("192.0.2.10:4006"))

	if m := nextRequest(t, "the far end", c.farGot); m.Method != sip.BYE {
		t.Fatalf("the far end got %s, want the BYE of the release and no ACK", m.Method)
	}
	if m := nextRequest(t, "the UE", c.ueGot); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want the BYE of the release", m.Method)
	}
	if n := len(pcrf.mediaRequests()); n != authorised {
		t.Errorf("the PCRF got %d service information for the answer that the relay did not take, want none", n-authorised)
	}
}

// offerIn2xxOfTheUE has the UE of the call in answer a re-INVITE
// without an offer with its own offer at media (RFC 3261 14.1), and
// returns the channel that waitACK reads: the ACK with the answer, or
// what kept the UE from answering.
func offerIn2xxOfTheUE(in *sip.IncomingCall, media netip.AddrPort) <-chan error {
	acked := make(chan error, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		if len(r.Request.Body) != 0 {
			acked <- fmt.Errorf("the UE got a re-INVITE with %d bytes of body, want one without an offer", len(r.Request.Body))
			return
		}
		_, err := r.Accept(sdpBody(sdp.Offer("3", media)))
		acked <- err
	})
	return acked
}

// fromFarEnd is a request of the far end of c within the dialog of its
// INVITE, with the method and the CSeq number cseq.
func (c *rawINVITE) fromFarEnd(method sip.Method, cseq string) *sip.Message {
	m := &sip.Message{Method: method, RequestURI: c.ueContact}
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", c.st.Request.Get("To")+";tag=callee")
	m.Add("To", c.st.Request.Get("From"))
	m.Add("Call-ID", c.st.Request.Get("Call-ID"))
	m.Add("CSeq", cseq+" "+string(method))
	return m
}

// TestOfferOfTheNextUPDATEThatFailsGoesBackToTheOfferOfThe2xx: the offer
// of the 2xx and the answer of the ACK are the last offer and answer
// that are done, so when the next offer gets a final response other than
// 2xx, the termination that the offer moved goes back where they said
// (RFC 3261 14.1, RFC 3311 5.3).
func TestOfferOfTheNextUPDATEThatFailsGoesBackToTheOfferOfThe2xx(t *testing.T) {
	c := newRawINVITE(t)
	c.upCall(t)
	ct, st := c.reinviteWithoutOffer(t, "2")
	farMoved := netip.MustParseAddrPort("192.0.2.99:6008")
	c.offerIn2xx(t, ct, st, "3", farMoved)
	c.ackWithAnswer("2", "4", netip.MustParseAddrPort("192.0.2.10:4006"))
	nextRequest(t, "the far end", c.farGot)

	// The far end offers again in an UPDATE, and the UE turns it down.
	c.ueAnswers.Store(488)
	update := c.fromFarEnd(sip.UPDATE, "1")
	update.Add("Content-Type", "application/sdp")
	update.Body = sdp.Offer("5", netip.MustParseAddrPort("192.0.2.99:6010")).Bytes()
	if r := c.through(t, c.far, update); r.StatusCode != 488 {
		t.Fatalf("the UPDATE got %d, want the 488 of the UE", r.StatusCode)
	}
	if got := c.relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v after the 488, want %v of the offer of the 2xx", got, farMoved)
	}
}

// TestOfferInThe2xxOfTheUEIsRelayedAndAuthorised: the far end asks the
// UE for an offer with a re-INVITE without one, so the UE offers in its
// 2xx and the far end answers in the ACK (RFC 3261 14.1). The offer of
// the UE moves the access termination, which is asked to latch onto the
// new source of the UE's media, and the PCRF gets the UE's offer with
// the answer that the UE receives (TS 29.214 A.1).
func TestOfferInThe2xxOfTheUEIsRelayedAndAuthorised(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	out, in := h.callUE()
	acked := accept(in, answerTo(t, in.Request, ueMedia))
	answered(t, out, 200)
	waitACK(t, acked)

	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	offered := offerIn2xxOfTheUE(in, ueMoved)
	resp, err := out.ReInvite(sip.Body{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the re-INVITE without an offer got %v, %v; want 200 with the offer of the UE", resp, err)
	}
	if got := audio(t, resp.Body); got != relayCore {
		t.Errorf("the offer in the 2xx names %v at the far end, want the core termination %v", got, relayCore)
	}
	if got := relay.remote("1", agw.Access); got != ueMoved {
		t.Errorf("the access termination sends to %v, want the UE's new address %v", got, ueMoved)
	}
	wantLatchings(t, relay, agw.Access, "", agw.NAPTRelatch)

	farMoved := netip.MustParseAddrPort("192.0.2.99:6008")
	if err := out.Ack(sdpBody(sdp.Offer("4", farMoved))); err != nil {
		t.Fatal(err)
	}
	waitACK(t, offered)
	ack := h.ueGot.wait(t, "the ACK with the answer at the UE", func(m *sip.Message) bool {
		return m.Method == sip.ACK && len(m.Body) > 0
	})
	if got := audio(t, ack.Body); got != relayAccess {
		t.Errorf("the answer in the ACK names %v at the UE, want the access termination %v", got, relayAccess)
	}
	if got := relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v, want the address of the answer %v", got, farMoved)
	}

	requests := waitForAuthorisations(t, pcrf, 2)
	r := requests[len(requests)-1]
	if r.Session != "session-1" || !r.SDP.UEOffered {
		t.Errorf("request after the ACK = %+v; want session-1 with the offer of the UE", r)
	}
	if got := audio(t, r.SDP.Offer); got != ueMoved {
		t.Errorf("the offer the PCRF authorises names %v, want the UE's new address %v", got, ueMoved)
	}
	if got := audio(t, r.SDP.Answer); got != relayAccess {
		t.Errorf("the answer the PCRF authorises names %v, want the access termination %v", got, relayAccess)
	}
}

// TestAnswerInTheACKWithoutAMediaRelayIsAuthorised: a P-CSCF that has no
// media relay sends the PCRF the service information of the offer in the
// 2xx and the answer in the ACK all the same (TS 29.214 A.1).
func TestAnswerInTheACKWithoutAMediaRelayIsAuthorised(t *testing.T) {
	pcrf := &fakePolicy{}
	h := newCallHarness(t, harnessConfig{
		association: func(a *Association) { a.SignallingSession = "signalling" },
		configure:   func(p *PCSCF) { p.SetPolicyControl(pcrf) },
	})
	out, in := h.callUE()
	acked := accept(in, answerTo(t, in.Request, ueMedia))
	answered(t, out, 200)
	waitACK(t, acked)

	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	offered := offerIn2xxOfTheUE(in, ueMoved)
	resp, err := out.ReInvite(sip.Body{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the re-INVITE without an offer got %v, %v; want 200 with the offer of the UE", resp, err)
	}
	farMoved := netip.MustParseAddrPort("192.0.2.99:6008")
	if err := out.Ack(sdpBody(sdp.Offer("4", farMoved))); err != nil {
		t.Fatal(err)
	}
	waitACK(t, offered)

	requests := waitForAuthorisations(t, pcrf, 2)
	r := requests[len(requests)-1]
	if got := audio(t, r.SDP.Offer); got != ueMoved {
		t.Errorf("the offer the PCRF authorises names %v, want the UE's own address %v", got, ueMoved)
	}
	if got := audio(t, r.SDP.Answer); got != farMoved {
		t.Errorf("the answer the PCRF authorises names %v, want the address of the far end %v", got, farMoved)
	}
}

// waitForAuthorisations waits until the PCRF has taken want requests to
// authorise media. The one of the answer in an ACK comes once the P-CSCF
// has taken that ACK, which is after the UA that sent it has returned.
func waitForAuthorisations(t *testing.T, pcrf *fakePolicy, want int) []policy.MediaRequest {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		requests := pcrf.mediaRequests()
		if len(requests) >= want {
			return requests
		}
		if time.Now().After(deadline) {
			t.Fatalf("the PCRF got %d service information, want %d", len(requests), want)
		}
	}
}

// TestACKWithoutAnAnswerAfterAnOfferInAReliable183: the offer may also
// come in a reliable provisional response, with the answer in the PRACK
// (RFC 3262 5). That offer goes through the relay too, and the 2xx that
// repeats it leaves the ACK without an answer, which changes nothing.
func TestACKWithoutAnAnswerAfterAnOfferInAReliable183(t *testing.T) {
	c := newRawINVITE(t)
	c.upCall(t)

	ct, st := c.reinviteWithoutOffer(t, "2")
	farMoved := netip.MustParseAddrPort("192.0.2.99:6008")
	progress := sip.NewResponse(st.Request, 183, "")
	progress.Add("Contact", "<"+c.farContact+">")
	progress.Add("Require", sip.Option100rel)
	progress.Add("RSeq", "1")
	progress.Add("Content-Type", "application/sdp")
	progress.Body = sdp.Offer("3", farMoved).Bytes()
	st.Respond(progress)
	r := next(t, ct.Responses())
	for r.StatusCode == 100 {
		r = next(t, ct.Responses())
	}
	if r.StatusCode != 183 {
		t.Fatalf("the UE got %d, want the reliable 183 with the offer", r.StatusCode)
	}
	if got := audio(t, r.Body); got != relayAccess {
		t.Errorf("the offer in the reliable 183 names %v at the UE, want the access termination %v", got, relayAccess)
	}
	if got := c.relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v, want the address of the offer %v", got, farMoved)
	}

	// The PRACK carries the answer, which goes through the relay as the
	// SDP of any PRACK does.
	ueMoved := netip.MustParseAddrPort("192.0.2.10:4006")
	prack := c.inDialog(sip.PRACK, "3")
	prack.Add("RAck", "1 2 INVITE")
	prack.Add("Content-Type", "application/sdp")
	prack.Body = sdp.Offer("4", ueMoved).Bytes()
	pct, err := c.ue.SendRequest(prack, sip.Destination{Transport: sip.UDP, Addr: c.pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	if got := final(t, pct.Responses(), 3*time.Second); got.StatusCode != 200 {
		t.Fatalf("the PRACK got %d, want the 200 of the far end", got.StatusCode)
	}
	nextRequest(t, "the far end", c.farGot)
	if got := c.relay.remote("1", agw.Access); got != ueMoved {
		t.Errorf("the access termination sends to %v, want the address of the answer %v", got, ueMoved)
	}

	// The 2xx repeats the offer, and the ACK carries no answer.
	c.offerIn2xx(t, ct, st, "3", farMoved)
	c.ack("2")
	ack := nextRequest(t, "the far end", c.farGot)
	if ack.Method != sip.ACK || len(ack.Body) != 0 {
		t.Fatalf("the far end got %s with %d bytes of body, want an ACK without an answer", ack.Method, len(ack.Body))
	}
	if got := c.relay.remote("1", agw.Access); got != ueMoved {
		t.Errorf("the access termination sends to %v after the ACK, want the address of the answer %v", got, ueMoved)
	}
	noRequest(t, "the far end after the ACK", c.farGot)
}
