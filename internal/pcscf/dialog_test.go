package pcscf

import (
	"context"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// callHarness is a P-CSCF between two user agents. ue is a registered
// UE. core stands for everything behind the P-CSCF at once: the S-CSCF
// and the user at the far end.
type callHarness struct {
	t     *testing.T
	p     *PCSCF
	store *MemoryStore
	pcscf *sip.Stack
	ue    *sip.UA
	core  *sip.UA
	// ueContact and coreContact are the Contact URIs of the two.
	ueContact, coreContact string
	// toUE and toCore deliver the calls that reach each of them.
	toUE, toCore chan *sip.IncomingCall
	// ueGot and coreGot are the messages that each of them received.
	ueGot, coreGot *received
}

// harnessConfig is what newCallHarness varies.
type harnessConfig struct {
	relay Relay
	// ueIP is the loopback address the UE listens on, 127.0.0.1 when it
	// is not set.
	ueIP string
	// association changes the registration of the UE before it is stored.
	association func(*Association)
	// configure sets up the P-CSCF before it takes requests.
	configure func(*PCSCF)
	// wrap, when set, makes the Store of the P-CSCF of the MemoryStore
	// of the harness.
	wrap func(*MemoryStore) Store
}

func newCallHarness(t *testing.T, cfg harnessConfig) *callHarness {
	t.Helper()
	h := &callHarness{t: t, store: NewMemoryStore(), toUE: make(chan *sip.IncomingCall, 1), toCore: make(chan *sip.IncomingCall, 1),
		ueGot: &received{}, coreGot: &received{}}
	if cfg.ueIP == "" {
		cfg.ueIP = "127.0.0.1"
	}
	// Both user agents send everything to the P-CSCF, which they know
	// once it listens.
	var pcscfAddr netip.AddrPort
	toPCSCF := func(sip.URI) ([]sip.Destination, error) {
		return []sip.Destination{{Transport: sip.UDP, Addr: pcscfAddr}}, nil
	}
	newUA := func(ip, user string, calls chan *sip.IncomingCall, got *received) (*sip.UA, string) {
		tr, err := sip.ListenObserved(ip+":0", quiet(), got.observe)
		if err != nil {
			t.Fatal(err)
		}
		contact := "sip:" + user + "@" + tr.LocalAddr().String()
		ua, err := sip.NewUA(tr, sip.UAConfig{SentBy: tr.LocalAddr().String(), Contact: contact, Locate: toPCSCF,
			OnCall: func(ic *sip.IncomingCall) { calls <- ic }}, quiet())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(ua.Close)
		return ua, contact
	}
	h.ue, h.ueContact = newUA(cfg.ueIP, "001010000000001", h.toUE, h.ueGot)
	h.core, h.coreContact = newUA("127.0.0.1", "far", h.toCore, h.coreGot)

	coreAddr := h.core.Stack().LocalAddr()
	locate := locatorFunc(func(string) []sip.Destination { return []sip.Destination{{Transport: sip.UDP, Addr: coreAddr}} })
	var store Store = h.store
	if cfg.wrap != nil {
		store = cfg.wrap(h.store)
	}
	h.p = newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, store, only(cfg.relay), quiet())
	if cfg.configure != nil {
		cfg.configure(h.p)
	}
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	h.pcscf, err = sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), Screen: h.p.Screen, Admit: h.p.AdmitUnprotected, OnRequest: h.p.OnRequest, OnACK: h.p.OnACK}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.pcscf.Close)
	h.p.SetStack(h.pcscf)
	pcscfAddr = h.pcscf.LocalAddr()

	ueAddr := h.ue.Stack().LocalAddr()
	a := Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: ueAddr},
		SentBy:           ueAddr.String(),
		Contact:          h.ueContact,
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu},
		ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires:          time.Now().Add(time.Hour),
	}
	if cfg.association != nil {
		cfg.association(&a)
	}
	h.p.store.PutAssociation(context.Background(), a)
	return h
}

var (
	ueMedia   = netip.MustParseAddrPort("192.0.2.10:4000")
	coreMedia = netip.MustParseAddrPort("192.0.2.99:6000")
)

func sdpBody(s sdp.Session) sip.Body { return sip.Body{Type: "application/sdp", Content: s.Bytes()} }

// answerTo is the SDP answer of a party whose media is at addr.
func answerTo(t *testing.T, invite *sip.Message, addr netip.AddrPort) sip.Body {
	t.Helper()
	offer, err := sdp.Parse(invite.Body)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := sdp.Answer(offer, "2", addr)
	if err != nil {
		t.Fatal(err)
	}
	return sdpBody(answer)
}

// call starts a call from the UE to the far user and returns it with
// the INVITE as it reached the core.
func (h *callHarness) call() (*sip.OutgoingCall, *sip.IncomingCall) {
	h.t.Helper()
	out, err := h.ue.Invite(sip.Invitation{RequestURI: "sip:001010000000002@" + domain, From: impu,
		Route: []string{"<sip:pcscf." + domain + ":5060;lr>"}, Body: sdpBody(sdp.Offer("1", ueMedia))})
	if err != nil {
		h.t.Fatal(err)
	}
	return out, receiveCall(h.t, h.toCore)
}

// callUE starts a call from the far user to the UE, as the S-CSCF sends
// it: to the registered contact, along the Path.
func (h *callHarness) callUE() (*sip.OutgoingCall, *sip.IncomingCall) {
	h.t.Helper()
	out, err := h.core.Invite(sip.Invitation{RequestURI: h.ueContact, From: "sip:001010000000002@" + domain, To: impu,
		Route: []string{"<sip:term@pcscf." + domain + ":5060;lr>"}, Body: sdpBody(sdp.Offer("1", coreMedia))})
	if err != nil {
		h.t.Fatal(err)
	}
	return out, receiveCall(h.t, h.toUE)
}

func receiveCall(t *testing.T, calls <-chan *sip.IncomingCall) *sip.IncomingCall {
	t.Helper()
	select {
	case ic := <-calls:
		return ic
	case <-time.After(3 * time.Second):
		t.Fatal("the INVITE did not arrive")
		return nil
	}
}

// answered waits for the final response of a call and checks its code.
func answered(t *testing.T, call *sip.OutgoingCall, want int) *sip.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case resp, ok := <-call.Responses():
			if !ok {
				t.Fatalf("the INVITE ended with %v, want %d", call.Err(), want)
			}
			if resp.StatusCode < 200 {
				continue
			}
			if resp.StatusCode != want {
				t.Fatalf("the INVITE was answered %d, want %d", resp.StatusCode, want)
			}
			return resp
		case <-deadline:
			t.Fatalf("no final response, want %d", want)
			return nil
		}
	}
}

// accept answers in with a 200 in the background. The channel gets the
// error of Accept once the ACK has reached the callee, and so has passed
// the P-CSCF.
func accept(in *sip.IncomingCall, body sip.Body) <-chan error {
	acked := make(chan error, 1)
	go func() {
		_, err := in.Accept(body)
		acked <- err
	}()
	return acked
}

// waitACK waits for the result of accept.
func waitACK(t *testing.T, acked <-chan error) {
	t.Helper()
	select {
	case err := <-acked:
		if err != nil {
			t.Fatalf("the 200 got no ACK: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the 200 got no ACK")
	}
}

func ended(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

// dialog reads the dialog of key from the Store. The tests read it
// there because TS 24.229 5.2.6.3.3 item 8, 5.2.6.3.4, 5.2.6.4.3 and
// 5.2.6.4.4 name what the P-CSCF saves of a dialog, and the only message
// that shows it is the BYE of a release, which release_test.go checks
// for the parts it uses.
func (h *callHarness) dialog(key DialogKey) (Dialog, bool) {
	d, ok, _ := h.store.UpdateDialog(context.Background(), key, func(*Dialog) {})
	return d, ok
}

// waitNoDialog waits until the dialog of key is gone: the P-CSCF ends
// it after it has passed the response on.
func (h *callHarness) waitNoDialog(key DialogKey) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := h.dialog(key); !ok {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the dialog %+v is still in the Store", key)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// keyOf is the key under which the P-CSCF keeps the dialog of a call:
// the tag of the UE is the local tag of the UE's own dialog.
func keyOf(t *testing.T, d sip.Dialog, ok bool) DialogKey {
	t.Helper()
	if !ok {
		t.Fatal("the call has no dialog")
	}
	return DialogKey{CallID: d.ID.CallID, UETag: d.ID.LocalTag}
}

// TestDialogOfAnOriginatingCall is TS 24.229 5.2.6.3.3 item 8 and
// 5.2.6.3.4: what the P-CSCF saves of the INVITE and of its responses,
// and 5.2.8.2: the BYE ends it.
func TestDialogOfAnOriginatingCall(t *testing.T) {
	relay := newFakeRelay()
	h := newCallHarness(t, harnessConfig{relay: relay})
	out, in := h.call()
	// The 183 is reliable, so the UE sends a PRACK within the dialog.
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	ueDialog, ok := out.Dialog()
	key := keyOf(t, ueDialog, ok)
	early, ok := h.dialog(key)
	if !ok {
		t.Fatalf("no dialog under %+v after the 183", key)
	}
	if early.Confirmed || !early.Originating {
		t.Errorf("after the 183: confirmed %v, originating %v; want an early dialog of the caller", early.Confirmed, early.Originating)
	}
	if early.Caller.CSeq != 2 {
		t.Errorf("CSeq of the caller = %d, want 2 after the INVITE and the PRACK", early.Caller.CSeq)
	}
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	ok200 := answered(t, out, 200)
	waitACK(t, acked)

	d, _ := h.dialog(key)
	vector, _ := sip.ChargingVectorOf(in.Request)
	want := Dialog{
		Key: key, Originating: true, Confirmed: true, Acknowledged: true,
		Caller: Party{NameAddr: out.Request.Get("From"), Target: h.ueContact, CSeq: 2},
		Callee: Party{NameAddr: ok200.Get("To"), Target: h.coreContact},
		Relay:  &RelayContext{Relay: testRelay, ID: "1", Access: netip.MustParseAddrPort("127.0.0.1:42000"), Core: netip.MustParseAddrPort("127.0.0.1:42002")},
		// The icid-value that the P-CSCF wrote in the INVITE.
		ICID: vector.ICID,
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("dialog = %+v\nwant     %+v", d, want)
	}

	// The callee hangs up: the BYE passes to the UE, and the dialog and
	// its relay context go.
	if err := in.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the UE's call", out.Done())
	h.waitNoDialog(key)
	relay.waitHeld(t, 0, time.Second)
}

// TestDialogOfATerminatingCall is TS 24.229 5.2.6.4.3 item 3 and
// 5.2.6.4.4: the dialog of the called UE.
func TestDialogOfATerminatingCall(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	out, in := h.callUE()
	acked := accept(in, answerTo(t, in.Request, ueMedia))
	ok200 := answered(t, out, 200)
	waitACK(t, acked)

	ueDialog, ok := in.Dialog()
	key := keyOf(t, ueDialog, ok)
	d, ok := h.dialog(key)
	if !ok {
		t.Fatalf("no dialog under %+v", key)
	}
	want := Dialog{
		Key: key, Confirmed: true, Acknowledged: true,
		Caller: Party{NameAddr: out.Request.Get("From"), Target: h.coreContact, CSeq: 1},
		Callee: Party{NameAddr: ok200.Get("To"), Target: h.ueContact},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("dialog = %+v\nwant     %+v", d, want)
	}

	// The UE hangs up with the first request it sends in the dialog.
	if err := in.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the caller's call", out.Done())
	h.waitNoDialog(key)
}

// TestRejectedCallLeavesNoDialog: the early dialog of a 183 ends with
// the 486 (RFC 3261 12.3), and so does what the P-CSCF kept of it.
func TestRejectedCallLeavesNoDialog(t *testing.T) {
	relay := newFakeRelay()
	h := newCallHarness(t, harnessConfig{relay: relay})
	out, in := h.call()
	if err := in.Progress(183, answerTo(t, in.Request, coreMedia)); err != nil {
		t.Fatal(err)
	}
	ueDialog, ok := out.Dialog()
	key := keyOf(t, ueDialog, ok)
	if _, ok := h.dialog(key); !ok {
		t.Fatal("no dialog after the 183")
	}
	in.Reject(486)
	answered(t, out, 486)
	h.waitNoDialog(key)
	relay.waitHeld(t, 0, time.Second)
}

// TestTerminatingDialogKeepsTheRouteToTheCaller is TS 24.229 5.2.6.4.3
// item 2: the Record-Route of the INVITE, below the P-CSCF's own value,
// is the route for a request to the caller.
func TestTerminatingDialogKeepsTheRouteToTheCaller(t *testing.T) {
	const scscfTerm, scscfOrig = "<sip:term@scscf." + domain + ":5060;lr>", "<sip:orig@scscf." + domain + ":5060;lr>"
	store := NewMemoryStore()
	ue := newStack(t, func(st *sip.ServerTransaction) {
		// A UAS copies the Record-Route into the response that creates
		// the dialog (RFC 3261 12.1.1).
		ok := sip.NewResponse(st.Request, 200, "ue")
		for _, r := range st.Request.GetAll("Record-Route") {
			ok.Add("Record-Route", r)
		}
		ok.Add("Contact", "<sip:001010000000001@"+st.Request.Get("X-UE")+">")
		st.Respond(ok)
	})
	contact := "sip:001010000000001@" + ue.LocalAddr().String()
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locatorFunc(func(string) []sip.Destination { return nil }), store, nil, quiet())
	p.store.PutAssociation(context.Background(), Association{Source: sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()}, Contact: contact,
		PrivateIdentity: impi, PublicIdentities: []string{impu}, Expires: time.Now().Add(time.Hour)})
	pcscf := newPCSCFStack(t, p)
	p.SetStack(pcscf)

	network := newStack(t, func(*sip.ServerTransaction) {})
	m := invite(contact, "<sip:term@pcscf."+domain+":5060;lr>")
	m.Add("Record-Route", scscfTerm)
	m.Add("Record-Route", scscfOrig)
	m.Add("X-UE", ue.LocalAddr().String())
	ct, err := network.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	if r := final(t, ct.Responses(), 3*time.Second); r.StatusCode != 200 {
		t.Fatalf("got %d, want 200", r.StatusCode)
	}
	d, ok, _ := store.UpdateDialog(context.Background(), DialogKey{CallID: m.Get("Call-ID"), UETag: "ue"}, func(*Dialog) {})
	if !ok {
		t.Fatal("no dialog of the called UE")
	}
	if want := []string{scscfTerm, scscfOrig}; !slices.Equal(d.Caller.Route, want) {
		t.Errorf("route to the caller = %v, want %v", d.Caller.Route, want)
	}
	if len(d.Callee.Route) != 0 {
		t.Errorf("route to the called UE = %v, want none", d.Callee.Route)
	}
}

func TestRouteSets(t *testing.T) {
	// A call between two UEs of the same P-CSCF: the INVITE passes P,
	// the S-CSCF twice, and P again.
	const p, s1, s2 = "<sip:pcscf;lr>", "<sip:orig@scscf;lr>", "<sip:term@scscf;lr>"
	recordRoute := []string{p, s2, s1, p}
	cases := []struct {
		name               string
		upstream           int
		toCaller, toCallee []string
	}{
		{"originating pass", 0, nil, []string{s1, s2, p}},
		{"terminating pass", 3, []string{s2, s1, p}, nil},
	}
	for _, c := range cases {
		toCaller, toCallee, ok := routeSets(recordRoute, c.upstream)
		if !ok || !slices.Equal(toCaller, c.toCaller) || !slices.Equal(toCallee, c.toCallee) {
			t.Errorf("%s: to the caller %v, to the callee %v, ok %v; want %v and %v", c.name, toCaller, toCallee, ok, c.toCaller, c.toCallee)
		}
	}
	if _, _, ok := routeSets([]string{s1}, 1); ok {
		t.Error("a Record-Route without the P-CSCF's own value was accepted")
	}
}
