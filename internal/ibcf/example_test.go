package ibcf_test

import (
	"log/slog"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// locator stands for the DNS resolver of the role (RFC 3263), such as
// the *sipdns.Resolver of the other roles, which the wiring in cmd/ims
// passes. The example is compiled but not run.
var locator ims.Locator

// This example builds the IBCF role of network A towards network B:
// the agreement with network B, the TrGW over the media relay's control
// API, the Store, the counters, and the two transports of the B2BUA.
// The addresses are from the documentation ranges: 192.0.2.0/24 inside
// network A, and 198.51.100.0/24 between the two networks.
func Example() {
	log := slog.Default()
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

	inside, err := sip.Listen("192.0.2.50:5060", log)
	if err != nil {
		panic(err)
	}
	outside, err := sip.Listen("198.51.100.10:5060", log)
	if err != nil {
		panic(err)
	}
	if err := role.Listen(inside, outside); err != nil {
		panic(err)
	}
	defer role.Close()
}
