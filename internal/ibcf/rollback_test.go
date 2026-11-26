package ibcf_test

import (
	"net/netip"
	"testing"

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
