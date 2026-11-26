package ue_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/amr"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

func voLTECodecs(t *testing.T) []sdp.Codec {
	t.Helper()
	codecs, err := ue.CodecsNamed(ue.VoLTECodecNames)
	if err != nil {
		t.Fatal(err)
	}
	return codecs
}

// answerIn has callee answer its next call after ring, in a goroutine;
// the returned channel delivers how Answer ended.
func answerIn(t *testing.T, callee *party, speech ue.Speech, opts ue.AnswerOptions) <-chan error {
	t.Helper()
	answered := make(chan error, 1)
	go func() {
		select {
		case ic := <-callee.calls:
			answered <- callee.ue.Answer(context.Background(), ic, callee.media, speech, opts)
		case <-time.After(5 * time.Second):
			answered <- context.DeadlineExceeded
		}
	}()
	return answered
}

func wait(t *testing.T, what string, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not end", what)
		return nil
	}
}

func sameFrames(a, b []amr.Frame) bool { return slices.EqualFunc(a, b, sameFrame) }

// TestVoLTECallWithPreconditions is the call of IR.92 2.4.1 and TS
// 24.229 5.1.3.1, 5.1.4.1 and 6.1 between two UEs on an access network
// that sets up the resources for media once the P-CSCFs have seen the
// answer (L.2.2.5.1 NOTE 3); the test plays that network. The callee
// alerts only after the caller's UPDATE tells it that both ends have
// their resources, and the AMR-WB frames arrive bit for bit.
func TestVoLTECallWithPreconditions(t *testing.T) {
	addr := network(t, time.Second)
	codecs := voLTECodecs(t)
	callerResources, calleeResources := ue.NewResources(), ue.NewResources()
	caller := newPartyWith(t, addr, impi, impu, password, ue.CallConfig{Codecs: codecs, Resources: callerResources})
	callee := newPartyWith(t, addr, impi2, impu2, password2, ue.CallConfig{Codecs: codecs, Resources: calleeResources})

	answered := answerIn(t, callee, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	called := make(chan error, 1)
	speech := ue.ToneSpeech(500 * time.Millisecond)
	go func() {
		called <- caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, speech, ue.CallOptions{})
	}()

	invite := callee.wire.find(t, "< INVITE")
	callee.wire.find(t, "> 183 INVITE")
	calleeResources.Set(true)
	caller.wire.find(t, "< 183 INVITE")
	alerted := func() bool { return callee.wire.has("> 180 INVITE") }
	if came(watch, alerted) {
		t.Error("the callee alerted before the caller had its resources")
	}
	callerResources.Set(true)
	if !came(watch, alerted) {
		t.Errorf("the callee did not alert within %v of the caller having its resources", watch)
	}
	if err := wait(t, "the call", called); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}

	// TS 24.229 5.1.3.1: Supported, not Require.
	if !has(invite, "Supported", sip.OptionPrecondition) || invite.Requires(sip.OptionPrecondition) || !has(invite, "Supported", sip.Option100rel) {
		t.Errorf("INVITE Supported %q, Require %q", invite.GetAll("Supported"), invite.GetAll("Require"))
	}
	// TS 24.229 6.1.2, with the codecs in the order of sdp.VoLTECodecs.
	if err := sdpHas(invite, "m=audio "+itoa(caller.media.Port())+" RTP/AVP 96 97 98 99 0 100 101",
		"a=curr:qos local none", "a=curr:qos remote none", "a=des:qos mandatory local sendrecv", "a=des:qos optional remote sendrecv"); err != nil {
		t.Error("INVITE: ", err)
	}
	progress := caller.wire.find(t, "< 183 INVITE")
	// TS 24.229 5.1.4.1, 6.1.3: one codec, and the callee asks the caller
	// to confirm its resources.
	if !progress.Requires(sip.OptionPrecondition) || !progress.Requires(sip.Option100rel) {
		t.Errorf("183 Require %q", progress.GetAll("Require"))
	}
	if err := sdpHas(progress, "m=audio "+itoa(callee.media.Port())+" RTP/AVP 96 100", "a=curr:qos remote none",
		"a=des:qos mandatory remote sendrecv", "a=conf:qos remote sendrecv"); err != nil {
		t.Error("183: ", err)
	}
	update := caller.wire.find(t, "> UPDATE")
	// IR.92 2.4.1: only the codec of the 183.
	if err := sdpHas(update, "m=audio "+itoa(caller.media.Port())+" RTP/AVP 96 100", "a=curr:qos local sendrecv"); err != nil || !update.Requires(sip.OptionPrecondition) {
		t.Errorf("UPDATE Require %q: %v", update.GetAll("Require"), err)
	}
	// TS 24.229 5.1.3.1 NOTE 5: the 180 may overtake the 200 to the
	// UPDATE on the way to the caller.
	caller.wire.inOrder(t, "> INVITE", "< 183 INVITE", "> PRACK", "< 200 PRACK", "> UPDATE", "< 180 INVITE", "> PRACK", "< 200 INVITE", "> ACK", "> BYE", "< 200 BYE")
	caller.wire.inOrder(t, "> UPDATE", "< 200 UPDATE")
	callee.wire.inOrder(t, "< INVITE", "> 183 INVITE", "< PRACK", "< UPDATE", "> 200 UPDATE", "> 180 INVITE", "> 200 INVITE", "< ACK")

	sent, received := caller.media.SentFrames(), callee.media.ReceivedFrames()
	if len(sent) != 25 || !sameFrames(sent, speech.AMRWB) || !sameFrames(received, sent) {
		t.Errorf("the caller sent %d AMR-WB frames and the callee received %d; want the 25 of the tone", len(sent), len(received))
	}
	if back := caller.media.ReceivedFrames(); len(back) == 0 {
		t.Error("the caller heard nothing from the callee")
	}
}

// TestUpdateWaitsForThe2xxToThePRACK has the caller's resources come
// up as soon as its INVITE is out, so the caller owes the UPDATE (TS
// 24.229 5.1.3.1) the moment the 183 arrives. The PRACK of the 183 is
// small and goes over UDP, the UPDATE over TCP (RFC 3261 18.1.1): sent
// together, the UPDATE can reach the callee first, and the callee has
// to answer the PRACK, the older request, with 500 (RFC 3261 12.2.2).
// So the caller sends the UPDATE once the PRACK has its 2xx.
func TestUpdateWaitsForThe2xxToThePRACK(t *testing.T) {
	addr := network(t, time.Second)
	codecs := voLTECodecs(t)
	callerResources, calleeResources := ue.NewResources(), ue.NewResources()
	calleeResources.Set(true)
	caller := newPartyWith(t, addr, impi, impu, password, ue.CallConfig{Codecs: codecs, Resources: callerResources})
	callee := newPartyWith(t, addr, impi2, impu2, password2, ue.CallConfig{Codecs: codecs, Resources: calleeResources})

	answered := answerIn(t, callee, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(100*time.Millisecond),
		ue.CallOptions{Session: func(*ue.Session) { callerResources.Set(true) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	caller.wire.inOrder(t, "< 183 INVITE", "> PRACK", "< 200 PRACK", "> UPDATE", "< 180 INVITE", "> PRACK", "< 200 INVITE")
	callee.wire.inOrder(t, "> 183 INVITE", "< PRACK", "> 200 PRACK", "< UPDATE")
	// IR.92 2.6: the UE sends its PRACKs itself, and with them its
	// User-Agent, as with every other request.
	if prack := caller.wire.find(t, "> PRACK"); prack.Get("User-Agent") != ue.UserAgent {
		t.Errorf("PRACK User-Agent %q", prack.Get("User-Agent"))
	}
}

// TestCallToAPeerWithoutPreconditionsOrAMR is the interworking of RFC
// 3312 and IR.92 2.4.1 with a softphone: the callee answers PCMU without
// Require: precondition, so the caller drops the mechanism and sends no
// UPDATE, and the call goes on in PCMU.
func TestCallToAPeerWithoutPreconditionsOrAMR(t *testing.T) {
	addr := network(t, time.Second)
	callerResources := ue.NewResources()
	caller := newPartyWith(t, addr, impi, impu, password, ue.CallConfig{Codecs: voLTECodecs(t), Resources: callerResources})
	callee := newPartyWith(t, addr, impi2, impu2, password2, ue.CallConfig{NoPreconditions: true})

	answered := answerIn(t, callee, ue.Speech{PCM: ue.Tone(880, time.Second)}, ue.AnswerOptions{PickUp: time.After(0)})
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media,
			ue.Speech{PCM: ue.Tone(440, 300*time.Millisecond)}, ue.CallOptions{})
	}()
	caller.wire.find(t, "< 183 INVITE")
	callerResources.Set(true)
	if err := wait(t, "the call", called); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	if invite := callee.wire.find(t, "< INVITE"); !has(invite, "Supported", sip.OptionPrecondition) {
		t.Error("the caller did not offer preconditions")
	}
	if progress := caller.wire.find(t, "< 183 INVITE"); progress.Requires(sip.OptionPrecondition) {
		t.Error("the callee without preconditions required them")
	} else if err := sdpHas(progress, "m=audio "+itoa(callee.media.Port())+" RTP/AVP 0 101"); err != nil {
		t.Error("183: ", err)
	}
	if slices.Contains(caller.wire.labels(), "> UPDATE") {
		t.Errorf("the caller sent an UPDATE to a callee without preconditions: %v", caller.wire.labels())
	}
	got, packets := callee.media.Received()
	if packets != 15 || string(got) != string(caller.media.Sent()) {
		t.Errorf("the callee got %d packets of PCMU; want the 15 the caller sent", packets)
	}
}

// TestCalleeWithoutPreconditionsWaitsForItsResources is IR.92 2.4.1: a
// caller that does not use preconditions gets a reliable 183 with the
// answer, and the 180 only once the callee has its resources.
func TestCalleeWithoutPreconditionsWaitsForItsResources(t *testing.T) {
	addr := network(t, time.Second)
	calleeResources := ue.NewResources()
	caller := newPartyWith(t, addr, impi, impu, password, ue.CallConfig{NoPreconditions: true})
	callee := newPartyWith(t, addr, impi2, impu2, password2, ue.CallConfig{Codecs: voLTECodecs(t), Resources: calleeResources})

	answered := answerIn(t, callee, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(100*time.Millisecond), ue.CallOptions{})
	}()
	progress := caller.wire.find(t, "< 183 INVITE")
	if progress.Requires(sip.OptionPrecondition) || !progress.Requires(sip.Option100rel) || len(progress.Body) == 0 {
		t.Errorf("183 Require %q with %d octets of SDP; want a reliable 183 with the answer, without preconditions",
			progress.GetAll("Require"), len(progress.Body))
	}
	alerted := func() bool { return caller.wire.has("< 180 INVITE") }
	if came(watch, alerted) {
		t.Fatal("180 before the callee had its resources")
	}
	calleeResources.Set(true)
	if !came(watch, alerted) {
		t.Errorf("no 180 within %v of the callee having its resources", watch)
	}
	if err := wait(t, "the call", called); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	caller.wire.inOrder(t, "< 183 INVITE", "< 180 INVITE", "< 200 INVITE")
}

func itoa(n uint16) string { return strconv.Itoa(int(n)) }

// TestCallCarriesTheHeadersOfIR92 checks the header fields of a call
// that IR.92 and TS 24.229 ask of a UE: User-Agent and Server (IR.92
// 2.6), the ICSI of MMTel and audio in Contact (2.2.4), P-Preferred-
// Service and Accept-Contact (TS 24.229 5.1.2A.1.1), P-Access-Network-
// Info, the session timer (IR.92 2.2.8) and the Reason of the BYE
// (2.2.4). What the UA sends by itself carries them too: the ACK and the
// 200 to the BYE.
func TestCallCarriesTheHeadersOfIR92(t *testing.T) {
	addr := network(t, time.Second)
	pani := ue.AccessNetworkInfo(stubCell)
	caller := newPartyTweaked(t, addr, impi, impu, password, ue.CallConfig{}, func(c *sip.UAConfig) { c.OwnHeaders = ue.OwnHeaders(pani) })
	caller.ue = ue.New(ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain,
		PCSCF: addr, PCSCFURI: "sip:pcscf." + domain + ":5060", AccessNetworkInfo: pani}, caller.ua.Stack(), caller.contact, quiet())
	if _, err := caller.ue.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	callee := newParty(t, addr, impi2, impu2, password2)
	answered := answerIn(t, callee, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	if err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(100*time.Millisecond), ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
	invite := callee.wire.find(t, "< INVITE")
	contact, _ := sip.ParseNameAddr(invite.Get("Contact"))
	for _, h := range []struct{ name, want string }{
		{"User-Agent", ue.UserAgent},
		{"Accept-Contact", `*;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`},
		// The caller's cell stays in the network (TS 24.229 4.4.3).
		{"P-Access-Network-Info", ""},
		{"Session-Expires", "1800"},
	} {
		if got := invite.Get(h.name); got != h.want {
			t.Errorf("INVITE %s %q, want %q", h.name, got, h.want)
		}
	}
	// The S-CSCF turns the P-Preferred-Service of the caller into a
	// P-Asserted-Service (TS 24.229 5.4.3.2 step 4C).
	if got := caller.wire.find(t, "> INVITE").Get("P-Access-Network-Info"); got != pani {
		t.Errorf("caller's INVITE P-Access-Network-Info %q, want %q", got, pani)
	}
	if got := caller.wire.find(t, "> INVITE").Get("P-Preferred-Service"); got != ims.ICSIMMTel {
		t.Errorf("caller's INVITE P-Preferred-Service %q, want %q", got, ims.ICSIMMTel)
	}
	if got := invite.Get("P-Preferred-Service"); got != "" {
		t.Errorf("callee's INVITE P-Preferred-Service %q, want none", got)
	}
	if _, audio := contact.Params.Get("audio"); !audio || !has(invite, "Supported", sip.OptionTimer) {
		t.Errorf("INVITE Contact %q, Supported %q", invite.Get("Contact"), invite.GetAll("Supported"))
	}
	ok := caller.wire.find(t, "< 200 INVITE")
	if ok.Get("Server") != ue.UserAgent || ok.Get("Session-Expires") != "1800;refresher=uac" {
		t.Errorf("200 Server %q, Session-Expires %q", ok.Get("Server"), ok.Get("Session-Expires"))
	}
	if c, _ := sip.ParseNameAddr(ok.Get("Contact")); c.Params.String() != ue.DialogContactParams.String() {
		t.Errorf("200 Contact %q", ok.Get("Contact"))
	}
	if ack := caller.wire.find(t, "> ACK"); ack.Get("User-Agent") != ue.UserAgent || ack.Get("P-Access-Network-Info") != pani {
		t.Errorf("ACK User-Agent %q, P-Access-Network-Info %q", ack.Get("User-Agent"), ack.Get("P-Access-Network-Info"))
	}
	bye := callee.wire.find(t, "< BYE")
	if got := bye.Get("Reason"); got != `RELEASE_CAUSE;cause=1;text="User ends call"` {
		t.Errorf("BYE Reason %q", got)
	}
	if ok := callee.wire.find(t, "> 200 BYE"); ok.Get("Server") != ue.UserAgent {
		t.Errorf("200 to the BYE: Server %q", ok.Get("Server"))
	}
	caller.wire.mu.Lock()
	defer caller.wire.mu.Unlock()
	var register *sip.Message
	for _, m := range caller.wire.messages {
		if m.label == "> REGISTER" {
			register = m.message // the last one is that of the UE with the cell
		}
	}
	if register.Get("User-Agent") != ue.UserAgent || register.Get("P-Access-Network-Info") != pani {
		t.Errorf("REGISTER User-Agent %q, P-Access-Network-Info %q", register.Get("User-Agent"), register.Get("P-Access-Network-Info"))
	}
}

// TestOptionsTellTheCapabilitiesOfTheUE is RFC 3261 11.2 and IR.92
// 2.2.9: 200 with the ICSI in Contact while the line is free, and 486
// while it holds a call.
func TestOptionsTellTheCapabilitiesOfTheUE(t *testing.T) {
	line := ue.NewLine(true)
	tr := listenTransport(t)
	ua, err := sip.NewUA(tr, ue.UAConfig(sip.UAConfig{SentBy: tr.LocalAddr().String(), Contact: "sip:ue@" + tr.LocalAddr().String(),
		OnCall: line.Ring, OnOptions: line.Options}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	options := func() *sip.Message {
		t.Helper()
		m := &sip.Message{Method: sip.OPTIONS, RequestURI: "sip:ue@" + tr.LocalAddr().String()}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<sip:caller@example.org>;tag="+sip.NewTag())
		m.Add("To", "<sip:ue@example.org>")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "1 OPTIONS")
		s := stack(t, func(*sip.ServerTransaction) {})
		ct, err := s.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: tr.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		var final *sip.Message
		for r := range ct.Responses() {
			final = r
		}
		return final
	}
	r := options()
	if r == nil || r.StatusCode != 200 || !strings.Contains(r.Get("Contact"), "+g.3gpp.icsi-ref") || !has(r, "Supported", sip.OptionPrecondition) {
		t.Fatalf("OPTIONS got %v", r)
	}
	caller := newTestUA(t, "caller", func(ic *sip.IncomingCall) { ic.Reject(486) })
	if _, err := caller.Invite(sip.Invitation{RequestURI: "sip:ue@" + tr.LocalAddr().String(), From: impu}); err != nil {
		t.Fatal(err)
	}
	<-line.Calls()
	if r := options(); r == nil || r.StatusCode != 486 {
		t.Errorf("OPTIONS while busy got %v, want 486", r)
	}
}

// TestLossOfTheMediaBearerEndsTheCall is IR.92 2.4.2.3 and TS 24.229
// 6.1.1: when the access network takes the voice bearer away during a
// call, the UE stops its media and, as the network does not end the
// call here, ends it itself with a BYE that says why.
func TestLossOfTheMediaBearerEndsTheCall(t *testing.T) {
	ue.SetBearerLossGrace(t, 200*time.Millisecond)
	addr := network(t, time.Second)
	codecs := voLTECodecs(t)
	callerResources, calleeResources := ue.NewResources(), ue.NewResources()
	callerResources.Set(true)
	calleeResources.Set(true)
	caller := newPartyWith(t, addr, impi, impu, password, ue.CallConfig{Codecs: codecs, Resources: callerResources})
	callee := newPartyWith(t, addr, impi2, impu2, password2, ue.CallConfig{Codecs: codecs, Resources: calleeResources})
	answered := answerIn(t, callee, ue.ToneSpeech(10*time.Second), ue.AnswerOptions{PickUp: time.After(0)})
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(10*time.Second), ue.CallOptions{})
	}()
	waitFor(t, "media", func() bool { return callee.media.Stats().PacketsReceived > 2 })
	callerResources.Set(false)
	if err := wait(t, "the call", called); !errors.Is(err, ue.ErrMediaBearerLost) {
		t.Errorf("Call ended with %v, want ErrMediaBearerLost", err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Error(err)
	}
	if got := callee.wire.find(t, "< BYE").Get("Reason"); got != `RELEASE_CAUSE;cause=3;text="Media bearer loss"` {
		t.Errorf("BYE Reason %q", got)
	}
}

// TestSessionTellsWhetherTheAccessNetworkReserves calls from a UE whose
// access network reserves the resources for media, as the EPC does with
// a dedicated bearer (IR.92 2.4.1), to one whose access network needs no
// reservation, as a softphone's LAN (TS 24.229 6.1.2). Both have the
// resources once the call is up; only the first needed them reserved.
func TestSessionTellsWhetherTheAccessNetworkReserves(t *testing.T) {
	addr := network(t, time.Second)
	codecs := voLTECodecs(t)
	callerResources := ue.NewResources()
	callerResources.Set(true)
	caller := newPartyWith(t, addr, impi, impu, password, ue.CallConfig{Codecs: codecs, Resources: callerResources})
	callee := newPartyWith(t, addr, impi2, impu2, password2, ue.CallConfig{Codecs: codecs})
	sessions := make(chan *ue.Session, 2)
	keep := func(s *ue.Session) { sessions <- s }
	ctx, hangUp := context.WithCancel(context.Background())
	answered := answerIn(t, callee, ue.ToneSpeech(10*time.Second), ue.AnswerOptions{PickUp: time.After(0), Session: keep})
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(10*time.Second),
			ue.CallOptions{StayUp: true, Session: keep})
	}()
	waitFor(t, "media both ways", func() bool {
		return caller.media.Stats().PacketsReceived > 2 && callee.media.Stats().PacketsReceived > 2
	})
	byNeed := map[bool]ue.SessionStatus{}
	for range 2 {
		st := (<-sessions).Status()
		byNeed[st.ReservationNeeded] = st
	}
	if st, ok := byNeed[true]; !ok || !st.MediaResources {
		t.Errorf("the session over the access network that reserves: %+v, found %v", st, ok)
	}
	if st, ok := byNeed[false]; !ok || !st.MediaResources {
		t.Errorf("the session over the access network that needs no reservation: %+v, found %v", st, ok)
	}
	hangUp()
	wait(t, "the call", called)
	wait(t, "the answer", answered)
}
