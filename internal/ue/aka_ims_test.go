package ue_test

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
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
	var u atomic.Pointer[ue.UE]
	p := &party{calls: make(chan *sip.IncomingCall, 1), contact: ue.NewContact(addr)}
	var err error
	p.ua, err = sip.NewUA(tr, sip.UAConfig{
		SentBy:    addr.String(),
		Contact:   p.contact.URI(),
		OnCall:    func(ic *sip.IncomingCall) { p.calls <- ic },
		OnMessage: func(st *sip.ServerTransaction) { u.Load().ReceiveSMS(st) },
		Locate:    func(sip.URI) (sip.Destination, error) { return u.Load().Outbound(), nil },
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.ua.Close)
	cfg := ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Features: ue.DefaultFeatures, SCAddress: scAddress}
	if usim != nil {
		c := *usim
		c.Unprotected = stack(t, func(*sip.ServerTransaction) {})
		cfg.AKA = &c
	}
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
	protectedAddr := a.ua.Stack().LocalAddr()
	if sas, ok := n.pcscfStore.SecurityAssociations(protectedAddr); !ok || sas.Temporary || sas.PrivateIdentity != impiAKA {
		t.Errorf("the P-CSCF's SA table has %+v, %v; want established associations", sas, ok)
	}
	if r, ok := n.scscfStore.Registration(impiAKA); !ok || r.Contact != a.contact.URI() {
		t.Errorf("the S-CSCF registered %+v, %v", r, ok)
	}
	// TS 24.229 5.2.3: the P-CSCF subscribes to the registration that
	// names its protected server port in Path.
	n.pcscfLog.waitFor(t, map[string]string{"public_identity": sipAKA, "state": "active", "contact_event": "registered"})
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
	if sas, ok := n.pcscfStore.SecurityAssociations(protectedAddr); ok {
		t.Errorf("the P-CSCF keeps %+v after the deregistration", sas)
	}
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
	if sas, ok := n.pcscfStore.SecurityAssociations(a.ua.Stack().LocalAddr()); ok {
		t.Errorf("the P-CSCF keeps %+v", sas)
	}
	if _, ok := n.scscfStore.Registration(impiAKA); ok {
		t.Error("the S-CSCF registered the UE")
	}
}
