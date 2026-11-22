package fivegc

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/n5"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

func TestParseFDesc(t *testing.T) {
	for _, c := range []struct {
		in   n5.FlowDescription
		want policy.FlowDescription
	}{
		{"permit in 17 from 172.28.0.110 49152 to 172.28.0.50 40000", policy.FlowDescription{
			Direction: policy.Uplink, Protocol: 17,
			Source: netip.MustParsePrefix("172.28.0.110/32"), SourcePort: 49152,
			Destination: netip.MustParsePrefix("172.28.0.50/32"), DestinationPort: 40000}},
		{"permit out 17 from 172.28.0.50 to 172.28.0.110 49152", policy.FlowDescription{
			Direction: policy.Downlink, Protocol: 17,
			Source:      netip.MustParsePrefix("172.28.0.50/32"),
			Destination: netip.MustParsePrefix("172.28.0.110/32"), DestinationPort: 49152}},
		{"permit in ip from any to 198.51.100.0/24", policy.FlowDescription{
			Direction: policy.Uplink, Destination: netip.MustParsePrefix("198.51.100.0/24")}},
		{"permit out 17 from 198.51.100.7/24 to any", policy.FlowDescription{
			Direction: policy.Downlink, Protocol: 17, Source: netip.MustParsePrefix("198.51.100.0/24")}},
	} {
		got, err := parseFDesc(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// TS 29.214 5.3.8 allows only permit, one port on each side and no
// options; anything else is FILTER_RESTRICTIONS (TS 29.514 4.2.2.2).
func TestParseFDescRefusesWhatTS29214Restricts(t *testing.T) {
	for _, in := range []n5.FlowDescription{
		"deny in 17 from any to any",
		"permit both 17 from any to any",
		"permit in udp from any to any",
		"permit in 17 from any to any 5000-5010",
		"permit in 17 from any to any 5000,5002",
		"permit in 17 from any to any frag",
		"permit in 17 from !172.28.0.1 to any",
		"permit in 17 from assigned to any",
		"permit in 17 from 2001:db8::1 to any",
		"permit in 17 from any to 2001:db8::/32",
		"permit in 17 to any",
	} {
		if _, err := parseFDesc(in); !errors.Is(err, errFilterRestrictions) {
			t.Errorf("%q: %v, want FILTER_RESTRICTIONS", in, err)
		}
	}
}

// Seen from the UE, the remote end of an uplink flow is its destination
// and that of a downlink flow its source (TS 24.501 9.11.4.13).
func TestPacketFilterIsSeenFromTheUE(t *testing.T) {
	up, _ := parseFDesc("permit in 17 from 172.28.0.110 49152 to 172.28.0.50 40000")
	if got, want := packetFilter(up), (PacketFilter{Direction: UplinkOnly, IPv4RemoteAddress: netip.MustParsePrefix("172.28.0.50/32"),
		ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49152, SingleRemotePort: 40000}); got != want {
		t.Errorf("uplink filter = %+v, want %+v", got, want)
	}
	down, _ := parseFDesc("permit out 17 from 172.28.0.50 40000 to 172.28.0.110 49152")
	if got, want := packetFilter(down), (PacketFilter{Direction: DownlinkOnly, IPv4RemoteAddress: netip.MustParsePrefix("172.28.0.50/32"),
		ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49152, SingleRemotePort: 40000}); got != want {
		t.Errorf("downlink filter = %+v, want %+v", got, want)
	}
}
