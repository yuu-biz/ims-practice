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
	if err := appendFrames(&b, c, frames); err != nil {
		return err
	}
	_, err := w.Write(b.Bytes())
	return err
}

// appendFrames appends each frame as one header octet and its
// octet-aligned bits (RFC 4867 5.3).
func appendFrames(b *bytes.Buffer, c Codec, frames []Frame) error {
	for i, f := range frames {
		if _, err := c.checkFrame(f); err != nil {
			return fmt.Errorf("frame %d: %w", i+1, err)
		}
		b.WriteByte(storageHeader(f))
		b.Write(f.Data)
	}
	return nil
}

// A StreamWriter writes the same storage format while the call is still
// running: the magic number before the first frame, then the frames in
// the order they are handed over. Each Write is one write on w, so a
// listener reading the other end of a pipe hears the frames as they
// arrive instead of waiting for the end of the call.
//
// The stream holds what a listener can still hear. A frame that arrives
// after its place has gone out is left out, where the file of the whole
// call, written at the end, holds it in its place.
type StreamWriter struct {
	w       io.Writer
	codec   Codec
	started bool
}

func NewStreamWriter(w io.Writer, c Codec) *StreamWriter {
	return &StreamWriter{w: w, codec: c}
}

// Write puts frames at the end of the stream, oldest first. An empty
// slice writes nothing, so that the magic number goes out with the
// first frame and the stream of a call that received nothing is empty.
func (s *StreamWriter) Write(frames []Frame) error {
	if len(frames) == 0 {
		return nil
	}
	var b bytes.Buffer
	if !s.started {
		b.WriteString(magic(s.codec))
	}
	if err := appendFrames(&b, s.codec, frames); err != nil {
		return err
	}
	if _, err := s.w.Write(b.Bytes()); err != nil {
		return err
	}
	s.started = true
	return nil
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
