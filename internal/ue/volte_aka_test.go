package ue_test

import (
	"context"
	"net/netip"
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
	p.contact = ue.NewContact(public, addr)
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
