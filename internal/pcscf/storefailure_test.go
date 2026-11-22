package pcscf

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the P-CSCF on a Store that fails. The P-CSCF answers a
// request whose state it cannot read or write with 500 (RFC 3261
// 21.5.1), rather than take the failure for state that is not there.

// failingStore is a Store that fails every operation while down is set,
// as a store outside the process does once the P-CSCF cannot reach it.
type failingStore struct {
	Store
	down atomic.Bool
}

var errUnreachable = errors.New("the store cannot be reached")

func (s *failingStore) Association(ctx context.Context, source sip.Destination) (Association, bool, error) {
	if s.down.Load() {
		return Association{}, false, errUnreachable
	}
	return s.Store.Association(ctx, source)
}

func (s *failingStore) AssociationOfContact(ctx context.Context, contact string) (Association, bool, error) {
	if s.down.Load() {
		return Association{}, false, errUnreachable
	}
	return s.Store.AssociationOfContact(ctx, contact)
}

func (s *failingStore) PutAssociation(ctx context.Context, a Association) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.PutAssociation(ctx, a)
}

func (s *failingStore) DeleteAssociation(ctx context.Context, source sip.Destination) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.DeleteAssociation(ctx, source)
}

func (s *failingStore) AssociationOfPolicySession(ctx context.Context, session policy.SessionID) (Association, bool, error) {
	if s.down.Load() {
		return Association{}, false, errUnreachable
	}
	return s.Store.AssociationOfPolicySession(ctx, session)
}

func (s *failingStore) PutDialog(ctx context.Context, d Dialog) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.PutDialog(ctx, d)
}

func (s *failingStore) UpdateDialog(ctx context.Context, key DialogKey, change func(*Dialog)) (Dialog, bool, error) {
	if s.down.Load() {
		return Dialog{}, false, errUnreachable
	}
	return s.Store.UpdateDialog(ctx, key, change)
}

func (s *failingStore) TakeDialog(ctx context.Context, key DialogKey) (Dialog, bool, error) {
	if s.down.Load() {
		return Dialog{}, false, errUnreachable
	}
	return s.Store.TakeDialog(ctx, key)
}

func (s *failingStore) DialogOfPolicySession(ctx context.Context, session policy.SessionID) (Dialog, bool, error) {
	if s.down.Load() {
		return Dialog{}, false, errUnreachable
	}
	return s.Store.DialogOfPolicySession(ctx, session)
}

func (s *failingStore) SecurityAssociations(ctx context.Context, ue netip.AddrPort) (SecurityAssociations, bool, error) {
	if s.down.Load() {
		return SecurityAssociations{}, false, errUnreachable
	}
	return s.Store.SecurityAssociations(ctx, ue)
}

func (s *failingStore) PutSecurityAssociations(ctx context.Context, sas SecurityAssociations) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.PutSecurityAssociations(ctx, sas)
}

func (s *failingStore) DeleteSecurityAssociations(ctx context.Context, ue netip.AddrPort) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.DeleteSecurityAssociations(ctx, ue)
}

// TestRegisterWhenTheStoreFails: a REGISTER whose IP association the
// P-CSCF cannot read gets 500 and goes no further. When the P-CSCF
// cannot store the registration that the home network accepted, the UE
// gets 500 rather than a 200 that no P-CSCF instance could serve. Once
// the store is back, the REGISTER that the UE sends again registers it.
func TestRegisterWhenTheStoreFails(t *testing.T) {
	store := &failingStore{Store: NewMemoryStore()}
	var downAtTheHome atomic.Bool
	registers := make(chan *sip.Message, 4)
	messages := make(chan *sip.Message, 4)
	home := newStack(t, func(st *sip.ServerTransaction) {
		if st.Request.Method == sip.MESSAGE {
			messages <- st.Request
			st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
			return
		}
		registers <- st.Request
		if downAtTheHome.Load() {
			store.down.Store(true)
		}
		resp := sip.NewResponse(st.Request, 200, sip.NewTag())
		resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
		resp.Add("P-Associated-URI", "<"+impu+">")
		resp.Add("Contact", "<sip:ue@127.0.0.1>;expires=3600")
		st.Respond(resp)
	})
	locate := locatorFunc(func(string) []sip.Destination { return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}} })
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, store, nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	ue := newStack(t, func(*sip.ServerTransaction) {})
	toPCSCF := sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()}
	send := func(m *sip.Message) *sip.Message {
		t.Helper()
		ct, err := ue.SendRequest(m, toPCSCF)
		if err != nil {
			t.Fatal(err)
		}
		var final *sip.Message
		for r := range ct.Responses() {
			final = r
		}
		if final == nil {
			t.Fatalf("no answer to the %s: %v", m.Method, ct.Err())
		}
		return final
	}
	register := func() *sip.Message {
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "1 REGISTER")
		m.Add("Contact", "<sip:ue@127.0.0.1>")
		m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
		return send(m)
	}

	store.down.Store(true)
	if resp := register(); resp.StatusCode != 500 {
		t.Fatalf("REGISTER with the store down answered %d, want 500", resp.StatusCode)
	}
	select {
	case <-registers:
		t.Fatal("the REGISTER reached the home network with the store down")
	case <-time.After(50 * time.Millisecond):
	}

	store.down.Store(false)
	downAtTheHome.Store(true)
	if resp := register(); resp.StatusCode != 500 {
		t.Fatalf("REGISTER whose 200 the P-CSCF could not store answered %d, want 500", resp.StatusCode)
	}
	<-registers

	store.down.Store(false)
	downAtTheHome.Store(false)
	if resp := register(); resp.StatusCode != 200 {
		t.Fatalf("REGISTER with the store back answered %d, want 200", resp.StatusCode)
	}
	<-registers
	// The UE is served: its MESSAGE goes on along the Service-Route.
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: "sip:001010000000002@" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<sip:001010000000002@"+domain+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	if resp := send(m); resp.StatusCode != 200 {
		t.Fatalf("MESSAGE of the registered UE answered %d, want 200", resp.StatusCode)
	}
	if got := (<-messages).Get("Route"); got != "<sip:orig@scscf."+domain+":5060;lr>" {
		t.Errorf("Route of the MESSAGE = %q, want the Service-Route", got)
	}
}

// failingCallHarness is a call harness whose Store goes down when the
// test says.
func failingCallHarness(t *testing.T, relay Relay) (*callHarness, *failingStore) {
	t.Helper()
	var store *failingStore
	h := newCallHarness(t, harnessConfig{relay: relay, wrap: func(s *MemoryStore) Store {
		store = &failingStore{Store: s}
		return store
	}})
	return h, store
}

// noCall checks that no INVITE reaches calls.
func noCall(t *testing.T, calls <-chan *sip.IncomingCall, who string) {
	t.Helper()
	select {
	case <-calls:
		t.Errorf("the INVITE reached %s with the store down", who)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestInviteWhenTheStoreFails: an INVITE from the UE, or to it, whose IP
// association the P-CSCF cannot read gets 500 and goes no further. The
// INVITE to the UE does not get the 480 of a UE that is not registered.
func TestInviteWhenTheStoreFails(t *testing.T) {
	h, store := failingCallHarness(t, nil)
	store.down.Store(true)

	out, err := h.ue.Invite(sip.Invitation{RequestURI: "sip:001010000000002@" + domain, From: impu,
		Route: []string{"<sip:pcscf." + domain + ":5060;lr>"}, Body: sdpBody(sdp.Offer("1", ueMedia))})
	if err != nil {
		t.Fatal(err)
	}
	answered(t, out, 500)
	noCall(t, h.toCore, "the core")

	out, err = h.core.Invite(sip.Invitation{RequestURI: h.ueContact, From: "sip:001010000000002@" + domain, To: impu,
		Route: []string{"<sip:term@pcscf." + domain + ":5060;lr>"}, Body: sdpBody(sdp.Offer("1", coreMedia))})
	if err != nil {
		t.Fatal(err)
	}
	answered(t, out, 500)
	noCall(t, h.toUE, "the UE")
}

// TestInviteWhoseDialogCannotBeStored: the store goes down while the
// INVITE is on its way, so the P-CSCF cannot keep the dialog that the
// first response creates. It could not release that session, nor free
// what it holds, so it gives the INVITE up: the caller gets 500, the
// callee a CANCEL, and the relay context goes.
func TestInviteWhoseDialogCannotBeStored(t *testing.T) {
	relay := newFakeRelay()
	h, store := failingCallHarness(t, relay)
	out, in := h.call()
	store.down.Store(true)
	// The 180 is reliable, and Progress waits for a PRACK that the UE
	// never sends: the CANCEL ends the call first.
	go in.Progress(180, sip.Body{})
	answered(t, out, 500)
	ended(t, "the far end's call", in.Done())
	if in.Err() != sip.ErrCancelled {
		t.Errorf("the far end's call ended with %v, want a CANCEL", in.Err())
	}
	relay.waitHeld(t, 0, time.Second)
}

// TestAnsweredInviteWhoseDialogCannotBeStored: the callee has answered,
// but the P-CSCF cannot keep the dialog of the 2xx. The caller gets
// 500. Once the store is back, the P-CSCF acknowledges a retransmission
// of the 2xx in the caller's name and ends the call with a BYE, as for a
// 2xx that crossed a CANCEL.
func TestAnsweredInviteWhoseDialogCannotBeStored(t *testing.T) {
	h, store := failingCallHarness(t, nil)
	out, in := h.call()
	store.down.Store(true)
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	answered(t, out, 500)
	store.down.Store(false)
	waitACK(t, acked)
	ended(t, "the far end's call", in.Done())
}

// TestRequestWithinADialogWhenTheStoreFails: an UPDATE from the UE in a
// call whose dialog the P-CSCF cannot read gets 500, which leaves the
// dialog up (RFC 3261 12.2.1.2). Once the store is back, the BYE ends
// the call.
func TestRequestWithinADialogWhenTheStoreFails(t *testing.T) {
	h, store := failingCallHarness(t, nil)
	out, in := h.call()
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	waitACK(t, acked)

	store.down.Store(true)
	resp, err := out.Update(sip.Body{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("UPDATE with the store down answered %d, want 500", resp.StatusCode)
	}

	store.down.Store(false)
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the far end's call", in.Done())
}
