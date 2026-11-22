package ibcf_test

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// forkedCallee stands in network B for a forking proxy and the two UEs
// behind it: it answers each INVITE with a reliable 183 from each of
// two UASs, each with its own To tag and SDP answer, and then with a
// 200 without SDP from one of them, once that one's 183 has its PRACK
// (RFC 3261 13.2.2.4, 16.7; RFC 3262 3, 5).
type forkedCallee struct {
	stack *sip.Stack
	addr  netip.AddrPort
	// sent1 and sent2 are closed once the 183 of each branch went out;
	// next lets the next response go.
	sent1, sent2, next chan struct{}
	answering          int
	// pracked gets the To tag of each PRACK.
	pracked chan string
}

// branchMedia is where branch i takes its media.
func (f *forkedCallee) branchMedia(i int) netip.AddrPort {
	return netip.AddrPortFrom(f.addr.Addr(), uint16(41000+1000*i))
}

func (f *forkedCallee) response(req *sip.Message, code, branch int) *sip.Message {
	resp := sip.NewResponse(req, code, fmt.Sprintf("branch-%d", branch))
	for _, rr := range req.GetAll("Record-Route") {
		resp.Add("Record-Route", rr)
	}
	resp.Add("Contact", fmt.Sprintf("<sip:branch-%d@%s>", branch, f.addr))
	if code == 200 {
		// The answer went in the reliable 183 (RFC 3262 5).
		return resp
	}
	resp.Add("Require", sip.Option100rel)
	resp.Add("RSeq", "1")
	offer, err := sdp.Parse(req.Body)
	if err != nil {
		return resp
	}
	answer, err := sdp.AudioAnswer(offer, fmt.Sprint(7000+branch), f.branchMedia(branch), sdp.VoLTECodecs())
	if err != nil {
		return resp
	}
	resp.Set("Content-Type", "application/sdp")
	resp.Body = answer.Bytes()
	return resp
}

func (f *forkedCallee) onRequest(st *sip.ServerTransaction) {
	if st.Request.Method != sip.INVITE {
		if st.Request.Method == sip.PRACK {
			to, _ := sip.ParseNameAddr(st.Request.Get("To"))
			tag, _ := to.Params.Get("tag")
			f.pracked <- tag
		}
		st.Respond(sip.NewResponse(st.Request, 200, ""))
		return
	}
	go func() {
		st.Respond(f.response(st.Request, 183, 1))
		close(f.sent1)
		<-f.next
		st.Respond(f.response(st.Request, 183, 2))
		close(f.sent2)
		<-f.next
		for tag := range f.pracked {
			if tag == fmt.Sprintf("branch-%d", f.answering) {
				break
			}
		}
		st.Respond(f.response(st.Request, 200, f.answering))
	}()
}

// startForkedCallee has the INVITEs for network B's UE go to a forked
// callee whose branch answering answers.
func (n *network) startForkedCallee(t *testing.T, answering int) *forkedCallee {
	t.Helper()
	tl, _ := listen(t, n.plan.internal+".12", 0, "forked", n.rec)
	f := &forkedCallee{addr: tl.LocalAddr(), sent1: make(chan struct{}), sent2: make(chan struct{}),
		next: make(chan struct{}), answering: answering, pracked: make(chan string, 4)}
	f.stack = newStack(t, tl, tl.LocalAddr().String(), f.onRequest, func(*sip.Message) {})
	n.contact = "sip:forked@" + f.addr.String()
	return f
}

// The INVITE forks beyond network B's IBCF: two UASs answer it with a
// reliable 183 each, and one of them with the 200, whose answer was in
// its 183. Network B's IBCF is one UAS to the caller's side (RFC 3261
// 8.2.6.2): the caller has one early dialog, whose session description
// is that of the first branch, and gets the 183 of the second branch
// without its SDP, since a UAC takes the first answer and ignores the
// rest (13.2.1). Network B's TrGW sends the media to the branch that
// answered, whichever 183 came last.
//
// When that is the second branch, network B's IBCF offers the caller's
// side the session of the branch that answered once it has the ACK of
// the 200, in the session that side has, with its version one up (RFC
// 3264 8). The offer crosses network A, whose IBCF passes it on with
// the SDP through its own TrGW, so the media of UE A1 still goes
// through both TrGWs: UE A1 gets it in an UPDATE (RFC 3311), or in a
// re-INVITE when its INVITE did not list UPDATE in Allow (RFC 3261
// 14.1), and TrGW A learns from the answer where UE A1 takes its media.
func TestForkBeyondTheBorder(t *testing.T) {
	// offer is the method in which the session of the branch that
	// answered reaches UE A1, and none when the branch that answered is
	// the one whose session description it got.
	for _, c := range []struct {
		answering int
		offer     sip.Method
		what      string
	}{
		{answering: 1, what: "no offer"},
		{answering: 2, offer: sip.UPDATE, what: "the offer in an UPDATE"},
		{answering: 2, offer: sip.INVITE, what: "the offer in a re-INVITE"},
	} {
		t.Run(fmt.Sprintf("branch %d answers, %s", c.answering, c.what), func(t *testing.T) {
			answering := c.answering
			p := newPair(t, nil)
			f := p.b.startForkedCallee(t, answering)
			inDialog := make(chan *sip.Message, 2)
			var headers []sip.HeaderField
			if c.offer == sip.INVITE {
				headers = append(headers, sip.HeaderField{Name: "Allow", Value: "INVITE, ACK, CANCEL, BYE, PRACK"})
			}
			call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), inDialog, headers...)
			var got []*sip.Message
			next := func(what string) *sip.Message {
				t.Helper()
				select {
				case resp, ok := <-call.Responses():
					if !ok {
						t.Fatalf("no %s at the caller: %v", what, call.Err())
					}
					got = append(got, resp)
					return resp
				case <-time.After(10 * time.Second):
					t.Fatalf("no %s at the caller", what)
				}
				return nil
			}
			<-f.sent1
			if resp := next("183 of the first branch"); resp.StatusCode != 183 || len(resp.Body) == 0 {
				t.Fatalf("the caller got %d with %d bytes of body first, want the 183 with the answer", resp.StatusCode, len(resp.Body))
			}
			f.next <- struct{}{}
			<-f.sent2
			if resp := next("183 of the second branch"); resp.StatusCode != 183 {
				t.Fatalf("the caller got %d, want the 183 of the second branch", resp.StatusCode)
			} else if len(resp.Body) != 0 || resp.Get("Content-Type") != "" {
				t.Errorf("the caller got a second session description in its early dialog:\n%s", resp.Body)
			}
			f.next <- struct{}{}
			ok := next("200")
			if ok.StatusCode != 200 {
				t.Fatalf("the call got %d", ok.StatusCode)
			}
			if len(ok.Body) != 0 {
				t.Errorf("the 200 reached the caller with a session description:\n%s", ok.Body)
			}
			for _, resp := range got {
				if tag := tagOf(t, resp.Get("To")); tag != tagOf(t, got[0].Get("To")) {
					t.Errorf("the caller got %d with To tag %s, want the one early dialog %s", resp.StatusCode, tag, tagOf(t, got[0].Get("To")))
				}
			}

			ctxB := onlyContext(t, p.b.trgw)
			if remote := ctxB.remote[ibcf.CoreSide]; remote != f.branchMedia(answering) {
				t.Errorf("TrGW B sends the media to %s, want %s of the branch that answered", remote, f.branchMedia(answering))
			}
			ctxA := onlyContext(t, p.a.trgw)
			if remote := ctxA.remote[ibcf.PeerSide]; remote != ctxB.local.Peer {
				t.Errorf("TrGW A sends the media to %s, want TrGW B at %s", remote, ctxB.local.Peer)
			}

			if c.offer == "" {
				select {
				case req := <-inDialog:
					t.Errorf("the caller got a %s, want no offer for the branch whose session description it has", req.Method)
				case <-time.After(20 * testTimers.T1):
				}
			} else {
				var offer *sip.Message
				select {
				case offer = <-inDialog:
				case <-time.After(10 * time.Second):
					t.Fatal("no offer of the session of the branch that answered at the caller")
				}
				if offer.Method != c.offer {
					t.Errorf("the caller got the session of the branch that answered in a %s, want a %s", offer.Method, c.offer)
				}
				if media, origin := audioOf(t, offer); media != ctxA.local.Core || origin != ctxA.local.Core.Addr() {
					t.Errorf("the offer has media %s and origin %s, want TrGW A's core side %s", media, origin, ctxA.local.Core)
				}
				had, err := sdp.Parse(got[0].Body)
				if err != nil {
					t.Fatalf("SDP of the first 183: %v", err)
				}
				offered, err := sdp.Parse(offer.Body)
				if err != nil {
					t.Fatalf("SDP of the offer: %v", err)
				}
				want := had.Origin
				want.Version++
				if offered.Origin != want {
					t.Errorf("the offer has the o= line %+v, want %+v of the session that the caller has", offered.Origin, want)
				}
				// The answer of UE A1 crosses the border too, and tells
				// TrGW A where that UE now takes its media.
				answered := netip.AddrPortFrom(p.a.media.Addr(), 50000)
				deadline := time.Now().Add(10 * time.Second)
				for onlyContext(t, p.a.trgw).remote[ibcf.CoreSide] != answered {
					if time.Now().After(deadline) {
						t.Fatalf("TrGW A sends the media to %s, want %s of the answer of UE A1 to the offer",
							onlyContext(t, p.a.trgw).remote[ibcf.CoreSide], answered)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}

			if err := call.Bye(); err != nil {
				t.Fatalf("BYE: %v", err)
			}
			p.waitReleased(t)
		})
	}
}
