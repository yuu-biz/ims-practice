package ue_test

import (
	"context"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// newEPCParty is a party that attaches to the EPC stub of n first, as
// "ue -epc" does: it takes the P-CSCF from the PCO, its cell for
// P-Access-Network-Info, and the voice bearer as the resources of its
// calls.
func newEPCParty(t *testing.T, n testNetwork, private, public, pass string) *party {
	t.Helper()
	imsi, err := ue.IMSIOf(private)
	if err != nil {
		t.Fatal(err)
	}
	access := ue.NewEPCAccess(n.epc, imsi, quiet())
	att, err := access.Attach(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { access.Detach(context.Background()) })
	resources, _ := ue.FollowAccess(access)
	// The PCO gives the address only; the port is the P-CSCF's.
	pcscfAddr := netip.AddrPortFrom(att.PCSCFs[0], n.pcscf.Port())
	calls := ue.CallConfig{Codecs: voLTECodecs(t), Resources: resources}

	p := &party{calls: make(chan *sip.IncomingCall, 1), wire: &wireLog{}}
	tr, err := sip.ListenObserved(netip.AddrPortFrom(att.Address, 0).String(), quiet(), p.wire.observe)
	if err != nil {
		t.Skipf("cannot listen on %v, the PDN address of the UE: %v", att.Address, err)
	}
	addr := tr.LocalAddr()
	cfg := ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscfAddr, PCSCFURI: "sip:pcscf." + domain + ":5060", AccessNetworkInfo: att.AccessNetworkInfo, Calls: calls}
	p.contact = ue.NewContact(cfg, addr)
	p.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy:  addr.String(),
		Contact: p.contact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { p.calls <- ic },
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: pcscfAddr}}, nil
		},
	}, calls), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.ua.Close)
	p.ue = ue.New(cfg, p.ua.Stack(), p.contact, quiet())
	if p.media, err = ue.ListenMedia(netip.AddrPortFrom(att.Address, 0).String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.media.Close() })
	if p.reg, err = p.ue.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p
}

func voiceBearer(t *testing.T, api, imsi string) bool {
	t.Helper()
	resp, err := http.Get(api + "/ues/" + imsi)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st epc.Status
	decodeBody(t, resp, &st)
	for _, b := range st.Bearers {
		if b.EPSQoS.QCI == epc.QCIConversationalVoice && b.State == epc.BearerActive {
			return true
		}
	}
	return false
}

// TestVoLTECallOverTheEPCStub is the VoLTE call with nothing
// played by the test: both UEs attach to the EPC stub, the P-CSCF has
// the PCRF authorise the media of each side from the answer in the 183
// (TS 29.214 4.4.1), the stub sets up a voice bearer for each UE, the
// caller reports its reservation in an UPDATE, the callee alerts, and
// the bearers go with the call (TS 29.214 4.4.4).
func TestVoLTECallOverTheEPCStub(t *testing.T) {
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, epc: true})
	caller := newEPCParty(t, n, impi, impu, password)
	callee := newEPCParty(t, n, impi2, impu2, password2)
	answered := answerIn(t, callee, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	speech := ue.ToneSpeech(500 * time.Millisecond)
	if err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, speech, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	caller.wire.inOrder(t, "> INVITE", "< 183 INVITE", "> PRACK", "> UPDATE", "< 180 INVITE", "< 200 INVITE", "> ACK", "> BYE")
	callee.wire.inOrder(t, "< INVITE", "> 183 INVITE", "< UPDATE", "> 200 UPDATE", "> 180 INVITE", "> 200 INVITE")
	if err := sdpHas(caller.wire.find(t, "> UPDATE"), "a=curr:qos local sendrecv"); err != nil {
		t.Error("UPDATE: ", err)
	}
	if pani := caller.wire.find(t, "> INVITE").Get("P-Access-Network-Info"); pani != ue.AccessNetworkInfo(stubCell) {
		t.Errorf("P-Access-Network-Info %q", pani)
	}
	sent, received := caller.media.SentFrames(), callee.media.ReceivedFrames()
	if len(sent) != 25 || !sameFrames(sent, speech.AMRWB) || !sameFrames(received, sent) {
		t.Errorf("the caller sent %d AMR-WB frames and the callee received %d; want the 25 of the tone", len(sent), len(received))
	}
	for _, imsi := range []string{"001010000000001", "001010000000002"} {
		waitFor(t, "the voice bearer of "+imsi+" released after the call", func() bool { return !voiceBearer(t, n.epc, imsi) })
	}
}

// TestNetworkEndsTheCallWhenItTakesTheBearer is IR.92 2.4.2.3 and TS
// 24.229 5.2.8.1.2: the stub releases the caller's voice bearer, the
// PCRF tells the P-CSCF, and the P-CSCF ends the call with a BYE to
// each side. The UE has stopped its media and ends the call that way,
// without a BYE of its own.
func TestNetworkEndsTheCallWhenItTakesTheBearer(t *testing.T) {
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, epc: true})
	caller := newEPCParty(t, n, impi, impu, password)
	callee := newEPCParty(t, n, impi2, impu2, password2)
	answered := answerIn(t, callee, ue.ToneSpeech(10*time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(10*time.Second), ue.CallOptions{})
	}()
	waitFor(t, "media", func() bool {
		return callee.media.Stats().PacketsReceived > 2 && voiceBearer(t, n.epc, "001010000000001")
	})
	req, _ := http.NewRequest(http.MethodDelete, n.epc+"/network-initiated/ues/001010000000001/bearers/6", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := wait(t, "the call", called); err != nil {
		t.Errorf("Call ended with %v, want the network's BYE", err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Error(err)
	}
	if bye := caller.wire.find(t, "< BYE"); bye.Get("Reason") == "" {
		t.Error("the network's BYE says not why")
	}
	for _, l := range caller.wire.labels() {
		if l == "> BYE" {
			t.Error("the caller sent a BYE of its own besides the network's")
		}
	}
}
