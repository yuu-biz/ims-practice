package ibcf_test

import (
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const (
	homeDomain = "ims.mnc001.mcc001.3gppnetwork.org"
	peerDomain = "ims.mnc002.mcc001.3gppnetwork.org"
)

func testPeer(name, domain string) ibcf.Peer {
	return ibcf.Peer{
		Profile: interconnect.Profile{Name: name, Domain: domain, Transports: []sip.Transport{sip.UDP}},
	}
}

func TestPeerTable(t *testing.T) {
	b := testPeer("peer-b", peerDomain)
	b.EntryPoint = "sip:ibcf.nni.mnc002.example.net:5070"
	b.Hosts = []string{"ibcf2.nni.mnc002.example.net"}
	b.Sources = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/28")}
	c := testPeer("peer-c", "ims.mnc003.mcc001.3gppnetwork.org")
	c.Sources = []netip.Prefix{netip.MustParsePrefix("203.0.113.7/32")}
	table, err := ibcf.NewPeerTable(b, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want string
	}{
		{peerDomain, "peer-b"},
		{"scscf." + peerDomain, "peer-b"},
		{"IBCF.NNI.MNC002.EXAMPLE.NET", "peer-b"}, // the entry point
		{"ibcf2.nni.mnc002.example.net", "peer-b"},
		{"icscf.ims.mnc003.mcc001.3gppnetwork.org.", "peer-c"},
		{"x" + peerDomain, ""},
		{homeDomain, ""},
	} {
		p, ok := table.ByHost(tc.host)
		if got := p.Profile.Name; ok != (tc.want != "") || got != tc.want {
			t.Errorf("ByHost(%s) = %q, %v; want %q", tc.host, got, ok, tc.want)
		}
	}
	for _, tc := range []struct {
		addr string
		want string
	}{
		{"198.51.100.3", "peer-b"},
		{"::ffff:203.0.113.7", "peer-c"},
		{"203.0.113.8", ""},
	} {
		p, ok := table.BySource(netip.MustParseAddr(tc.addr))
		if got := p.Profile.Name; ok != (tc.want != "") || got != tc.want {
			t.Errorf("BySource(%s) = %q, %v; want %q", tc.addr, got, ok, tc.want)
		}
	}
}

func TestPeerTableRefusesMistakes(t *testing.T) {
	noTransport := testPeer("peer-b", peerDomain)
	noTransport.Profile.Transports = nil
	badEntry := testPeer("peer-b", peerDomain)
	badEntry.EntryPoint = "tel:+12025550151"
	nowhere := testPeer("peer-b", "")
	for name, peers := range map[string][]ibcf.Peer{
		"invalid profile":     {noTransport},
		"same name twice":     {testPeer("peer-b", peerDomain), testPeer("peer-b", "ims.mnc003.mcc001.3gppnetwork.org")},
		"entry point not SIP": {badEntry},
		"nowhere to send":     {nowhere},
	} {
		if _, err := ibcf.NewPeerTable(peers...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
