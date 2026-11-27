package ibcf_test

import (
	"maps"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file have a re-INVITE of UE A1 move its media to a
// new port, and the offer get a final response other than 2xx beyond
// IBCF-A. IBCF-A has told TrGW-A to send to the new port already, while
// the session stays as it was before the re-INVITE (RFC 3261 14.1), on
// the old port. So IBCF-A puts TrGW-A back on the old port, or releases
// the call when TrGW-A does not take that change.

// offerAt is an SDP offer of the network's UE, of version, with its
// media at port.
func (n *network) offerAt(t *testing.T, port uint16, version uint64) sip.Body {
	t.Helper()
	s, err := sdp.AudioOffer("1352", netip.AddrPortFrom(n.media.Addr(), port), sdp.VoLTECodecs())
	if err != nil {
		t.Fatal(err)
	}
	s.Origin.Version = version
	return sip.Body{Type: "application/sdp", Content: s.Bytes()}
}

func TestRejectedOfferPutsTheTrGWBack(t *testing.T) {
	for _, c := range []struct {
		name   string
		reject func(p *pair, in *sip.IncomingCall)
		status int
	}{
		{"TrGW-B refuses the offer", func(p *pair, _ *sip.IncomingCall) {
			p.b.trgw.refuse(ibcf.PeerSide, errRefusedModify)
		}, 500},
		{"UE B1 refuses the offer", func(p *pair, in *sip.IncomingCall) {
			in.Handle(func(r *sip.InDialogRequest) { r.Reject(488) })
		}, 488},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPair(t, nil)
			call, in, _, _ := p.answeredCall(t)
			c.reject(p, in)
			if resp, err := call.ReInvite(p.a.offerAt(t, p.a.media.Port()+2, 2)); err != nil || resp.StatusCode != c.status {
				t.Fatalf("the re-INVITE got %v %v, want %d", resp, err, c.status)
			}
			if got := onlyContext(t, p.a.trgw).remote[ibcf.CoreSide]; got != p.a.media {
				t.Errorf("TrGW-A sends to %s after the refused offer, want %s again", got, p.a.media)
			}
			select {
			case <-call.Done():
				t.Fatal("the call ended")
			default:
			}
			if err := call.Bye(); err != nil {
				t.Fatalf("BYE: %v", err)
			}
			waitDone(t, "the callee's call after the caller's BYE", in.Done())
			p.waitReleased(t)
		})
	}
}

// When TrGW-A does not take the change back to the old port, its
// termination keeps sending where the refused offer said, and IBCF-A
// releases the call.
func TestRejectedOfferEndsTheCallWhenTheTrGWDoesNotGoBack(t *testing.T) {
	p := newPair(t, nil)
	call, in, _, _ := p.answeredCall(t)
	p.b.trgw.refuse(ibcf.PeerSide, errRefusedModify)
	p.a.trgw.refuseRemote(p.a.media, errRefusedModify)
	if resp, err := call.ReInvite(p.a.offerAt(t, p.a.media.Port()+2, 2)); err != nil || resp.StatusCode != 500 {
		t.Errorf("the re-INVITE got %v %v, want 500", resp, err)
	}
	endedByBYE(t, "the caller's call", call.Done(), call.EndedBy)
	endedByBYE(t, "the callee's call", in.Done(), in.EndedBy)
	p.waitReleased(t)
}

// The caller's UPDATE in the early dialog moves its media to a new port,
// and the callee sends its 180 before it refuses the UPDATE with 488.
// The 180 ends no offer: the session of the caller is still the one of
// the INVITE and the 183 (RFC 3311 5.3), so IBCF-A puts TrGW-A back on
// the old port all the same.
func TestRefusedOfferAfterACrossing180PutsTheTrGWBack(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	updates := make(chan *sip.InDialogRequest, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		if r.Request.Method != sip.UPDATE {
			r.Accept(sip.Body{})
			return
		}
		updates <- r
	})
	if err := in.Progress(183, p.b.answer(t, in.Request)); err != nil {
		t.Fatalf("183: %v", err)
	}
	provisional(t, call, 183)
	updated := make(chan *sip.Message, 1)
	go func() {
		resp, err := call.Update(p.a.offerAt(t, p.a.media.Port()+2, 2))
		if err != nil {
			t.Errorf("UPDATE: %v", err)
		}
		updated <- resp
	}()
	var update *sip.InDialogRequest
	select {
	case update = <-updates:
	case <-time.After(10 * time.Second):
		t.Fatal("the UPDATE did not reach the callee")
	}
	// The 180 is reliable too, and its PRACK follows the UPDATE.
	rang := make(chan error, 1)
	go func() { rang <- in.Progress(180, sip.Body{}) }()
	provisional(t, call, 180)
	update.Reject(488)
	select {
	case resp := <-updated:
		if resp == nil || resp.StatusCode != 488 {
			t.Fatalf("the UPDATE got %v, want 488", resp)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the UPDATE got no response")
	}
	select {
	case err := <-rang:
		if err != nil {
			t.Fatalf("180: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the 180 got no PRACK")
	}
	if got := onlyContext(t, p.a.trgw).remote[ibcf.CoreSide]; got != p.a.media {
		t.Errorf("TrGW-A sends to %s after the refused offer, want %s again", got, p.a.media)
	}
	call.Cancel()
	waitDone(t, "the callee's call after the CANCEL", in.Done())
	p.waitReleased(t)
}

// provisional waits until call has a provisional response with status.
func provisional(t *testing.T, call *sip.OutgoingCall, status int) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case resp, ok := <-call.Responses():
			if !ok {
				t.Fatalf("the INVITE ended before its %d: %v", status, call.Err())
			}
			if resp.StatusCode == status {
				return
			}
		case <-timeout:
			t.Fatalf("no %d reached the caller", status)
		}
	}
}

// The caller cancels its INVITE while TrGW-A takes its time over the
// offer, which it then turns down. The INVITE has its 487 already, so
// the 500 of IBCF-A does not go, and does not count among its refusals.
func TestA500ThatDoesNotGoIsNotCounted(t *testing.T) {
	p := newPair(t, nil)
	release := p.a.trgw.hold(p.a.media)
	p.a.trgw.refuseRemote(p.a.media, errRefusedModify)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	call.Cancel()
	if resp := final(t, call); resp.StatusCode != 487 {
		t.Fatalf("the cancelled INVITE got %d, want 487", resp.StatusCode)
	}
	release()
	p.waitReleased(t)
	series := `ims_ibcf_requests_rejected_total{peer="peer-b",direction="exit",method="INVITE",status="500"}`
	if got := p.a.counter(t, series); got != 0 {
		t.Errorf("network A counted %v INVITEs it answered 500, want 0", got)
	}
}

// previewingCallee stands in network B for UE B1 on the transaction
// layer of package sip, since the UA of UE B1 answers a re-INVITE with a
// final response only. It answers the INVITE with a 200 and its SDP
// answer at media, hands each re-INVITE to the test, and tells it of
// each PRACK it answered.
type previewingCallee struct {
	addr      netip.AddrPort
	media     netip.AddrPort
	reinvites chan *sip.ServerTransaction
	pracks    chan *sip.Message
}

// startPreviewingCallee has the INVITEs for network n's UE go to a
// previewingCallee.
func (n *network) startPreviewingCallee(t *testing.T) *previewingCallee {
	t.Helper()
	tl, _ := listen(t, n.plan.internal+".12", 0, "callee", n.rec)
	c := &previewingCallee{addr: tl.LocalAddr(), media: netip.AddrPortFrom(tl.LocalAddr().Addr(), 46000),
		reinvites: make(chan *sip.ServerTransaction, 1), pracks: make(chan *sip.Message, 4)}
	newStack(t, tl, c.addr.String(), c.onRequest, func(*sip.Message) {})
	n.contact = "sip:callee@" + c.addr.String()
	return c
}

func (c *previewingCallee) onRequest(st *sip.ServerTransaction) {
	switch {
	case st.Request.Method != sip.INVITE:
		st.Respond(sip.NewResponse(st.Request, 200, ""))
		if st.Request.Method == sip.PRACK {
			c.pracks <- st.Request
		}
	case ims.IsInDialog(st.Request):
		c.reinvites <- st
	default:
		st.Respond(c.response(st.Request, 200, c.media))
	}
}

// response is the callee's response with status to req, with the SDP
// answer to the offer of req at media.
func (c *previewingCallee) response(req *sip.Message, status int, media netip.AddrPort) *sip.Message {
	resp := sip.NewResponse(req, status, "callee")
	for _, rr := range req.GetAll("Record-Route") {
		resp.Add("Record-Route", rr)
	}
	resp.Add("Contact", "<sip:callee@"+c.addr.String()+">")
	offer, err := sdp.Parse(req.Body)
	if err != nil {
		return resp
	}
	answer, err := sdp.AudioAnswer(offer, "4680", media, sdp.VoLTECodecs())
	if err != nil {
		return resp
	}
	resp.Set("Content-Type", "application/sdp")
	resp.Body = answer.Bytes()
	return resp
}

// reliable is the callee's reliable provisional response with status to
// req, with the SDP answer at media (RFC 3262 3).
func (c *previewingCallee) reliable(req *sip.Message, status int, media netip.AddrPort) *sip.Message {
	resp := c.response(req, status, media)
	resp.Add("Require", sip.Option100rel)
	resp.Add("RSeq", "1")
	return resp
}

// TestRejectedOfferAfterAPreviewGoesBackToTheLastOfferAndAnswer: the
// callee shows its answer to a re-INVITE of UE A1 in a 183 that is not
// reliable, at another port, and then turns the offer down with 488.
// Only an answer in a 2xx or a reliable provisional response is the
// answer (RFC 3261 13.2.1), so the session is as it was before the
// re-INVITE (14.1). Each IBCF passes the 183 on to its other leg, and
// its SDP through its TrGW, as it does with any provisional response,
// and copies it unreliably because it came so: that session description
// counts for nothing on either leg. After the 488, each termination of
// the two TrGWs sends where the INVITE and its answer said again:
// TrGW-A's to the old port of UE A1, and TrGW-B's to where the 200 of
// the callee said.
func TestRejectedOfferAfterAPreviewGoesBackToTheLastOfferAndAnswer(t *testing.T) {
	p := newPair(t, nil)
	callee := p.b.startPreviewingCallee(t)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the INVITE got %d, want 200", resp.StatusCode)
	}
	// Until its ACK has come, each IBCF answers a re-INVITE 491 (RFC
	// 3261 14.1).
	inA, _ := planA.instanceHosts(0)
	inB, _ := planB.instanceHosts(0)
	p.a.waitAcknowledged(t, inA)
	p.b.waitAcknowledged(t, inB)
	wantA, wantB := onlyContext(t, p.a.trgw).remote, onlyContext(t, p.b.trgw).remote
	if wantB[ibcf.CoreSide] != callee.media {
		t.Fatalf("TrGW-B sends to %s for the 200, want %s", wantB[ibcf.CoreSide], callee.media)
	}

	offer := p.a.offerAt(t, p.a.media.Port()+2, 2)
	rejected := make(chan *sip.Message, 1)
	go func() {
		resp, err := call.ReInvite(offer)
		if err != nil {
			t.Errorf("re-INVITE: %v", err)
		}
		rejected <- resp
	}()
	var st *sip.ServerTransaction
	select {
	case st = <-callee.reinvites:
	case <-time.After(10 * time.Second):
		t.Fatal("the re-INVITE did not reach the callee")
	}
	st.Respond(callee.response(st.Request, 183, netip.AddrPortFrom(callee.media.Addr(), callee.media.Port()+2)))
	st.Respond(sip.NewResponse(st.Request, 488, ""))
	select {
	case resp := <-rejected:
		if resp == nil || resp.StatusCode != 488 {
			t.Fatalf("the re-INVITE got %v, want 488", resp)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the re-INVITE got no final response")
	}

	if got := onlyContext(t, p.a.trgw).remote; !maps.Equal(got, wantA) {
		t.Errorf("TrGW-A sends to %v after the 488, want %v of the INVITE and its answer again", got, wantA)
	}
	if got := onlyContext(t, p.b.trgw).remote; !maps.Equal(got, wantB) {
		t.Errorf("TrGW-B sends to %v after the 488, want %v of the INVITE and its answer again", got, wantB)
	}
	select {
	case <-call.Done():
		t.Fatal("the call ended")
	default:
	}
	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	p.waitReleased(t)
}

// TestRejectedOfferAfterAReliableAnswerKeepsIt: the callee answers the
// re-INVITE of UE A1 in a reliable 183, at another port, and then turns
// the offer down with 488. That answer is the one that counts, and the
// offer and the answer are done with it (RFC 3261 13.2.1, RFC 3262 5):
// UE A1 has it, since each IBCF copies a reliable provisional response
// reliably and passes the PRACK back. So the 488 puts no termination
// back, and the media go on through both TrGWs as the 183 said. RFC
// 6141 3.3 has the UAS accept such a re-INVITE rather than reject it,
// which this callee does not, and that is what makes the two cases
// differ.
func TestRejectedOfferAfterAReliableAnswerKeepsIt(t *testing.T) {
	p := newPair(t, nil)
	callee := p.b.startPreviewingCallee(t)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the INVITE got %d, want 200", resp.StatusCode)
	}
	inA, _ := planA.instanceHosts(0)
	inB, _ := planB.instanceHosts(0)
	p.a.waitAcknowledged(t, inA)
	p.b.waitAcknowledged(t, inB)

	moved := netip.AddrPortFrom(p.a.media.Addr(), p.a.media.Port()+2)
	answered := netip.AddrPortFrom(callee.media.Addr(), callee.media.Port()+2)
	rejected := make(chan *sip.Message, 1)
	go func() {
		resp, err := call.ReInvite(p.a.offerAt(t, moved.Port(), 2))
		if err != nil {
			t.Errorf("re-INVITE: %v", err)
		}
		rejected <- resp
	}()
	var st *sip.ServerTransaction
	select {
	case st = <-callee.reinvites:
	case <-time.After(10 * time.Second):
		t.Fatal("the re-INVITE did not reach the callee")
	}
	st.Respond(callee.reliable(st.Request, 183, answered))
	// The 183 waits for its PRACK, which UE A1 sends and both IBCFs pass
	// on (RFC 3262 3, 4).
	select {
	case <-callee.pracks:
	case <-time.After(10 * time.Second):
		t.Fatal("the reliable 183 of the callee got no PRACK")
	}
	st.Respond(sip.NewResponse(st.Request, 488, ""))
	select {
	case resp := <-rejected:
		if resp == nil || resp.StatusCode != 488 {
			t.Fatalf("the re-INVITE got %v, want 488", resp)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the re-INVITE got no final response")
	}

	if got := onlyContext(t, p.a.trgw).remote[ibcf.CoreSide]; got != moved {
		t.Errorf("TrGW-A sends to %s after the 488, want %s of the offer that the 183 answered", got, moved)
	}
	if got := onlyContext(t, p.b.trgw).remote[ibcf.CoreSide]; got != answered {
		t.Errorf("TrGW-B sends to %s after the 488, want %s of the answer in the reliable 183", got, answered)
	}
	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	p.waitReleased(t)
}
