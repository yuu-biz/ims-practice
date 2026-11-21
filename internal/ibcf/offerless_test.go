package ibcf_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// checkMedia checks what each TrGW was told after a call whose offer
// came from network B's UE and whose answer from network A's: each
// termination sends to its side, and the TrGWs to each other.
func checkMedia(t *testing.T, p *pair) {
	t.Helper()
	ctxA, ctxB := onlyContext(t, p.a.trgw), onlyContext(t, p.b.trgw)
	want := map[string]map[ibcf.Side]netip.AddrPort{
		"A": {ibcf.CoreSide: netip.AddrPortFrom(p.a.media.Addr(), 50000), ibcf.PeerSide: ctxB.local.Peer},
		"B": {ibcf.CoreSide: p.b.media, ibcf.PeerSide: ctxA.local.Peer},
	}
	for name, ctx := range map[string]fakeContext{"A": ctxA, "B": ctxB} {
		for side, addr := range want[name] {
			if got := ctx.remote[side]; got != addr {
				t.Errorf("TrGW %s %s side sends to %s, want %s", name, side, got, addr)
			}
		}
	}
}

// An INVITE without an offer (RFC 3261 13.2.1): the callee's reliable
// 183 has the offer, and the caller's PRACK the answer (RFC 3262 5).
// Each IBCF passes the offer on in its own reliable 183, and the PRACK
// of its caller's side as the PRACK of that 183, both through the
// TrGW: the TrGW context is made for the offer in the response.
func TestOfferInAReliable183AndAnswerInThePRACK(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), sip.Body{}, nil)
	in := p.b.nextCall(t)
	pracked := make(chan *sip.Message, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		if r.Request.Method == sip.PRACK {
			pracked <- r.Request
		}
		r.Accept(sip.Body{})
	})
	progressed := make(chan error, 1)
	go func() { progressed <- in.Progress(183, p.b.offer(t, sdp.VoLTECodecs(), 1, "")) }()

	var offer *sip.Message
	select {
	case offer = <-call.Responses():
	case <-time.After(10 * time.Second):
		t.Fatal("no 183 reached the caller")
	}
	if offer.StatusCode != 183 || !offer.Requires(sip.Option100rel) || len(offer.Body) == 0 {
		t.Fatalf("the caller got %d with Require %q and %d bytes of body, want a reliable 183 with the offer",
			offer.StatusCode, offer.Get("Require"), len(offer.Body))
	}
	ctxA := onlyContext(t, p.a.trgw)
	if media, _ := audioOf(t, offer); media != ctxA.local.Core {
		t.Errorf("the offer at the caller has media %s, want TrGW A's core side %s", media, ctxA.local.Core)
	}
	resp, err := call.PRACK(offer, p.a.answer(t, offer))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PRACK with the answer: %v %v", resp, err)
	}
	if err := <-progressed; err != nil {
		t.Fatalf("183 at the callee: %v", err)
	}
	select {
	case prack := <-pracked:
		ctxB := onlyContext(t, p.b.trgw)
		if media, _ := audioOf(t, prack); media != ctxB.local.Core {
			t.Errorf("the answer in the callee's PRACK has media %s, want TrGW B's core side %s", media, ctxB.local.Core)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the PRACK with the answer did not reach the callee")
	}
	checkMedia(t, p)

	go in.Accept(sip.Body{})
	if ok := final(t, call); ok.StatusCode != 200 {
		t.Fatalf("the call got %d", ok.StatusCode)
	}
	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the callee's call after the caller's BYE", in.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
}

// An INVITE without an offer whose 200 has the offer: the caller's ACK
// has the answer (RFC 3261 13.2.2.4). Each IBCF passes the ACK with the
// answer on in its own dialog, through the TrGW.
func TestOfferInThe200AndAnswerInTheACK(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), sip.Body{}, nil)
	in := p.b.nextCall(t)
	acked := make(chan *sip.Message, 1)
	go func() {
		ack, err := in.Accept(p.b.offer(t, sdp.VoLTECodecs(), 1, ""))
		if err != nil {
			t.Errorf("no ACK at the callee: %v", err)
		}
		acked <- ack
	}()
	ok := final(t, call)
	if ok.StatusCode != 200 || len(ok.Body) == 0 {
		t.Fatalf("the caller got %d with %d bytes of body, want a 200 with the offer", ok.StatusCode, len(ok.Body))
	}
	ctxA := onlyContext(t, p.a.trgw)
	if media, _ := audioOf(t, ok); media != ctxA.local.Core {
		t.Errorf("the offer at the caller has media %s, want TrGW A's core side %s", media, ctxA.local.Core)
	}
	if err := call.Ack(p.a.answer(t, ok)); err != nil {
		t.Fatalf("ACK with the answer: %v", err)
	}
	select {
	case ack := <-acked:
		if ack == nil {
			t.FailNow()
		}
		ctxB := onlyContext(t, p.b.trgw)
		if media, _ := audioOf(t, ack); media != ctxB.local.Core {
			t.Errorf("the answer in the callee's ACK has media %s, want TrGW B's core side %s", media, ctxB.local.Core)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the ACK did not reach the callee")
	}
	checkMedia(t, p)

	if err := in.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the caller's call after the callee's BYE", call.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
}
