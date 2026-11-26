package ibcf_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
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
