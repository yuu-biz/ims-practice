package fivegc

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// FlowDirection is the direction of an IP flow seen from the UE (TS
// 29.214 5.3.8): "in" is uplink and "out" is downlink.
type FlowDirection string

const (
	Uplink   FlowDirection = "uplink"
	Downlink FlowDirection = "downlink"
)

// FlowDescription is one IP flow of a media sub-component: the fDescs
// of N5, an IPFilterRule of RFC 6733 4.3 within the restrictions of TS
// 29.214 5.3.8 (TS 29.514 5.6.3.2). An invalid prefix is "any", a zero
// port any port, and a zero protocol the keyword "ip".
type FlowDescription struct {
	Direction       FlowDirection `json:"direction"`
	Protocol        uint8         `json:"protocol,omitempty"`
	Source          netip.Prefix  `json:"source,omitzero"`
	SourcePort      uint16        `json:"source_port,omitempty"`
	Destination     netip.Prefix  `json:"destination,omitzero"`
	DestinationPort uint16        `json:"destination_port,omitempty"`
}

// errFilterRestrictions: a flow description breaks the restrictions of
// TS 29.214 5.3.8, which the PCF answers with FILTER_RESTRICTIONS (TS
// 29.514 4.2.2.2).
var errFilterRestrictions = errors.New("fivegc: flow description breaks the restrictions of TS 29.214 5.3.8")

// parseFlowDescription reads "permit in|out <protocol> from <address>
// [<port>] to <address> [<port>]". TS 29.214 5.3.8 allows only the
// action permit, one port at most on each side, no options, no "!" and
// no "assigned".
//
// SIMPLIFIED: TS 29.214 5.3.8. Addresses are IPv4 only, as the stub
// serves IPv4 PDU sessions.
func parseFlowDescription(s string) (FlowDescription, error) {
	bad := func(why string) (FlowDescription, error) {
		return FlowDescription{}, fmt.Errorf("%w: %q: %s", errFilterRestrictions, s, why)
	}
	f := strings.Fields(s)
	if len(f) < 3 || f[0] != "permit" {
		return bad(`want "permit <in|out> <protocol> from <address> [port] to <address> [port]"`)
	}
	var fd FlowDescription
	switch f[1] {
	case "in":
		fd.Direction = Uplink
	case "out":
		fd.Direction = Downlink
	default:
		return bad("direction is neither in nor out")
	}
	if f[2] != "ip" {
		proto, err := strconv.ParseUint(f[2], 10, 8)
		if err != nil {
			return bad("protocol is neither a number nor ip")
		}
		fd.Protocol = uint8(proto)
	}
	rest := f[3:]
	var err error
	if fd.Source, fd.SourcePort, rest, err = endpoint(rest, "from"); err != nil {
		return bad(err.Error())
	}
	if fd.Destination, fd.DestinationPort, rest, err = endpoint(rest, "to"); err != nil {
		return bad(err.Error())
	}
	if len(rest) > 0 {
		return bad("has options or more than one port")
	}
	return fd, nil
}

// endpoint reads "<keyword> <address> [<port>]" from the head of f.
func endpoint(f []string, keyword string) (netip.Prefix, uint16, []string, error) {
	if len(f) < 2 || f[0] != keyword {
		return netip.Prefix{}, 0, nil, fmt.Errorf("no %q and address", keyword)
	}
	var prefix netip.Prefix
	switch h := f[1]; {
	case h == "any":
	case strings.Contains(h, "/"):
		p, err := netip.ParsePrefix(h)
		if err != nil || !p.Addr().Is4() {
			return netip.Prefix{}, 0, nil, fmt.Errorf("%q is not an IPv4 address with a mask", h)
		}
		prefix = p.Masked()
	default:
		a, err := netip.ParseAddr(h)
		if err != nil || !a.Is4() {
			return netip.Prefix{}, 0, nil, fmt.Errorf("%q is not an IPv4 address or any", h)
		}
		prefix = netip.PrefixFrom(a, 32)
	}
	f = f[2:]
	if len(f) > 0 && f[0] != "to" {
		port, err := strconv.ParseUint(f[0], 10, 16)
		if err != nil || port == 0 {
			return netip.Prefix{}, 0, nil, fmt.Errorf("%q is not one port", f[0])
		}
		return prefix, uint16(port), f[1:], nil
	}
	return prefix, 0, f, nil
}

// packetFilter turns a flow into the packet filter of a QoS rule, seen
// from the UE: the remote end is the destination of an uplink flow and
// the source of a downlink one (TS 24.501 9.11.4.13: local is the UE,
// remote the far end).
//
// SIMPLIFIED: TS 24.501 9.11.4.13. Each flow becomes a filter of its
// own direction, and the local address is left out, as the UE has only
// the one address of the PDU session.
func (f FlowDescription) packetFilter() PacketFilter {
	pf := PacketFilter{ProtocolIdentifierNextHeader: f.Protocol}
	if f.Direction == Uplink {
		pf.Direction, pf.IPv4RemoteAddress = UplinkOnly, f.Destination
		pf.SingleRemotePort, pf.SingleLocalPort = f.DestinationPort, f.SourcePort
	} else {
		pf.Direction, pf.IPv4RemoteAddress = DownlinkOnly, f.Source
		pf.SingleRemotePort, pf.SingleLocalPort = f.SourcePort, f.DestinationPort
	}
	return pf
}
