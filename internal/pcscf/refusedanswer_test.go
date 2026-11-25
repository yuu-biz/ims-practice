package pcscf

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have a media relay that does not take the SDP
// answer of a response, as one that restarts while the call is set up.
// The P-CSCF then has no answer that names the relay to pass on, and
// ends the call.

// callLegs are the two ways a call passes the P-CSCF. The answer of a
// call from the UE comes from the network and tells the core
// termination where to send; the answer of a call to the UE tells the
// access termination.
var callLegs = []struct {
	name string
	call func(*callHarness) (*sip.OutgoingCall, *sip.IncomingCall)
	// answered is the termination that the answer is for.
	answered agw.TerminationID
	// media is where the callee takes its media.
	media netip.AddrPort
}{
	{"from the UE", (*callHarness).call, agw.Core, coreMedia},
	{"to the UE", (*callHarness).callUE, agw.Access, ueMedia},
}

// wantNoFailureCause checks the 500 with which the P-CSCF ends a call
// whose answer the relay did not take. FAILURE_CAUSE tells of radio or
// bearer resources that are gone (TS 24.229 7.2A.18.12.2), and those
// are still there.
func wantNoFailureCause(t *testing.T, resp *sip.Message) {
	t.Helper()
	if got := resp.Get("Reason"); got != "" {
		t.Errorf("Reason of the 500 = %q, want none", got)
	}
}

// TestRefusedAnswerOfA183EndsTheCall: the relay does not take the answer
// of the 183. The P-CSCF ends the call being set up as TS 24.229
// 5.2.8.1.1 does: the caller gets 500, and the callee a CANCEL with the
// Reason of 503. The PCRF never gets the answer, and the relay context
// goes.
func TestRefusedAnswerOfA183EndsTheCall(t *testing.T) {
	for _, leg := range callLegs {
		t.Run(leg.name, func(t *testing.T) {
			h, pcrf, relay := newPolicyHarness(t)
			relay.refuseModify(leg.answered)
			out, in := leg.call(h)
			// The 183 is reliable, and Progress waits for a PRACK that the
			// caller never sends: the CANCEL ends the call first.
			go in.Progress(183, answerTo(t, in.Request, leg.media))

			wantNoFailureCause(t, answered(t, out, 500))
			ended(t, "the callee's call", in.Done())
			if in.Err() != sip.ErrCancelled {
				t.Fatalf("the callee's call ended with %v, want a CANCEL", in.Err())
			}
			wantCancelReason(t, in.EndedBy())
			relay.waitHeld(t, 0, time.Second)
			if n := len(pcrf.mediaRequests()); n != 0 {
				t.Errorf("%d requests to authorise media, want none for an answer that the relay did not take", n)
			}
		})
	}
}

// TestRefusedAnswerOfThe200EndsTheCall: the answer comes with the 200,
// and the relay does not take it. The callee has answered, but its 200
// would lead the caller past the relay, so the caller gets 500 in its
// place, as for an INVITE that the P-CSCF gives up (TS 24.229
// 5.2.8.1.1). The P-CSCF acknowledges the 200 in the caller's name (RFC
// 3261 13.2.2.4) and ends the callee's session with a BYE (RFC 3261 15),
// and the relay context goes at once.
func TestRefusedAnswerOfThe200EndsTheCall(t *testing.T) {
	for _, leg := range callLegs {
		t.Run(leg.name, func(t *testing.T) {
			h, pcrf, relay := newPolicyHarness(t)
			out, in := leg.call(h)
			relay.refuseModify(leg.answered)
			acked := accept(in, answerTo(t, in.Request, leg.media))

			wantNoFailureCause(t, answered(t, out, 500))
			waitACK(t, acked)
			ended(t, "the callee's call", in.Done())
			bye := in.EndedBy()
			if bye == nil || bye.Method != sip.BYE {
				t.Fatalf("the callee's call ended by %v, want a BYE", bye)
			}
			if got := bye.Get("Reason"); got != reasonServiceUnavailable {
				t.Errorf("Reason of the BYE = %q, want %q", got, reasonServiceUnavailable)
			}
			relay.waitHeld(t, 0, time.Second)
			if n := len(pcrf.mediaRequests()); n != 0 {
				t.Errorf("%d requests to authorise media, want none for an answer that the relay did not take", n)
			}
		})
	}
}

// rawINVITE is an INVITE from a registered UE through the P-CSCF to a
// far end whose responses a test sends by hand, retransmissions
// included.
type rawINVITE struct {
	p     *PCSCF
	store *MemoryStore
	relay *fakeRelay
	// ct is the INVITE as the UE sent it, and st as the far end got it.
	ct *sip.ClientTransaction
	st *sip.ServerTransaction
	// farGot are the ACKs and the other requests that reach the far end,
	// and ueGot the requests that reach the UE.
	farGot, ueGot chan *sip.Message
	farContact    string
}

func newRawINVITE(t *testing.T) *rawINVITE {
	t.Helper()
	c := &rawINVITE{relay: newFakeRelay(), store: NewMemoryStore(),
		farGot: make(chan *sip.Message, 4), ueGot: make(chan *sip.Message, 4)}
	invites := make(chan *sip.ServerTransaction, 1)
	far := newStackConfig(t, sip.Config{Timers: sip.DefaultTimers(),
		OnACK: func(ack *sip.Message) { c.farGot <- ack },
		OnRequest: func(st *sip.ServerTransaction) {
			if st.Request.Method == sip.INVITE {
				invites <- st
				return
			}
			c.farGot <- st.Request
		}})
	c.farContact = "sip:callee@" + far.LocalAddr().String()
	ue := newStack(t, func(st *sip.ServerTransaction) { c.ueGot <- st.Request })
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: far.LocalAddr()}}
	})
	c.p = newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, c.store, only(c.relay), quiet())
	ueContact := "sip:001010000000001@" + ue.LocalAddr().String()
	c.store.PutAssociation(context.Background(), Association{
		Source: sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()}, SentBy: ue.LocalAddr().String(), Contact: ueContact,
		PrivateIdentity: impi, PublicIdentities: []string{impu}, ServiceRoute: []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires: time.Now().Add(time.Hour),
	})
	pcscf := newPCSCFStack(t, c.p)
	c.p.SetStack(pcscf)

	inv := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	inv.Set("Contact", "<"+ueContact+">")
	var err error
	if c.ct, err = ue.SendRequest(inv, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()}); err != nil {
		t.Fatal(err)
	}
	select {
	case c.st = <-invites:
	case <-time.After(3 * time.Second):
		t.Fatal("the callee got no INVITE")
	}
	return c
}

// response is a response of the far end to the INVITE, with the answer
// of a party whose media is at coreMedia.
func (c *rawINVITE) response(code int) *sip.Message {
	resp := sip.NewResponse(c.st.Request, code, "callee")
	for _, r := range c.st.Request.GetAll("Record-Route") {
		resp.Add("Record-Route", r)
	}
	resp.Add("Contact", "<"+c.farContact+">")
	resp.Add("Content-Type", "application/sdp")
	resp.Body = sdp.Offer("2", coreMedia).Bytes()
	return resp
}

// TestRefusedRetransmissionOfThe200KeepsTheCall: the relay took the
// answer of the 200, and the caller has it. A retransmission of that 200
// (RFC 6026 8.1) that the relay does not take changes nothing: it goes
// on to the caller, nobody gets an ACK or a BYE from the P-CSCF, and the
// call keeps its relay context.
func TestRefusedRetransmissionOfThe200KeepsTheCall(t *testing.T) {
	c := newRawINVITE(t)
	ok := c.response(200)
	c.st.Respond(ok)
	if r := final(t, c.ct.Responses(), 3*time.Second); r.StatusCode != 200 || audio(t, r.Body) != relayAccess {
		t.Fatalf("the caller got %d naming %v, want the 200 naming the relay", r.StatusCode, audio(t, r.Body))
	}

	c.relay.refuseModify(agw.Core)
	c.st.Respond(ok)
	select {
	case r := <-c.ct.Responses():
		if r.StatusCode != 200 {
			t.Errorf("the caller got %d after its 200, want the retransmission of the 200", r.StatusCode)
		}
	case <-time.After(3 * time.Second):
		t.Error("the retransmission of the 200 did not reach the caller")
	}
	noRequest(t, "the callee", c.farGot)
	noRequest(t, "the caller", c.ueGot)
	c.relay.waitHeld(t, 1, 0)
}

// answerOffers has the far end answer each offer within the dialog of
// in, as a UA does, with its media at coreMedia.
func answerOffers(in *sip.IncomingCall) {
	in.Handle(func(r *sip.InDialogRequest) {
		var body sip.Body
		if r.Offer {
			body = sdpBody(sdp.Offer("3", coreMedia))
		}
		r.Accept(body)
	})
}

// endedByBYE checks that a call has ended with a BYE of the P-CSCF.
func endedByBYE(t *testing.T, what string, call interface {
	Done() <-chan struct{}
	EndedBy() *sip.Message
}) {
	t.Helper()
	ended(t, what, call.Done())
	bye := call.EndedBy()
	if bye == nil || bye.Method != sip.BYE || bye.Get("Reason") != reasonServiceUnavailable {
		t.Errorf("%s ended by %v, want a BYE with Reason %q", what, bye, reasonServiceUnavailable)
	}
}

// TestRefusedAnswerWithinTheDialogReleasesTheCall: the relay does not
// take the answer in the 200 to an UPDATE, or to a re-INVITE such as the
// one of a hold, of a call that is up. The 200 goes on as it came; the
// P-CSCF then releases the dialog with a BYE to each party (TS 24.229
// 5.2.8.1.2), much as TS 24.229 6.2 has it release after a 200 whose
// SDP offer the local policy does not allow, but with the Reason 503 of
// lost resources rather than 488. The PCRF never gets that answer, and
// the policy session and the relay context go.
func TestRefusedAnswerWithinTheDialogReleasesTheCall(t *testing.T) {
	for _, offer := range []struct {
		request string
		send    func(*sip.OutgoingCall, sip.Body, ...sip.HeaderField) (*sip.Message, error)
	}{
		{"UPDATE", (*sip.OutgoingCall).Update},
		{"re-INVITE", (*sip.OutgoingCall).ReInvite},
	} {
		t.Run(offer.request, func(t *testing.T) {
			h, pcrf, relay := newPolicyHarness(t)
			out, in := h.call()
			answerOffers(in)
			acked := accept(in, answerTo(t, in.Request, coreMedia))
			answered(t, out, 200)
			waitACK(t, acked)

			relay.refuseModify(agw.Core)
			resp, err := offer.send(out, sdpBody(sdp.Offer("2", netip.MustParseAddrPort("192.0.2.10:4002"))))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("the %s got %d, want the 200 of the far end", offer.request, resp.StatusCode)
			}
			endedByBYE(t, "the UE's call", out)
			endedByBYE(t, "the far end's call", in)
			pcrf.waitReleased(t, "session-1")
			relay.waitHeld(t, 0, time.Second)
			if n := len(pcrf.mediaRequests()); n != 1 {
				t.Errorf("%d requests to authorise media, want only the one of the INVITE", n)
			}
		})
	}
}

// TestRefusedAnswerOfAnUPDATEInTheEarlyDialogEndsTheCall: the UPDATE
// comes before the 200 to the INVITE, as when a VoLTE UE tells that its
// resources are reserved, and the relay does not take its answer. The
// 200 to the UPDATE goes on, and the P-CSCF ends the call being set up
// as for a 183 whose answer the relay did not take (TS 24.229
// 5.2.8.1.1): the caller gets 500, without FAILURE_CAUSE, and the callee
// a CANCEL with the Reason of 503.
func TestRefusedAnswerOfAnUPDATEInTheEarlyDialogEndsTheCall(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	out, in := h.call()
	answerOffers(in)
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}

	relay.refuseModify(agw.Core)
	resp, err := out.Update(sdpBody(sdp.Offer("2", netip.MustParseAddrPort("192.0.2.10:4002"))))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("the UPDATE got %d, want the 200 of the far end", resp.StatusCode)
	}
	wantNoFailureCause(t, answered(t, out, 500))
	ended(t, "the callee's call", in.Done())
	if in.Err() != sip.ErrCancelled {
		t.Fatalf("the callee's call ended with %v, want a CANCEL", in.Err())
	}
	wantCancelReason(t, in.EndedBy())
	pcrf.waitReleased(t, "session-1")
	relay.waitHeld(t, 0, time.Second)
	if n := len(pcrf.mediaRequests()); n != 1 {
		t.Errorf("%d requests to authorise media, want only the one of the 183", n)
	}
}

// TestNonSDPBodyOfAResponseGoesOn: a 180 whose body is not SDP, such as
// one that a service puts there, carries no answer for the relay. It
// goes on to the caller as it came, and the call goes on to its 200.
func TestNonSDPBodyOfAResponseGoesOn(t *testing.T) {
	c := newRawINVITE(t)
	ringing := c.response(180)
	ringing.Set("Content-Type", "text/plain")
	ringing.Body = []byte("ringing")
	c.st.Respond(ringing)
	r := next(t, c.ct.Responses())
	for r.StatusCode == 100 {
		r = next(t, c.ct.Responses())
	}
	if r.StatusCode != 180 || string(r.Body) != "ringing" {
		t.Fatalf("the caller got %d with %q, want the 180 as it came", r.StatusCode, r.Body)
	}

	c.st.Respond(c.response(200))
	if r := final(t, c.ct.Responses(), 3*time.Second); r.StatusCode != 200 || audio(t, r.Body) != relayAccess {
		t.Fatalf("the caller got %d, want the 200 naming the relay", r.StatusCode)
	}
	select {
	case <-c.st.Cancelled():
		t.Error("the callee got a CANCEL")
	default:
	}
}

// TestRefusedRetransmissionOfAReliable183KeepsTheCall: the relay took
// the answer of a reliable 183, which the callee sends again until its
// PRACK comes (RFC 3262 3). A retransmission that the relay does not
// take changes nothing, as for a retransmitted 200: it goes on to the
// caller, the INVITE is neither answered nor cancelled, and the call
// keeps its relay context.
func TestRefusedRetransmissionOfAReliable183KeepsTheCall(t *testing.T) {
	c := newRawINVITE(t)
	progress := c.response(183)
	progress.Add("Require", sip.Option100rel)
	progress.Add("RSeq", "1")
	c.st.Respond(progress)
	r := next(t, c.ct.Responses())
	for r.StatusCode == 100 {
		r = next(t, c.ct.Responses())
	}
	if r.StatusCode != 183 || audio(t, r.Body) != relayAccess {
		t.Fatalf("the caller got %d, want the 183 naming the relay", r.StatusCode)
	}

	c.relay.refuseModify(agw.Core)
	c.st.Respond(progress)
	deadline := time.After(watch)
	for done := false; !done; {
		select {
		case r := <-c.ct.Responses():
			if r.StatusCode >= 200 {
				t.Fatalf("the caller got %d after the retransmitted 183, want the INVITE to go on", r.StatusCode)
			}
		case <-c.st.Cancelled():
			t.Fatal("the callee got a CANCEL for the retransmitted 183")
		case <-deadline:
			done = true
		}
	}
	c.relay.waitHeld(t, 1, 0)
}
