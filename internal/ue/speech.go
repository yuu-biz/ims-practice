package ue

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/yuu-biz/ims-practice/internal/amr"
)

// The UE never encodes AMR or AMR-WB: it sends
// frames that an encoder made beforehand. These are the one-second
// tones of internal/amr/testdata, which tools/amrgen encoded, copied
// here so that the UE can play them without any file of its own.
//
//go:embed speech/tone-amr-12k2.amr speech/tone-amrwb-23k85.amr
var tones embed.FS

// Speech is what the UE plays in a call. Which recording goes out
// depends on the codec of the call: PCM is encoded as G.711 for PCMU,
// and the AMR and AMR-WB frames are sent as they are.
type Speech struct {
	// PCM is 8 kHz, 16-bit audio.
	PCM []int16
	// AMR and AMRWB are speech frames of 20 ms each, oldest first.
	AMR   []amr.Frame
	AMRWB []amr.Frame
}

// ToneSpeech is a tone of d in every codec: a 440 Hz sine for PCMU,
// and the embedded AMR and AMR-WB tones repeated for as long.
func ToneSpeech(d time.Duration) Speech {
	return Speech{PCM: Tone(440, d), AMR: repeatTone("speech/tone-amr-12k2.amr", d), AMRWB: repeatTone("speech/tone-amrwb-23k85.amr", d)}
}

func repeatTone(name string, d time.Duration) []amr.Frame {
	b, err := tones.ReadFile(name)
	if err != nil {
		panic(err)
	}
	_, tone, err := amr.ReadStorage(bytes.NewReader(b))
	if err != nil {
		panic(fmt.Sprintf("%s: %v", name, err))
	}
	n := int(d / (20 * time.Millisecond))
	frames := make([]amr.Frame, 0, n)
	for len(frames) < n {
		frames = append(frames, tone[:min(len(tone), n-len(frames))]...)
	}
	return frames
}

// frames is the recording that goes out in a call of codec c.
func (s Speech) frames(c amr.Codec) []amr.Frame {
	if c == amr.AMRWB {
		return s.AMRWB
	}
	return s.AMR
}

// modes are the speech modes that the recording of codec c was encoded
// in, lowest first, or nil when it has no speech frame. Comfort noise
// and NO_DATA are no mode, and any mode-set allows them (RFC 4867 8.1,
// 8.2).
func (s Speech) modes(c amr.Codec) []amr.FrameType {
	var modes []amr.FrameType
	for _, f := range s.frames(c) {
		if c.IsSpeech(f.Type) && !slices.Contains(modes, f.Type) {
			modes = append(modes, f.Type)
		}
	}
	slices.Sort(modes)
	return modes
}

// ReadAMR reads an AMR or AMR-WB file in the storage format of RFC
// 4867 5 into the recording of its codec in s.
func (s *Speech) ReadAMR(r io.Reader) (amr.Codec, error) {
	c, frames, err := amr.ReadStorage(r)
	if err != nil {
		return 0, err
	}
	if c == amr.AMRWB {
		s.AMRWB = frames
	} else {
		s.AMR = frames
	}
	return c, nil
}
