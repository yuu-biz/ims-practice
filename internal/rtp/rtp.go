// Package rtp reads and writes RTP packets (RFC 3550).
package rtp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// version is the RTP version of RFC 3550 5.1.
const version = 2

// headerLen is the fixed header without CSRCs or extensions.
const headerLen = 12

// Packet is one RTP packet (RFC 3550 5.1).
type Packet struct {
	Marker         bool
	PayloadType    uint8
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
	Payload        []byte
}

// Marshal writes p with no padding, extension or CSRCs.
func (p Packet) Marshal() []byte {
	b := make([]byte, headerLen+len(p.Payload))
	b[0] = version << 6
	b[1] = p.PayloadType & 0x7f
	if p.Marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:], p.SequenceNumber)
	binary.BigEndian.PutUint32(b[4:], p.Timestamp)
	binary.BigEndian.PutUint32(b[8:], p.SSRC)
	copy(b[headerLen:], p.Payload)
	return b
}

// Unmarshal reads a packet. CSRCs, a header extension and padding are
// skipped, so Payload is only the media.
func Unmarshal(b []byte) (Packet, error) {
	if len(b) < headerLen {
		return Packet{}, errors.New("rtp: shorter than the fixed header")
	}
	if b[0]>>6 != version {
		return Packet{}, fmt.Errorf("rtp: version %d, want 2", b[0]>>6)
	}
	p := Packet{
		Marker:         b[1]&0x80 != 0,
		PayloadType:    b[1] & 0x7f,
		SequenceNumber: binary.BigEndian.Uint16(b[2:]),
		Timestamp:      binary.BigEndian.Uint32(b[4:]),
		SSRC:           binary.BigEndian.Uint32(b[8:]),
	}
	n := headerLen + 4*int(b[0]&0x0f) // the CSRC list
	if b[0]&0x10 != 0 {
		// RFC 3550 5.3.1: the extension's own length is in 32-bit words.
		if len(b) < n+4 {
			return Packet{}, errors.New("rtp: truncated header extension")
		}
		n += 4 + 4*int(binary.BigEndian.Uint16(b[n+2:]))
	}
	end := len(b)
	if b[0]&0x20 != 0 && end > n {
		// RFC 3550 5.1: the last octet counts the padding, itself included.
		end -= int(b[end-1])
	}
	if n > end {
		return Packet{}, errors.New("rtp: header longer than the packet")
	}
	p.Payload = b[n:end]
	return p, nil
}
