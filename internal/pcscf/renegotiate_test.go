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

// answering makes a stack that answers every request 200 as the UAS of
// a dialog does: with its tag, its Contact and the Record-Route of the
// request (RFC 3261 12.1.1), and with an SDP answer naming media when
// the request has an offer. It hands each request to the test.
//
// A BYE is answered only once holdBYE is closed, unless holdBYE is nil.
func answering(t *testing.T, tag string, media netip.AddrPort, holdBYE <-chan struct{}) (*sip.Stack, <-chan *sip.Message) {
	t.Helper()
	got := make(chan *sip.Message, 4)
	// The handler names the stack in its Contact, and the stack exists
	// only once NewStack has returned.
	var stack *sip.Stack
	listening := make(chan struct{})
	stack = newStack(t, func(st *sip.ServerTransaction) {
		<-listening
		got <- st.Request
		if st.Request.Method == sip.BYE && holdBYE != nil {
			<-holdBYE
		}
		ok := sip.NewResponse(st.Request, 200, tag)
		for _, r := range st.Request.GetAll("Record-Route") {
			ok.Add("Record-Route", r)
		}
		ok.Add("Contact", "<sip:"+tag+"@"+stack.LocalAddr().String()+">")
		if sip.HasSessionDescription(st.Request) {
			ok.Add("Content-Type", "application/sdp")
			ok.Body = sdp.Offer("2", media).Bytes()
		}
		st.Respond(ok)
	})
	close(listening)
	return stack, got
}

// update is an UPDATE within the dialog of invite with a new offer (RFC
// 3311 5.1), from the caller or, with fromCallee, from the callee.
func update(inviteRequest *sip.Message, fromCallee bool, target string, cseq string, offer netip.AddrPort) *sip.Message {
	from, to := inviteRequest.Get("From"), inviteRequest.Get("To")+";tag=callee"
	if fromCallee {
		from, to = to, from
	}
	m := &sip.Message{Method: "UPDATE", RequestURI: target}
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", from)
	m.Add("To", to)
	m.Add("Call-ID", inviteRequest.Get("Call-ID"))
	m.Add("CSeq", cseq+" UPDATE")
	m.Add("Content-Type", "application/sdp")
	m.Body = sdp.Offer("3", offer).Bytes()
	return m
}

// rawCall is a confirmed call from a UE through a P-CSCF with a PCRF
// and a media relay, between two stacks that show a test each request
// they get. The policy session of the call is session-1.
type rawCall struct {
	t      *testing.T
	p      *PCSCF
	store  *MemoryStore
	pcrf   *fakePolicy
	relay  *fakeRelay
	pcscf  *sip.Stack
	ue     *sip.Stack
	far    *sip.Stack
	toUE   <-chan *sip.Message
	toFar  <-chan *sip.Message
	invite *sip.Message
	// ueContact and farContact are the Contact URIs of the two.
	ueContact, farContact string
}

func newRawCall(t *testing.T) *rawCall {
	t.Helper()
	c := newRawCallBeforeACK(t, nil)
	c.ack()
	return c
}

// newRawCallBeforeACK is a rawCall whose UE has the 200 to its INVITE
// and has not sent the ACK yet. The other party answers a BYE only once
// farHoldsBYE is closed, unless it is nil.
func newRawCallBeforeACK(t *testing.T, farHoldsBYE <-chan struct{}) *rawCall {
	t.Helper()
	c := &rawCall{t: t, pcrf: &fakePolicy{}, relay: newFakeRelay(), store: NewMemoryStore()}
	c.far, c.toFar = answering(t, "callee", coreMedia, farHoldsBYE)
	c.ue, c.toUE = answering(t, "ue", ueMedia, nil)
	c.ueContact = "sip:001010000000001@" + c.ue.LocalAddr().String()
	c.farContact = "sip:callee@" + c.far.LocalAddr().String()
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: c.far.LocalAddr()}}
	})
	c.p = newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, c.store, only(c.relay), quiet())
	c.p.SetPolicyControl(c.pcrf)
	c.p.store.PutAssociation(context.Background(), Association{
		Source: sip.Destination{Transport: sip.UDP, Addr: c.ue.LocalAddr()}, SentBy: c.ue.LocalAddr().String(), Contact: c.ueContact,
		PrivateIdentity: impi, PublicIdentities: []string{impu}, ServiceRoute: []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires: time.Now().Add(time.Hour), SignallingSession: "signalling",
	})
	c.pcscf = newPCSCFStack(t, c.p)
	c.p.SetStack(c.pcscf)

	c.invite = invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	c.invite.Set("Contact", "<"+c.ueContact+">")
	c.send(c.ue, c.invite)
	<-c.toFar
	return c
}

// ack hands the P-CSCF the UE's ACK for the 200 (RFC 3261 13.2.2.4) the
// way its stack does. When ack returns, the P-CSCF has taken it.
func (c *rawCall) ack() {
	m := &sip.Message{Method: sip.ACK, RequestURI: c.farContact}
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", c.invite.Get("From"))
	m.Add("To", c.invite.Get("To")+";tag=callee")
	m.Add("Call-ID", c.invite.Get("Call-ID"))
	m.Add("CSeq", "1 ACK")
	c.p.OnACK(m)
}

// send sends m through the P-CSCF and returns its 200.
func (c *rawCall) send(from *sip.Stack, m *sip.Message) *sip.Message {
	c.t.Helper()
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: c.pcscf.LocalAddr()})
	if err != nil {
		c.t.Fatal(err)
	}
	resp := final(c.t, ct.Responses(), 3*time.Second)
	if resp.StatusCode != 200 {
		c.t.Fatalf("%s answered %d, want 200", m.Method, resp.StatusCode)
	}
	return resp
}

// TestOfferWithinTheDialogIsRelayedAndAuthorisedAgain: a new offer in
// an UPDATE goes through the relay context of the dialog, in either
// direction, and its answer gives the PCRF the changed service
// information in the session of the dialog (TS 29.214 A.1, 4.4.2).
func TestOfferWithinTheDialogIsRelayedAndAuthorisedAgain(t *testing.T) {
	c := newRawCall(t)
	pcrf, relay, inv, ue, far, toFar, toUE := c.pcrf, c.relay, c.invite, c.ue, c.far, c.toFar, c.toUE
	send, ueContact := c.send, c.ueContact

	// The UE moves its media: the far end hears the relay's address, and
	// the relay learns the new one.
	ueMoved := netip.MustParseAddrPort("192.0.2.10:4002")
	answer := send(ue, update(inv, false, c.farContact, "2", ueMoved))
	if got := audio(t, (<-toFar).Body); got != relayCore {
		t.Errorf("offer of the UE's UPDATE names %v towards the network, want the core termination %v", got, relayCore)
	}
	if got := audio(t, answer.Body); got != relayAccess {
		t.Errorf("answer to the UE's UPDATE names %v, want the access termination %v", got, relayAccess)
	}
	if got := relay.remote("1", agw.Access); got != ueMoved {
		t.Errorf("the access termination sends to %v, want the UE's new address %v", got, ueMoved)
	}
	requests := pcrf.mediaRequests()
	if len(requests) != 2 {
		t.Fatalf("%d requests to authorise media after the UPDATE, want 2", len(requests))
	}
	r := requests[1]
	if r.Session != "session-1" || !r.SDP.UEOffered || audio(t, r.SDP.Offer) != ueMoved || audio(t, r.SDP.Answer) != relayAccess {
		t.Errorf("request after the UE's UPDATE = %+v; want session-1 with the UE's new offer and the answer it received", r)
	}

	// The far end moves its media.
	farMoved := netip.MustParseAddrPort("192.0.2.99:6002")
	answer = send(far, update(inv, true, ueContact, "1", farMoved))
	if got := audio(t, (<-toUE).Body); got != relayAccess {
		t.Errorf("offer of the far end's UPDATE names %v towards the UE, want the access termination %v", got, relayAccess)
	}
	if got := audio(t, answer.Body); got != relayCore {
		t.Errorf("answer to the far end's UPDATE names %v, want the core termination %v", got, relayCore)
	}
	requests = pcrf.mediaRequests()
	if len(requests) != 3 {
		t.Fatalf("%d requests to authorise media after the second UPDATE, want 3", len(requests))
	}
	r = requests[2]
	if r.Session != "session-1" || r.SDP.UEOffered || audio(t, r.SDP.Offer) != relayAccess || audio(t, r.SDP.Answer) != ueMedia {
		t.Errorf("request after the far end's UPDATE = %+v; want session-1 with the offer the UE received and its answer", r)
	}
	if got := relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v, want the far end's new address %v", got, farMoved)
	}
}

// TestTargetRefreshReplacesTheContacts is TS 24.229 5.2.6.4.5 item 5
// with 5.2.6.4.6 item 3: an UPDATE is a target refresh request (RFC
// 3311 5.1), so once it is answered the dialog keeps the Contact of the
// request for the party that sent it, and the Contact of the response
// for the other one. A BYE of the P-CSCF then goes to them.
func TestTargetRefreshReplacesTheContacts(t *testing.T) {
	c := newRawCall(t)
	farMoved := "sip:moved@" + c.far.LocalAddr().String()
	refresh := update(c.invite, true, c.ueContact, "1", coreMedia)
	refresh.Set("Contact", "<"+farMoved+">")
	c.send(c.far, refresh)
	<-c.toUE

	// The targets are read from the Store. The UE's stack answers with a
	// Contact that is not the one the UE registered, so in this harness
	// a BYE to it would go the way of a request to the network; the BYEs
	// of a release to the targets of the dialog are checked in
	// release_test.go.
	key := DialogKey{CallID: c.invite.Get("Call-ID"), UETag: tagOf(c.invite.Get("From"))}
	d, ok, _ := c.store.UpdateDialog(context.Background(), key, func(*Dialog) {})
	if !ok {
		t.Fatal("the dialog is gone after the UPDATE")
	}
	if d.Callee.Target != farMoved {
		t.Errorf("target of the callee = %q, want the Contact of its UPDATE %q", d.Callee.Target, farMoved)
	}
	if want := "sip:ue@" + c.ue.LocalAddr().String(); d.Caller.Target != want {
		t.Errorf("target of the UE = %q, want the Contact of its 200 %q", d.Caller.Target, want)
	}
}
