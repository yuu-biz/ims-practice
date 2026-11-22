package pcscf

import (
	"context"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// fakePolicy is a PCRF that keeps what the P-CSCF asks of it, and
// answers as a test tells it to.
type fakePolicy struct {
	mu sync.Mutex
	// subscribeErr and authorizeErr are what the next requests fail with.
	subscribeErr, authorizeErr error
	next                       int
	// signalling are the UEs whose signalling path is watched, media the
	// requests to authorise media, and released the sessions released,
	// each in the order they came.
	signalling []netip.Addr
	media      []policy.MediaRequest
	released   []policy.SessionID
}

func (f *fakePolicy) newSession() policy.SessionID {
	f.next++
	return policy.SessionID("session-" + strconv.Itoa(f.next))
}

func (f *fakePolicy) SubscribeSignallingPath(_ context.Context, ue netip.Addr) (policy.SessionID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signalling = append(f.signalling, ue)
	if f.subscribeErr != nil {
		return "", f.subscribeErr
	}
	return f.newSession(), nil
}

func (f *fakePolicy) AuthorizeMedia(_ context.Context, r policy.MediaRequest) (policy.SessionID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.media = append(f.media, r)
	if f.authorizeErr != nil {
		return "", f.authorizeErr
	}
	if r.Session != "" {
		return r.Session, nil
	}
	return f.newSession(), nil
}

func (f *fakePolicy) Release(_ context.Context, s policy.SessionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, s)
	return nil
}

func (f *fakePolicy) fail(subscribe, authorize error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribeErr, f.authorizeErr = subscribe, authorize
}

func (f *fakePolicy) signallingRequests() []netip.Addr {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.signalling)
}

func (f *fakePolicy) mediaRequests() []policy.MediaRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.media)
}

// waitReleased waits until the sessions released are exactly want. The
// P-CSCF releases a session after it has passed the SIP message on.
func (f *fakePolicy) waitReleased(t *testing.T, want ...policy.SessionID) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		got := slices.Clone(f.released)
		f.mu.Unlock()
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("released the policy sessions %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// registrar is a UE and its home network around a P-CSCF with policy
// control: the home network answers every REGISTER with 200.
type registrar struct {
	t      *testing.T
	p      *PCSCF
	store  *MemoryStore
	policy *fakePolicy
	pcscf  *sip.Stack
	ue     *sip.Stack
}

func newRegistrar(t *testing.T) *registrar {
	t.Helper()
	r := &registrar{t: t, store: NewMemoryStore(), policy: &fakePolicy{}}
	home := newStack(t, func(st *sip.ServerTransaction) {
		resp := sip.NewResponse(st.Request, 200, sip.NewTag())
		resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
		resp.Add("P-Associated-URI", "<"+impu+">")
		resp.Add("Contact", st.Request.Get("Contact"))
		st.Respond(resp)
	})
	locate := locatorFunc(func(string) []sip.Destination { return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}} })
	r.p = New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, r.store, nil, quiet())
	r.p.SetPolicyControl(r.policy)
	r.pcscf = newPCSCFStack(t, r.p)
	r.p.SetStack(r.pcscf)
	r.ue = newStack(t, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 200, "ue")) })
	return r
}

// register sends a REGISTER for expires seconds and returns the UE's
// association once the P-CSCF has done all that follows the 200.
func (r *registrar) register(expires int) (Association, bool) {
	r.t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<sip:ue@"+r.ue.LocalAddr().String()+">;expires="+strconv.Itoa(expires))
	m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
	ct, err := r.ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: r.pcscf.LocalAddr()})
	if err != nil {
		r.t.Fatal(err)
	}
	if resp := final(r.t, ct.Responses(), 3*time.Second); resp.StatusCode != 200 {
		r.t.Fatalf("REGISTER answered %d, want 200", resp.StatusCode)
	}
	// The P-CSCF asks the PCRF after it has answered the UE.
	time.Sleep(100 * time.Millisecond)
	// The association is read from the Store: its policy session is what
	// the P-CSCF keeps of the PCRF's answer, and no message shows it until
	// the P-CSCF releases the session.
	a, ok, _ := r.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: r.ue.LocalAddr()})
	return a, ok
}

// TestRegistrationWatchesTheSignallingPath is TS 29.214 A.4: the P-CSCF
// subscribes to the status of the signalling path on the initial
// registration, keeps the subscription over a refresh, and cancels it
// when the user is deregistered.
func TestRegistrationWatchesTheSignallingPath(t *testing.T) {
	r := newRegistrar(t)
	a, ok := r.register(3600)
	if !ok || a.SignallingSession != "session-1" {
		t.Fatalf("association after the registration = %+v, %v; want the policy session in it", a, ok)
	}
	if got, want := r.policy.signallingRequests(), []netip.Addr{r.ue.LocalAddr().Addr()}; !slices.Equal(got, want) {
		t.Fatalf("subscribed for %v, want the address of the UE %v", got, want)
	}

	a, _ = r.register(3600)
	if a.SignallingSession != "session-1" {
		t.Errorf("policy session after the refresh = %q, want the one of the registration", a.SignallingSession)
	}
	if n := len(r.policy.signallingRequests()); n != 1 {
		t.Errorf("%d subscriptions after the refresh, want 1", n)
	}
	r.policy.waitReleased(t)

	a, _ = r.register(0)
	r.policy.waitReleased(t, "session-1")
	if a.SignallingSession != "" {
		t.Errorf("policy session %q still in the association after the deregistration", a.SignallingSession)
	}
}

// TestUEWithoutIPCANSessionRegistersWithoutPolicyControl: a UE
// the PCRF does not know, such as a softphone on the host, registers
// all the same.
func TestUEWithoutIPCANSessionRegistersWithoutPolicyControl(t *testing.T) {
	r := newRegistrar(t)
	r.policy.fail(policy.ErrIPCANSessionNotAvailable, nil)
	a, ok := r.register(3600)
	if !ok || a.SignallingSession != "" {
		t.Fatalf("association = %+v, %v; want one without a policy session", a, ok)
	}
	r.register(0)
	r.policy.waitReleased(t)
}

// newPolicyHarness is a call harness whose UE came through the EPC: its
// registration has a policy session, and the P-CSCF has a PCRF and a
// media relay.
func newPolicyHarness(t *testing.T) (*callHarness, *fakePolicy, *fakeRelay) {
	t.Helper()
	pcrf, relay := &fakePolicy{}, newFakeRelay()
	h := newCallHarness(t, harnessConfig{
		relay:       relay,
		association: func(a *Association) { a.SignallingSession = "signalling" },
		configure:   func(p *PCSCF) { p.SetPolicyControl(pcrf) },
	})
	return h, pcrf, relay
}

// The addresses of the fake relay's terminations.
var (
	relayAccess = netip.MustParseAddrPort("127.0.0.1:42000")
	relayCore   = netip.MustParseAddrPort("127.0.0.1:42002")
)

// TestOriginatingCallHasItsMediaAuthorised is TS 29.214 A.1 at the
// P-CSCF of the caller: one request with the answer of the 183, none
// for the same answer in the 200, and the session released with the BYE
// (4.4.4).
func TestOriginatingCallHasItsMediaAuthorised(t *testing.T) {
	h, pcrf, _ := newPolicyHarness(t)
	out, in := h.call()
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	requests := pcrf.mediaRequests()
	if len(requests) != 1 {
		t.Fatalf("%d requests to authorise media after the 183, want 1", len(requests))
	}
	r := requests[0]
	if r.Session != "" || r.UE != h.ue.Stack().LocalAddr().Addr() || !r.SDP.UEOffered || !slices.Equal(r.Subscribe, mediaEvents) {
		t.Errorf("request = %+v; want a new session for the UE's address, the UE's offer, and the events that end a session", r)
	}
	// The PCRF gets the SDP as the UE has it: its own offer, and the
	// answer that names the relay.
	if got := audio(t, r.SDP.Offer); got != ueMedia {
		t.Errorf("offer names %v, want the UE's own address %v", got, ueMedia)
	}
	if got := audio(t, r.SDP.Answer); got != relayAccess {
		t.Errorf("answer names %v, want the access termination %v", got, relayAccess)
	}

	go in.Accept(answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	if n := len(pcrf.mediaRequests()); n != 1 {
		t.Errorf("%d requests after the 200 with the same answer, want still 1", n)
	}
	ueDialog, ok := out.Dialog()
	key := keyOf(t, ueDialog, ok)
	if d, _ := h.dialog(key); d.PolicySession != "session-1" {
		t.Errorf("policy session of the dialog = %q, want session-1", d.PolicySession)
	}

	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	pcrf.waitReleased(t, "session-1")
	h.waitNoDialog(key)
}

// TestTerminatingCallHasItsMediaAuthorised is TS 29.214 A.1 at the
// P-CSCF of the callee: the UE received the offer, with the relay's
// address, and sent the answer.
func TestTerminatingCallHasItsMediaAuthorised(t *testing.T) {
	h, pcrf, _ := newPolicyHarness(t)
	const mmtel = "urn:urn-7:3gpp-service.ims.icsi.mmtel"
	out, err := h.core.Invite(sip.Invitation{RequestURI: h.ueContact, From: "sip:001010000000002@" + domain, To: impu,
		Route:   []string{"<sip:term@pcscf." + domain + ":5060;lr>"},
		Headers: []sip.HeaderField{{Name: "P-Asserted-Service", Value: mmtel}},
		Body:    sdpBody(sdp.Offer("1", coreMedia))})
	if err != nil {
		t.Fatal(err)
	}
	in := receiveCall(t, h.toUE)
	go in.Accept(answerTo(t, in.Request, ueMedia))
	answered(t, out, 200)
	// RFC 6050 5.1.2: the asserted service does not leave the trust
	// domain.
	if got := in.Request.Get("P-Asserted-Service"); got != "" {
		t.Errorf("the UE received P-Asserted-Service: %s", got)
	}

	requests := pcrf.mediaRequests()
	if len(requests) != 1 {
		t.Fatalf("%d requests to authorise media, want 1", len(requests))
	}
	r := requests[0]
	if r.SDP.UEOffered || r.ApplicationID != mmtel {
		t.Errorf("request = %+v; want the offer received by the UE and the service of P-Asserted-Service", r)
	}
	if got := audio(t, r.SDP.Offer); got != relayAccess {
		t.Errorf("offer names %v, want the access termination %v", got, relayAccess)
	}
	if got := audio(t, r.SDP.Answer); got != ueMedia {
		t.Errorf("answer names %v, want the UE's own address %v", got, ueMedia)
	}
	if err := in.Bye(); err != nil {
		t.Fatal(err)
	}
	pcrf.waitReleased(t, "session-1")
}

// TestCallOfAUEWithoutIPCANSessionSkipsThePCRF: the registration
// of the UE has no policy session, so its call asks nothing of the PCRF.
func TestCallOfAUEWithoutIPCANSessionSkipsThePCRF(t *testing.T) {
	pcrf := &fakePolicy{}
	h := newCallHarness(t, harnessConfig{relay: newFakeRelay(), configure: func(p *PCSCF) { p.SetPolicyControl(pcrf) }})
	out, in := h.call()
	go in.Accept(answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the callee's call", in.Done())
	if n := len(pcrf.mediaRequests()); n != 0 {
		t.Errorf("%d requests to authorise media, want none", n)
	}
	pcrf.waitReleased(t)
}

// TestCallRightAfterTheRegistrationHasItsMediaAuthorised: the P-CSCF
// subscribes to the signalling path after it has sent the 200 to the
// REGISTER, so the UE's first INVITE can reach it before the PCRF has
// answered. The media of that call goes to the PCRF all the same.
func TestCallRightAfterTheRegistrationHasItsMediaAuthorised(t *testing.T) {
	pcrf := &fakePolicy{}
	h := newCallHarness(t, harnessConfig{relay: newFakeRelay(), configure: func(p *PCSCF) { p.SetPolicyControl(pcrf) }})
	out, in := h.call()
	// The PCRF answers the subscription while the INVITE is on its way.
	source := sip.Destination{Transport: sip.UDP, Addr: h.ue.Stack().LocalAddr()}
	a, ok, _ := h.store.Association(context.Background(), source)
	if !ok {
		t.Fatal("no association for the UE")
	}
	a.SignallingSession = "signalling"
	h.store.PutAssociation(context.Background(), a)

	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	if n := len(pcrf.mediaRequests()); n != 1 {
		t.Fatalf("%d requests to authorise media after the 183, want 1", n)
	}
	go in.Accept(answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	pcrf.waitReleased(t, "session-1")
}

// TestRefusedMediaEndsTheINVITE is TS 24.229 5.2.7.2: when the network
// cannot grant the resources, the P-CSCF answers the INVITE 500, and
// 5.2.8.1.1: it cancels the INVITE towards the callee.
func TestRefusedMediaEndsTheINVITE(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	pcrf.fail(nil, policy.ErrRequestedServiceNotAuthorized)
	out, in := h.call()
	go in.Progress(183, answerTo(t, in.Request, coreMedia))

	resp := answered(t, out, 500)
	if got, want := resp.Get("Reason"), `FAILURE_CAUSE;cause=1;text="Media bearer or QoS lost"`; got != want {
		t.Errorf("Reason = %q, want %q", got, want)
	}
	if got, want := resp.Get("Response-Source"), "fe=<urn:3gpp:fe:p-cscf.orig>"; got != want {
		t.Errorf("Response-Source = %q, want %q", got, want)
	}
	ended(t, "the callee's call", in.Done())
	if in.Err() != sip.ErrCancelled {
		t.Errorf("the callee's call ended with %v, want a CANCEL", in.Err())
	}
	relay.waitHeld(t, 0, time.Second)
	if _, ok := h.dialog(DialogKey{CallID: out.Request.Get("Call-ID"), UETag: tagOf(out.Request.Get("From"))}); ok {
		t.Error("the dialog of the refused call is still in the Store")
	}
}

// TestCancelledAndRejectedCallsReleaseThePolicySession is TS 29.214
// 4.4.4 for a session that ends before it is set up.
func TestCancelledAndRejectedCallsReleaseThePolicySession(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	out, in := h.call()
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	if err := out.Cancel(); err != nil {
		t.Fatal(err)
	}
	answered(t, out, 487)
	pcrf.waitReleased(t, "session-1")

	out, in = h.call()
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	in.Reject(486)
	answered(t, out, 486)
	pcrf.waitReleased(t, "session-1", "session-2")
	relay.waitHeld(t, 0, time.Second)
}

// TestLostSignallingPathRefusesRequestsToTheUE is TS 24.229 5.2.6.4.3
// item 1 and 5.2.6.4.7 item 1: after the PCRF reported the signalling
// bearer lost, a request to the UE gets 500, until the UE registers
// again (NOTE 1), which also renews the subscription.
func TestLostSignallingPathRefusesRequestsToTheUE(t *testing.T) {
	r := newRegistrar(t)
	r.register(3600)
	network := newStack(t, func(*sip.ServerTransaction) {})
	contact := "sip:ue@" + r.ue.LocalAddr().String()
	toUE := func(m *sip.Message) int {
		t.Helper()
		ct, err := network.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: r.pcscf.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		return final(t, ct.Responses(), 3*time.Second).StatusCode
	}
	path := "<sip:term@pcscf." + domain + ":5060;lr>"
	if got := toUE(message(contact, path)); got != 200 {
		t.Fatalf("MESSAGE to the registered UE answered %d, want its 200", got)
	}

	r.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.ReleaseOfBearer})
	a, _, _ := r.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: r.ue.LocalAddr()})
	if !a.SignallingLost || a.SignallingSession != "" {
		t.Errorf("association after the event = %+v; want the signalling marked lost and no policy session", a)
	}
	// The bearer was released, not the policy session, so the P-CSCF
	// ends the session.
	r.policy.waitReleased(t, "session-1")
	if got := toUE(invite(contact, path)); got != 500 {
		t.Errorf("INVITE to the UE answered %d, want 500", got)
	}
	if got := toUE(message(contact, path)); got != 500 {
		t.Errorf("MESSAGE to the UE answered %d, want 500", got)
	}

	a, _ = r.register(3600)
	if a.SignallingLost || a.SignallingSession != "session-2" {
		t.Errorf("association after the new REGISTER = %+v; want the signalling back and a new policy session", a)
	}
	if got := toUE(message(contact, path)); got != 200 {
		t.Errorf("MESSAGE after the new REGISTER answered %d, want the UE's 200", got)
	}
}

// TestEndedIPCANSessionNeedsNoRelease: when the PCRF itself ended the
// session that watches the signalling path, the P-CSCF sends it nothing
// more (TS 29.214 4.4.6.1).
func TestEndedIPCANSessionNeedsNoRelease(t *testing.T) {
	r := newRegistrar(t)
	r.register(3600)
	r.p.PolicyEvent(policy.Event{Session: "session-1", Kind: policy.SessionTerminated})
	a, _, _ := r.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: r.ue.LocalAddr()})
	if !a.SignallingLost {
		t.Error("the signalling is not marked lost")
	}
	time.Sleep(100 * time.Millisecond)
	r.policy.waitReleased(t)
}
