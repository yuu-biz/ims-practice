// Package rtcp reads and writes RTCP packets (RFC 3550 6): sender and
// receiver reports, source descriptions with a CNAME, and BYE, sent as
// compound packets. It also keeps the receiver statistics that fill a
// report block (RFC 3550 6.4.1, A.1, A.3, A.8) and computes when the
// next compound packet is due (RFC 3550 6.3.1).
//
// IR.92 3.2.4 is the profile: every compound packet holds one report
// and one SDES with CNAME, no padding is sent, and every packet type is
// accepted on receipt.
package rtcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// version is the RTP version of RFC 3550 6.4.1.
const version = 2

// Packet types (RFC 3550 12.1).
const (
	TypeSR   = 200
	TypeRR   = 201
	TypeSDES = 202
	TypeBYE  = 203
	TypeAPP  = 204
)

// Packet is one RTCP packet of a compound packet.
type Packet interface {
	// Type is the packet type of the header.
	Type() uint8
	// count and body are the header's 5-bit field and what follows the
	// header, a multiple of 4 octets.
	count() int
	body() []byte
}

// ReportBlock is the reception report about one source (RFC 3550
// 6.4.1).
type ReportBlock struct {
	SSRC uint32
	// FractionLost is the share of packets lost since the last report,
	// in 1/256.
	FractionLost uint8
	// CumulativeLost counts the packets lost since the start: expected
	// minus received, negative when duplicates arrived. It is 24 bits.
	CumulativeLost int32
	// HighestSequence is the extended highest sequence number: the
	// cycles of the 16-bit number above it.
	HighestSequence uint32
	// Jitter is the interarrival jitter in RTP timestamp units.
	Jitter uint32
	// LastSR is the middle 32 bits of the NTP timestamp of the last
	// sender report from the source, or 0.
	LastSR uint32
	// DelaySinceLastSR is the time since that report, in 1/65536 s.
	DelaySinceLastSR uint32
}

const blockLen = 24

func (b ReportBlock) marshal() []byte {
	out := make([]byte, blockLen)
	binary.BigEndian.PutUint32(out[0:], b.SSRC)
	binary.BigEndian.PutUint32(out[4:], uint32(b.FractionLost)<<24|uint32(b.CumulativeLost)&0xffffff)
	binary.BigEndian.PutUint32(out[8:], b.HighestSequence)
	binary.BigEndian.PutUint32(out[12:], b.Jitter)
	binary.BigEndian.PutUint32(out[16:], b.LastSR)
	binary.BigEndian.PutUint32(out[20:], b.DelaySinceLastSR)
	return out
}

func parseBlock(b []byte) ReportBlock {
	lost := binary.BigEndian.Uint32(b[4:])
	cumulative := int32(lost<<8) >> 8 // sign-extend the 24 bits
	return ReportBlock{
		SSRC:             binary.BigEndian.Uint32(b[0:]),
		FractionLost:     uint8(lost >> 24),
		CumulativeLost:   cumulative,
		HighestSequence:  binary.BigEndian.Uint32(b[8:]),
		Jitter:           binary.BigEndian.Uint32(b[12:]),
		LastSR:           binary.BigEndian.Uint32(b[16:]),
		DelaySinceLastSR: binary.BigEndian.Uint32(b[20:]),
	}
}

// SenderReport is SR (RFC 3550 6.4.1): what a source that sent RTP
// since its last report says about its sending, and its reception
// reports.
type SenderReport struct {
	SSRC uint32
	// NTPTime is the wallclock time of the report as an NTP timestamp,
	// and RTPTime the same instant in the RTP timestamp of the stream.
	NTPTime     uint64
	RTPTime     uint32
	PacketCount uint32
	OctetCount  uint32
	Reports     []ReportBlock
}

func (SenderReport) Type() uint8  { return TypeSR }
func (r SenderReport) count() int { return len(r.Reports) }
func (r SenderReport) body() []byte {
	b := make([]byte, 24, 24+blockLen*len(r.Reports))
	binary.BigEndian.PutUint32(b[0:], r.SSRC)
	binary.BigEndian.PutUint64(b[4:], r.NTPTime)
	binary.BigEndian.PutUint32(b[12:], r.RTPTime)
	binary.BigEndian.PutUint32(b[16:], r.PacketCount)
	binary.BigEndian.PutUint32(b[20:], r.OctetCount)
	for _, rb := range r.Reports {
		b = append(b, rb.marshal()...)
	}
	return b
}

// ReceiverReport is RR (RFC 3550 6.4.2): the reception reports of a
// participant that has not sent RTP since its last report.
type ReceiverReport struct {
	SSRC    uint32
	Reports []ReportBlock
}

func (ReceiverReport) Type() uint8  { return TypeRR }
func (r ReceiverReport) count() int { return len(r.Reports) }
func (r ReceiverReport) body() []byte {
	b := make([]byte, 4, 4+blockLen*len(r.Reports))
	binary.BigEndian.PutUint32(b, r.SSRC)
	for _, rb := range r.Reports {
		b = append(b, rb.marshal()...)
	}
	return b
}

// SourceDescription is SDES (RFC 3550 6.5) with the CNAME of each
// source, the one item IR.92 3.2.4 sends.
type SourceDescription struct {
	Chunks []Chunk
}

// Chunk names one source. CNAME is the source's canonical name, such
// as user@host, which stays the same when the SSRC changes (RFC 3550
// 6.5.1).
type Chunk struct {
	SSRC  uint32
	CNAME string
}

// sdesCNAME is the item type of CNAME (RFC 3550 6.5.1).
const sdesCNAME = 1

func (SourceDescription) Type() uint8  { return TypeSDES }
func (s SourceDescription) count() int { return len(s.Chunks) }
func (s SourceDescription) body() []byte {
	var b []byte
	for _, c := range s.Chunks {
		b = binary.BigEndian.AppendUint32(b, c.SSRC)
		b = append(b, sdesCNAME, byte(len(c.CNAME)))
		b = append(b, c.CNAME...)
		// The item list ends with a null octet, and the chunk is padded
		// with more to a multiple of 4 octets (RFC 3550 6.5).
		b = append(b, 0)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	return b
}

// Goodbye is BYE (RFC 3550 6.6): the sources leave the session.
type Goodbye struct {
	SSRCs  []uint32
	Reason string
}

func (Goodbye) Type() uint8  { return TypeBYE }
func (g Goodbye) count() int { return len(g.SSRCs) }
func (g Goodbye) body() []byte {
	var b []byte
	for _, s := range g.SSRCs {
		b = binary.BigEndian.AppendUint32(b, s)
	}
	if g.Reason != "" {
		b = append(b, byte(len(g.Reason)))
		b = append(b, g.Reason...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	return b
}

// Unknown is a packet type this package does not read, such as APP or
// the feedback of RFC 4585. IR.92 3.2.4 asks to accept them.
type Unknown struct {
	PacketType uint8
	Count      int
	Body       []byte
}

func (u Unknown) Type() uint8  { return u.PacketType }
func (u Unknown) count() int   { return u.Count }
func (u Unknown) body() []byte { return u.Body }

// Marshal writes a compound packet (RFC 3550 6.1). The first packet
// must be a report; IR.92 3.2.4 adds a SourceDescription after it. No
// padding is written.
func Marshal(packets ...Packet) ([]byte, error) {
	if len(packets) == 0 {
		return nil, errors.New("rtcp: empty compound packet")
	}
	if t := packets[0].Type(); t != TypeSR && t != TypeRR {
		return nil, fmt.Errorf("rtcp: a compound packet starts with SR or RR, not type %d (RFC 3550 6.1)", t)
	}
	var out []byte
	for _, p := range packets {
		if p.count() > 31 {
			return nil, fmt.Errorf("rtcp: type %d with %d items; the count field holds 31", p.Type(), p.count())
		}
		body := p.body()
		out = append(out, version<<6|byte(p.count()), p.Type())
		// The length is in 32-bit words, minus one, header included.
		out = binary.BigEndian.AppendUint16(out, uint16(len(body)/4))
		out = append(out, body...)
	}
	return out, nil
}

// Unmarshal reads a compound packet and checks it as RFC 3550 A.2 does:
// version 2, a report first, padding only in the last packet, and
// lengths that add up to the datagram.
func Unmarshal(b []byte) ([]Packet, error) {
	var out []Packet
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, errors.New("rtcp: truncated header")
		}
		if b[0]>>6 != version {
			return nil, fmt.Errorf("rtcp: version %d, want 2", b[0]>>6)
		}
		padded, count, typ := b[0]&0x20 != 0, int(b[0]&0x1f), b[1]
		n := 4 * (int(binary.BigEndian.Uint16(b[2:])) + 1)
		if n > len(b) {
			return nil, fmt.Errorf("rtcp: type %d says %d octets, %d left", typ, n, len(b))
		}
		body := b[4:n]
		b = b[n:]
		if padded {
			if len(b) != 0 {
				return nil, errors.New("rtcp: padding before the last packet")
			}
			if len(body) == 0 || int(body[len(body)-1]) > len(body) {
				return nil, errors.New("rtcp: bad padding")
			}
			body = body[:len(body)-int(body[len(body)-1])]
		}
		if len(out) == 0 && typ != TypeSR && typ != TypeRR {
			return nil, fmt.Errorf("rtcp: compound packet starts with type %d, not SR or RR", typ)
		}
		p, err := parse(typ, count, body)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("rtcp: empty datagram")
	}
	return out, nil
}

func parse(typ uint8, count int, b []byte) (Packet, error) {
	switch typ {
	case TypeSR:
		if len(b) < 24+blockLen*count {
			return nil, errors.New("rtcp: SR shorter than its report count")
		}
		sr := SenderReport{
			SSRC:        binary.BigEndian.Uint32(b[0:]),
			NTPTime:     binary.BigEndian.Uint64(b[4:]),
			RTPTime:     binary.BigEndian.Uint32(b[12:]),
			PacketCount: binary.BigEndian.Uint32(b[16:]),
			OctetCount:  binary.BigEndian.Uint32(b[20:]),
		}
		sr.Reports = parseBlocks(b[24:], count)
		return sr, nil
	case TypeRR:
		if len(b) < 4+blockLen*count {
			return nil, errors.New("rtcp: RR shorter than its report count")
		}
		return ReceiverReport{SSRC: binary.BigEndian.Uint32(b), Reports: parseBlocks(b[4:], count)}, nil
	case TypeSDES:
		return parseSDES(b, count)
	case TypeBYE:
		if len(b) < 4*count {
			return nil, errors.New("rtcp: BYE shorter than its source count")
		}
		var g Goodbye
		for i := range count {
			g.SSRCs = append(g.SSRCs, binary.BigEndian.Uint32(b[4*i:]))
		}
		if rest := b[4*count:]; len(rest) > 0 && 1+int(rest[0]) <= len(rest) {
			g.Reason = string(rest[1 : 1+rest[0]])
		}
		return g, nil
	}
	return Unknown{PacketType: typ, Count: count, Body: b}, nil
}

func parseBlocks(b []byte, count int) []ReportBlock {
	var out []ReportBlock
	for i := range count {
		out = append(out, parseBlock(b[blockLen*i:]))
	}
	return out
}

// parseSDES keeps the CNAME of each chunk and skips the other items.
func parseSDES(b []byte, count int) (SourceDescription, error) {
	var s SourceDescription
	for range count {
		if len(b) < 4 {
			return SourceDescription{}, errors.New("rtcp: SDES shorter than its chunk count")
		}
		c := Chunk{SSRC: binary.BigEndian.Uint32(b)}
		i := 4
		for i < len(b) && b[i] != 0 {
			if i+2 > len(b) || i+2+int(b[i+1]) > len(b) {
				return SourceDescription{}, errors.New("rtcp: SDES item runs past the packet")
			}
			if b[i] == sdesCNAME {
				c.CNAME = string(b[i+2 : i+2+int(b[i+1])])
			}
			i += 2 + int(b[i+1])
		}
		i = (i + 4) / 4 * 4 // the null octet, then padding to 32 bits
		s.Chunks = append(s.Chunks, c)
		b = b[min(i, len(b)):]
	}
	return s, nil
}

// ntpEpoch is 1900-01-01, the origin of NTP timestamps (RFC 3550 4).
var ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// NTPTime is t as a 64-bit NTP timestamp: seconds since 1900 and the
// fraction of a second in units of 2^-32 (RFC 3550 4).
func NTPTime(t time.Time) uint64 {
	d := t.Sub(ntpEpoch)
	secs := uint64(d / time.Second)
	frac := uint64(d%time.Second) << 32 / uint64(time.Second)
	return secs<<32 | frac
}

// Middle32 is the middle 32 bits of an NTP timestamp, the form LSR
// uses (RFC 3550 6.4.1).
func Middle32(ntp uint64) uint32 { return uint32(ntp >> 16) }
