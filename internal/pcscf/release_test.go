package pcscf

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

func nextRequest(t *testing.T, what string, requests <-chan *sip.Message) *sip.Message {
	t.Helper()
	select {
	case m := <-requests:
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("%s got no request", what)
		return nil
	}
}

func noRequest(t *testing.T, what string, requests <-chan *sip.Message) {
	t.Helper()
	select {
	case m := <-requests:
		t.Errorf("%s got a %s", what, m.Method)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestReleasedBearerEndsTheCallWithBYEs is TS 24.229 5.2.8.1.2: when
// the PCRF reports that the bearer of a confirmed session is gone, the
// P-CSCF sends a BYE made of what it saved of the dialog. The event may
// come again, and then changes nothing.
func TestReleasedBearerEndsTheCallWithBYEs(t *testing.T) {
	c := newRawCall(t)
	// The UE has sent a second request, so its CSeq stands at 2.
	c.send(c.ue, update(c.invite, false, c.farContact, "2", ueMedia))
	<-c.toFar

	event := policy.Event{Session: "session-1", Kind: policy.ReleaseOfBearer}
	c.p.PolicyEvent(event)

	caller, callee := c.invite.Get("From"), c.invite.Get("To")+";tag=callee"
	toFar := nextRequest(t, "the other party", c.toFar)
	want := map[string]string{"From": caller, "To": callee, "Call-ID": c.invite.Get("Call-ID"), "CSeq": "3 BYE",
		"Reason": `SIP;cause=503;text="Service Unavailable"`}
	if toFar.Method != sip.BYE || toFar.RequestURI != c.farContact {
		t.Errorf("the other party got %s %s, want a BYE to its Contact %s", toFar.Method, toFar.RequestURI, c.farContact)
	}
	for name, value := range want {
		if got := toFar.Get(name); got != value {
			t.Errorf("BYE to the other party: %s = %q, want %q", name, got, value)
		}
	}
	// The BYE to the UE speaks for the callee, who has sent no request
	// yet.
	toUE := nextRequest(t, "the UE", c.toUE)
	want["From"], want["To"], want["CSeq"] = callee, caller, "1 BYE"
	if toUE.Method != sip.BYE || toUE.RequestURI != c.ueContact {
		t.Errorf("the UE got %s %s, want a BYE to its Contact %s", toUE.Method, toUE.RequestURI, c.ueContact)
	}
	for name, value := range want {
		if got := toUE.Get(name); got != value {
			t.Errorf("BYE to the UE: %s = %q, want %q", name, got, value)
		}
	}
	// The session is still open at the PCRF after such an event, so the
	// P-CSCF releases it (TS 29.214 4.4.4).
	c.pcrf.waitReleased(t, "session-1")
	c.relay.waitHeld(t, 0, time.Second)

	// The dialog has gone, so the same event again releases nothing.
	c.p.PolicyEvent(event)
	noRequest(t, "the other party", c.toFar)
	noRequest(t, "the UE", c.toUE)
	c.pcrf.waitReleased(t, "session-1")
}

// TestTerminatedPolicySessionEndsTheCallWithoutARelease is TS 29.214
// 4.4.6.1: a session that the PCRF aborted is over, so the P-CSCF ends
// the call and sends the PCRF nothing more.
func TestTerminatedPolicySessionEndsTheCallWithoutARelease(t *testing.T) {
	c := newRawCall(t)
	c.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.SessionTerminated})
	if m := nextRequest(t, "the other party", c.toFar); m.Method != sip.BYE {
		t.Errorf("the other party got %s, want BYE", m.Method)
	}
	if m := nextRequest(t, "the UE", c.toUE); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want BYE", m.Method)
	}
	c.relay.waitHeld(t, 0, time.Second)
	c.pcrf.waitReleased(t)
}

// TestBYEOfTheUEWinsOverALateEvent: an event that comes after the UE
// has hung up finds no dialog, and no second BYE goes out.
func TestBYEOfTheUEWinsOverALateEvent(t *testing.T) {
	c := newRawCall(t)
	bye := byeRequest(c.invite.Get("Call-ID"), 2)
	bye.Set("From", c.invite.Get("From"))
	bye.Set("To", c.invite.Get("To")+";tag=callee")
	bye.RequestURI = c.farContact
	c.send(c.ue, bye)
	if m := nextRequest(t, "the other party", c.toFar); m.Method != sip.BYE || m.Get("Reason") != "" {
		t.Fatalf("the other party got %s with Reason %q, want the UE's own BYE", m.Method, m.Get("Reason"))
	}
	c.pcrf.waitReleased(t, "session-1")

	c.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.SessionTerminated})
	noRequest(t, "the other party", c.toFar)
	noRequest(t, "the UE", c.toUE)
}

// TestLostBearerCancelsACallBeingSetUp is TS 24.229 5.2.8.1.1 at the
// P-CSCF of the caller: the INVITE is answered 500 and cancelled
// towards the callee.
func TestLostBearerCancelsACallBeingSetUp(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	out, in := h.call()
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	h.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.LossOfBearer})

	resp := answered(t, out, 500)
	if got, want := resp.Get("Reason"), `FAILURE_CAUSE;cause=1;text="Media bearer or QoS lost"`; got != want {
		t.Errorf("Reason = %q, want %q", got, want)
	}
	ended(t, "the callee's call", in.Done())
	if in.Err() != sip.ErrCancelled {
		t.Fatalf("the callee's call ended with %v, want a CANCEL", in.Err())
	}
	wantCancelReason(t, in.EndedBy())
	pcrf.waitReleased(t, "session-1")
	relay.waitHeld(t, 0, time.Second)
}

// wantCancelReason checks the Reason of TS 24.229 5.2.8.1.1 in the
// CANCEL of the P-CSCF: 503, as the access network gave no cause.
func wantCancelReason(t *testing.T, cancel *sip.Message) {
	t.Helper()
	if got, want := cancel.Get("Reason"), `SIP;cause=503;text="Service Unavailable"`; got != want {
		t.Errorf("Reason of the CANCEL = %q, want %q", got, want)
	}
}

// TestLostBearerCancelsACallBeingSetUpToTheUE is the same at the P-CSCF
// of the callee: the 500 goes to the network, and the CANCEL to the UE.
func TestLostBearerCancelsACallBeingSetUpToTheUE(t *testing.T) {
	h, pcrf, _ := newPolicyHarness(t)
	out, in := h.callUE()
	if err := in.Progress(183, answerTo(t, in.Request, ueMedia)); err != nil {
		t.Fatal(err)
	}
	h.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.FailedResourcesAllocation})

	resp := answered(t, out, 500)
	if got, want := resp.Get("Response-Source"), "fe=<urn:3gpp:fe:p-cscf.term>"; got != want {
		t.Errorf("Response-Source = %q, want %q", got, want)
	}
	if got := resp.Get("Reason"); got != "" {
		t.Errorf("Reason = %q towards the network, want none without a cause from the access network", got)
	}
	ended(t, "the UE's call", in.Done())
	if in.Err() != sip.ErrCancelled {
		t.Fatalf("the UE's call ended with %v, want a CANCEL", in.Err())
	}
	wantCancelReason(t, in.EndedBy())
	pcrf.waitReleased(t, "session-1")
}

// TestMediaRefusedWithThe2xxEndsTheCallWithBYEs: when the answer only
// comes with the 200, the callee has answered before the PCRF refuses.
// The caller gets the 200, and then both get a BYE (TS 24.229
// 5.2.8.1.2).
func TestMediaRefusedWithThe2xxEndsTheCallWithBYEs(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	pcrf.fail(nil, policy.ErrRequestedServiceNotAuthorized)
	out, in := h.call()
	go in.Accept(answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	ended(t, "the UE's call", out.Done())
	ended(t, "the callee's call", in.Done())
	relay.waitHeld(t, 0, time.Second)
}

// TestReleaseWaitsForTheACKOfThe2xx is RFC 3261 15 for the BYE that the
// P-CSCF sends in the name of the callee: an event between the 200 and
// its ACK holds the BYEs back until the ACK has passed, so that they
// cannot overtake the 200 and find the caller without a dialog.
func TestReleaseWaitsForTheACKOfThe2xx(t *testing.T) {
	c := newRawCallBeforeACK(t)
	c.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.FailedResourcesAllocation})
	noRequest(t, "the other party before the ACK", c.toFar)
	noRequest(t, "the UE before the ACK", c.toUE)

	c.ack()
	if m := nextRequest(t, "the other party", c.toFar); m.Method != sip.BYE {
		t.Errorf("the other party got %s, want BYE", m.Method)
	}
	if m := nextRequest(t, "the UE", c.toUE); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want BYE", m.Method)
	}
	c.pcrf.waitReleased(t, "session-1")
	c.relay.waitHeld(t, 0, time.Second)
}

// TestReleaseGoesAheadWhenNoACKComes: without an ACK the BYEs go out
// once the callee would have given up waiting for it (RFC 3261
// 13.3.1.4, 15).
func TestReleaseGoesAheadWhenNoACKComes(t *testing.T) {
	c := newRawCallBeforeACK(t)
	c.p.ackWait = 100 * time.Millisecond
	c.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.FailedResourcesAllocation})
	if m := nextRequest(t, "the other party", c.toFar); m.Method != sip.BYE {
		t.Errorf("the other party got %s, want BYE", m.Method)
	}
	if m := nextRequest(t, "the UE", c.toUE); m.Method != sip.BYE {
		t.Errorf("the UE got %s, want BYE", m.Method)
	}
	c.pcrf.waitReleased(t, "session-1")
}

func TestEventForAnUnknownPolicySessionIsIgnored(t *testing.T) {
	c := newRawCall(t)
	c.p.PolicyEvent(policy.Event{Session: "session-9", Kind: policy.SessionTerminated})
	noRequest(t, "the other party", c.toFar)
	// The dialog of the call is still there to be released.
	c.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.ReleaseOfBearer})
	if m := nextRequest(t, "the other party", c.toFar); m.Method != sip.BYE {
		t.Errorf("the other party got %s after the event of its session, want BYE", m.Method)
	}
}

// TestA2xxThatCrossesTheCancelIsAcknowledgedAndReleased is RFC 3261 9.1
// at the P-CSCF that gave an INVITE up (TS 24.229 5.2.8.1.1): the callee
// answers 200 before the CANCEL reaches it. The caller keeps its 500,
// and the P-CSCF acknowledges the 200 and each retransmission in the
// caller's name, then releases the dialog with one BYE to the callee
// (5.2.8.1.2 item 1). The caller has sent an UPDATE in the early
// dialog, so the BYE takes the number after it.
func TestA2xxThatCrossesTheCancelIsAcknowledgedAndReleased(t *testing.T) {
	pcrf, relay, store := &fakePolicy{}, newFakeRelay(), NewMemoryStore()
	requests, acks := make(chan *sip.Message, 4), make(chan *sip.Message, 4)
	invites := make(chan *sip.ServerTransaction, 1)
	var far *sip.Stack
	listening := make(chan struct{})
	far = newStackConfig(t, sip.Config{Timers: fast, OnACK: func(ack *sip.Message) { acks <- ack },
		OnRequest: func(st *sip.ServerTransaction) {
			<-listening
			if st.Request.Method == sip.INVITE {
				invites <- st
				return
			}
			requests <- st.Request
			st.Respond(sip.NewResponse(st.Request, 200, ""))
		}})
	close(listening)
	farContact := "sip:callee@" + far.LocalAddr().String()
	ue, _ := answering(t, "ue", ueMedia)
	ueContact := "sip:001010000000001@" + ue.LocalAddr().String()
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: far.LocalAddr()}}
	})
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, store, only(relay), quiet())
	p.SetPolicyControl(pcrf)
	store.PutAssociation(context.Background(), Association{
		Source: sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()}, SentBy: ue.LocalAddr().String(), Contact: ueContact,
		PrivateIdentity: impi, PublicIdentities: []string{impu}, ServiceRoute: []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires: time.Now().Add(time.Hour), SignallingSession: "signalling",
	})
	pcscf := newPCSCFStack(t, p)
	p.SetStack(pcscf)

	inv := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	inv.Set("Contact", "<"+ueContact+">")
	ct, err := ue.SendRequest(inv, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var st *sip.ServerTransaction
	select {
	case st = <-invites:
	case <-time.After(3 * time.Second):
		t.Fatal("the callee got no INVITE")
	}
	answer := func(code int) *sip.Message {
		resp := sip.NewResponse(st.Request, code, "callee")
		for _, r := range st.Request.GetAll("Record-Route") {
			resp.Add("Record-Route", r)
		}
		resp.Add("Contact", "<"+farContact+">")
		resp.Add("Content-Type", "application/sdp")
		resp.Body = sdp.Offer("2", coreMedia).Bytes()
		return resp
	}
	st.Respond(answer(183))
	// The P-CSCF has the policy session of the 183 once the caller has it.
	for r := range ct.Responses() {
		if r.StatusCode == 183 {
			break
		}
	}

	upd := &sip.Message{Method: sip.UPDATE, RequestURI: farContact}
	upd.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	upd.Add("Max-Forwards", "70")
	upd.Add("From", inv.Get("From"))
	upd.Add("To", inv.Get("To")+";tag=callee")
	upd.Add("Call-ID", inv.Get("Call-ID"))
	upd.Add("CSeq", "2 UPDATE")
	updated, err := ue.SendRequest(upd, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	if resp := final(t, updated.Responses(), 3*time.Second); resp.StatusCode != 200 {
		t.Fatalf("the UPDATE got %d, want 200", resp.StatusCode)
	}
	if m := nextRequest(t, "the callee", requests); m.Method != sip.UPDATE {
		t.Fatalf("the callee got %s, want the UPDATE", m.Method)
	}

	p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.LossOfBearer})
	if resp := final(t, ct.Responses(), 3*time.Second); resp.StatusCode != 500 {
		t.Fatalf("the caller got %d, want 500", resp.StatusCode)
	}
	select {
	case <-st.Cancelled():
	case <-time.After(3 * time.Second):
		t.Fatal("the callee got no CANCEL")
	}
	// The 200 left the callee before the CANCEL came.
	st.Respond(answer(200))

	callee := inv.Get("To") + ";tag=callee"
	for i := range 2 {
		if i > 0 {
			// The callee sends its 200 again, as a UAS does until the ACK
			// comes (RFC 6026 8.1).
			st.Respond(answer(200))
		}
		ack := nextRequest(t, "the callee", acks)
		if ack.RequestURI != farContact || ack.Get("CSeq") != "1 ACK" || ack.Get("To") != callee || ack.Get("From") != inv.Get("From") {
			t.Errorf("ACK %d: %s with CSeq %q, To %q, From %q; want the ACK of the 200 at %s", i+1, ack.RequestURI, ack.Get("CSeq"), ack.Get("To"), ack.Get("From"), farContact)
		}
	}
	bye := nextRequest(t, "the callee", requests)
	want := map[string]string{"From": inv.Get("From"), "To": callee, "Call-ID": inv.Get("Call-ID"), "CSeq": "3 BYE",
		"Reason": `SIP;cause=503;text="Service Unavailable"`}
	if bye.Method != sip.BYE || bye.RequestURI != farContact {
		t.Errorf("the callee got %s %s, want a BYE to its Contact %s", bye.Method, bye.RequestURI, farContact)
	}
	for name, value := range want {
		if got := bye.Get(name); got != value {
			t.Errorf("BYE: %s = %q, want %q", name, got, value)
		}
	}
	noRequest(t, "the callee after the BYE", requests)
	for r := range ct.Responses() {
		if r.StatusCode/100 == 2 {
			t.Errorf("the caller got %d after its 500", r.StatusCode)
		}
	}
	pcrf.waitReleased(t, "session-1")
}
