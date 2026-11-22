package ibcf_test

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// audioOf is where the SDP of m sends the audio, and the address in its
// o= line.
func audioOf(t *testing.T, m *sip.Message) (media netip.AddrPort, origin netip.Addr) {
	t.Helper()
	s, err := sdp.Parse(m.Body)
	if err != nil {
		t.Fatalf("SDP of %s: %v", m.Get("CSeq"), err)
	}
	media, _, err = s.Audio()
	if err != nil {
		t.Fatal(err)
	}
	return media, s.Origin.Address
}

func codecNames(t *testing.T, m *sip.Message) []string {
	t.Helper()
	s, err := sdp.Parse(m.Body)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range s.Media[0].Codecs() {
		names = append(names, c.Name)
	}
	return names
}

func contactOf(t *testing.T, m *sip.Message) (host string, params sip.Params) {
	t.Helper()
	na, err := sip.ParseNameAddr(m.Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := sip.ParseURI(na.URI)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host, na.Params
}

// onlyContext is the one open context of a TrGW.
func onlyContext(t *testing.T, trgw *fakeTrGW) fakeContext {
	t.Helper()
	open, _ := trgw.open()
	if len(open) != 1 {
		t.Fatalf("%d TrGW contexts open, want 1", len(open))
	}
	return open[0]
}

// A call from network A to network B: INVITE with SDP, a reliable 183
// with the answer, 180, 200 and ACK; then a re-INVITE from the caller,
// an UPDATE from the callee and a BYE from the callee. Each network's
// IBCF is a B2BUA, so the call has three dialogs: UE A and IBCF A, IBCF
// A and IBCF B, IBCF B and UE B, each with its own Call-ID. Neither
// network receives anything that names the inside of the other, the
// SDP of each side names its own TrGW, and both TrGW contexts go with
// the BYE.
func TestCallAcrossTheBorder(t *testing.T) {
	p := newPair(t, nil)
	callerGot := make(chan *sip.Message, 2)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), callerGot)
	in := p.b.nextCall(t)
	calleeGot := make(chan *sip.Message, 2)
	in.Handle(p.b.answerInDialog(calleeGot))
	answer := p.b.answer(t, in.Request)
	if err := in.Progress(183, answer); err != nil {
		t.Fatalf("183: %v", err)
	}
	if err := in.Progress(180, sip.Body{}); err != nil {
		t.Fatalf("180: %v", err)
	}
	accepted := make(chan error, 1)
	go func() {
		_, err := in.Accept(answer)
		accepted <- err
	}()
	ok := final(t, call)
	if ok.StatusCode != 200 {
		t.Fatalf("the call got %d", ok.StatusCode)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("no ACK at the callee: %v", err)
	}

	ctxA, ctxB := onlyContext(t, p.a.trgw), onlyContext(t, p.b.trgw)
	// The offer at the callee names the core side of network B's TrGW,
	// and the answer at the caller the core side of network A's.
	if media, origin := audioOf(t, in.Request); media != ctxB.local.Core || origin != ctxB.local.Core.Addr() {
		t.Errorf("the callee's offer has media %s and origin %s, want TrGW B's core side %s", media, origin, ctxB.local.Core)
	}
	if media, origin := audioOf(t, ok); media != ctxA.local.Core || origin != ctxA.local.Core.Addr() {
		t.Errorf("the caller's answer has media %s and origin %s, want TrGW A's core side %s", media, origin, ctxA.local.Core)
	}
	// Each termination knows where its side takes the media: the TrGWs
	// send to each other over the II-NNI.
	wantA := map[ibcf.Side]netip.AddrPort{ibcf.CoreSide: p.a.media, ibcf.PeerSide: ctxB.local.Peer}
	wantB := map[ibcf.Side]netip.AddrPort{ibcf.CoreSide: netip.AddrPortFrom(p.b.media.Addr(), 50000), ibcf.PeerSide: ctxA.local.Peer}
	for side, want := range wantA {
		if got := ctxA.remote[side]; got != want {
			t.Errorf("TrGW A %s side sends to %s, want %s", side, got, want)
		}
	}
	for side, want := range wantB {
		if got := ctxB.remote[side]; got != want {
			t.Errorf("TrGW B %s side sends to %s, want %s", side, got, want)
		}
	}

	// The profile of network B takes no AMR (TS 29.165 7.1).
	if names := codecNames(t, in.Request); slices.Contains(names, sdp.NameAMR) || !slices.Contains(names, sdp.NameAMRWB) {
		t.Errorf("the callee was offered %v, want AMR-WB without AMR", names)
	}
	// Network B is not trusted (TS 24.229 4.4), and P-Charging-Function-
	// Addresses never crosses (5.10.2.2 step 9).
	for _, h := range []string{"P-Asserted-Identity", "P-Access-Network-Info", "P-Charging-Vector", "P-Charging-Function-Addresses"} {
		if v := in.Request.Get(h); v != "" {
			t.Errorf("the callee got %s: %s", h, v)
		}
	}

	// Three dialogs, three Call-IDs (TS 24.229 5.10.5).
	invites := p.b.rec.requests("ibcf1-external", sip.INVITE)
	if len(invites) == 0 {
		t.Fatal("network B's IBCF received no INVITE")
	}
	crossed := invites[0]
	callIDs := []string{call.Request.Get("Call-ID"), crossed.Get("Call-ID"), in.Request.Get("Call-ID")}
	if callIDs[0] == callIDs[1] || callIDs[1] == callIDs[2] || callIDs[0] == callIDs[2] {
		t.Errorf("Call-IDs of the three dialogs: %q, want three different ones", callIDs)
	}
	if tagOf(t, crossed.Get("From")) == tagOf(t, call.Request.Get("From")) {
		t.Error("the INVITE at the II-NNI has the caller's From tag")
	}
	// At the II-NNI the INVITE has the Via and Contact of network A's
	// IBCF instance only, and no Record-Route.
	_, exA := planA.instanceHosts(0)
	if vias := crossed.GetAll("Via"); len(vias) != 1 || !strings.Contains(vias[0], exA) {
		t.Errorf("Via at the II-NNI: %q", vias)
	}
	if rr := crossed.GetAll("Record-Route"); len(rr) != 0 {
		t.Errorf("Record-Route at the II-NNI: %q", rr)
	}
	// Each UE sees the IBCF of its own network in Contact, with the
	// feature tags of the far end (RFC 3840).
	inB, _ := planB.instanceHosts(0)
	inA, _ := planA.instanceHosts(0)
	for _, c := range []struct {
		what string
		m    *sip.Message
		want string
	}{{"the INVITE at the II-NNI", crossed, exA}, {"the callee's INVITE", in.Request, inB}, {"the caller's 200", ok, inA}} {
		host, params := contactOf(t, c.m)
		if host != c.want {
			t.Errorf("Contact of %s at %s, want %s", c.what, host, c.want)
		}
		if _, ok := params.Get("+g.3gpp.icsi-ref"); !ok {
			t.Errorf("Contact of %s lost the ICSI feature tag: %v", c.what, params)
		}
	}
	for _, c := range p.b.rec.received() {
		if c.node == "ibcf1-external" && c.Message.Method == sip.INVITE {
			// TS 24.229 4.2A: what is longer than 1300 bytes does not go
			// over UDP.
			t.Logf("the INVITE crossed over %s with %d bytes", c.Transport, len(c.Wire))
			if len(c.Wire) > 1300 && c.Transport != sip.TCP {
				t.Errorf("an INVITE of %d bytes crossed over %s", len(c.Wire), c.Transport)
			}
		}
	}

	// From here on the pool name of network A points at its second IBCF
	// instance, but the requests within the call go to the first one,
	// whose name the dialogs have in Contact: it holds them.
	p.a.use(1)
	resp, err := call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 2, "sendonly"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("re-INVITE: %v %v", resp, err)
	}
	if got := p.a.rec.requests("ibcf1-internal", sip.INVITE); len(got) != 2 {
		t.Errorf("the first IBCF instance received %d INVITEs, want the INVITE and the re-INVITE", len(got))
	}
	if got := p.a.rec.requests("ibcf2-internal", sip.INVITE); len(got) != 0 {
		t.Errorf("the second IBCF instance received %d INVITEs of a call it does not hold", len(got))
	}
	select {
	case reInvite := <-calleeGot:
		if media, _ := audioOf(t, reInvite); media != ctxB.local.Core {
			t.Errorf("the callee's new offer has media %s, want TrGW B's core side %s", media, ctxB.local.Core)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the re-INVITE did not reach the callee")
	}
	if media, _ := audioOf(t, resp); media != ctxA.local.Core {
		t.Errorf("the caller's new answer has media %s, want TrGW A's core side %s", media, ctxA.local.Core)
	}

	// An UPDATE with an offer from the callee (RFC 3311) crosses the
	// other way.
	resp, err = in.Update(p.b.offer(t, sdp.VoLTECodecs(), 3, ""))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("UPDATE: %v %v", resp, err)
	}
	select {
	case update := <-callerGot:
		if update.Method != sip.UPDATE {
			t.Errorf("the caller got %s, want UPDATE", update.Method)
		}
		if media, _ := audioOf(t, update); media != ctxA.local.Core {
			t.Errorf("the caller's offer in the UPDATE has media %s, want TrGW A's core side %s", media, ctxA.local.Core)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the UPDATE did not reach the caller")
	}

	if err := in.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the caller's call after the callee's BYE", call.Done())
	if got := p.a.rec.requests("ibcf1-external", sip.BYE); len(got) != 1 {
		t.Errorf("the first IBCF instance received %d BYEs, want 1", len(got))
	}
	if got := p.a.rec.requests("ue", sip.BYE); len(got) != 1 || got[0].Get("Call-ID") != callIDs[0] {
		t.Errorf("the caller got BYEs %v, want one in its own dialog", got)
	}
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each, one for each pass through an IBCF", reservedA, reservedB)
	}

	// A new call goes to the instance that the pool name points at.
	if resp, _ := p.callThrough(t, p.b.sipURI(), sdp.VoLTECodecs()); resp.StatusCode != 200 {
		t.Fatalf("the second call got %d", resp.StatusCode)
	}
	if got := p.a.rec.requests("ibcf2-internal", sip.INVITE); len(got) != 1 {
		t.Errorf("the second IBCF instance received %d INVITEs, want the one of the new call", len(got))
	}
	p.waitReleased(t)

	if got := p.a.counter(t, `ims_ibcf_session_attempts_total{peer="peer-b",direction="exit"}`); got != 2 {
		t.Errorf("network A counted %v sessions to network B, want 2", got)
	}
	if got := p.b.counter(t, `ims_ibcf_session_attempts_total{peer="peer-a",direction="entry"}`); got != 2 {
		t.Errorf("network B counted %v sessions from network A, want 2", got)
	}

	assertHidden(t, p.b, planA)
	assertHidden(t, p.a, planB)
	dumpCaptures(t, p.a)
	dumpCaptures(t, p.b)
}

func tagOf(t *testing.T, value string) string {
	t.Helper()
	na, err := sip.ParseNameAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// A MESSAGE from network B to network A (TS 29.165 Table 6.1: by
// agreement). It is a standalone request: the IBCF answers it with the
// final response of a MESSAGE of its own, and no TrGW is involved.
func TestMessageAcrossTheBorder(t *testing.T) {
	p := newPair(t, nil)
	if resp := p.b.message(t, p.a.sipURI(), "hello from network B"); resp.StatusCode != 200 {
		t.Fatalf("MESSAGE got %d", resp.StatusCode)
	}
	select {
	case m := <-p.a.messages:
		if string(m.Body) != "hello from network B" {
			t.Errorf("the UE got %q", m.Body)
		}
		if v := m.Get("P-Asserted-Identity"); v != "" {
			t.Errorf("an untrusted peer's P-Asserted-Identity came in: %s", v)
		}
		if v := m.Get("Record-Route"); v != "" {
			t.Errorf("a MESSAGE was record-routed: %s", v)
		}
		sent := p.b.rec.requests("ibcf1-internal", sip.MESSAGE)
		crossed := p.a.rec.requests("ibcf1-external", sip.MESSAGE)
		if len(sent) != 1 || len(crossed) != 1 {
			t.Fatalf("the IBCFs received %d and %d MESSAGEs, want 1 each", len(sent), len(crossed))
		}
		if ids := []string{sent[0].Get("Call-ID"), crossed[0].Get("Call-ID"), m.Get("Call-ID")}; ids[0] == ids[1] || ids[1] == ids[2] {
			t.Errorf("Call-IDs of the three MESSAGEs: %q", ids)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the MESSAGE did not reach the UE")
	}
	if got := p.a.rec.requests("ibcf1-external", sip.MESSAGE); len(got) == 1 && len(got[0].GetAll("Via")) != 1 {
		t.Errorf("Via of the MESSAGE at the II-NNI: %q", got[0].GetAll("Via"))
	}
	assertHidden(t, p.a, planB)
	assertHidden(t, p.b, planA)
}

// A MESSAGE that the client retransmits crosses the border once. The
// client is a socket of the test inside network B, which sends network
// B's IBCF the same MESSAGE three times. The second copy comes while
// the IBCF waits for the other network, as network A's UE holds its
// answer: the server transaction is in Trying and discards it (RFC 3261
// 17.2.2). The third comes after the final response: the server
// transaction is in Completed and sends that response again.
func TestRetransmittedMessageCrossesOnce(t *testing.T) {
	p := newPair(t, nil)
	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort(planB.internal+".98:0")))
	if err != nil {
		t.Fatalf("cannot listen on %s.98: %s: %v", planB.internal, noLoopbackAddresses, err)
	}
	defer conn.Close()
	client := conn.LocalAddr().(*net.UDPAddr).AddrPort()

	req := &sip.Message{Method: sip.MESSAGE, RequestURI: p.a.sipURI()}
	req.Add("Via", sip.Via{Transport: string(sip.UDP), Host: client.Addr().String(), Port: int(client.Port()),
		Params: sip.Params{{Name: "branch", Value: sip.NewBranch()}, {Name: "rport"}}}.String())
	req.Add("Route", sip.NameAddr{URI: looseRoute(p.b.ibcfInternal)}.String())
	req.Add("Max-Forwards", "70")
	req.Add("From", "<"+p.b.sipURI()+">;tag="+sip.NewTag())
	req.Add("To", "<"+p.a.sipURI()+">")
	req.Add("Call-ID", sip.NewCallID())
	req.Add("CSeq", "1 MESSAGE")
	req.Add("Content-Type", "text/plain")
	req.Body = []byte("only once")
	sendCopy := func() {
		t.Helper()
		if _, err := conn.WriteToUDPAddrPort(req.Bytes(), p.b.instances[0].internal); err != nil {
			t.Fatal(err)
		}
	}
	buf := make([]byte, 65535)
	response := func(what string) []byte {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			t.Fatalf("no response to %s: %v", what, err)
		}
		return slices.Clone(buf[:n])
	}
	copies := func() (n int) {
		for _, c := range p.b.rec.received() {
			if c.Source == client {
				n++
			}
		}
		return n
	}

	release := p.a.holdMessages()
	defer release()
	sendCopy()
	select {
	case <-p.a.messages:
	case <-time.After(5 * time.Second):
		t.Fatal("the MESSAGE did not reach the UE")
	}
	sendCopy()
	waitFor(t, "the second copy at network B's IBCF", func() bool { return copies() == 2 })
	release()
	first := response("the first two copies")
	sendCopy()
	again := response("the third copy")

	if resp, err := sip.Parse(first); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the client got %v (%v), want 200", resp, err)
	}
	if string(again) != string(first) {
		t.Errorf("the third copy got another response:\n%s\nafter:\n%s", again, first)
	}
	if got := p.b.rec.requests("ibcf1-internal", sip.MESSAGE); len(got) != 1 {
		t.Errorf("network B's IBCF received %d MESSAGEs in %d copies, want 1", len(got), copies())
	}
	if got := p.a.rec.requests("ibcf1-external", sip.MESSAGE); len(got) != 1 {
		t.Errorf("network B's IBCF sent %d MESSAGEs across the border, want 1", len(got))
	}
	if got := p.a.rec.requests("ue", sip.MESSAGE); len(got) != 1 {
		t.Errorf("network A's UE received %d MESSAGEs, want 1", len(got))
	}
	select {
	case m := <-p.a.messages:
		t.Errorf("network A's UE got a second MESSAGE: %q", m.Body)
	default:
	}
}

// callThrough has network A's UE call network B's UE, which answers
// when the call reaches it, and hangs up when the call is answered. It
// returns the final response and the INVITE that the callee got, or
// nil, after the callee's call has ended.
func (p *pair) callThrough(t *testing.T, requestURI string, codecs []sdp.Codec, headers ...sip.HeaderField) (*sip.Message, *sip.Message) {
	t.Helper()
	got := make(chan *sip.IncomingCall, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case in := <-p.b.calls:
			got <- in
			answer, err := answerTo(p.b, in.Request)
			if err != nil {
				in.Reject(488)
				return
			}
			in.Accept(answer)
		case <-done:
		}
	}()
	call := p.a.invite(t, requestURI, p.a.offer(t, codecs, 1, ""), nil, headers...)
	resp := final(t, call)
	var in *sip.IncomingCall
	select {
	case in = <-got:
	default:
	}
	if resp.StatusCode == 200 {
		if err := call.Bye(); err != nil {
			t.Fatalf("BYE: %v", err)
		}
		// The caller's BYE ends the dialogs on the other side of both
		// IBCFs.
		waitDone(t, "the callee's call after the caller's BYE", in.Done())
	}
	if in == nil {
		return resp, nil
	}
	return resp, in.Request
}

// Towards a trusted peer the trust domain's header fields cross (TS
// 24.229 4.4), the charging ones by policy (5.10.2.2 step 8); towards
// an untrusted one they do not (TestCallAcrossTheBorder).
// P-Charging-Function-Addresses crosses towards none (step 9).
func TestTrustedPeerGetsTheAssertedIdentity(t *testing.T) {
	p := newPair(t, func(ofB, ofA *interconnect.Profile) { ofB.Trusted, ofA.Trusted = true, true })
	resp, invite := p.callThrough(t, p.b.sipURI(), sdp.VoLTECodecs())
	if resp.StatusCode != 200 || invite == nil {
		t.Fatalf("the call got %d", resp.StatusCode)
	}
	if got, want := invite.Get("P-Asserted-Identity"), "<"+p.a.sipURI()+">"; got != want {
		t.Errorf("P-Asserted-Identity at the callee: %q, want %q", got, want)
	}
	for _, h := range []string{"P-Access-Network-Info", "P-Charging-Vector"} {
		if invite.Get(h) == "" {
			t.Errorf("%s did not reach the callee in the trust domain", h)
		}
	}
	if v := invite.Get("P-Charging-Function-Addresses"); v != "" {
		t.Errorf("P-Charging-Function-Addresses crossed: %s", v)
	}
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
}

// TestMismatchedProfiles: profiles that do not match make the call or the
// MESSAGE fail with the status that tells why, and the adjusted
// profiles let it through. The TrGW contexts that a failed call took go
// with its failure response.
func TestMismatchedProfiles(t *testing.T) {
	amrWB := slices.DeleteFunc(sdp.VoLTECodecs(), func(c sdp.Codec) bool { return c.Name != sdp.NameAMRWB })
	local := "sip:5550151;phone-context=+1-202@" + domainB + ";user=phone"
	cases := []struct {
		name string
		// mismatched and adjusted change network A's profile of B.
		mismatched, adjusted func(ofB *interconnect.Profile)
		run                  func(t *testing.T, p *pair) int
		want                 int
		// method is the method that network A's IBCF refuses itself.
		method sip.Method
	}{
		{
			// TS 29.165 Table 6.1: MESSAGE crosses by agreement only.
			name:       "MESSAGE outside the agreement",
			mismatched: func(ofB *interconnect.Profile) { ofB.Methods = nil },
			run: func(t *testing.T, p *pair) int {
				return p.a.message(t, p.b.sipURI(), "hello from network A").StatusCode
			},
			want: 405, method: sip.MESSAGE,
		},
		{
			// TS 29.165 7.1: no codec of the offer is in the agreement.
			name:       "no codec in common",
			mismatched: func(ofB *interconnect.Profile) { ofB.Codecs = []string{"PCMU"} },
			adjusted:   func(ofB *interconnect.Profile) { ofB.Codecs = []string{"AMR-WB", "telephone-event"} },
			run: func(t *testing.T, p *pair) int {
				resp, _ := p.callThrough(t, p.b.sipURI(), amrWB)
				return resp.StatusCode
			},
			want: 488, method: sip.INVITE,
		},
		{
			// TS 29.165 8.1: a local number crosses only once the
			// agreement says how it becomes global.
			name: "local number",
			adjusted: func(ofB *interconnect.Profile) {
				ofB.LocalNumbers = []interconnect.LocalNumberPlan{{PhoneContext: "+1-202", GlobalPrefix: "+1202"}}
			},
			run: func(t *testing.T, p *pair) int {
				resp, _ := p.callThrough(t, local, sdp.VoLTECodecs())
				return resp.StatusCode
			},
			want: 404, method: sip.INVITE,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tweak := func(change func(*interconnect.Profile)) func(ofB, ofA *interconnect.Profile) {
				return func(ofB, _ *interconnect.Profile) {
					if change != nil {
						change(ofB)
					}
				}
			}
			p := newPair(t, tweak(c.mismatched))
			if got := c.run(t, p); got != c.want {
				t.Errorf("with the mismatched profiles: %d, want %d", got, c.want)
			}
			series := `ims_ibcf_requests_rejected_total{peer="peer-b",direction="exit",method="` + string(c.method) + `",status="` + strconv.Itoa(c.want) + `"}`
			if got := p.a.counter(t, series); got != 1 {
				t.Errorf("%s = %v, want 1", series, got)
			}
			p.waitReleased(t)
			p = newPair(t, tweak(c.adjusted))
			if got := c.run(t, p); got != 200 {
				t.Errorf("with the adjusted profiles: %d, want 200", got)
			}
			p.waitReleased(t)
		})
	}
}

// The precondition mechanism is optional at the II-NNI (TS 29.165
// Table 6.1.3.1 item 20, Table C.3.1.2). Whether a call uses it is
// decided by its two ends: network B's UE does not support it, so it
// answers an INVITE that requires it with 420 (RFC 3261 8.2.2.3), and
// takes one that only supports it without the mechanism (RFC 3312 11).
// An agreement without the mechanism has network A's IBCF, the UAS of
// the caller's dialog, answer the 420 itself; an INVITE that only
// supports preconditions crosses as it came.
func TestPreconditionsOutsideTheAgreement(t *testing.T) {
	required := []sip.HeaderField{
		{Name: "Supported", Value: "100rel, timer, precondition"},
		{Name: "Require", Value: sip.OptionPrecondition},
	}
	supported := []sip.HeaderField{{Name: "Supported", Value: "100rel, timer, precondition"}}

	p := newPair(t, nil)
	if resp, _ := p.callThrough(t, p.b.sipURI(), sdp.VoLTECodecs(), required...); resp.StatusCode != 420 {
		t.Errorf("an INVITE that requires preconditions got %d, want the 420 of network B's UE", resp.StatusCode)
	}
	if got := p.a.counter(t, `ims_ibcf_session_failures_total{peer="peer-b",direction="exit",status="420"}`); got != 1 {
		t.Errorf("network A counted %v 420s that came back from network B, want 1", got)
	}
	p.waitReleased(t)

	p = newPair(t, func(ofB, _ *interconnect.Profile) {
		ofB.OptionTags = map[string]interconnect.OptionTagPolicy{sip.OptionPrecondition: interconnect.OptionReject}
	})
	resp, invite := p.callThrough(t, p.b.sipURI(), sdp.VoLTECodecs(), required...)
	if resp.StatusCode != 420 || resp.Get("Unsupported") != sip.OptionPrecondition || invite != nil {
		t.Errorf("an INVITE that requires preconditions outside the agreement got %d with Unsupported %q, and reached network B: %v",
			resp.StatusCode, resp.Get("Unsupported"), invite != nil)
	}
	if got := p.a.counter(t, `ims_ibcf_requests_rejected_total{peer="peer-b",direction="exit",method="INVITE",status="420"}`); got != 1 {
		t.Errorf("network A counted %v INVITEs it answered 420, want 1", got)
	}
	resp, invite = p.callThrough(t, p.b.sipURI(), sdp.VoLTECodecs(), supported...)
	if resp.StatusCode != 200 || invite == nil || !invite.Supports(sip.OptionPrecondition) {
		t.Errorf("an INVITE that supports preconditions got %d; the callee saw them supported: %v", resp.StatusCode, invite != nil && invite.Supports(sip.OptionPrecondition))
	}
	p.waitReleased(t)
}

// A CANCEL ends the INVITE with 487 on both sides of the border, and
// both TrGW contexts go.
func TestCancelReleasesTheTrGW(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	if err := in.Progress(180, sip.Body{}); err != nil {
		t.Fatalf("180: %v", err)
	}
	if err := call.Cancel(); err != nil {
		t.Fatal(err)
	}
	if resp := final(t, call); resp.StatusCode != 487 {
		t.Errorf("the cancelled INVITE got %d, want 487", resp.StatusCode)
	}
	waitDone(t, "the callee's call after the CANCEL", in.Done())
	if got := p.b.rec.requests("ibcf1-external", sip.CANCEL); len(got) != 1 {
		t.Errorf("network B's IBCF received %d CANCELs, want 1", len(got))
	}
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
	assertHidden(t, p.b, planA)
}

// A peer that never answers: the IBCF gives up after Timer B (RFC 3261
// 17.1.1.2), answers 504, and releases the context.
func TestTimeoutReleasesTheTrGW(t *testing.T) {
	p := newPair(t, nil)
	p.a.names.set(planB.nniName, netip.AddrPortFrom(netip.MustParseAddr("127.0.3.99"), uint16(p.b.ibcfExternal.Port)))
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	if resp := final(t, call); resp.StatusCode != 504 {
		t.Errorf("the INVITE got %d, want 504", resp.StatusCode)
	}
	if reservedA, _ := p.waitReleased(t); reservedA != 1 {
		t.Errorf("TrGW A reserved %d contexts, want 1", reservedA)
	}
	if got := p.a.counter(t, `ims_ibcf_requests_rejected_total{peer="peer-b",direction="exit",method="INVITE",status="504"}`); got != 1 {
		t.Errorf("network A counted %v INVITEs it answered 504, want 1", got)
	}
}

// When one leg of an answered call fails, the IBCF ends the other one:
// here network B's UE restarts and forgets the call, so the re-INVITE
// of the caller gets 481 on network B's side; network B's IBCF ends
// that leg (RFC 3261 12.2.1.2, 14.1) and sends a BYE on its other leg,
// and so does network A's IBCF on the way to the caller.
func TestFailedLegEndsTheOtherLeg(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	go in.Accept(p.b.answer(t, in.Request))
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the call got %d", resp.StatusCode)
	}
	p.b.restartUE(t)
	if resp, err := call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 2, "")); err == nil && resp.StatusCode < 300 {
		t.Errorf("the re-INVITE to a UE that forgot the call got %d", resp.StatusCode)
	}
	waitDone(t, "the caller's call after the callee's leg failed", call.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
}

// The IBCF sends to the peer over the transport that the agreement
// prefers (TS 29.165 6.2.1). A short MESSAGE goes over UDP when the
// agreement lists UDP first, and over TCP when it lists TCP alone.
func TestPreferredTransport(t *testing.T) {
	for _, transports := range [][]sip.Transport{{sip.UDP, sip.TCP}, {sip.TCP}} {
		p := newPair(t, func(ofB, _ *interconnect.Profile) { ofB.Transports = transports })
		if resp := p.a.message(t, p.b.sipURI(), "hi"); resp.StatusCode != 200 {
			t.Fatalf("MESSAGE got %d", resp.StatusCode)
		}
		var got []sip.Transport
		for _, c := range p.b.rec.transactions("ibcf1-external") {
			if c.Message.Method == sip.MESSAGE {
				got = append(got, c.Transport)
				if len(c.Wire) > 1300 {
					t.Fatalf("the MESSAGE has %d bytes, too long to show the preference", len(c.Wire))
				}
			}
		}
		if len(got) == 0 || got[0] != transports[0] {
			t.Errorf("agreement %v: the MESSAGE crossed over %v", transports, got)
		}
	}
}

// fromNoPeer is a MESSAGE that network B's UE sends straight to network
// A's IBCF, bypassing network B's IBCF: from an address of no peer.
func fromNoPeer(p *pair) *sip.Message {
	req := &sip.Message{Method: sip.MESSAGE, RequestURI: p.a.sipURI()}
	req.Add("Max-Forwards", "70")
	req.Add("From", "<"+p.b.sipURI()+">;tag="+sip.NewTag())
	req.Add("To", "<"+p.a.sipURI()+">")
	req.Add("Call-ID", sip.NewCallID())
	req.Add("CSeq", "1 MESSAGE")
	return req
}

// strangerRequests are the requests that a host at addr on the II-NNI,
// which is no peer of network A, sends to network A's IBCF: an INVITE,
// a CANCEL of it, an ACK as for a 2xx of it, and a MESSAGE.
func strangerRequests(t *testing.T, p *pair, addr netip.AddrPort) []*sip.Message {
	via := func() string {
		return sip.Via{Transport: string(sip.UDP), Host: addr.Addr().String(), Port: int(addr.Port()),
			Params: sip.Params{{Name: "branch", Value: sip.NewBranch()}, {Name: "rport"}}}.String()
	}
	request := func(method sip.Method, callID, cseq string) *sip.Message {
		m := &sip.Message{Method: method, RequestURI: p.a.sipURI()}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<"+p.b.sipURI()+">;tag=stranger")
		m.Add("To", "<"+p.a.sipURI()+">")
		m.Add("Call-ID", callID)
		m.Add("CSeq", cseq)
		return m
	}
	callID := sip.NewCallID()
	invite := request(sip.INVITE, callID, "1 INVITE")
	invite.Add("Via", via())
	invite.Add("Contact", "<sip:"+addr.String()+">")
	invite.Add("Content-Type", "application/sdp")
	invite.Body = p.b.offer(t, sdp.VoLTECodecs(), 1, "").Content
	// RFC 3261 9.1: the CANCEL has the Via of the INVITE it cancels.
	cancel := request(sip.CANCEL, callID, "1 CANCEL")
	cancel.Add("Via", invite.Get("Via"))
	ack := request(sip.ACK, callID, "1 ACK")
	ack.Set("To", "<"+p.a.sipURI()+">;tag=never-sent")
	ack.Add("Via", via())
	message := request(sip.MESSAGE, sip.NewCallID(), "1 MESSAGE")
	message.Add("Via", via())
	message.Add("Content-Type", "text/plain")
	message.Body = []byte("hi")
	return []*sip.Message{invite, cancel, ack, message}
}

// A border answers nothing to a host that is no peer of it, so that a
// sender outside every agreement learns nothing about the network. A
// host on the II-NNI sends network A's IBCF an INVITE twice, a CANCEL,
// an ACK and a MESSAGE. Nothing comes back to it, not even the 100
// (Trying) of the INVITE or the 481 of the CANCEL; nothing enters
// network A; and the IBCF counts each copy as discarded. Network B's
// IBCF, a peer, goes on as before.
func TestRequestFromNoPeerIsDiscarded(t *testing.T) {
	p := newPair(t, nil)
	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.3.99:0")))
	if err != nil {
		t.Fatalf("cannot listen on 127.0.3.99: %s: %v", noLoopbackAddresses, err)
	}
	defer conn.Close()
	stranger := conn.LocalAddr().(*net.UDPAddr).AddrPort()
	requests := strangerRequests(t, p, stranger)
	ibcfA := p.a.instances[0].external
	for i, req := range append([]*sip.Message{requests[0]}, requests...) {
		// The first INVITE goes twice: a retransmission, from the
		// stranger's point of view, of a request that got no answer.
		if _, err := conn.WriteToUDPAddrPort(req.Bytes(), ibcfA); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	// The MESSAGE of network B crosses through its IBCF to the same
	// socket of network A's IBCF after the stranger's requests. The
	// stack handles what that socket receives in order, so once the
	// MESSAGE has its answer, the stack has already dropped each of the
	// stranger's requests.
	if resp := p.b.message(t, p.a.sipURI(), "from the peer"); resp.StatusCode != 200 {
		t.Fatalf("the MESSAGE from network B, a peer, got %d", resp.StatusCode)
	}
	conn.SetReadDeadline(time.Now().Add(10 * testTimers.T1))
	buf := make([]byte, 65535)
	if n, from, err := conn.ReadFromUDPAddrPort(buf); err == nil {
		t.Errorf("the stranger got an answer from %s:\n%s", from, buf[:n])
	}

	// Only the external side of network A's IBCF saw the stranger's
	// requests. Inside network A the IBCF would have given a request a
	// Call-ID of its own, so what tells that none entered is that the
	// only request inside is the MESSAGE of the peer.
	var atBorder int
	for _, c := range p.a.rec.received() {
		switch {
		case c.Source == stranger:
			if c.node != "ibcf1-external" {
				t.Errorf("%s of network A received %s from the stranger", c.node, c.Message.Method)
			}
			atBorder++
		case c.Message.IsRequest() && !strings.HasSuffix(c.node, "-external") &&
			(c.Message.Method != sip.MESSAGE || string(c.Message.Body) != "from the peer"):
			t.Errorf("%s of network A received %s %s", c.node, c.Message.Method, c.Message.Get("CSeq"))
		}
	}
	if atBorder != 5 {
		t.Errorf("network A's IBCF received %d of the stranger's requests, want all 5", atBorder)
	}
	for method, want := range map[sip.Method]float64{sip.INVITE: 2, sip.CANCEL: 1, sip.ACK: 1, sip.MESSAGE: 1} {
		series := `ims_ibcf_requests_discarded_total{reason="unknown_source",method="` + string(method) + `"}`
		if got := p.a.counter(t, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
	for _, status := range []string{"403", "481", "500"} {
		for _, method := range []sip.Method{sip.INVITE, sip.CANCEL, sip.MESSAGE} {
			series := `ims_ibcf_requests_rejected_total{peer="other",direction="entry",method="` + string(method) + `",status="` + status + `"}`
			if got := p.a.counter(t, series); got != 0 {
				t.Errorf("%s = %v, want 0", series, got)
			}
		}
	}
	if _, reserved := p.a.trgw.open(); reserved != 0 {
		t.Errorf("network A's TrGW reserved %d contexts for the stranger", reserved)
	}
	if inA, _ := planA.instanceHosts(0); len(p.a.callsOf(t, inA)) != 0 {
		t.Errorf("network A's IBCF keeps a call of the stranger: %+v", p.a.callsOf(t, inA))
	}
}

// Screen lets the requests of a peer through, and turns away and counts
// those of any other address.
func TestScreen(t *testing.T) {
	p := newPair(t, nil)
	b := p.a.ibcfs[0]
	req := fromNoPeer(p)
	if !b.Screen(req, sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("127.0.3.11:5060")}) {
		t.Error("a request from network B's IBCF was turned away")
	}
	if b.Screen(req, sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("127.0.2.10:5060")}) {
		t.Error("a request from network B's UE was let through")
	}
	ack := fromNoPeer(p)
	ack.Method = sip.ACK
	b.Screen(ack, sip.Destination{Transport: sip.TCP, Addr: netip.MustParseAddrPort("192.0.2.7:5060")})
	for series, want := range map[string]float64{
		`ims_ibcf_requests_discarded_total{reason="unknown_source",method="MESSAGE"}`: 1,
		`ims_ibcf_requests_discarded_total{reason="unknown_source",method="ACK"}`:     1,
	} {
		if got := p.a.counter(t, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
}

// An IBCF instance that starts again under the same name ends the
// calls that its earlier process left: it releases their TrGW
// contexts, and sends a BYE in each of their two dialogs, from what the
// Store kept of them (TS 24.229 5.10.2.4).
func TestRestartedInstanceEndsTheCallsItLeft(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	go in.Accept(p.b.answer(t, in.Request))
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the call got %d", resp.StatusCode)
	}
	// The call has its two dialogs in the record once the 2xx crossed.
	inA, _ := planA.instanceHosts(0)
	deadline := time.Now().Add(5 * time.Second)
	for {
		records := p.a.callsOf(t, inA)
		if len(records) == 1 && records[0].Home != nil && records[0].PeerSide != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("records of the instance: %+v", records)
		}
		time.Sleep(10 * time.Millisecond)
	}

	p.a.restartIBCF(t, 0)
	waitDone(t, "the caller's call", call.Done())
	waitDone(t, "the callee's call", in.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
	if records := p.a.callsOf(t, inA); len(records) != 0 {
		t.Errorf("records left after the restart: %+v", records)
	}
	// The BYEs went in the dialogs of the call, with a CSeq above the
	// last one of each dialog.
	byes := p.a.rec.requests("ue", sip.BYE)
	if len(byes) != 1 || byes[0].Get("Call-ID") != call.Request.Get("Call-ID") {
		t.Fatalf("the caller got BYEs %v, want one in its dialog", byes)
	}
	// The new process takes new calls.
	if resp, _ := p.callThrough(t, p.b.sipURI(), sdp.VoLTECodecs()); resp.StatusCode != 200 {
		t.Errorf("a call after the restart got %d", resp.StatusCode)
	}
}

// The record of a call follows the target refreshes of its dialogs (TS
// 24.229 5.10.3.3 item 2): network B's UE moves to another contact
// with a re-INVITE, and when network B's IBCF starts again, its BYE in
// that dialog goes to the new contact, not to the one of the INVITE.
func TestRestartedInstanceEndsACallAtItsNewTarget(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	in.Handle(p.b.answerInDialog(nil))
	go in.Accept(p.b.answer(t, in.Request))
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the call got %d", resp.StatusCode)
	}

	tl, _ := listen(t, planB.internal+".13", 0, "moved", p.b.rec)
	newStack(t, tl, tl.LocalAddr().String(), func(st *sip.ServerTransaction) {
		st.Respond(sip.NewResponse(st.Request, 200, ""))
	}, func(*sip.Message) {})
	moved := "sip:moved@" + tl.LocalAddr().String()
	in.SetContact(sip.NameAddr{URI: moved}, false)
	if resp, err := in.ReInvite(p.b.offer(t, sdp.VoLTECodecs(), 2, "")); err != nil || resp.StatusCode != 200 {
		t.Fatalf("re-INVITE from the new contact: %v %v", resp, err)
	}

	inB, _ := planB.instanceHosts(0)
	deadline := time.Now().Add(5 * time.Second)
	for {
		records := p.b.callsOf(t, inB)
		if len(records) == 1 && records[0].Home != nil && records[0].Home.RemoteTarget == moved {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the record does not have the new contact %s: %+v", moved, records)
		}
		time.Sleep(10 * time.Millisecond)
	}

	p.b.restartIBCF(t, 0)
	waitDone(t, "the caller's call", call.Done())
	deadline = time.Now().Add(5 * time.Second)
	for len(p.b.rec.requests("moved", sip.BYE)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no BYE reached the new contact")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := p.b.rec.requests("ue", sip.BYE); len(got) != 0 {
		t.Errorf("the old contact got %d BYEs", len(got))
	}
	p.waitReleased(t)
}
