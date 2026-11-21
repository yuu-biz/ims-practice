package amr

import (
	"bytes"
	"os"
	"testing"
)

// TestFixtures reads the tones that tools/amrgen encoded with real AMR
// and AMR-WB encoders, sends every frame through an RTP payload of
// both formats, and writes the file again bit for bit.
func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		file string
		c    Codec
		mode FrameType
	}{
		{"testdata/tone-amr-12k2.amr", AMR, 7},
		{"testdata/tone-amrwb-23k85.amr", AMRWB, 8},
	} {
		file, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		c, frames, err := ReadStorage(bytes.NewReader(file))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if c != tc.c || len(frames) != 50 {
			t.Fatalf("%s: %s with %d frames, want %s with 50 (one second)", tc.file, c, len(frames), tc.c)
		}
		received := make([]Frame, 0, len(frames))
		for i, f := range frames {
			if f.Type != tc.mode || !f.Quality {
				t.Fatalf("%s: frame %d is type %d, quality %v", tc.file, i+1, f.Type, f.Quality)
			}
			format := Format{Codec: c, OctetAligned: i%2 == 1}
			b, err := format.Marshal(Payload{CMR: NoModeRequest, Frames: []Frame{f}})
			if err != nil {
				t.Fatal(err)
			}
			p, err := format.Unmarshal(b)
			if err != nil {
				t.Fatal(err)
			}
			received = append(received, p.Frames...)
		}
		var out bytes.Buffer
		if err := WriteStorage(&out, c, received); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), file) {
			t.Errorf("%s: the file written from the received frames differs from the one sent", tc.file)
		}
	}
}
