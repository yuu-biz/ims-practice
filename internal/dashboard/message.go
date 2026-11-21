package dashboard

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hep"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

type Protocol string

const (
	ProtocolSIP      Protocol = "sip"
	ProtocolDiameter Protocol = "diameter"
)

// Direction is relative to the node that captured the message.
type Direction string

const (
	DirectionSend Direction = "send"
	DirectionRecv Direction = "recv"
	// DirectionUnknown is left when both ends of the message are on the
	// capturing node, as when one node runs several roles.
	DirectionUnknown Direction = ""
)

// Message is one captured copy of a SIP or Diameter message. A message
// that passes from one node to another is captured twice: once by its
// sender and once by its receiver.
type Message struct {
	ID            uint64    `json:"id"`
	Time          time.Time `json:"time"`
	Protocol      Protocol  `json:"protocol"`
	Transport     string    `json:"transport"`
	Src           string    `json:"src"`
	Dst           string    `json:"dst"`
	Node          string    `json:"node"`
	Direction     Direction `json:"direction"`
	Summary       string    `json:"summary"`
	CorrelationID string    `json:"correlation_id"`
	// CallID is the SIP Call-ID, or for a Cx message the Call-ID of the
	// SIP request it was sent for (see causeWindow). It is left out when
	// no request caused the Cx message.
	CallID string `json:"call_id,omitempty"`
	// Payload is the SIP message as it went on the wire, or the JSON
	// rendering of the Diameter message.
	Payload string `json:"payload"`
	Refs    []Ref  `json:"refs"`
	// SMS is the body of a MESSAGE of SMS over IP, decoded; it is left
	// out for every other message.
	SMS *ShortMessage `json:"sms,omitempty"`
}

var transports = map[hep.IPProtocol]string{
	hep.IPProtocolUDP:  "udp",
	hep.IPProtocolTCP:  "tcp",
	hep.IPProtocolSCTP: "sctp",
}

// capture is a Message with what the store derives state from: the
// capturing node and the parsed message.
type capture struct {
	Message
	// from is the address the HEP packet came from, which is the
	// capturing node's own address.
	from     netip.Addr
	sip      *sip.Message     // nil for Diameter
	diameter *diameterMessage // nil for SIP
}

// newMessage turns a HEP packet that came from the address from into a
// capture.
func newMessage(p hep.Packet, from netip.Addr) (capture, error) {
	src, dst := ownAddress(p.Source, from), ownAddress(p.Destination, from)
	m := Message{
		Time:          p.Time,
		Transport:     transports[p.IPProtocol],
		Src:           src.String(),
		Dst:           dst.String(),
		Node:          p.NodeName,
		Direction:     direction(src.Addr(), dst.Addr(), from),
		CorrelationID: p.CorrelationID,
		Payload:       string(p.Payload),
	}
	if m.Transport == "" {
		return capture{}, fmt.Errorf("IP protocol %d", p.IPProtocol)
	}
	switch p.ProtocolType {
	case hep.ProtocolTypeSIP:
		s, err := sip.Parse(p.Payload)
		if err != nil {
			return capture{}, err
		}
		m.Protocol = ProtocolSIP
		m.CallID = s.Get("Call-ID")
		m.Summary, m.Refs = sipSummary(s), sipRefs(s)
		m.SMS = shortMessageOf(s)
		return capture{Message: m, from: from, sip: s}, nil
	case hep.ProtocolTypeDiameterJSON:
		d, err := parseDiameter(p.Payload)
		if err != nil {
			return capture{}, err
		}
		m.Protocol = ProtocolDiameter
		m.Summary, m.Refs = d.summary(), d.refs()
		return capture{Message: m, from: from, diameter: &d}, nil
	}
	return capture{}, errors.New("neither SIP nor DIAMETER (JSON)")
}

// ownAddress puts the capturing node's address in place of the wildcard
// address that a node listening on 0.0.0.0 may report as its own. The
// sender's and the receiver's copies of a message then name the same
// addresses, which is how the UI pairs them into one arrow.
func ownAddress(a netip.AddrPort, from netip.Addr) netip.AddrPort {
	if a.Addr().IsUnspecified() {
		return netip.AddrPortFrom(from, a.Port())
	}
	return a
}

// direction finds which end of the message is the capturing node. HEP
// has no direction chunk, so it compares the addresses with the one the
// HEP packet came from.
func direction(src, dst, from netip.Addr) Direction {
	switch {
	case src == from && dst != from:
		return DirectionSend
	case dst == from && src != from:
		return DirectionRecv
	}
	return DirectionUnknown
}

// sipSummary is the start line, shortened for a response to its code,
// reason and the method it answers: "200 OK (INVITE)".
func sipSummary(m *sip.Message) string {
	if m.IsRequest() {
		return string(m.Method) + " " + m.RequestURI
	}
	return fmt.Sprintf("%d %s (%s)", m.StatusCode, m.Reason, cseqMethod(m))
}

// cseqMethod is the method a message belongs to: its own for a request,
// the one it answers for a response (RFC 3261 8.2.6.2 copies CSeq).
func cseqMethod(m *sip.Message) sip.Method {
	if m.IsRequest() {
		return m.Method
	}
	cseq, err := sip.ParseCSeq(m.Get("CSeq"))
	if err != nil {
		return ""
	}
	return cseq.Method
}
