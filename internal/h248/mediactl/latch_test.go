package mediactl

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// signalsOf is the Signals descriptor of the command for side in the
// first request of ds whose first command is c, in a few words.
func signalsOf(t *testing.T, ds []datagram, c h248.Command, side agw.TerminationID) string {
	t.Helper()
	for _, cmd := range firstRequest(t, ds, c).Actions[0].Commands {
		if !strings.Contains(string(cmd.TerminationID), "/"+string(side)+"/") {
			continue
		}
		switch {
		case cmd.Signals == nil:
			return "no Signals"
		case len(cmd.Signals.Signals) == 0:
			return "empty Signals"
		}
		var parts []string
		for _, s := range cmd.Signals.Signals {
			for _, p := range s.SigParList {
				parts = append(parts, fmt.Sprintf("%s %s=%s", s.SignalName, p.Name, p.Value.Text))
			}
		}
		return strings.Join(parts, ", ")
	}
	t.Fatalf("no %s of %s", c, side)
	return ""
}

// The latching of an Add, as the Controller asks for it on the wire and
// as the Gateway hands it to the relay. Without the signal the gateway
// does not latch (H.248.37 (06/2008) 6.6.1); a request without a
// latching gets the default of the HTTP API of the media relay.
func TestIqLatchingOfAnAdd(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	tests := []struct {
		latch agw.Latch
		wire  string
		relay agw.Latch
	}{
		{"", "ipnapt/latch napt=LATCH", agw.NAPTLatch},
		{agw.NAPTLatch, "ipnapt/latch napt=LATCH", agw.NAPTLatch},
		{agw.NAPTRelatch, "ipnapt/latch napt=RELATCH", agw.NAPTRelatch},
		{agw.NAPTOff, "no Signals", agw.NAPTOff},
		{agw.StopLatching, "no Signals", agw.NAPTOff},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.latch), func(t *testing.T) {
			before := len(b.net.datagrams())
			_, err := b.controller.Add(context.Background(), agw.TerminationRequest{ID: agw.Access, Latch: tt.latch}, agw.TerminationRequest{ID: agw.Core})
			if err != nil {
				t.Fatal(err)
			}
			if got := signalsOf(t, b.net.datagrams()[before:], h248.Add, agw.Access); got != tt.wire {
				t.Errorf("the Add carries %s, want %s", got, tt.wire)
			}
			if got := b.relay.newest().requests[agw.Access][0].Latch; got != tt.relay {
				t.Errorf("the relay was asked for %q, want %q", got, tt.relay)
			}
		})
	}
}

// The latching of a Modify. No Signals descriptor keeps the latching,
// and the relay is not asked for anything; one without the signal stops
// latching (H.248.37 (06/2008) 6.6.4).
func TestIqLatchingOfAModify(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	tests := []struct {
		latch agw.Latch
		wire  string
		relay []agw.Latch
	}{
		{"", "no Signals", nil},
		{agw.NAPTOff, "ipnapt/latch napt=OFF", []agw.Latch{agw.NAPTOff}},
		{agw.NAPTLatch, "ipnapt/latch napt=LATCH", []agw.Latch{agw.NAPTLatch}},
		{agw.NAPTRelatch, "ipnapt/latch napt=RELATCH", []agw.Latch{agw.NAPTRelatch}},
		{agw.StopLatching, "empty Signals", []agw.Latch{agw.StopLatching}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.latch), func(t *testing.T) {
			ctx := context.Background()
			r, err := reserve(ctx, b.controller, agw.Media{})
			if err != nil {
				t.Fatal(err)
			}
			before := len(b.net.datagrams())
			if err := b.controller.Modify(ctx, r.ID, agw.Access, agw.TerminationRequest{Latch: tt.latch}); err != nil {
				t.Fatal(err)
			}
			if got := signalsOf(t, b.net.datagrams()[before:], h248.Modify, agw.Access); got != tt.wire {
				t.Errorf("the Modify carries %s, want %s", got, tt.wire)
			}
			var asked []agw.Latch
			for _, request := range b.relay.newest().requests[agw.Access][1:] {
				asked = append(asked, request.Latch)
			}
			if fmt.Sprint(asked) != fmt.Sprint(tt.relay) {
				t.Errorf("the relay was asked for %q after the Add, want %q", asked, tt.relay)
			}
		})
	}
}

func TestIqControllerRefusesAnUnknownLatching(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	before := len(b.net.datagrams())
	if _, err := b.controller.Add(ctx, agw.TerminationRequest{ID: agw.Access, Latch: "sometimes"}, agw.TerminationRequest{ID: agw.Core}); err == nil {
		t.Error("Add with an unknown latching succeeded")
	}
	if sent(b.net.datagrams()[before:], "Add") {
		t.Error("the controller sent the Add")
	}
	r, err := reserve(ctx, b.controller, agw.Media{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Modify(ctx, r.ID, agw.Access, agw.TerminationRequest{Latch: "sometimes"}); err == nil {
		t.Error("Modify with an unknown latching succeeded")
	}
}

// Mp has no ipnapt: the MRFC cannot ask for latching, and a termination
// of the MRFP sends to its Remote descriptor (H.248.37 (06/2008) 6.6.1).
func TestMpNeverLatches(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	remote := mustAddrPort("192.0.2.30:49170")
	if _, err := b.controller.Add(ctx, agw.TerminationRequest{Remote: remote, Latch: agw.NAPTLatch}); err == nil {
		t.Error("an Add of Mp with a latching succeeded")
	}
	cx, err := b.controller.Add(ctx, agw.TerminationRequest{Remote: remote})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.controller.AddTermination(ctx, cx.ID, agw.TerminationRequest{Remote: remote}); err != nil {
		t.Fatal(err)
	}
	if _, terms := b.processor.only(t); terms["t1"].latch != agw.NAPTOff || terms["t2"].latch != agw.NAPTOff {
		t.Errorf("the processor was asked for %q and %q, want %q", terms["t1"].latch, terms["t2"].latch, agw.NAPTOff)
	}
}

// TestIqGatewayLatchesOnlyWhenAsked drives the media relay of
// internal/agw through the Gateway. The access termination faces a UE
// behind NAT, which sends from ue while its SDP names sdp. Added
// without ipnapt/latch, the termination sends to sdp (H.248.37
// (06/2008) 6.6.1); LATCH, OFF and RELATCH in a Modify then each take
// effect (6.6.1 to 6.6.3).
func TestIqGatewayLatchesOnlyWhenAsked(t *testing.T) {
	minPort, maxPort := rtptest.PortRange(t, 20)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(agw.Handler(relay))
	t.Cleanup(api.Close)
	t.Cleanup(func() {
		for _, c := range relay.Contexts() {
			relay.Subtract(c.ID)
		}
	})
	b := newTestbed(t, ControllerConfig{}, func(c *GatewayConfig) { c.Relay = agw.NewClient(api.URL) })
	b.waitRegistered(t)

	sdpConn, ue, other, network := localUDP(t), localUDP(t), localUDP(t), localUDP(t)
	ctx := context.Background()
	c, err := b.controller.Add(ctx,
		agw.TerminationRequest{ID: agw.Access, Latch: agw.NAPTOff, Remote: localAddr(sdpConn)},
		agw.TerminationRequest{ID: agw.Core, Remote: localAddr(network)})
	if err != nil {
		t.Fatal(err)
	}
	access, core := c.Terminations[agw.Access].Local, c.Terminations[agw.Core].Local
	exchange := func(from, to *net.UDPConn, what string) {
		t.Helper()
		from.WriteToUDPAddrPort([]byte("from "+what), access)
		expectPacket(t, network, "from "+what)
		network.WriteToUDPAddrPort([]byte("to "+what), core)
		expectPacket(t, to, "to "+what)
	}
	modify := func(l agw.Latch) {
		t.Helper()
		if err := b.controller.Modify(ctx, c.ID, agw.Access, agw.TerminationRequest{Latch: l}); err != nil {
			t.Fatal(err)
		}
	}

	exchange(ue, sdpConn, "a termination added without the signal")
	modify(agw.NAPTLatch)
	exchange(ue, ue, "LATCH")
	modify(agw.NAPTOff)
	exchange(ue, sdpConn, "OFF")
	modify(agw.NAPTRelatch)
	exchange(other, other, "RELATCH")
	if err := b.controller.Subtract(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
}

func localUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func localAddr(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

// expectPacket reads the next packet that reaches c, and fails unless
// it is want.
func expectPacket(t *testing.T, c *net.UDPConn, want string) {
	t.Helper()
	const limit = 2 * time.Second
	c.SetReadDeadline(time.Now().Add(limit))
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("%q did not reach %v within %v: %v", want, localAddr(c), limit, err)
	}
	if got := string(buf[:n]); got != want {
		t.Fatalf("%v got %q, want %q", localAddr(c), got, want)
	}
}
