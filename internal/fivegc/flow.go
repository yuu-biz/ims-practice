package fivegc

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/n5"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

// errFilterRestrictions: a flow description breaks the restrictions of
// TS 29.214 5.3.8, which the PCF answers with FILTER_RESTRICTIONS (TS
// 29.514 4.2.2.2).
var errFilterRestrictions = errors.New("fivegc: flow description breaks the restrictions of TS 29.214 5.3.8")

// parseFDesc reads one fDescs entry of a media sub-component (TS 29.514
// 5.6.3.2), an IPFilterRule of TS 29.214 5.3.8.
//
// SIMPLIFIED: TS 29.214 5.3.8. Addresses are IPv4 only, as the stub
// serves IPv4 PDU sessions.
func parseFDesc(d n5.FlowDescription) (policy.FlowDescription, error) {
	f, err := policy.ParseFlowDescription(string(d))
	if err != nil {
		return policy.FlowDescription{}, fmt.Errorf("%w: %v", errFilterRestrictions, err)
	}
	ipv4 := func(p netip.Prefix) bool { return !p.IsValid() || p.Addr().Is4() }
	if !ipv4(f.Source) || !ipv4(f.Destination) {
		return policy.FlowDescription{}, fmt.Errorf("%w: %q: addresses are IPv4 only", errFilterRestrictions, d)
	}
	return f, nil
}

// packetFilter turns a flow into the packet filter of a QoS rule, seen
// from the UE: the remote end is the destination of an uplink flow and
// the source of a downlink one (TS 24.501 9.11.4.13: local is the UE,
// remote the far end).
//
// SIMPLIFIED: TS 24.501 9.11.4.13. Each flow becomes a filter of its
// own direction, and the local address is left out, as the UE has only
// the one address of the PDU session.
func packetFilter(f policy.FlowDescription) PacketFilter {
	pf := PacketFilter{ProtocolIdentifierNextHeader: f.Protocol}
	if f.Direction == policy.Uplink {
		pf.Direction, pf.IPv4RemoteAddress = UplinkOnly, f.Destination
		pf.SingleRemotePort, pf.SingleLocalPort = f.DestinationPort, f.SourcePort
	} else {
		pf.Direction, pf.IPv4RemoteAddress = DownlinkOnly, f.Source
		pf.SingleRemotePort, pf.SingleLocalPort = f.SourcePort, f.DestinationPort
	}
	return pf
}
