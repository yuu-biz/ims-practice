package ibcf_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// newRelay is a media relay on loopback with RTP ports that no other
// test holds while this one runs (rtptest.PortRange).
func newRelay(t *testing.T) *agw.Relay {
	t.Helper()
	minPort, maxPort := rtptest.PortRange(t, 40)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return relay
}

// AGWClient drives the real media relay over its control API,
// so that a change to the API shows up here.
func TestAGWClient(t *testing.T) {
	relay := newRelay(t)
	server := httptest.NewServer(agw.Handler(relay))
	defer server.Close()
	core, peer := netip.MustParseAddr("192.0.2.50"), netip.MustParseAddr("198.51.100.50")
	trgw := ibcf.NewAGWClient(server.URL, core, peer)
	ctx := context.Background()

	c, err := trgw.Reserve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contexts := relay.Contexts()
	if len(contexts) != 1 || contexts[0].ID != c.ID {
		t.Fatalf("the relay has %+v after Reserve of %s", contexts, c.ID)
	}
	terms := contexts[0].Terminations
	// The SDP shows the TrGW's address of each network, with the
	// relay's ports: core is the relay's core termination, peer its
	// access termination.
	if c.Core != netip.AddrPortFrom(core, terms[agw.Core].Local.Port()) || c.Peer != netip.AddrPortFrom(peer, terms[agw.Access].Local.Port()) {
		t.Errorf("Reserve gave core %s and peer %s for the relay's %+v", c.Core, c.Peer, terms)
	}

	remote := netip.MustParseAddrPort("198.51.100.60:30002")
	if err := trgw.Configure(ctx, c.ID, ibcf.PeerSide, remote); err != nil {
		t.Fatal(err)
	}
	if got := relay.Contexts()[0].Terminations[agw.Access].Remote; got != remote {
		t.Errorf("the relay's access termination sends to %s, want %s", got, remote)
	}

	if err := trgw.Release(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if contexts := relay.Contexts(); len(contexts) != 0 {
		t.Errorf("the relay still has %+v after Release", contexts)
	}
	if err := trgw.Release(ctx, c.ID); !errors.Is(err, ibcf.ErrNoContext) {
		t.Errorf("a second Release gave %v, want ErrNoContext", err)
	}
}

// Without addresses of its own, the TrGW shows the relay's address.
func TestAGWClientKeepsTheRelaysAddress(t *testing.T) {
	relay := newRelay(t)
	server := httptest.NewServer(agw.Handler(relay))
	defer server.Close()
	trgw := ibcf.NewAGWClient(server.URL, netip.Addr{}, netip.Addr{})
	c, err := trgw.Reserve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer trgw.Release(context.Background(), c.ID)
	if c.Core.Addr() != netip.MustParseAddr("127.0.0.1") || c.Peer.Addr() != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("Reserve gave core %s and peer %s, want the relay's address", c.Core, c.Peer)
	}
}
