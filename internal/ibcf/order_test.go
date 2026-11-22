package ibcf_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// sdpBody is s as the body of a message.
func sdpBody(s sdp.Session) sip.Body {
	return sip.Body{Type: "application/sdp", Content: s.Bytes()}
}

// A caller that sends a request within the dialog without waiting for
// the answer to its PRACK: after an INVITE without an offer, the
// callee's reliable 183 has the offer, the caller's PRACK the answer
// (RFC 3262 5), and right after it an INFO.
// Network A's IBCF passes both on to network B in the order they came:
// the INFO waits until the PRACK has gone on.
//
// The caller is a SIP stack of the test, so that it can send the INFO
// before the PRACK has its answer. The test holds network A's TrGW
// while the IBCF passes on the answer of the PRACK, which keeps that
// PRACK in the IBCF, and lets it go once the INFO has reached network
// B, or after half a second if it has not: an IBCF that sent the INFO
// ahead of the PRACK shows it then.
func TestPRACKGoesBeforeTheRequestThatFollowsIt(t *testing.T) {
	// INFO crosses by agreement (TS 29.165 Table 6.1).
	p := newPair(t, func(ofB, ofA *interconnect.Profile) {
		ofB.Methods = append(ofB.Methods, "INFO")
		ofA.Methods = append(ofA.Methods, "INFO")
	})
	tl, _ := listen(t, planA.internal+".14", 0, "caller", p.a.rec)
	caller := newStack(t, tl, tl.LocalAddr().String(), func(st *sip.ServerTransaction) {
		st.Respond(sip.NewResponse(st.Request, 200, ""))
	}, func(*sip.Message) {})
	self := tl.LocalAddr()
	contact := "<sip:caller@" + self.String() + ">"
	answerAt := netip.AddrPortFrom(self.Addr(), 45000)
	send := func(req *sip.Message, to string) *sip.ClientTransaction {
		t.Helper()
		u, err := sip.ParseURI(to)
		if err != nil {
			t.Fatal(err)
		}
		targets, err := p.a.names.locate(u)
		if err != nil {
			t.Fatal(err)
		}
		ct, err := caller.SendRequest(req, targets...)
		if err != nil {
			t.Fatal(err)
		}
		return ct
	}

	invite := &sip.Message{Method: sip.INVITE, RequestURI: p.b.sipURI()}
	invite.Add("Route", sip.NameAddr{URI: looseRoute(p.a.ibcfInternal)}.String())
	invite.Add("Max-Forwards", "70")
	invite.Add("From", "<"+p.a.sipURI()+">;tag="+sip.NewTag())
	invite.Add("To", "<"+p.b.sipURI()+">")
	invite.Add("Call-ID", sip.NewCallID())
	invite.Add("CSeq", "1 INVITE")
	invite.Add("Contact", contact)
	invite.Add("Supported", sip.Option100rel)
	invite.Add("Allow", "INVITE, ACK, CANCEL, BYE, PRACK, UPDATE")
	ict := send(invite, p.a.ibcfInternal.String())

	in := p.b.nextCall(t)
	in.Handle(p.b.answerInDialog(nil))
	go in.Progress(183, p.b.offer(t, sdp.VoLTECodecs(), 1, ""))
	var offer *sip.Message
	for resp := range ict.Responses() {
		if resp.StatusCode == 183 {
			offer = resp
			break
		}
	}
	if offer == nil || !offer.Requires(sip.Option100rel) {
		t.Fatalf("the caller got no reliable 183: %v", ict.Err())
	}
	remote, err := sip.ParseNameAddr(offer.Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	within := func(method sip.Method, cseq string, body sip.Body) *sip.Message {
		m := &sip.Message{Method: method, RequestURI: remote.URI}
		m.Add("Max-Forwards", "70")
		m.Add("From", invite.Get("From"))
		m.Add("To", offer.Get("To"))
		m.Add("Call-ID", invite.Get("Call-ID"))
		m.Add("CSeq", cseq)
		m.Add("Contact", contact)
		m.Add("Content-Type", body.Type)
		m.Body = body.Content
		return m
	}

	parsed, err := sdp.Parse(offer.Body)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := sdp.AudioAnswer(parsed, "2468", answerAt, sdp.VoLTECodecs())
	if err != nil {
		t.Fatal(err)
	}
	prack := within(sip.PRACK, "2 PRACK", sdpBody(answer))
	prack.Add("RAck", offer.Get("RSeq")+" 1 INVITE")
	info := within("INFO", "3 INFO", sip.Body{Type: "application/dtmf-relay", Content: []byte("Signal=5\r\nDuration=160\r\n")})

	release := p.a.trgw.hold(answerAt)
	defer release()
	pct := send(prack, remote.URI)
	// The INFO goes once network A's IBCF has the PRACK, which its stack
	// then handles first (RFC 3261 12.2.2).
	waitFor(t, "the PRACK at network A's IBCF", func() bool { return len(p.a.rec.requests("ibcf1-internal", sip.PRACK)) > 0 })
	ict2 := send(info, remote.URI)
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(p.b.rec.requests("ibcf1-external", "INFO")) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	release()

	for what, ct := range map[string]*sip.ClientTransaction{"PRACK": pct, "INFO": ict2} {
		var final *sip.Message
		for resp := range ct.Responses() {
			final = resp
		}
		if final == nil || final.StatusCode != 200 {
			t.Errorf("the %s got %v: %v", what, final, ct.Err())
		}
	}
	var order []sip.Method
	for _, c := range p.b.rec.transactions("ibcf1-external") {
		if c.Message.Method == sip.PRACK || c.Message.Method == "INFO" {
			order = append(order, c.Message.Method)
		}
	}
	if len(order) < 2 || order[0] != sip.PRACK || order[1] != "INFO" {
		t.Errorf("network B's IBCF received %v, want the PRACK and then the INFO", order)
	}
}

// waitFor waits until cond holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
