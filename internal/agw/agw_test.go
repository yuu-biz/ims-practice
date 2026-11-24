package agw

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func udp(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addr(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

// newRelay is a relay on fixed ports for one test, which subtracts the
// contexts that the test leaves, so that the next run of the test finds
// the ports free. The ranges of the tests lie below 32768, where Linux
// hands out no ephemeral ports (32768-60999 by default) and Windows and
// macOS neither (49152 and up): a socket that another test binds to port
// 0 cannot take one of them.
// newRelay is a relay on loopback with n ports of its own
// (rtptest.PortRange).
func newRelay(t *testing.T, n int) *Relay {
	t.Helper()
	return newMRFP(t, n, nil)
}

// newMRFP is newRelay with announcements to play.
func newMRFP(t *testing.T, n int, announcements Announcements) *Relay {
	t.Helper()
	minPort, maxPort := rtptest.PortRange(t, n)
	r, err := NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, announcements, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range r.Contexts() {
			r.Subtract(c.ID)
		}
	})
	return r
}

func read(t *testing.T, c *net.UDPConn) ([]byte, netip.AddrPort) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, src, err := c.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n], src
}

// waitForPackets waits until termination tid has received n RTP
// packets. The relay counts a packet once it has decided where the
// packet goes, so after the wait a change of mode no longer applies to
// those packets.
func waitForPackets(t *testing.T, r *Relay, contextID string, tid TerminationID, n uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, c := range r.Contexts() {
			if c.ID == contextID && c.Terminations[tid].Packets >= n {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("termination %s did not receive %d packets", tid, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRelayLatchesAndForwards plays a UE behind NAT on the access side:
// the SDP names an address the UE does not really send from, so the
// answer only reaches the UE once the relay latches onto the real source.
func TestRelayLatchesAndForwards(t *testing.T) {
	r := newRelay(t, 20)
	api := httptest.NewServer(checkedHandler(t, r))
	defer api.Close()
	client := NewClient(api.URL)

	c, err := client.Add(t.Context(), TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	access, core := c.Terminations[Access].Local, c.Terminations[Core].Local
	if access.Port()%2 != 0 || core.Port()%2 != 0 {
		t.Errorf("ports %d and %d, want even ones for RTP", access.Port(), core.Port())
	}

	ue, network := udp(t), udp(t)
	// The SDP of the UE names a private address that is not where it
	// sends from; the network side's SDP is right.
	if err := client.Modify(t.Context(), c.ID, Access, TerminationRequest{Remote: netip.MustParseAddrPort("192.0.2.10:4000")}); err != nil {
		t.Fatal(err)
	}
	if err := client.Modify(t.Context(), c.ID, Core, TerminationRequest{Remote: addr(network)}); err != nil {
		t.Fatal(err)
	}

	ue.WriteToUDPAddrPort([]byte("from the UE"), access)
	got, src := read(t, network)
	if !bytes.Equal(got, []byte("from the UE")) || src != core {
		t.Errorf("network got %q from %v, want it from the core termination %v", got, src, core)
	}
	network.WriteToUDPAddrPort([]byte("to the UE"), core)
	got, src = read(t, ue)
	if !bytes.Equal(got, []byte("to the UE")) || src != access {
		t.Errorf("UE got %q from %v, want it from the access termination %v", got, src, access)
	}

	contexts := r.Contexts()
	if len(contexts) != 1 || !contexts[0].Terminations[Access].Latched || contexts[0].Terminations[Access].Remote != addr(ue) {
		t.Errorf("contexts = %+v, want the access side latched onto %v", contexts[0].Terminations[Access], addr(ue))
	}
	if access := contexts[0].Terminations[Access]; access.Packets != 1 || access.PacketsSent != 1 {
		t.Errorf("the access termination counts %+v, want one packet each way", access)
	}
	if err := client.Subtract(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.Contexts()) != 0 {
		t.Errorf("the relay still has %+v after Subtract", r.Contexts())
	}
	if err := client.Subtract(t.Context(), c.ID); !errors.Is(err, ErrNoContext) {
		t.Errorf("a second Subtract got %v, want ErrNoContext", err)
	}
}

func TestRelayRunsOutOfPorts(t *testing.T) {
	r := newRelay(t, 4)
	pair := []TerminationRequest{{ID: Access}, {ID: Core}}
	c, err := r.Add(pair...) // takes both even ports of the four
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(pair...); !errors.Is(err, ErrNoPort) {
		t.Fatalf("a second context from an empty range got %v, want ErrNoPort", err)
	}
	if _, err := r.Subtract(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(pair...); err != nil {
		t.Errorf("ports were not given back: %v", err)
	}
}

func TestClientGivesUpOnARelayThatStalls(t *testing.T) {
	stalled := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stalled:
		}
	}))
	t.Cleanup(api.Close)
	// Runs before api.Close, which waits for the handler.
	t.Cleanup(func() { close(stalled) })

	done := make(chan error, 1)
	go func() {
		_, err := NewClient(api.URL).Add(context.Background(), TerminationRequest{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Add succeeded against a relay that never answers")
		}
	case <-time.After(RequestTimeout + time.Second):
		t.Fatalf("Add still waits after %v", RequestTimeout+time.Second)
	}
}

// TestRelayCarriesRTCP sends RTCP through the odd ports. The network
// side's RTCP goes where its SDP says plus one; the UE's RTCP comes
// from a port that is not its RTP port plus one, as behind NAT, and the
// relay latches onto it separately from RTP.
func TestRelayCarriesRTCP(t *testing.T) {
	r := newRelay(t, 20)
	c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Subtract(c.ID)
	access, core := c.Terminations[Access], c.Terminations[Core]
	if access.RTCPLocal.Port() != access.Local.Port()+1 || core.RTCPLocal.Port() != core.Local.Port()+1 {
		t.Fatalf("RTCP ports %v and %v are not above the RTP ports %v and %v", access.RTCPLocal, core.RTCPLocal, access.Local, core.Local)
	}

	// The network side listens on an even port and the one above it.
	var networkRTP, networkRTCP *net.UDPConn
	for port := uint16(41300); port < 41400 && networkRTCP == nil; port += 2 {
		a, err1 := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)))
		b, err2 := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port+1)))
		if err1 == nil && err2 == nil {
			networkRTP, networkRTCP = a, b
			t.Cleanup(func() { a.Close(); b.Close() })
			break
		}
		for _, c := range []*net.UDPConn{a, b} {
			if c != nil {
				c.Close()
			}
		}
	}
	if networkRTCP == nil {
		t.Fatal("no free pair of ports between 41300 and 41399 for the network side")
	}
	ueRTCP := udp(t)
	if _, err := r.Modify(c.ID, Access, TerminationRequest{Remote: netip.MustParseAddrPort("192.0.2.10:4000")}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Modify(c.ID, Core, TerminationRequest{Remote: addr(networkRTP)}); err != nil {
		t.Fatal(err)
	}

	ueRTCP.WriteToUDPAddrPort([]byte("RR from the UE"), access.RTCPLocal)
	got, src := read(t, networkRTCP)
	if string(got) != "RR from the UE" || src != core.RTCPLocal {
		t.Errorf("network got %q from %v, want it from %v", got, src, core.RTCPLocal)
	}
	networkRTCP.WriteToUDPAddrPort([]byte("SR to the UE"), core.RTCPLocal)
	got, src = read(t, ueRTCP)
	if string(got) != "SR to the UE" || src != access.RTCPLocal {
		t.Errorf("UE got %q from %v, want it from %v", got, src, access.RTCPLocal)
	}

	view := r.Contexts()[0].Terminations[Access]
	if !view.RTCPLatched || view.RTCPRemote != addr(ueRTCP) || view.Latched || view.RTCPPackets != 1 || view.Packets != 0 {
		t.Errorf("access termination %+v; want RTCP latched onto %v and RTP untouched", view, addr(ueRTCP))
	}
}

// TestModeGatesMedia is H.248.1 (03/2013) 7.1.7.1.1: media passes from
// a termination that receives to one that sends. RTCP passes anyway.
func TestModeGatesMedia(t *testing.T) {
	r := newRelay(t, 20)
	a, b := udp(t), udp(t)
	c, err := r.Add(TerminationRequest{ID: "a", Remote: addr(a), Mode: SendOnly}, TerminationRequest{ID: "b", Remote: addr(b)})
	if err != nil {
		t.Fatal(err)
	}
	ta, tb := c.Terminations["a"], c.Terminations["b"]
	if ta.Mode != SendOnly || tb.Mode != SendReceive {
		t.Fatalf("modes %q and %q, want sendonly and the default sendrecv", ta.Mode, tb.Mode)
	}
	// a only sends: what arrives from its party stays out of the context.
	a.WriteToUDPAddrPort([]byte("dropped"), ta.Local)
	b.WriteToUDPAddrPort([]byte("to a"), tb.Local)
	if got, _ := read(t, a); string(got) != "to a" {
		t.Errorf("a got %q, want what b sent", got)
	}
	// The relay reads each termination on its own, so b's packet coming
	// through tells nothing of a's. The count tells that the relay has
	// taken a's packet while a was still sendonly.
	waitForPackets(t, r, c.ID, "a", 1)
	if _, err := r.Modify(c.ID, "a", TerminationRequest{Mode: SendReceive}); err != nil {
		t.Fatal(err)
	}
	a.WriteToUDPAddrPort([]byte("to b"), ta.Local)
	if got, _ := read(t, b); string(got) != "to b" {
		t.Errorf("b got %q, want only what a sent after it was made sendrecv", got)
	}
	if _, err := r.Modify(c.ID, "a", TerminationRequest{Mode: "loopback"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an unknown mode got %v, want ErrBadRequest", err)
	}
}

// TestSubtractingTheLastTerminationDeletesTheContext is H.248.1 (03/2013)
// 7: a context lives as long as it has a termination. It also tries a
// context of one termination, and IDs that the relay chooses.
func TestSubtractingTheLastTerminationDeletesTheContext(t *testing.T) {
	r := newRelay(t, 20)
	api := httptest.NewServer(checkedHandler(t, r))
	defer api.Close()
	client := NewClient(api.URL)

	c, err := client.Add(t.Context(), TerminationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.AddTermination(t.Context(), c.ID, TerminationRequest{Mode: ReceiveOnly})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Terminations) != 1 || c.Terminations["t1"] == nil || second.ID != "t2" || second.Mode != ReceiveOnly {
		t.Fatalf("terminations %v and %+v, want t1 and then t2 in recvonly", c.Terminations, second)
	}
	if _, err := client.AddTermination(t.Context(), c.ID, TerminationRequest{ID: "t2"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a second t2 got %v, want ErrBadRequest", err)
	}
	if _, deleted, err := client.SubtractTermination(t.Context(), c.ID, "t1"); err != nil || deleted {
		t.Fatalf("subtracting t1 got deleted %v, %v; want the context kept for t2", deleted, err)
	}
	if _, deleted, err := client.SubtractTermination(t.Context(), c.ID, "t2"); err != nil || !deleted {
		t.Fatalf("subtracting t2 got deleted %v, %v; want the context deleted", deleted, err)
	}
	if n := len(r.Contexts()); n != 0 {
		t.Errorf("%d contexts left", n)
	}
	if _, err := client.AddTermination(t.Context(), c.ID, TerminationRequest{}); !errors.Is(err, ErrNoContext) {
		t.Errorf("adding to the deleted context got %v, want ErrNoContext", err)
	}
	if err := client.Modify(t.Context(), "c0", "t1", TerminationRequest{}); !errors.Is(err, ErrNoContext) {
		t.Errorf("Modify of an unknown context got %v, want ErrNoContext", err)
	}
}
