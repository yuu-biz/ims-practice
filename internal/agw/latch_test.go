package agw

import (
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// latchBed is a context of two terminations. The access termination
// faces a UE behind NAT: the SDP names the address of sdp, while the
// UE sends from ue, and its RTCP from ueRTCP, which is not the port
// above ue. The core termination faces the network, whose SDP is right.
type latchBed struct {
	t                      *testing.T
	relay                  *Relay
	client                 *Client
	id                     string
	access, core           Termination
	sdp, ue, ueRTCP, other *net.UDPConn
	network                *net.UDPConn
}

// newLatchBed adds the context over the HTTP API, with access as the
// request for the access termination; the bed fills in its ID and its
// remote address.
func newLatchBed(t *testing.T, access TerminationRequest) *latchBed {
	t.Helper()
	b := &latchBed{t: t, relay: newRelay(t, 20), sdp: udp(t), ue: udp(t), ueRTCP: udp(t), other: udp(t), network: udp(t)}
	api := httptest.NewServer(checkedHandler(t, b.relay))
	t.Cleanup(api.Close)
	b.client = NewClient(api.URL)
	access.ID, access.Remote = Access, addr(b.sdp)
	c, err := b.client.Add(t.Context(), access, TerminationRequest{ID: Core, Remote: addr(b.network)})
	if err != nil {
		t.Fatal(err)
	}
	b.id, b.access, b.core = c.ID, *c.Terminations[Access], *c.Terminations[Core]
	return b
}

func (b *latchBed) modify(change TerminationRequest) {
	b.t.Helper()
	if err := b.client.Modify(b.t.Context(), b.id, Access, change); err != nil {
		b.t.Fatal(err)
	}
}

// fromUE sends RTP to the access termination from conn, and waits until
// it has reached the network: by then the relay has latched, or not.
func (b *latchBed) fromUE(conn *net.UDPConn, payload string) {
	b.t.Helper()
	conn.WriteToUDPAddrPort([]byte(payload), b.access.Local)
	b.receive(b.network, payload)
}

// toUE sends RTP from the network, and checks that it reaches want.
func (b *latchBed) toUE(want *net.UDPConn, payload string) {
	b.t.Helper()
	b.network.WriteToUDPAddrPort([]byte(payload), b.core.Local)
	b.receive(want, payload)
}

func (b *latchBed) receive(conn *net.UDPConn, payload string) {
	b.t.Helper()
	const limit = 2 * time.Second
	conn.SetReadDeadline(time.Now().Add(limit))
	buf := make([]byte, 1500)
	n, _, err := conn.ReadFromUDPAddrPort(buf)
	if err != nil {
		b.t.Fatalf("%q did not reach %v within %v: %v", payload, addr(conn), limit, err)
	}
	if got := string(buf[:n]); got != payload {
		b.t.Fatalf("%v got %q, want %q", addr(conn), got, payload)
	}
}

// rtcpFromUE sends RTCP to the access termination from ueRTCP, and
// waits until the relay has counted it.
func (b *latchBed) rtcpFromUE() {
	b.t.Helper()
	before := b.view().RTCPPackets
	b.ueRTCP.WriteToUDPAddrPort([]byte("RR"), b.access.RTCPLocal)
	deadline := time.Now().Add(2 * time.Second)
	for b.view().RTCPPackets == before {
		if time.Now().After(deadline) {
			b.t.Fatal("the access termination did not receive the RTCP packet")
		}
		time.Sleep(time.Millisecond)
	}
}

func (b *latchBed) view() Termination {
	for _, c := range b.relay.Contexts() {
		if c.ID == b.id {
			return *c.Terminations[Access]
		}
	}
	b.t.Fatalf("no context %s", b.id)
	return Termination{}
}

// wantRemote checks where the access termination sends RTP and RTCP,
// as GET /contexts shows it.
func (b *latchBed) wantRemote(rtp, rtcp netip.AddrPort, latched bool) {
	b.t.Helper()
	v := b.view()
	if v.Remote != rtp || v.Latched != latched || v.RTCPRemote != rtcp || v.RTCPLatched != latched {
		b.t.Errorf("access sends RTP to %v (latched %v) and RTCP to %v (latched %v), want %v and %v (latched %v)",
			v.Remote, v.Latched, v.RTCPRemote, v.RTCPLatched, rtp, rtcp, latched)
	}
}

func above(a netip.AddrPort) netip.AddrPort { return netip.AddrPortFrom(a.Addr(), a.Port()+1) }

// H.248.37 (06/2008) 6.6.1: with latching off, a termination sends to
// the address of the SDP, whatever source its packets come from.
func TestLatchOffSendsToTheRemoteAddress(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{Latch: NAPTOff})
	b.fromUE(b.ue, "from the UE")
	b.rtcpFromUE()
	b.toUE(b.sdp, "to the SDP address")
	b.wantRemote(addr(b.sdp), above(addr(b.sdp)), false)
}

// H.248.37 (06/2008) 6.6.1 and 6.6.4: NAPTOff after a latch goes back
// to the address of the SDP, the one that the latest Modify gave.
func TestLatchOffAfterALatch(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{})
	b.fromUE(b.ue, "latches")
	b.rtcpFromUE()
	b.wantRemote(addr(b.ue), addr(b.ueRTCP), true)

	b.modify(TerminationRequest{Remote: addr(b.other)})
	b.toUE(b.ue, "a new remote address does not undo the latch")
	b.modify(TerminationRequest{Latch: NAPTOff})
	b.toUE(b.other, "to the remote address again")
	b.wantRemote(addr(b.other), above(addr(b.other)), false)
}

// An Add without a latching latches, as the media relay always has; a
// Modify without one keeps the latching of the termination.
func TestLatchLeftOutOfARequest(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{})
	b.fromUE(b.ue, "latches")
	b.toUE(b.ue, "to where the UE sends from")

	off := newLatchBed(t, TerminationRequest{Latch: NAPTOff})
	off.modify(TerminationRequest{Mode: SendReceive})
	off.fromUE(off.ue, "does not latch")
	off.toUE(off.sdp, "still to the SDP address")
}

// H.248.37 (06/2008) 6.6.2: NAPTLatch latches onto the first packet
// after it and keeps that address; asked again, it latches anew.
func TestLatchOnceAndAgain(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{Latch: NAPTLatch})
	b.fromUE(b.ue, "latches")
	b.rtcpFromUE()
	b.fromUE(b.other, "from another source")
	b.toUE(b.ue, "to the first source")
	b.wantRemote(addr(b.ue), addr(b.ueRTCP), true)

	b.modify(TerminationRequest{Latch: NAPTLatch})
	b.toUE(b.ue, "to the latched address until a packet comes")
	b.fromUE(b.other, "latches again")
	b.toUE(b.other, "to the new source")
}

// H.248.37 (06/2008) 6.6.3: NAPTRelatch latches onto the first packet
// from another source than the address the termination sends to, once.
func TestRelatchFollowsANewSource(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{Latch: NAPTRelatch})
	b.fromUE(b.sdp, "from the address it sends to")
	b.wantRemote(addr(b.sdp), above(addr(b.sdp)), false)
	b.fromUE(b.ue, "from a new source")
	b.rtcpFromUE()
	b.toUE(b.ue, "to the new source")
	b.wantRemote(addr(b.ue), addr(b.ueRTCP), true)
	b.fromUE(b.other, "from yet another source")
	b.toUE(b.ue, "the relatch is done")

	b.modify(TerminationRequest{Latch: NAPTRelatch})
	b.fromUE(b.ue, "from the latched address")
	b.fromUE(b.other, "the UE has moved")
	b.toUE(b.other, "to where the UE moved")
}

// H.248.37 (06/2008) 1.1.1 and 6.6.3: a termination that has latched
// follows a party that has moved only when its controller asks it to
// latch again, which the controller does for each move it expects. The
// address of a new Remote descriptor alone does not move the
// termination (6.6.2, 6.6.4), and until a packet arrives from the new
// source it sends where it latched before.
func TestRelatchMovesALatchedTerminationToTheNewSource(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{})
	b.fromUE(b.ue, "latches")
	b.toUE(b.ue, "to where the UE sends from")

	// The UE has offered a new port, which its NAT maps to the source
	// address of other.
	b.modify(TerminationRequest{Remote: addr(b.other), Latch: NAPTRelatch})
	b.toUE(b.ue, "still to the latched address")
	b.fromUE(b.other, "from the new port")
	b.toUE(b.other, "to the new port")
}

// A renegotiation whose offer gets no 2xx leaves the session as it was
// before it (RFC 3261 14.1), so the controller asks the termination to
// latch again for the way back as it did for the way out (H.248.37
// (06/2008) 1.1.1).
func TestRelatchTakesALatchedTerminationBackWhenARenegotiationFails(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{})
	b.fromUE(b.ue, "latches")
	b.modify(TerminationRequest{Remote: addr(b.other), Latch: NAPTRelatch})
	b.fromUE(b.other, "the UE moves to its new port")
	b.toUE(b.other, "to the new port")

	// The offer got no 2xx, so the termination goes back to the address
	// of the offer and answer before it.
	b.modify(TerminationRequest{Remote: addr(b.sdp), Latch: NAPTRelatch})
	b.fromUE(b.ue, "the UE takes its media where it did before")
	b.toUE(b.ue, "to the port the UE had before")
}

// H.248.37 (06/2008) 6.6.4: StopLatching stops a latching that waits for
// its packet, and keeps an address that the termination has latched.
func TestStopLatching(t *testing.T) {
	b := newLatchBed(t, TerminationRequest{Latch: NAPTRelatch})
	b.modify(TerminationRequest{Latch: StopLatching})
	b.fromUE(b.ue, "does not latch")
	b.toUE(b.sdp, "to the SDP address")

	latched := newLatchBed(t, TerminationRequest{})
	latched.fromUE(latched.ue, "latches")
	latched.modify(TerminationRequest{Latch: StopLatching, Remote: addr(latched.other)})
	latched.fromUE(latched.other, "from another source")
	latched.toUE(latched.ue, "still to the latched address")
}

func TestUnknownLatching(t *testing.T) {
	r := newRelay(t, 20)
	if _, err := r.Add(TerminationRequest{ID: Access, Latch: "sometimes"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Add got %v, want ErrBadRequest", err)
	}
	c, err := r.Add(TerminationRequest{ID: Access})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Modify(c.ID, Access, TerminationRequest{Mode: SendOnly, Latch: "sometimes"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Modify got %v, want ErrBadRequest", err)
	}
	if mode := r.Contexts()[0].Terminations[Access].Mode; mode != SendReceive {
		t.Errorf("the Modify that failed left the mode %q", mode)
	}
}

// A request that sets no latching is the JSON that it was before
// latching could be set.
func TestRequestWithoutLatchLeavesItOutOfTheJSON(t *testing.T) {
	b, err := json.Marshal(TerminationRequest{ID: Access, Remote: netip.MustParseAddrPort("192.0.2.10:4000")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "latch") {
		t.Errorf("%s names the latching", b)
	}
}
