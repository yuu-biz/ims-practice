package ibcf

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/openapitest"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The media relay of internal/agw serves as the TrGW over its HTTP API,
// as it does in the deployment: its core termination faces the home
// network and its access termination the peer network. The relay
// writes one address for both, and the SDP of each side shows the
// TrGW's address in that network from Config, with the port that the
// relay chose.
func TestTheMediaRelayAsTheTrGW(t *testing.T) {
	minPort, maxPort := rtptest.PortRange(t, 8)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(openapitest.Load(t, "../agw/openapi.yaml").Handler(t, agw.Handler(relay)))
	defer server.Close()
	core, peer := netip.MustParseAddr("192.0.2.50"), netip.MustParseAddr("198.51.100.50")
	b := &IBCF{cfg: Config{TrGWCoreAddress: core, TrGWPeerAddress: peer}, trgw: agw.NewClient(server.URL)}
	ctx := context.Background()

	c, err := b.reserveMedia(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contexts := relay.Contexts()
	if len(contexts) != 1 || contexts[0].ID != c.ID || len(contexts[0].Terminations) != 2 {
		t.Fatalf("the relay has %+v after the IBCF reserved %s", contexts, c.ID)
	}
	terms := contexts[0].Terminations
	if terms[agw.Core] == nil || terms[agw.Access] == nil {
		t.Fatalf("the relay has the terminations %+v, want core and access", terms)
	}
	if c.Core != netip.AddrPortFrom(core, terms[agw.Core].Local.Port()) || c.Peer != netip.AddrPortFrom(peer, terms[agw.Access].Local.Port()) {
		t.Errorf("the IBCF shows core %s and peer %s for the relay's %+v", c.Core, c.Peer, terms)
	}

	// An offer enters from the peer network: the access termination
	// learns where the peer takes its media, and the offer goes on into
	// the home network with the core address of the TrGW.
	sender := netip.MustParseAddrPort("198.51.100.60:30002")
	offer := &sip.Message{Method: sip.INVITE, Body: []byte("v=0\r\no=- 1 1 IN IP4 198.51.100.60\r\ns=-\r\nc=IN IP4 198.51.100.60\r\nt=0 0\r\nm=audio 30002 RTP/AVP 0\r\n")}
	if err := b.throughTrGW(offer, c, interconnect.Entry); err != nil {
		t.Fatal(err)
	}
	if got := relay.Contexts()[0].Terminations[agw.Access].Remote; got != sender {
		t.Errorf("the access termination sends to %s, want %s", got, sender)
	}
	description, err := sdp.Parse(offer.Body)
	if err != nil {
		t.Fatal(err)
	}
	if shown, _, err := description.Audio(); err != nil || shown != c.Core {
		t.Errorf("the offer shows %s (%v), want %s", shown, err, c.Core)
	}

	b.releaseMedia(c, slog.New(slog.DiscardHandler))
	if contexts := relay.Contexts(); len(contexts) != 0 {
		t.Errorf("the relay still has %+v after the release", contexts)
	}

	// Without addresses in Config, the SDP shows the relay's own.
	b.cfg = Config{}
	c, err = b.reserveMedia(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.releaseMedia(c, slog.New(slog.DiscardHandler))
	if loopback := netip.MustParseAddr("127.0.0.1"); c.Core.Addr() != loopback || c.Peer.Addr() != loopback {
		t.Errorf("the IBCF shows core %s and peer %s, want the relay's address", c.Core, c.Peer)
	}
}
