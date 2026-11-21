package amr

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// The magic numbers of single-channel files (RFC 4867 5.1). The "\n"
// is part of each, which keeps them apart from the multi-channel ones.
const (
	magicAMR   = "#!AMR\n"
	magicAMRWB = "#!AMR-WB\n"
)

// WriteStorage writes a single-channel AMR or AMR-WB file: the magic
// number, then each frame as one header octet and its octet-aligned
// bits (RFC 4867 5.1, 5.3). A frame that was never received is stored
// as NO_DATA, so that the file keeps the timing of the call.
func WriteStorage(w io.Writer, c Codec, frames []Frame) error {
	var b bytes.Buffer
	b.WriteString(magic(c))
	for i, f := range frames {
		if _, err := c.checkFrame(f); err != nil {
			return fmt.Errorf("frame %d: %w", i+1, err)
		}
		b.WriteByte(storageHeader(f))
		b.Write(f.Data)
	}
	_, err := w.Write(b.Bytes())
	return err
}

func magic(c Codec) string {
	if c == AMRWB {
		return magicAMRWB
	}
	return magicAMR
}

// storageHeader is the octet P, FT, Q, P, P (RFC 4867 5.3).
func storageHeader(f Frame) byte {
	return byte(f.Type)<<3 | byte(bit(f.Quality))<<2
}

// ReadStorage reads a single-channel AMR or AMR-WB file and tells which
// codec it holds.
func ReadStorage(r io.Reader) (Codec, []Frame, error) {
	br := bufio.NewReader(r)
	c, err := readMagic(br)
	if err != nil {
		return 0, nil, err
	}
	var frames []Frame
	for {
		h, err := br.ReadByte()
		if err == io.EOF {
			return c, frames, nil
		}
		if err != nil {
			return 0, nil, err
		}
		f := Frame{Type: FrameType(h >> 3 & 0x0f), Quality: h>>2&1 == 1}
		bits, err := c.FrameBits(f.Type)
		if err != nil {
			return 0, nil, fmt.Errorf("frame %d: %w", len(frames)+1, err)
		}
		f.Data = make([]byte, (bits+7)/8)
		if _, err := io.ReadFull(br, f.Data); err != nil {
			return 0, nil, fmt.Errorf("amr: frame %d is cut short: %w", len(frames)+1, err)
		}
		frames = append(frames, f)
	}
}

func readMagic(br *bufio.Reader) (Codec, error) {
	head, _ := br.Peek(len(magicAMRWB))
	switch {
	case bytes.HasPrefix(head, []byte(magicAMRWB)):
		br.Discard(len(magicAMRWB))
		return AMRWB, nil
	case bytes.HasPrefix(head, []byte(magicAMR)):
		br.Discard(len(magicAMR))
		return AMR, nil
	case bytes.HasPrefix(head, []byte("#!AMR")):
		return 0, errors.New("amr: multi-channel files are not supported")
	}
	return 0, errors.New("amr: not an AMR or AMR-WB file (RFC 4867 5.1)")
}
