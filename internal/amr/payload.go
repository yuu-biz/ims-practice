package amr

import (
	"errors"
	"fmt"
)

// Payload is the RTP payload of RFC 4867 4.2: a codec mode request and
// the frames of consecutive 20 ms frame-blocks, oldest first.
type Payload struct {
	// CMR is the mode the sender asks the receiver to encode with, or
	// NoModeRequest (RFC 4867 4.3.1).
	CMR    FrameType
	Frames []Frame
}

// Format is one payload type of AMR or AMR-WB: the codec and the
// octet-align parameter of its a=fmtp (RFC 4867 8.1).
type Format struct {
	Codec        Codec
	OctetAligned bool
}

// Marshal packs p. IR.92 3.2.5 asks for one frame per packet (ptime
// 20), but a packet may carry more.
func (f Format) Marshal(p Payload) ([]byte, error) {
	if len(p.Frames) == 0 {
		return nil, errors.New("amr: a payload needs at least one frame")
	}
	if !f.validCMR(p.CMR) {
		return nil, fmt.Errorf("amr: CMR %d is not a %s mode", p.CMR, f.Codec)
	}
	bits := make([]int, len(p.Frames))
	for i, fr := range p.Frames {
		var err error
		if bits[i], err = f.Codec.checkFrame(fr); err != nil {
			return nil, err
		}
	}
	var w bitWriter
	if f.OctetAligned {
		f.marshalOctetAligned(&w, p)
	} else {
		f.marshalBandwidthEfficient(&w, p)
	}
	for i, fr := range p.Frames {
		w.writeBits(fr.Data, bits[i])
		if f.OctetAligned {
			w.align() // RFC 4867 4.4.3: each frame is octet-aligned
		}
	}
	w.align()
	return w.bytes, nil
}

// marshalBandwidthEfficient writes the 4-bit CMR and the 6-bit ToC
// entries F, FT, Q one after another (RFC 4867 4.3.1, 4.3.2, 4.3.4).
func (f Format) marshalBandwidthEfficient(w *bitWriter, p Payload) {
	w.writeUint(uint(p.CMR), 4)
	for i, fr := range p.Frames {
		w.writeUint(follows(i, p.Frames), 1)
		w.writeUint(uint(fr.Type), 4)
		w.writeUint(bit(fr.Quality), 1)
	}
}

// marshalOctetAligned writes the CMR with 4 reserved bits, and one
// octet F, FT, Q, P, P per frame (RFC 4867 4.4.1, 4.4.2).
func (f Format) marshalOctetAligned(w *bitWriter, p Payload) {
	w.writeUint(uint(p.CMR), 4)
	w.writeUint(0, 4)
	for i, fr := range p.Frames {
		w.writeUint(follows(i, p.Frames), 1)
		w.writeUint(uint(fr.Type), 4)
		w.writeUint(bit(fr.Quality), 1)
		w.writeUint(0, 2)
	}
}

// follows is the F bit: 1 when another frame comes after frame i.
func follows(i int, frames []Frame) uint { return bit(i < len(frames)-1) }

func bit(b bool) uint {
	if b {
		return 1
	}
	return 0
}

// Unmarshal unpacks a payload. A packet with a frame type the format
// does not allow is refused as a whole, so that the frames that follow
// are not read out of step (RFC 4867 4.3.2). A CMR that is neither a
// speech mode nor NO_DATA is ignored (RFC 4867 4.3.1) and comes back
// as NoModeRequest. Padding bits are ignored.
func (f Format) Unmarshal(b []byte) (Payload, error) {
	r := bitReader{bytes: b}
	var p Payload
	cmr, ok := r.readUint(4)
	if !ok {
		return Payload{}, errors.New("amr: empty payload")
	}
	p.CMR = FrameType(cmr)
	if !f.validCMR(p.CMR) {
		p.CMR = NoModeRequest
	}
	if f.OctetAligned {
		r.readUint(4) // reserved
	}
	for more := true; more; {
		fBit, _ := r.readUint(1)
		ft, _ := r.readUint(4)
		q, ok := r.readUint(1)
		if f.OctetAligned {
			_, ok = r.readUint(2)
		}
		if !ok {
			return Payload{}, errors.New("amr: payload ends inside the table of contents")
		}
		if _, err := f.Codec.FrameBits(FrameType(ft)); err != nil {
			return Payload{}, err
		}
		p.Frames = append(p.Frames, Frame{Type: FrameType(ft), Quality: q == 1})
		more = fBit == 1
	}
	for i := range p.Frames {
		bits, _ := f.Codec.FrameBits(p.Frames[i].Type)
		data, ok := r.readBits(bits)
		if !ok {
			return Payload{}, fmt.Errorf("amr: payload ends inside frame %d", i+1)
		}
		p.Frames[i].Data = data
		if f.OctetAligned {
			r.align()
		}
	}
	return p, nil
}

func (f Format) validCMR(cmr FrameType) bool {
	return cmr == NoModeRequest || f.Codec.IsSpeech(cmr)
}

// bitWriter packs fields from the most significant bit of each octet
// on, as RFC 4867 4.3.4 describes.
type bitWriter struct {
	bytes []byte
	n     int // bits written
}

func (w *bitWriter) writeUint(v uint, bits int) {
	for i := bits - 1; i >= 0; i-- {
		w.writeBit(byte(v>>i) & 1)
	}
}

func (w *bitWriter) writeBits(data []byte, bits int) {
	for i := range bits {
		w.writeBit(data[i/8] >> (7 - i%8) & 1)
	}
}

func (w *bitWriter) writeBit(b byte) {
	if w.n%8 == 0 {
		w.bytes = append(w.bytes, 0)
	}
	w.bytes[len(w.bytes)-1] |= b << (7 - w.n%8)
	w.n++
}

// align pads with zero bits up to the next octet.
func (w *bitWriter) align() { w.n = 8 * len(w.bytes) }

type bitReader struct {
	bytes []byte
	n     int // bits read
}

func (r *bitReader) readBit() (byte, bool) {
	if r.n >= 8*len(r.bytes) {
		return 0, false
	}
	b := r.bytes[r.n/8] >> (7 - r.n%8) & 1
	r.n++
	return b, true
}

func (r *bitReader) readUint(bits int) (uint, bool) {
	var v uint
	for range bits {
		b, ok := r.readBit()
		if !ok {
			return 0, false
		}
		v = v<<1 | uint(b)
	}
	return v, true
}

// readBits reads bits into octets, padding the last one with zeros.
func (r *bitReader) readBits(bits int) ([]byte, bool) {
	out := make([]byte, (bits+7)/8)
	for i := range bits {
		b, ok := r.readBit()
		if !ok {
			return nil, false
		}
		out[i/8] |= b << (7 - i%8)
	}
	return out, true
}

func (r *bitReader) align() { r.n = (r.n + 7) / 8 * 8 }
