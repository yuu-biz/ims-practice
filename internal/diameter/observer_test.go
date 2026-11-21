package diameter

import (
	"net/netip"
	"testing"
	"time"
)

type observed struct {
	sent     bool
	command  string
	src, dst netip.AddrPort
}

func observeInto(n *Node) <-chan observed {
	ch := make(chan observed, 64)
	n.Observe(func(c Capture) {
		ch <- observed{c.Sent, commandName(c.Message), c.Source, c.Destination}
	})
	return ch
}

func nextObserved(t *testing.T, ch <-chan observed) observed {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("nothing observed")
		return observed{}
	}
}

// Both ends observe the capabilities exchange with the addresses of
// the one connection, so a collector can pair each send with its
// receive.
func TestObserverSeesTheCapabilitiesExchange(t *testing.T) {
	hssNode, _ := newTestNode(hss, testTimers)
	hssSeen := observeInto(hssNode)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	scscfNode, _ := newTestNode(scscf, testTimers)
	scscfSeen := observeInto(scscfNode)
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	defer peer.Stop()

	cer := nextObserved(t, scscfSeen)
	hssAddr := netip.MustParseAddrPort(server.Addr())
	if cer.command != "CER" || !cer.sent || cer.dst != hssAddr || !cer.src.Addr().IsLoopback() {
		t.Fatalf("S-CSCF observed %+v first, want its CER to %v", cer, hssAddr)
	}
	want := []observed{
		{false, "CER", cer.src, cer.dst},
		{true, "CEA", cer.dst, cer.src},
	}
	for _, w := range want {
		if got := nextObserved(t, hssSeen); got != w {
			t.Errorf("HSS observed %+v, want %+v", got, w)
		}
	}
	if got, w := nextObserved(t, scscfSeen), (observed{false, "CEA", cer.dst, cer.src}); got != w {
		t.Errorf("S-CSCF observed %+v, want %+v", got, w)
	}
}
