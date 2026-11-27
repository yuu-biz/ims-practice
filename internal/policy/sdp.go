package policy

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// This file derives the service information of an IMS session from its
// SDP offer and answer, by the rules TS 29.213 6.2 makes mandatory for
// the P-CSCF: Table 6.2.1 for each Media-Component-Description and
// Table 6.2.2 for each Media-Sub-Component. "Uplink SDP" is the SDP the
// UE sent and "downlink SDP" the SDP it received (Table 6.2.1 NOTE 8).

// protocolUDP is the IP protocol of RTP over UDP, the proto of an "m="
// line with RTP/AVP (TS 29.213 Table 6.2.2).
const protocolUDP = 17

// ErrUnsupportedTransport: an "m=" line uses a transport other than UDP.
//
// SIMPLIFIED: TS 29.213 Tables 6.2.1 and 6.2.2 have rules for media
// over TCP, such as MSRP. Every call in this project carries RTP over
// UDP, so such a line is refused rather than authorised by rules that
// nothing exercises.
var ErrUnsupportedTransport = errors.New("policy: media over a transport other than UDP")

// MediaComponents derives one media component per "m=" line of the
// offer. Without an answer the fields that need it are wildcards (Table
// 6.2.2 NOTE 6).
func MediaComponents(oa OfferAnswer) ([]MediaComponent, error) {
	offer, err := readSDP(oa.Offer, true, oa.UEOffered)
	if err != nil {
		return nil, fmt.Errorf("offer: %w", err)
	}
	var answer []mediaLine
	if oa.Answer != nil {
		if answer, err = readSDP(oa.Answer, false, !oa.UEOffered); err != nil {
			return nil, fmt.Errorf("answer: %w", err)
		}
		// RFC 3264 6: the answer has as many "m=" lines as the offer.
		if len(answer) != len(offer) {
			return nil, fmt.Errorf("the answer has %d media lines, the offer %d", len(answer), len(offer))
		}
	}
	components := make([]MediaComponent, 0, len(offer))
	for i := range offer {
		pair := linePair{offer: &offer[i]}
		if answer != nil {
			pair.answer = &answer[i]
		}
		c, err := pair.component(uint32(i + 1))
		if err != nil {
			return nil, fmt.Errorf("media line %d: %w", i+1, err)
		}
		components = append(components, c)
	}
	return components, nil
}

// SignallingComponent is the media component with which the P-CSCF
// subscribes to the state of the signalling path: number 0 with one
// sub-component, flow 0, for AF signalling, and nothing else (TS 29.214
// 4.4.5).
func SignallingComponent() MediaComponent {
	return MediaComponent{SubComponents: []MediaSubComponent{{FlowNumber: 0, Usage: AFSignalling}}}
}

// mediaLine is one "m=" line of one SDP with what the rules read from
// it.
type mediaLine struct {
	media sdp.Media
	// session is the SDP that holds the media, for the "b=" lines
	// before its first "m=" line.
	session sdp.Session
	// address is the connection address of the media, its own or the
	// session's.
	address netip.Addr
	// direction is sendrecv, sendonly, recvonly or inactive (RFC 3264
	// 5.1).
	direction string
	fromUE    bool
	answer    bool
}

func readSDP(b []byte, offer, fromUE bool) ([]mediaLine, error) {
	s, err := sdp.Parse(b)
	if err != nil {
		return nil, err
	}
	lines := make([]mediaLine, len(s.Media))
	for i, m := range s.Media {
		lines[i] = mediaLine{
			media:     m,
			session:   s,
			address:   s.Address(m).Addr(),
			direction: s.Direction(m),
			fromUE:    fromUE,
			answer:    !offer,
		}
	}
	return lines, nil
}

func (l *mediaLine) port() uint16 { return uint16(l.media.Port) }

// rtcpPort is the port of RTCP: the one in "a=rtcp" (RFC 3605 2.1), or
// the RTP port plus one (RFC 3550 11).
func (l *mediaLine) rtcpPort() uint16 {
	if v, ok := l.media.Get("rtcp"); ok {
		port, _, _ := strings.Cut(v, " ")
		if n, err := strconv.ParseUint(port, 10, 16); err == nil {
			return uint16(n)
		}
	}
	return l.port() + 1
}

func (l *mediaLine) has(attribute string) bool {
	_, ok := l.media.Get(attribute)
	return ok
}

// linePair is the "m=" line of a media component in the offer and, once
// known, in the answer.
type linePair struct {
	offer  *mediaLine
	answer *mediaLine // nil while only the offer is known
}

func (p linePair) uplink() *mediaLine   { return p.from(true) }
func (p linePair) downlink() *mediaLine { return p.from(false) }

func (p linePair) from(ue bool) *mediaLine {
	if p.offer.fromUE == ue {
		return p.offer
	}
	return p.answer
}

// decisive is the line whose port and direction set the flow status and
// the flow descriptions: the answer once there is one (Table 6.2.1 NOTE
// 5, Table 6.2.2 NOTE 4).
func (p linePair) decisive() *mediaLine { return cmp.Or(p.answer, p.offer) }

// direction is the direction of the decisive line, except that an
// "a=inactive" in the offer wins, for the sake of clients that do not
// understand it (Table 6.2.1 NOTE 5).
func (p linePair) direction() string {
	if p.offer.direction == "inactive" {
		return "inactive"
	}
	return p.decisive().direction
}

// rtcpMux reports whether RTP and RTCP share one port. RFC 5761 5.1.1:
// the offerer asks with "a=rtcp-mux", and the answer agrees by repeating
// it.
func (p linePair) rtcpMux() bool { return p.decisive().has("rtcp-mux") }

func (p linePair) component(number uint32) (MediaComponent, error) {
	c := MediaComponent{Number: number, Type: mediaType(p.offer.media.Type)}
	if p.decisive().port() == 0 {
		c.FlowStatus = Removed
		return c, nil
	}
	if strings.Contains(strings.ToUpper(p.offer.media.Proto), "TCP") {
		return MediaComponent{}, ErrUnsupportedTransport
	}
	c.FlowStatus = p.flowStatus()
	c.MaxRequestedBandwidthUL = p.maxRequestedBandwidth(p.downlink())
	c.MaxRequestedBandwidthDL = p.maxRequestedBandwidth(p.uplink())
	c.RRBandwidth = p.decisive().bandwidthOf("RR")
	c.RSBandwidth = p.decisive().bandwidthOf("RS")
	c.Codecs = append(c.Codecs, codecData(p.offer))
	if p.answer != nil {
		c.Codecs = append(c.Codecs, codecData(p.answer))
	}
	c.SubComponents = p.subComponents()
	return c, nil
}

// flowStatus follows the Flow-Status row of Table 6.2.1 for UDP.
func (p linePair) flowStatus() FlowStatus {
	if p.rtcpMux() {
		return Enabled // RTCP must always pass (Table 6.2.1 NOTE 12)
	}
	ueSent := p.decisive().fromUE
	switch p.direction() {
	case "recvonly":
		if ueSent {
			return EnabledDownlink
		}
		return EnabledUplink
	case "sendonly":
		if ueSent {
			return EnabledUplink
		}
		return EnabledDownlink
	case "inactive":
		return Disabled
	}
	return Enabled
}

// maxRequestedBandwidth follows the Max-Requested-Bandwidth-UL and -DL
// rows of Table 6.2.1 for UDP: the uplink bandwidth comes from the
// downlink SDP, which says what the far end can receive, and the
// downlink bandwidth from the uplink SDP. Nil leaves the AVP out, as
// the table allows when there is no "b=AS" or the SDP is not known yet.
//
// SIMPLIFIED: TS 29.213 Table 6.2.1. "b=TIAS" (RFC 3890) is not
// supported, which the table allows; the "b=AS" branch applies.
func (p linePair) maxRequestedBandwidth(l *mediaLine) *uint32 {
	if l == nil {
		return nil
	}
	as, ok := l.bandwidth("AS")
	if !ok {
		return nil
	}
	bw := uint32(as) * 1000
	if p.rtcpMux() {
		rr, hasRR := l.bandwidth("RR")
		rs, hasRS := l.bandwidth("RS")
		if hasRR || hasRS {
			bw += uint32(rr + rs)
		} else {
			bw = uint32(as) * 1050
		}
	}
	return &bw
}

// bandwidth is the "b=" value of this type that holds for the media:
// its own, or else the session's (RFC 3556 4, which Table 6.2.1 NOTE 3
// refers to for b=RS and b=RR). A b=AS of the session is the bandwidth
// that all its media share, and so the most that any one of them may
// use.
func (l *mediaLine) bandwidth(bwtype string) (int, bool) {
	return l.session.Bandwidth(l.media, bwtype)
}

// bandwidthOf is a "b=" value as it is, for RR-Bandwidth and
// RS-Bandwidth (Table 6.2.1), or nil without one.
func (l *mediaLine) bandwidthOf(bwtype string) *uint32 {
	v, ok := l.bandwidth(bwtype)
	if !ok {
		return nil
	}
	b := uint32(v)
	return &b
}

// subComponents follows Table 6.2.2: RTP and RTCP each get a
// sub-component, or share one under rtcp-mux. Flows are numbered in the
// order of their downlink destination ports, the ports in the uplink
// SDP; without an uplink SDP, of their uplink destination ports.
func (p linePair) subComponents() []MediaSubComponent {
	if p.rtcpMux() {
		return []MediaSubComponent{{FlowNumber: 1, Flows: p.flows(false, true)}}
	}
	rtp := MediaSubComponent{Flows: p.flows(false, false)}
	rtcp := MediaSubComponent{Flows: p.flows(true, true), Usage: RTCP}
	ports := cmp.Or(p.uplink(), p.downlink())
	subs := []MediaSubComponent{rtp, rtcp}
	if ports.rtcpPort() < ports.port() {
		subs = []MediaSubComponent{rtcp, rtp}
	}
	for i := range subs {
		subs[i].FlowNumber = uint32(i + 1)
	}
	return subs
}

// flows are the Flow-Descriptions of RTP, or of RTCP when rtcp is true.
// RTCP and multiplexed flows go both ways whatever the direction
// attribute says (Table 6.2.2).
//
// SIMPLIFIED: TS 29.213 Table 6.2.2 NOTE 3. Both flows are kept when a
// sendrecv stream turns sendonly or recvonly in a later offer and
// answer; the rules here see one offer and answer, so they keep only
// the flow of the new direction.
func (p linePair) flows(rtcp, bothWays bool) []FlowDescription {
	up, down := p.uplink(), p.downlink()
	port := func(l *mediaLine) uint16 {
		switch {
		case l == nil:
			return 0
		case rtcp:
			return l.rtcpPort()
		}
		return l.port()
	}
	uplink := FlowDescription{Direction: Uplink, Protocol: protocolUDP,
		Source: address(up), Destination: address(down), DestinationPort: port(down)}
	downlink := FlowDescription{Direction: Downlink, Protocol: protocolUDP,
		Source: address(down), Destination: address(up), DestinationPort: port(up)}
	if !bothWays {
		ueSent := p.decisive().fromUE
		switch p.direction() {
		case "recvonly":
			if ueSent {
				return []FlowDescription{downlink}
			}
			return []FlowDescription{uplink}
		case "sendonly":
			if ueSent {
				return []FlowDescription{uplink}
			}
			return []FlowDescription{downlink}
		}
	}
	return []FlowDescription{uplink, downlink}
}

// address is the connection address of l as the one host of a flow,
// or any without l.
func address(l *mediaLine) netip.Prefix {
	if l == nil {
		return netip.Prefix{}
	}
	return netip.PrefixFrom(l.address, l.address.BitLen())
}

func mediaType(name string) MediaType {
	switch name {
	case "audio":
		return Audio
	case "video":
		return Video
	case "data":
		return Data
	case "application":
		return Application
	case "control":
		return Control
	case "text":
		return Text
	case "message":
		return Message
	}
	return OtherMedia
}

// codecData holds the "m=" line and the "b=" and "a=" lines that
// follow it. The direction attributes and b=AS, b=RS and b=RR may be
// left out, as other AVPs carry them (TS 29.214 5.3.7), and are.
func codecData(l *mediaLine) CodecData {
	m := l.media
	formats := make([]string, len(m.Formats))
	for i, f := range m.Formats {
		formats[i] = strconv.Itoa(f)
	}
	lines := []string{fmt.Sprintf("m=%s %d %s %s", m.Type, m.Port, m.Proto, strings.Join(formats, " "))}
	for _, b := range m.Bandwidths {
		if b.Type != "AS" && b.Type != "RS" && b.Type != "RR" {
			lines = append(lines, fmt.Sprintf("b=%s:%d", b.Type, b.Value))
		}
	}
	for _, a := range m.Attributes {
		switch a.Name {
		case "sendrecv", "sendonly", "recvonly", "inactive":
		default:
			lines = append(lines, "a="+a.String())
		}
	}
	return CodecData{Direction: directionFrom(l.fromUE), Answer: l.answer, Lines: lines}
}

func directionFrom(fromUE bool) Direction {
	if fromUE {
		return Uplink
	}
	return Downlink
}

// String writes the Codec-Data value of TS 29.214 5.3.7: "uplink" or
// "downlink", then "offer" or "answer", then the SDP lines, one per
// line.
func (c CodecData) String() string {
	first := "downlink"
	if c.Direction == Uplink {
		first = "uplink"
	}
	second := "offer"
	if c.Answer {
		second = "answer"
	}
	return strings.Join(append([]string{first, second}, c.Lines...), "\n")
}

// ParseCodecData reads the value of a Codec-Data AVP.
func ParseCodecData(s string) (CodecData, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(s, "\r\n"), "\r\n", "\n"), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[2], "m=") {
		return CodecData{}, fmt.Errorf("Codec-Data %q is not a direction, a kind and an m= line", s)
	}
	var c CodecData
	switch lines[0] {
	case "uplink":
		c.Direction = Uplink
	case "downlink":
		c.Direction = Downlink
	default:
		return CodecData{}, fmt.Errorf("Codec-Data starts with %q, not uplink or downlink", lines[0])
	}
	switch lines[1] {
	case "offer":
	case "answer":
		c.Answer = true
	default:
		return CodecData{}, fmt.Errorf("Codec-Data has %q, not offer or answer", lines[1])
	}
	c.Lines = slices.Clone(lines[2:])
	return c, nil
}

// String writes the flow as the IPFilterRule of a Flow-Description AVP
// (RFC 6733 4.3.1, TS 29.214 5.3.8): "in" for uplink, "out" for
// downlink, "ip" for any protocol, "any" for any address, and a host
// address without its mask.
func (f FlowDescription) String() string {
	dir := "out"
	if f.Direction == Uplink {
		dir = "in"
	}
	proto := "ip"
	if f.Protocol != 0 {
		proto = strconv.Itoa(int(f.Protocol))
	}
	return fmt.Sprintf("permit %s %s from %s to %s", dir, proto,
		endpoint(f.Source, f.SourcePort), endpoint(f.Destination, f.DestinationPort))
}

// endpoint writes "<address> [<port>]", one side of an IPFilterRule.
func endpoint(p netip.Prefix, port uint16) string {
	s := "any"
	switch {
	case !p.IsValid():
	case p.IsSingleIP():
		s = p.Addr().String()
	default:
		s = p.String()
	}
	if port != 0 {
		s += " " + strconv.Itoa(int(port))
	}
	return s
}

// ParseFlowDescription reads "permit in|out <protocol> from <address>
// [<port>] to <address> [<port>]", an IPFilterRule within the
// restrictions of TS 29.214 5.3.8: the action permit only, one port at
// most on each side, no options, no "!" and no "assigned". An address
// may carry a mask.
func ParseFlowDescription(s string) (FlowDescription, error) {
	bad := func(why string) (FlowDescription, error) {
		return FlowDescription{}, fmt.Errorf("Flow-Description %q: %s", s, why)
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
	if fd.Source, fd.SourcePort, rest, err = parseEndpoint(rest, "from"); err != nil {
		return bad(err.Error())
	}
	if fd.Destination, fd.DestinationPort, rest, err = parseEndpoint(rest, "to"); err != nil {
		return bad(err.Error())
	}
	if len(rest) > 0 {
		return bad("has options or more than one port")
	}
	return fd, nil
}

// parseEndpoint reads "<keyword> <address> [<port>]" from the head of f.
func parseEndpoint(f []string, keyword string) (netip.Prefix, uint16, []string, error) {
	if len(f) < 2 || f[0] != keyword {
		return netip.Prefix{}, 0, nil, fmt.Errorf("no %q and address", keyword)
	}
	var prefix netip.Prefix
	switch h := f[1]; {
	case h == "any":
	case strings.Contains(h, "/"):
		p, err := netip.ParsePrefix(h)
		if err != nil {
			return netip.Prefix{}, 0, nil, fmt.Errorf("%q is not an address with a mask", h)
		}
		prefix = p.Masked()
	default:
		a, err := netip.ParseAddr(h)
		if err != nil {
			return netip.Prefix{}, 0, nil, fmt.Errorf("%q is not an address or any", h)
		}
		prefix = netip.PrefixFrom(a, a.BitLen())
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
