// Package hepagent is the HEP capture agent of a node. It turns
// what the SIP transport layer and the Diameter node observe into HEP
// packets and sends them to a collector, the Lab.
package hepagent

import (
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hep"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// queueSize bounds the copies waiting to be sent. A call through the
// whole network is a few hundred messages, far fewer than this.
const queueSize = 1024

// Agent sends the copies of one node. Its methods are safe for
// concurrent use, as the observers run on many goroutines.
type Agent struct {
	sender *hep.Sender
	id     uint32 // capture agent ID, chunk 0x000c
}

// New starts an agent that sends to collector ("host:port"). A name that
// does not resolve yet only logs a warning, and the agent finds the
// collector when it starts (see hep.NewSender).
func New(collector string, id uint32, log *slog.Logger) (*Agent, error) {
	s, err := hep.NewSender(collector, queueSize, log)
	if err != nil {
		return nil, err
	}
	return &Agent{sender: s, id: id}, nil
}

// Close stops sending. A nil Agent does nothing.
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	return a.sender.Close()
}

// SIP is the observer for the SIP transport of the role node, such as
// "pcscf". A nil Agent gives a nil observer, so nothing is copied.
func (a *Agent) SIP(node string) sip.Observer {
	if a == nil {
		return nil
	}
	return func(c sip.Capture) {
		p := hep.Packet{
			IPProtocol:     hep.IPProtocolUDP,
			Source:         unmap(c.Source),
			Destination:    unmap(c.Destination),
			Time:           c.Time,
			ProtocolType:   hep.ProtocolTypeSIP,
			CaptureAgentID: a.id,
			CorrelationID:  c.Message.Get("Call-ID"),
			NodeName:       node,
			Payload:        c.Wire,
		}
		if c.Transport == sip.TCP {
			p.IPProtocol = hep.IPProtocolTCP
		}
		if c.Sent {
			p.Source = sendingAddr(p.Source, p.Destination)
		} else {
			p.Destination = sendingAddr(p.Destination, p.Source)
		}
		a.send(p)
	}
}

// Diameter is the observer for the Diameter node of the role node. A
// nil Agent gives a nil observer.
func (a *Agent) Diameter(node string) diameter.Observer {
	if a == nil {
		return nil
	}
	return func(c diameter.Capture) {
		payload, err := diameter.EncodeJSON(c.Message)
		if err != nil {
			return
		}
		a.send(hep.Packet{
			IPProtocol:     hep.IPProtocolTCP,
			Source:         unmap(c.Source),
			Destination:    unmap(c.Destination),
			Time:           time.Now(),
			ProtocolType:   hep.ProtocolTypeDiameterJSON,
			CaptureAgentID: a.id,
			// Empty for CER, DWR and DPR, which have no Session-Id.
			CorrelationID: diameter.SessionID(c.Message),
			NodeName:      node,
			Payload:       payload,
		})
	}
}

// send drops a packet that cannot be encoded, such as one with an IPv6
// address (see hep.Packet.Marshal). The copy is lost, never the
// message.
func (a *Agent) send(p hep.Packet) {
	_ = a.sender.Send(p)
}

// unmap turns an IPv4 address that a dual-stack socket reports as
// ::ffff:a.b.c.d back into a.b.c.d. Go opens such a socket even for a
// listen address of 0.0.0.0, and HEP here carries IPv4 only.
func unmap(a netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(a.Addr().Unmap(), a.Port())
}

// sendingAddr is the local address of a UDP socket that listens on
// every interface, such as 0.0.0.0:5060: the address the host sends
// from toward remote. HEP has no way to say "any address", and the
// collector pairs a copy that one node sent with the copy that the
// other node received by their addresses. Connecting a UDP socket only
// looks up the route; nothing is sent.
func sendingAddr(local, remote netip.AddrPort) netip.AddrPort {
	if !local.Addr().IsUnspecified() {
		return local
	}
	c, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(remote))
	if err != nil {
		return local
	}
	defer c.Close()
	ip := c.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
	return netip.AddrPortFrom(ip, local.Port())
}
