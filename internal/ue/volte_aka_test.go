package ue_test

import (
	"context"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// newVoLTEParty is newIMSParty with the VoLTE codecs, preconditions and
// the header fields of IR.92.
func newVoLTEParty(t *testing.T, pcscf netip.AddrPort, private, public, pass string, usim *ue.AKA) *party {
	t.Helper()
	p := &party{calls: make(chan *sip.IncomingCall, 1), wire: &wireLog{}}
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), p.wire.observe)
	if err != nil {
		t.Fatal(err)
	}
	addr := tr.LocalAddr()
	p.contact = ue.NewContact(addr)
	calls := ue.CallConfig{Codecs: voLTECodecs(t)}
	var u atomic.Pointer[ue.UE]
	p.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy:  addr.String(),
		Contact: p.contact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { p.calls <- ic },
		Locate:  func(sip.URI) (sip.Destination, error) { return u.Load().Outbound(), nil },
	}, calls), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.ua.Close)
	cfg := ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Calls: calls}
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
	if p.reg, err = p.ue.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestVoLTECallWithIMSAKA: the VoLTE INVITE is larger than 1300 bytes,
// so a UE with IMS AKA sends it over TCP to the protected server port
// of the P-CSCF (RFC 3261 18.1.1), which knows the UE by its Via.
func TestVoLTECallWithIMSAKA(t *testing.T) {
	n, _ := akaIMS(t)
	a := newVoLTEParty(t, n.pcscf, impiAKA, impuAKA, "", &ue.AKA{Secret: testSet1, SQN: aka.NewSQN(5000, 1)})
	b := newVoLTEParty(t, n.pcscf, impi2, impu2, password2, nil)
	answered := answerIn(t, b, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	speech := ue.ToneSpeech(300 * time.Millisecond)
	if err := a.ue.Call(context.Background(), a.ua, a.reg, impu2, a.media, speech, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	if got := b.media.ReceivedFrames(); !sameFrames(got, speech.AMRWB) {
		t.Errorf("UE-B received %d AMR-WB frames, want the %d of the tone", len(got), len(speech.AMRWB))
	}
}

// TestVoLTEContactCarriesNoIMSI is TS 24.229 5.1.1.2.1 item c: the user
// part of the contact that a UE registers is globally unique and reveals
// nothing private. A phone without an ISIM registers a temporary public
// identity made from its IMSI (C.2), so the user part of that identity
// would not do, in REGISTER or in the Contact of its calls. The refresh
// and the removal of the binding keep the user part (5.1.1.4.1 item c).
func TestVoLTEContactCarriesNoIMSI(t *testing.T) {
	const imsi = "001010000000010"
	n, _ := akaIMS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := newVoLTEParty(t, n.pcscf, impiAKA, impuAKA, "", &ue.AKA{Secret: testSet1, SQN: aka.NewSQN(5000, 1)})
	b := newVoLTEParty(t, n.pcscf, impi2, impu2, password2, nil)
	answered := answerIn(t, b, ue.Speech{}, ue.AnswerOptions{PickUp: time.After(0)})
	if err := a.ue.Call(ctx, a.ua, a.reg, impu2, a.media, ue.ToneSpeech(100*time.Millisecond), ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	registered, ok := n.scscfStore.Registration(impiAKA)
	if !ok {
		t.Fatal("the S-CSCF has no binding of the VoLTE UE")
	}
	invite, err := sip.ParseNameAddr(a.wire.find(t, "> INVITE").Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	for what, contact := range map[string]string{"binding at the S-CSCF": registered.Contact, "INVITE": invite.URI} {
		if u, err := sip.ParseURI(contact); err != nil || u.User == "" || strings.Contains(u.User, imsi) {
			t.Errorf("the Contact of the %s is %q, want a user part without the IMSI %s", what, contact, imsi)
		}
	}
	if _, err := a.ue.Register(ctx); err != nil {
		t.Fatalf("re-registration: %v", err)
	}
	if r, _ := n.scscfStore.Registration(impiAKA); r.Contact != registered.Contact {
		t.Errorf("the refresh bound %q, want the contact of the initial registration %q", r.Contact, registered.Contact)
	}
	if err := a.ue.Deregister(ctx); err != nil {
		t.Errorf("deregistration: %v", err)
	}
}
