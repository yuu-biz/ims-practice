package evs

import (
	"errors"
	"fmt"
)

// Payload is the content of one EVS RTP payload: an optional codec
// mode request and the frames of consecutive 20 ms frame-blocks,
// oldest first (TS 26.445 A.2).
type Payload struct {
	// CMR is the codec mode request, or zero for none. A payload with
	// an EVS AMR-WB IO frame always carries a CMR (A.2.1.2, A.2.2.1.1),
	// so for one of those Marshal sends NO_REQ (or the 3-bit "none")
	// in place of zero, and Unmarshal returns NoRequest.
	CMR    CMR
	Frames []Frame
}

// check validates p and returns the length in bits of each frame.
func (p Payload) check() ([]int, error) {
	if len(p.Frames) == 0 {
		return nil, errors.New("evs: a payload needs at least one frame")
	}
	if p.CMR != 0 && !p.CMR.Valid() {
		return nil, fmt.Errorf("evs: %s is not a code point of TS 26.445 Table A.3", p.CMR)
	}
	bits := make([]int, len(p.Frames))
	for i, f := range p.Frames {
		var err error
		if bits[i], err = f.check(); err != nil {
			return nil, fmt.Errorf("frame %d: %w", i+1, err)
		}
	}
	return bits, nil
}

// hasAMRWBIO tells that a frame of p is EVS AMR-WB IO, which makes the
// CMR byte mandatory in the Header-Full format (A.2.2.1.1).
func (p Payload) hasAMRWBIO() bool {
	for _, f := range p.Frames {
		if f.Mode == AMRWBIO {
			return true
		}
	}
	return false
}

// marshalHeaderFull writes the CMR byte if there is one, a ToC byte per
// frame, then each frame's bits padded to the octet (A.2.2.1, Figure
// A.3). EVS Primary frames are whole octets already; EVS AMR-WB IO
// frames are padded as in the octet-aligned mode of RFC 4867 4.4.3
// (A.2.2.1.3). The padding against Compact sizes is not added here.
func marshalHeaderFull(p Payload, bits []int) []byte {
	var w bitWriter
	cmr := p.CMR
	if cmr == 0 && p.hasAMRWBIO() {
		cmr = NoRequest
	}
	if cmr != 0 {
		w.writeUint(uint(cmr), 8)
	}
	for i, f := range p.Frames {
		w.writeUint(uint(tocByte(f, i < len(p.Frames)-1)), 8)
	}
	for i, f := range p.Frames {
		w.writeBits(f.Data, 0, bits[i])
		w.align()
	}
	return w.bytes
}

// tocByte is H (0), F, the EVS mode bit, the Q bit (unused and 0 for
// EVS Primary) and the 4-bit frame type (A.2.2.1.2, Figure A.5).
func tocByte(f Frame, follows bool) byte {
	var b byte
	if follows {
		b |= 1 << 6
	}
	b |= byte(f.Mode) << 5
	if f.Mode == AMRWBIO && f.Quality {
		b |= 1 << 4
	}
	return b | byte(f.Type)
}

// parseToC reads the frame a ToC byte announces, without its data.
// The F bit is left to the caller.
func parseToC(toc byte) (Frame, error) {
	f := Frame{Mode: Mode(toc >> 5 & 1), Type: FrameType(toc & 0xf), Quality: true}
	if f.Mode == AMRWBIO {
		f.Quality = toc>>4&1 == 1
	}
	_, err := f.Mode.FrameBits(f.Type)
	return f, err
}

// unmarshalHeaderFull reads a Header-Full payload. A CMR byte with a
// code point that Table A.3 does not define is ignored and comes back
// as NoRequest, as a CMR outside the negotiated parameters is
// (A.2.2.1.1). A frame type kept for future use refuses the packet as
// a whole, since the frames after it could not be found. The bytes
// after the last frame are padding and are discarded (A.2.2.1).
func unmarshalHeaderFull(b []byte) (Payload, error) {
	if len(b) == 0 {
		return Payload{}, errors.New("evs: empty payload")
	}
	var p Payload
	i := 0
	if b[0]&0x80 != 0 {
		p.CMR = CMR(b[0])
		if !p.CMR.Valid() {
			p.CMR = NoRequest
		}
		i++
	}
	for more := true; more; i++ {
		if i >= len(b) {
			return Payload{}, errors.New("evs: payload ends before the last ToC byte")
		}
		if b[i]&0x80 != 0 {
			return Payload{}, fmt.Errorf("evs: byte %d has H = 1 where a ToC byte belongs; only one CMR byte may come, and only first (TS 26.445 A.2.2.1)", i+1)
		}
		f, err := parseToC(b[i])
		if err != nil {
			return Payload{}, fmt.Errorf("frame %d: %w", len(p.Frames)+1, err)
		}
		p.Frames = append(p.Frames, f)
		more = b[i]>>6&1 == 1
	}
	r := bitReader{bytes: b, n: 8 * i}
	for k := range p.Frames {
		bits, _ := p.Frames[k].Mode.FrameBits(p.Frames[k].Type)
		if bits == 0 {
			continue
		}
		if r.left() < bits {
			return Payload{}, fmt.Errorf("evs: payload ends inside frame %d", k+1)
		}
		p.Frames[k].Data = make([]byte, (bits+7)/8)
		r.readBitsInto(p.Frames[k].Data, 0, bits)
		r.align()
	}
	return p, nil
}
