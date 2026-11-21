package evs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// magic is the magic number of the EVS storage format, "\n" included
// (TS 26.445 A.2.6.1).
const magic = "#!EVS_MC1.0\n"

// WriteStorage writes a mono EVS file: the magic number, the channel
// count 1 as a 32-bit number, then each frame as a ToC byte and its
// octet-aligned bits (A.2.6, Figures A.7 and A.8). A frame never
// received goes in as NO_DATA during silence and as SPEECH_LOST
// otherwise, so that the file keeps the timing of the call (A.2.6.2).
//
// SIMPLIFIED: TS 26.445 A.2.6.1. Only one channel; see the package
// comment.
func WriteStorage(w io.Writer, frames []Frame) error {
	var b bytes.Buffer
	b.WriteString(magic)
	b.Write(binary.BigEndian.AppendUint32(nil, 1))
	for i, f := range frames {
		bits, err := f.check()
		if err != nil {
			return fmt.Errorf("frame %d: %w", i+1, err)
		}
		var fw bitWriter
		fw.writeBits(f.Data, 0, bits)
		b.WriteByte(tocByte(f, false))
		b.Write(fw.bytes)
	}
	_, err := w.Write(b.Bytes())
	return err
}

// ReadStorage reads a mono EVS file.
func ReadStorage(r io.Reader) ([]Frame, error) {
	br := bufio.NewReader(r)
	head := make([]byte, len(magic)+4)
	if _, err := io.ReadFull(br, head); err != nil || string(head[:len(magic)]) != magic {
		return nil, errors.New("evs: not an EVS file (TS 26.445 A.2.6.1)")
	}
	if n := binary.BigEndian.Uint32(head[len(magic):]); n != 1 {
		return nil, fmt.Errorf("evs: the file has %d channels; only mono files are supported", n)
	}
	var frames []Frame
	for {
		toc, err := br.ReadByte()
		if err == io.EOF {
			return frames, nil
		}
		if err != nil {
			return nil, err
		}
		if toc&0x80 != 0 {
			return nil, fmt.Errorf("evs: frame %d does not start with a ToC byte (H = 1)", len(frames)+1)
		}
		f, err := parseToC(toc)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", len(frames)+1, err)
		}
		if bits, _ := f.Mode.FrameBits(f.Type); bits > 0 {
			octets := make([]byte, (bits+7)/8)
			if _, err := io.ReadFull(br, octets); err != nil {
				return nil, fmt.Errorf("evs: frame %d is cut short: %w", len(frames)+1, err)
			}
			// Copy only the frame's bits, so that padding bits come back as zeros.
			f.Data = make([]byte, len(octets))
			r := bitReader{bytes: octets}
			r.readBitsInto(f.Data, 0, bits)
		}
		frames = append(frames, f)
	}
}
