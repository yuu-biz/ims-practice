package fivegc

import (
	"errors"
	"net/netip"
	"testing"
)

func TestParseFlowDescription(t *testing.T) {
	for _, c := range []struct {
		in   string
		want FlowDescription
	}{
		{"permit in 17 from 172.28.0.110 49152 to 172.28.0.50 40000", FlowDescription{
			Direction: Uplink, Protocol: 17,
			Source: netip.MustParsePrefix("172.28.0.110/32"), SourcePort: 49152,
			Destination: netip.MustParsePrefix("172.28.0.50/32"), DestinationPort: 40000}},
		{"permit out 17 from 172.28.0.50 to 172.28.0.110 49152", FlowDescription{
			Direction: Downlink, Protocol: 17,
			Source:      netip.MustParsePrefix("172.28.0.50/32"),
			Destination: netip.MustParsePrefix("172.28.0.110/32"), DestinationPort: 49152}},
		{"permit in ip from any to 198.51.100.0/24", FlowDescription{
			Direction: Uplink, Destination: netip.MustParsePrefix("198.51.100.0/24")}},
		{"permit out 17 from 198.51.100.7/24 to any", FlowDescription{
			Direction: Downlink, Protocol: 17, Source: netip.MustParsePrefix("198.51.100.0/24")}},
	} {
		got, err := parseFlowDescription(c.in)
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
func TestParseFlowDescriptionRefusesWhatTS29214Restricts(t *testing.T) {
	for _, in := range []string{
		"deny in 17 from any to any",
		"permit both 17 from any to any",
		"permit in udp from any to any",
		"permit in 17 from any to any 5000-5010",
		"permit in 17 from any to any 5000,5002",
		"permit in 17 from any to any frag",
		"permit in 17 from !172.28.0.1 to any",
		"permit in 17 from assigned to any",
		"permit in 17 from 2001:db8::1 to any",
		"permit in 17 to any",
	} {
		if _, err := parseFlowDescription(in); !errors.Is(err, errFilterRestrictions) {
			t.Errorf("%q: %v, want FILTER_RESTRICTIONS", in, err)
		}
	}
}

// Seen from the UE, the remote end of an uplink flow is its destination
// and that of a downlink flow its source (TS 24.501 9.11.4.13).
func TestPacketFilterIsSeenFromTheUE(t *testing.T) {
	up, _ := parseFlowDescription("permit in 17 from 172.28.0.110 49152 to 172.28.0.50 40000")
	if got, want := up.packetFilter(), (PacketFilter{Direction: UplinkOnly, IPv4RemoteAddress: netip.MustParsePrefix("172.28.0.50/32"),
		ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49152, SingleRemotePort: 40000}); got != want {
		t.Errorf("uplink filter = %+v, want %+v", got, want)
	}
	down, _ := parseFlowDescription("permit out 17 from 172.28.0.50 40000 to 172.28.0.110 49152")
	if got, want := down.packetFilter(), (PacketFilter{Direction: DownlinkOnly, IPv4RemoteAddress: netip.MustParsePrefix("172.28.0.50/32"),
		ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49152, SingleRemotePort: 40000}); got != want {
		t.Errorf("downlink filter = %+v, want %+v", got, want)
	}
}
