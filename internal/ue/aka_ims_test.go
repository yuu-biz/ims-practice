package ue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// The tests of this file run IMS AKA through the whole IMS: the HSS
// with the vectors, the S-CSCF with the challenge, the P-CSCF with the
// security agreement and its protected server port, and the UE with its
// USIM.

// The AKA subscriber has a USIM and no ISIM. Its UE registers impuAKA,
// the temporary public identity derived from the IMSI, which the HSS
// bars (TS 23.228 E.3.1); its default public identity is sipAKA, the SIP
// URI of its number.
const (
	impiAKA = "001010000000010@" + domain
	impuAKA = "sip:001010000000010@" + domain
	sipAKA  = "sip:+12025550110@" + domain
	telAKA  = "tel:+12025550110"
)

// testSet1 is K and OPc of MILENAGE test set 1 (TS 35.208 4.3.1), as the
// AKA subscriber of deploy/compose/hss.yaml has them.
var testSet1 = aka.Secret{
	K:   [16]byte{0x46, 0x5b, 0x5c, 0xe8, 0xb1, 0x99, 0xb4, 0x9f, 0xaa, 0x5f, 0x0a, 0x2e, 0xe2, 0x38, 0xa6, 0xbc},
	OPc: [16]byte{0xcd, 0x63, 0xcb, 0x71, 0x95, 0x4a, 0x9f, 0x4e, 0x48, 0xa5, 0x99, 0x4e, 0x37, 0xa0, 0x2b, 0xaf},
}

// akaIMS is the IMS with the AKA subscriber and UE-B, who has a
// password, both served by the fake IP-SM-GW.
func akaIMS(t *testing.T) (testNetwork, *fakeIPSMGW) {
	t.Helper()
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour,
		subscriptions: []hss.Subscription{
			{PrivateIdentity: impiAKA, PublicIdentities: []string{impuAKA, sipAKA, telAKA}, BarredIdentities: []string{impuAKA},
				AKA: &hss.AKACredentials{Secret: testSet1}},
			{PrivateIdentity: impi2, PublicIdentities: []string{impu2, telB}, Password: password2},
		},
		criteria: []ifc.InitialFilterCriteria{moSMSCriterion()},
	})
	return n, newFakeIPSMGW(t, n)
}

// newIMSParty is a UE with a UA of its own that takes calls and short
// messages, not yet registered. With usim it registers with IMS AKA: the
// UA is then on the protected server port, and a stack of its own sends
// the REGISTERs that no security association protects.
func newIMSParty(t *testing.T, pcscf netip.AddrPort, private, public, pass string, usim *ue.AKA) *party {
	t.Helper()
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	cfg := ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Features: ue.DefaultFeatures, SCAddress: scAddress}
	if usim != nil {
		c := *usim
		c.Unprotected = stack(t, func(*sip.ServerTransaction) {})
		cfg.AKA = &c
	}
	var u atomic.Pointer[ue.UE]
	p := &party{calls: make(chan *sip.IncomingCall, 1), contact: ue.NewContact(cfg, addr)}
	var err error
	p.ua, err = sip.NewUA(tr, sip.UAConfig{
		SentBy:    addr.String(),
		Contact:   p.contact.URI(),
		OnCall:    func(ic *sip.IncomingCall) { p.calls <- ic },
		OnMessage: func(st *sip.ServerTransaction) { u.Load().ReceiveSMS(st) },
		Locate:    func(sip.URI) ([]sip.Destination, error) { return []sip.Destination{u.Load().Outbound()}, nil },
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.ua.Close)
	u.Store(ue.New(cfg, p.ua.Stack(), p.contact, quiet()))
	p.ue = u.Load()
	if p.media, err = ue.ListenMedia("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.media.Close() })
	return p
}

// TS 33.203 6.1 and 7 through every node: a UE whose USIM is ahead of
// the HSS registers after one re-synchronisation (6.1.3), then calls
// and sends a short message to a UE that registered with SIP Digest, and
// follows its registration state, all over the protected server port;
// its deregistration ends the security associations.
func TestIMSAKAThroughTheWholeIMS(t *testing.T) {
	n, g := akaIMS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The first vector of the HSS has SQN 1 with IND 1, which this USIM
	// has seen beaten.
	a := newIMSParty(t, n.pcscf, impiAKA, impuAKA, "", &ue.AKA{Secret: testSet1, SQN: aka.NewSQN(5000, 1)})
	reg, err := a.ue.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.reg = reg
	// TS 24.229 5.1.1.2.1 items b and c: the barred identity that the UE
	// registered is not among those it may use.
	if got := reg.PublicIdentities; len(got) != 2 || reg.DefaultIdentity() != sipAKA || got[1] != telAKA {
		t.Errorf("P-Associated-URI gave %v, want %s first and %s", got, sipAKA, telAKA)
	}
	if got := a.ue.Outbound(); got.Addr != n.pcscfProtected {
		t.Errorf("the UE sends to %s, want the protected server port %s", got.Addr, n.pcscfProtected)
	}
	// The entry of the SA table is what ESP would use, which this
	// project does not set up, so no message shows it.
	protectedAddr := a.ua.Stack().LocalAddr()
	if sas, ok, _ := n.pcscfStore.SecurityAssociations(context.Background(), protectedAddr); !ok || sas.Temporary || sas.PrivateIdentity != impiAKA {
		t.Errorf("the P-CSCF's SA table has %+v, %v; want established associations", sas, ok)
	}
	// TS 24.229 5.2.3: the P-CSCF subscribes to the registration that
	// names its protected server port in Path, and the NOTIFY names the
	// contact that the S-CSCF registered.
	n.pcscfLog.waitFor(t, map[string]string{"public_identity": sipAKA, "state": "active", "contact_event": "registered",
		"contact": a.contact.URI()})
	// A refresh goes over the associations and needs no new challenge.
	if _, err := a.ue.Register(ctx); err != nil {
		t.Fatalf("re-registration: %v", err)
	}

	b := newIMSParty(t, n.pcscf, impi2, impu2, password2, nil)
	if b.reg, err = b.ue.Register(ctx); err != nil {
		t.Fatal(err)
	}

	// A call from the UE with IMS AKA to the one with SIP Digest.
	answered := make(chan error, 1)
	var caller, asserted string
	go func() {
		ic := <-b.calls
		caller, asserted = ic.Request.Get("From"), ic.Request.Get("P-Asserted-Identity")
		answered <- b.ue.Answer(ctx, ic, b.media, ue.Speech{PCM: ue.Tone(880, 500*time.Millisecond)}, ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	if err := a.ue.Call(ctx, a.ua, a.reg, impu2, a.media, ue.Speech{PCM: ue.Tone(440, 500*time.Millisecond)}, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, answered, "the end of the call at UE-B"); err != nil {
		t.Fatal(err)
	}
	// The caller is the default public identity, in From as the UE wrote
	// it (5.1.2A.1.1) and in P-Asserted-Identity as the P-CSCF asserted
	// it (5.2.6.3.1); the temporary public identity stays out of sight.
	if !strings.Contains(caller, "<"+sipAKA+">") || !strings.Contains(asserted, "<"+sipAKA+">") {
		t.Errorf("UE-B got From %q and P-Asserted-Identity %q, want %s in both", caller, asserted, sipAKA)
	}
	if got, _ := b.media.Received(); !bytes.Equal(got, a.media.Sent()) {
		t.Errorf("UE-B got %d bytes of media, want the %d that UE-A sent", len(got), len(a.media.Sent()))
	}

	// A short message the other way round of the IP-SM-GW: the submit
	// report reaches UE-A over its protected server port.
	sent, err := a.ue.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: "Hello from the USIM"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, g.submissions, "submission")
	waitSent(t, a.ue, sent.CallID, func(s ue.SentSMS) bool { return s.State == ue.SubmitAccepted })
	if h, err := b.ue.WaitSMS(ctx, func(h ue.SMSHistory) bool { return len(h.Received) == 1 }); err != nil || h.Received[0].From != "+12025550110" {
		t.Errorf("UE-B received %+v (%v)", h.Received, err)
	}

	// The reg event package (TS 24.229 5.1.1.3).
	ev, err := a.ue.SubscribeRegEvent(a.ua, a.reg)
	if err != nil {
		t.Fatal(err)
	}
	if s := nextState(t, ev); s.Registration != reginfo.Active || s.Contact != reginfo.Active {
		t.Errorf("first NOTIFY to UE-A: %+v", s)
	}

	if err := a.ue.Deregister(ctx); err != nil {
		t.Fatalf("deregistration: %v", err)
	}
	noSecurityAssociations(t, n, a, "the deregistration")
	if got := a.ue.Outbound(); got.Addr != n.pcscf {
		t.Errorf("after the deregistration the UE sends to %s, want the unprotected port", got.Addr)
	}
}

// TS 33.203 6.1.2.2: a USIM with another K finds the MAC of the
// challenge wrong, reports it, and gets 403; no security associations
// remain.
func TestIMSAKAWithAnotherK(t *testing.T) {
	n, _ := akaIMS(t)
	stranger := testSet1
	stranger.K[15] ^= 0xff
	a := newIMSParty(t, n.pcscf, impiAKA, impuAKA, "", &ue.AKA{Secret: stranger})
	_, err := a.ue.Register(context.Background())
	if !errors.Is(err, ue.ErrForbidden) || !errors.Is(err, aka.ErrMACFailure) {
		t.Fatalf("err = %v, want ErrForbidden after ErrMACFailure", err)
	}
	noSecurityAssociations(t, n, a, "the 403")
	if err := n.scscf.Deregister(impiAKA, reginfo.Rejected); err == nil {
		t.Error("the S-CSCF registered the UE")
	}
}

// noSecurityAssociations checks that the P-CSCF has no security
// associations with the UE of p: a REGISTER of its contact from its
// protected port to the protected server port of the P-CSCF is refused
// with 403, as ESP would drop it (TS 33.203 7.1 item 3). Over security
// associations, the S-CSCF would refresh the registration with 200
// (TS 24.229 5.4.1.2.2).
func noSecurityAssociations(t *testing.T, n testNetwork, p *party, after string) {
	t.Helper()
	from := p.ua.Stack()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impuAKA+">;tag="+sip.NewTag())
	m.Add("To", "<"+impuAKA+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<"+p.contact.URI()+">;expires=600000")
	m.Add("Require", sip.OptionSecAgree)
	m.Add("Proxy-Require", sip.OptionSecAgree)
	m.Add("Security-Client", fmt.Sprintf("ipsec-3gpp;alg=hmac-sha2-256-128;ealg=null;spi-c=1111;spi-s=2222;port-c=50001;port-s=%d",
		from.LocalAddr().Port()))
	m.Add("Authorization", `Digest username="`+impiAKA+`", realm="`+domain+`", nonce="", uri="sip:`+domain+`", response="", algorithm=AKAv1-MD5`)
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: n.pcscfProtected})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	status := 0
	if final != nil {
		status = final.StatusCode
	}
	if status != 403 {
		t.Errorf("protected REGISTER after %s got %d, want 403", after, status)
	}
}

// TestRestartedUERefreshesItsBinding is a VoLTE UE that switches off
// without deregistering and starts again on the same addresses, as a UE
// container does when it restarts. It registers the contact it had, so
// the S-CSCF refreshes the binding instead of binding a new contact and
// deregistering the old one (TS 24.229 5.1.1.2.1 NOTE 13), and the
// refresh goes over the security associations of the new registration.
func TestRestartedUERefreshesItsBinding(t *testing.T) {
	n, _ := akaIMS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first := openAKAUE(t, n.pcscf, "127.0.0.1:0", "127.0.0.1:0")
	if _, err := first.ue.Register(ctx); err != nil {
		t.Fatal(err)
	}
	bound := first.wire.binding(t)
	first.close()

	restarted := openAKAUE(t, n.pcscf, first.protected.String(), first.unprotected.String())
	if _, err := restarted.ue.Register(ctx); err != nil {
		t.Fatalf("registration after the restart: %v", err)
	}
	if _, err := restarted.ue.Register(ctx); err != nil {
		t.Fatalf("refresh after the restart: %v", err)
	}
	if r := restarted.wire.binding(t); r.URI != bound.URI {
		t.Errorf("the S-CSCF binds %s, want the contact from before the restart, %s", r.URI, bound.URI)
	}
}

// akaUE is a UE of the AKA subscriber that a test can stop and start
// again on the same addresses.
type akaUE struct {
	ue *ue.UE
	// protected is the protected server port, the port of the contact;
	// unprotected is where the REGISTERs without protection leave from.
	protected, unprotected netip.AddrPort
	// wire has the messages of the protected server port.
	wire  *wireLog
	close func()
}

// openAKAUE opens the UE's two stacks on protected and unprotected, as
// "ue -k" does with -port-s and -listen.
func openAKAUE(t *testing.T, pcscf netip.AddrPort, protected, unprotected string) akaUE {
	t.Helper()
	unprotectedTransport, err := sip.Listen(unprotected, quiet())
	if err != nil {
		t.Fatal(err)
	}
	unprotectedStack, err := sip.NewStack(unprotectedTransport, sip.Config{SentBy: unprotectedTransport.LocalAddr().String(),
		OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireLog{}
	tr, err := sip.ListenObserved(protected, quiet(), wire.observe)
	if err != nil {
		unprotectedStack.Close()
		t.Fatal(err)
	}
	cfg := ue.Config{PrivateIdentity: impiAKA, PublicIdentity: impuAKA, HomeDomain: domain, PCSCF: pcscf,
		PCSCFURI: "sip:pcscf." + domain + ":5060",
		AKA:      &ue.AKA{Secret: testSet1, SQN: aka.NewSQN(5000, 1), Unprotected: unprotectedStack}}
	contact := ue.NewContact(cfg, tr.LocalAddr())
	var u atomic.Pointer[ue.UE]
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy:  tr.LocalAddr().String(),
		Contact: contact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { ic.Reject(486) },
		Locate:  func(sip.URI) ([]sip.Destination, error) { return []sip.Destination{u.Load().Outbound()}, nil },
	}, quiet())
	if err != nil {
		unprotectedStack.Close()
		t.Fatal(err)
	}
	stop := func() {
		ua.Close()
		unprotectedStack.Close()
	}
	t.Cleanup(stop)
	u.Store(ue.New(cfg, ua.Stack(), contact, quiet()))
	return akaUE{ue: u.Load(), protected: tr.LocalAddr(), unprotected: unprotectedTransport.LocalAddr(), wire: wire, close: stop}
}

// newAKAPhone is "ue serve" for the AKA subscriber. unprotected has the
// messages of the stack that sends the REGISTERs that no security
// association protects; the wire of the phone has those of its
// protected server port.
func newAKAPhone(t *testing.T, n testNetwork) (p *phone, unprotected *wireLog) {
	t.Helper()
	unprotected = &wireLog{}
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), unprotected.observe)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	cfg := ue.Config{PrivateIdentity: impiAKA, PublicIdentity: impuAKA, HomeDomain: domain, PCSCF: n.pcscf,
		PCSCFURI: "sip:pcscf." + domain + ":5060", Features: ue.DefaultFeatures, SCAddress: scAddress,
		AKA: &ue.AKA{Secret: testSet1, Unprotected: s}}
	return servePhone(t, cfg, autoAnswer), unprotected
}

// TestServeWithIMSAKARegistersAgainUnprotectedWhenDeactivated is TS
// 24.229 5.1.1.7 for a UE with IMS AKA. The NOTIFY that ends the
// registration has the UE delete its security associations towards the
// P-CSCF, as the P-CSCF ends its own (5.2.5.2). The initial
// registration that deactivated asks for is therefore unprotected, with
// an empty nonce and response (5.1.1.2.2), and the UE answers a new
// challenge over a new security agreement (5.1.1.5.1).
func TestServeWithIMSAKARegistersAgainUnprotectedWhenDeactivated(t *testing.T) {
	n, _ := akaIMS(t)
	p, unprotected := newAKAPhone(t, n)
	events := p.events()
	waitForUESubscription(t, p)
	if err := n.scscf.Deregister(impiAKA, reginfo.Deactivated); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "registered false", func(s statusJSON) bool { return !s.Registered })
	// A UE that kept its associations would get 403 and wait 30 s or
	// more before it tried unprotected (5.1.1.2.1).
	if !p.within(5*time.Second, func(s statusJSON) bool { return s.Registered }) {
		t.Error("not registered again within 5 s of the deactivation")
	}
	for _, w := range []*wireLog{p.wire, unprotected} {
		if w.has("< 403 REGISTER") {
			t.Errorf("a REGISTER was refused with 403; REGISTERs %v", w.registerLabels())
		}
	}

	open := unprotected.registers()
	if len(open) != 4 || open[2].label != "> REGISTER" || open[3].label != "< 401 REGISTER" {
		t.Fatalf("the unprotected port carried %v, want a REGISTER and its 401 for each initial registration",
			unprotected.registerLabels())
	}
	again := open[2].message
	creds, err := sip.ParseCredentials(again.Get("Authorization"))
	if err != nil || creds.Nonce != "" || creds.Response != "" {
		t.Errorf("the REGISTER after the deactivation has Authorization %q, want an empty nonce and response",
			again.Get("Authorization"))
	}
	if v := again.GetAll("Security-Verify"); len(v) != 0 {
		t.Errorf("the REGISTER after the deactivation has Security-Verify %v, want none", v)
	}

	protected := p.wire.registers()
	if len(protected) < 2 || protected[len(protected)-2].label != "> REGISTER" || protected[len(protected)-1].label != "< 200 REGISTER" {
		t.Fatalf("the protected server port carried %v, want the answer to the new challenge and its 200 last",
			p.wire.registerLabels())
	}
	answer := protected[len(protected)-2].message
	if got, want := answer.GetAll("Security-Verify"), open[3].message.GetAll("Security-Server"); !slices.Equal(got, want) {
		t.Errorf("the answer to the new challenge has Security-Verify %v, want the Security-Server of its 401, %v", got, want)
	}
}
