// Package hep encodes and decodes HEP version 3 packets (Homer Encapsulation
// Protocol). A node wraps a copy of each SIP or Diameter message it sends or
// receives in a HEP packet and sends it to a collector.
//
// The source is "HEP3 Network Protocol Specification", rev. 37, 2025-06-25,
// docs/HEP3_Network_Protocol_Specification_REV_37.pdf in
// https://github.com/sipcapture/HEP (read at commit 010d62e). The chunk type
// IDs come from its table "Generic chunk types" and the protocol types from
// its table "Capture protocol types (0x00b)". Only the generic chunks that
// this repository sends are decoded. Other chunks are skipped, which the
// chunk length field exists to allow.
//
// The capture agent ID (chunk 0x000c) is a uint32 in network byte order, as
// in the REV 37 table and its worked example. A later note in the same
// repository, docs/chunk-0x000c-capture-agent-id.md, asks for a uint16 in the
// first two octets and a new chunk 0x003e instead. This package follows
// REV 37 because heplify and the sipcapture Wireshark dissector both read
// 0x000c as a uint32.
//
// Wireshark has no built-in HEP dissector. The Lua dissector hep.lua from
// https://github.com/sipcapture/hep-wireshark decodes HEP on UDP port 9060.
// It picks the dissector for the payload from the protocol type chunk, and
// passes everything from the payload chunk to the end of the packet to it.
// Marshal therefore writes the protocol type before the payload and the
// payload last.
package hep

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// id is the protocol identifier 0x48455033 that starts every packet.
const id = "HEP3"

// headerLen is the HEP3 header: the ID and the two-octet total length.
const headerLen = 6

// chunkHeaderLen is the chunk vendor ID, chunk type ID and chunk length.
const chunkHeaderLen = 6

// maxTotalLength is the largest value of the two-octet total length.
const maxTotalLength = 65535

// genericVendorID marks the generic chunk types that REV 37 defines.
const genericVendorID = 0x0000

// Generic chunk type IDs (REV 37, "Generic chunk types").
const (
	chunkIPProtocolFamily       = 0x0001
	chunkIPProtocolID           = 0x0002
	chunkIPv4SourceAddress      = 0x0003
	chunkIPv4DestinationAddress = 0x0004
	chunkSourcePort             = 0x0007
	chunkDestinationPort        = 0x0008
	chunkTimestampSeconds       = 0x0009
	chunkTimestampMicroseconds  = 0x000a
	chunkProtocolType           = 0x000b
	chunkCaptureAgentID         = 0x000c
	chunkCapturedPayload        = 0x000f
	chunkCorrelationID          = 0x0011
	chunkNodeName               = 0x0013
)

// fixedPayloadLen is the payload size of each decoded chunk whose payload
// type has a fixed size (uint8, uint16, uint32, inet4-addr).
var fixedPayloadLen = map[uint16]int{
	chunkIPProtocolFamily:       1,
	chunkIPProtocolID:           1,
	chunkIPv4SourceAddress:      4,
	chunkIPv4DestinationAddress: 4,
	chunkSourcePort:             2,
	chunkDestinationPort:        2,
	chunkTimestampSeconds:       4,
	chunkTimestampMicroseconds:  4,
	chunkProtocolType:           1,
	chunkCaptureAgentID:         4,
}

// afINET is the IP protocol family of IPv4. Chunk 0x0001 carries the POSIX
// AF_INET value, not the IANA address family number 1
// (docs/chunk-0x0001-ip-protocol-family.md in the HEP repository).
const afINET = 2

// IPProtocol is the transport that carried the captured message, as an IANA
// protocol number (chunk 0x0002).
type IPProtocol uint8

const (
	IPProtocolTCP  IPProtocol = 6
	IPProtocolUDP  IPProtocol = 17
	IPProtocolSCTP IPProtocol = 132
)

// ProtocolType is the protocol of the captured payload (chunk 0x000b).
type ProtocolType uint8

const (
	ProtocolTypeSIP ProtocolType = 0x01
	// ProtocolTypeDiameterJSON is "DIAMETER (JSON)". Its payload is a JSON
	// rendering of a Diameter message, not the message itself. REV 37
	// assigns no type to a Diameter message in its binary form.
	ProtocolTypeDiameterJSON ProtocolType = 0x38
)

// Packet is one HEP3 packet: a captured message, where it went and when.
type Packet struct {
	IPProtocol     IPProtocol     // chunk 0x0002
	Source         netip.AddrPort // chunks 0x0003 and 0x0007
	Destination    netip.AddrPort // chunks 0x0004 and 0x0008
	Time           time.Time      // chunks 0x0009 and 0x000a, kept to the microsecond
	ProtocolType   ProtocolType   // chunk 0x000b
	CaptureAgentID uint32         // chunk 0x000c
	// CorrelationID (chunk 0x0011) groups the packets of one dialog or
	// session: the Call-ID for SIP and the Session-Id for Diameter. Marshal
	// leaves the chunk out when it is empty.
	CorrelationID string
	// NodeName (chunk 0x0013) names the node that captured the packet, such
	// as "pcscf", so that a collector can label it without an address
	// table. Marshal leaves the chunk out when it is empty.
	NodeName string
	Payload  []byte // chunk 0x000f
}

// Marshal writes p with the chunks in the order of the REV 37 example packet.
func (p Packet) Marshal() ([]byte, error) {
	// SIMPLIFIED: REV 37 "Generic chunk types" also defines IPv6 addresses
	// (chunks 0x0005 and 0x0006, family AF_INET6). Every node here has an
	// IPv4 address, so only IPv4 is written and read.
	if !p.Source.Addr().Is4() || !p.Destination.Addr().Is4() {
		return nil, fmt.Errorf("hep: source %v and destination %v must both be IPv4", p.Source, p.Destination)
	}
	b := []byte(id + "\x00\x00")
	b = appendChunk(b, chunkIPProtocolFamily, []byte{afINET})
	b = appendChunk(b, chunkIPProtocolID, []byte{byte(p.IPProtocol)})
	b = appendChunk(b, chunkIPv4SourceAddress, p.Source.Addr().AsSlice())
	b = appendChunk(b, chunkIPv4DestinationAddress, p.Destination.Addr().AsSlice())
	b = appendChunk(b, chunkSourcePort, binary.BigEndian.AppendUint16(nil, p.Source.Port()))
	b = appendChunk(b, chunkDestinationPort, binary.BigEndian.AppendUint16(nil, p.Destination.Port()))
	b = appendChunk(b, chunkTimestampSeconds, binary.BigEndian.AppendUint32(nil, uint32(p.Time.Unix())))
	b = appendChunk(b, chunkTimestampMicroseconds, binary.BigEndian.AppendUint32(nil, uint32(p.Time.Nanosecond()/1000)))
	b = appendChunk(b, chunkProtocolType, []byte{byte(p.ProtocolType)})
	b = appendChunk(b, chunkCaptureAgentID, binary.BigEndian.AppendUint32(nil, p.CaptureAgentID))
	if p.CorrelationID != "" {
		b = appendChunk(b, chunkCorrelationID, []byte(p.CorrelationID))
	}
	if p.NodeName != "" {
		b = appendChunk(b, chunkNodeName, []byte(p.NodeName))
	}
	b = appendChunk(b, chunkCapturedPayload, p.Payload)
	// SIMPLIFIED: docs/HEP3-extended-packet-length.md in the HEP repository
	// proposes a 32-bit length for packets over 65535 octets. SIP and
	// Diameter messages here stay far below that, so it is not implemented.
	if len(b) > maxTotalLength {
		return nil, fmt.Errorf("hep: packet of %d octets exceeds the total length limit of %d", len(b), maxTotalLength)
	}
	binary.BigEndian.PutUint16(b[4:], uint16(len(b)))
	return b, nil
}

func appendChunk(b []byte, chunkTypeID uint16, payload []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, genericVendorID)
	b = binary.BigEndian.AppendUint16(b, chunkTypeID)
	b = binary.BigEndian.AppendUint16(b, uint16(chunkHeaderLen+len(payload)))
	return append(b, payload...)
}

// Unmarshal reads one HEP3 packet that fills b exactly, as a UDP datagram
// does. The returned packet does not share memory with b.
func Unmarshal(b []byte) (Packet, error) {
	if len(b) < headerLen || string(b[:4]) != id {
		return Packet{}, errors.New("hep: not a HEP3 packet")
	}
	if total := int(binary.BigEndian.Uint16(b[4:])); total != len(b) {
		return Packet{}, fmt.Errorf("hep: total length is %d but the packet has %d octets", total, len(b))
	}
	var (
		p                     Packet
		sourceAddr, destAddr  netip.Addr
		sourcePort, destPort  uint16
		seconds, microseconds uint32
	)
	for rest := b[headerLen:]; len(rest) > 0; {
		if len(rest) < chunkHeaderLen {
			return Packet{}, fmt.Errorf("hep: %d octets left, too short for a chunk header", len(rest))
		}
		vendorID := binary.BigEndian.Uint16(rest[0:])
		chunkTypeID := binary.BigEndian.Uint16(rest[2:])
		chunkLen := int(binary.BigEndian.Uint16(rest[4:]))
		if chunkLen < chunkHeaderLen || chunkLen > len(rest) {
			return Packet{}, fmt.Errorf("hep: chunk %#04x has length %d with %d octets left", chunkTypeID, chunkLen, len(rest))
		}
		payload := rest[chunkHeaderLen:chunkLen]
		rest = rest[chunkLen:]
		if vendorID != genericVendorID {
			continue
		}
		if want, fixed := fixedPayloadLen[chunkTypeID]; fixed && len(payload) != want {
			return Packet{}, fmt.Errorf("hep: chunk %#04x has a %d-octet payload, want %d", chunkTypeID, len(payload), want)
		}
		switch chunkTypeID {
		case chunkIPProtocolID:
			p.IPProtocol = IPProtocol(payload[0])
		case chunkIPv4SourceAddress:
			sourceAddr = netip.AddrFrom4([4]byte(payload))
		case chunkIPv4DestinationAddress:
			destAddr = netip.AddrFrom4([4]byte(payload))
		case chunkSourcePort:
			sourcePort = binary.BigEndian.Uint16(payload)
		case chunkDestinationPort:
			destPort = binary.BigEndian.Uint16(payload)
		case chunkTimestampSeconds:
			seconds = binary.BigEndian.Uint32(payload)
		case chunkTimestampMicroseconds:
			microseconds = binary.BigEndian.Uint32(payload)
		case chunkProtocolType:
			p.ProtocolType = ProtocolType(payload[0])
		case chunkCaptureAgentID:
			p.CaptureAgentID = binary.BigEndian.Uint32(payload)
		case chunkCorrelationID:
			p.CorrelationID = string(payload)
		case chunkNodeName:
			p.NodeName = string(payload)
		case chunkCapturedPayload:
			p.Payload = bytes.Clone(payload)
		}
	}
	p.Source = netip.AddrPortFrom(sourceAddr, sourcePort)
	p.Destination = netip.AddrPortFrom(destAddr, destPort)
	p.Time = time.Unix(int64(seconds), int64(microseconds)*1000).UTC()
	return p, nil
}
