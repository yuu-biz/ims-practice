package ibcf_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// noDNS stands for the DNS resolver of the role (RFC 3263), such as
// the *sipdns.Resolver of the other roles, which the wiring in cmd/ims
// passes. The example sends no request, so it resolves nothing.
type noDNS struct{}

func (noDNS) Locate(context.Context, string, int, sip.Transport) ([]sip.Destination, error) {
	return nil, errors.New("the example resolves no name")
}

var locator ims.Locator = noDNS{}

// This example builds the IBCF role of network A towards network B:
// the agreement with network B, the TrGW over the media relay's control
// API, the Store, the counters, and the two transports of the B2BUA.
// The addresses are from the documentation ranges: 192.0.2.0/24 inside
// network A, and 198.51.100.0/24 between the two networks. The external
// UA lets in only the requests from the addresses of a peer.
func Example() {
	log := slog.New(slog.DiscardHandler)
	const home = "ims.mnc001.mcc001.3gppnetwork.org"
	internal := sip.URI{Scheme: "sip", Host: "ibcf." + home}
	external := sip.URI{Scheme: "sip", Host: "ibcf.nni.mnc001.example.net"}
	peers, err := ibcf.NewPeerTable(ibcf.Peer{
		Profile: interconnect.Profile{
			Name:         "peer-b",
			Domain:       "ims.mnc002.mcc001.3gppnetwork.org",
			Transports:   []sip.Transport{sip.UDP, sip.TCP},
			Methods:      []sip.Method{sip.MESSAGE},
			NumberFormat: interconnect.NumberFormatSIP,
			Codecs:       []string{"AMR-WB", "AMR", "PCMU", "telephone-event"},
		},
		EntryPoint: "sip:ibcf.nni.mnc002.example.net",
		Sources:    []netip.Prefix{netip.MustParsePrefix("198.51.100.20/32")},
	})
	if err != nil {
		panic(err)
	}
	// A node serves this registry with metrics.NewHandler.
	registry := metrics.NewRegistry()
	role, err := ibcf.New(ibcf.Config{
		Internal:   internal,
		External:   external,
		HomeDomain: home,
		ICSCF:      sip.URI{Scheme: "sip", Host: "icscf." + home},
		Metrics:    registry,
		// The TrGW shows 192.0.2.60 to network A and 198.51.100.60 to
		// network B.
		TrGWCoreAddress: netip.MustParseAddr("192.0.2.60"),
		TrGWPeerAddress: netip.MustParseAddr("198.51.100.60"),
	}, peers, agw.NewClient("http://trgw:8080"), ibcf.NewMemoryStore(), locator, log)
	if err != nil {
		panic(err)
	}

	// A node listens on 192.0.2.50:5060 and 198.51.100.10:5060; the
	// example on free ports of the loopback.
	inside, err := sip.Listen("127.0.0.1:0", log)
	if err != nil {
		panic(err)
	}
	outside, err := sip.Listen("127.0.0.1:0", log)
	if err != nil {
		panic(err)
	}
	if err := role.Listen(inside, outside); err != nil {
		panic(err)
	}
	defer role.Close()

	for _, source := range []string{"198.51.100.20:5060", "203.0.113.9:5060"} {
		from := sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort(source)}
		fmt.Println(source, role.Screen(&sip.Message{Method: sip.OPTIONS}, from))
	}
	// Output:
	// 198.51.100.20:5060 true
	// 203.0.113.9:5060 false
}
