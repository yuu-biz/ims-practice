package mixer

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
)

// wavFile builds a RIFF WAVE file the way internal/wav writes one, with
// the given format fields, and an extra chunk before the data that a
// reader has to skip.
func wavFile(format, channels uint16, rate uint32, bits uint16, samples []int16) []byte {
	var b bytes.Buffer
	data := uint32(2 * len(samples))
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, 36+12+data)
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, format)
	binary.Write(&b, binary.LittleEndian, channels)
	binary.Write(&b, binary.LittleEndian, rate)
	binary.Write(&b, binary.LittleEndian, rate*uint32(channels)*uint32(bits)/8)
	binary.Write(&b, binary.LittleEndian, channels*bits/8)
	binary.Write(&b, binary.LittleEndian, bits)
	b.WriteString("LIST")
	binary.Write(&b, binary.LittleEndian, uint32(3)) // odd: padded to 4
	b.WriteString("abc\x00")
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, data)
	binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

// ramp is n samples counting up from 1, so that each frame differs.
func ramp(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(10 * (i + 1))
	}
	return out
}

// playAll calls Next until the playback ends, at most limit times.
func playAll(t *testing.T, p *Playback, limit int) [][]byte {
	t.Helper()
	var out [][]byte
	for range limit {
		payload, ok := p.Next()
		if !ok {
			if _, again := p.Next(); again {
				t.Fatal("Next gave a payload after reporting the end")
			}
			return out
		}
		out = append(out, payload)
	}
	return out
}

func play(t *testing.T, c *Clip, opts PlayOptions) *Playback {
	t.Helper()
	p, err := c.Play(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadWAVFramesAndPadsTheLastFrame(t *testing.T) {
	// 2.5 frames: the third payload is half audio, half silence.
	samples := ramp(2*FrameSize + FrameSize/2)
	c, err := ReadWAV(bytes.NewReader(wavFile(1, 1, 8000, 16, samples)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Frames() != 3 {
		t.Fatalf("Frames() = %d, want 3", c.Frames())
	}
	got := playAll(t, play(t, c, PlayOptions{}), 10)
	if len(got) != 3 {
		t.Fatalf("got %d payloads, want 3", len(got))
	}
	var all []byte
	for i, payload := range got {
		if len(payload) != FrameSize {
			t.Fatalf("payload %d has %d bytes, want %d", i, len(payload), FrameSize)
		}
		all = append(all, payload...)
	}
	for i, s := range samples {
		if all[i] != g711.EncodeMuLaw(s) {
			t.Fatalf("byte %d is %#02x, want the encoding of %d", i, all[i], s)
		}
	}
	if pad := all[len(samples):]; !bytes.Equal(pad, bytes.Repeat([]byte{muLawSilence}, FrameSize/2)) {
		t.Errorf("padding is %x, want mu-law silence", pad)
	}
}

func TestReadWAVExactFrames(t *testing.T) {
	c, err := ReadWAV(bytes.NewReader(wavFile(1, 1, 8000, 16, ramp(2*FrameSize))))
	if err != nil {
		t.Fatal(err)
	}
	if c.Frames() != 2 {
		t.Errorf("Frames() = %d, want 2", c.Frames())
	}
}

func TestReadWAVRejectsOtherFormats(t *testing.T) {
	tests := map[string][]byte{
		"16 kHz":   wavFile(1, 1, 16000, 16, ramp(10)),
		"stereo":   wavFile(1, 2, 8000, 16, ramp(10)),
		"8 bits":   wavFile(1, 1, 8000, 8, ramp(10)),
		"mu-law":   wavFile(7, 1, 8000, 8, ramp(10)), // WAVE_FORMAT_MULAW, which internal/wav does not read either
		"empty":    wavFile(1, 1, 8000, 16, nil),
		"not RIFF": []byte("RIFX\x00\x00\x00\x00WAVE"),
	}
	for name, file := range tests {
		if _, err := ReadWAV(bytes.NewReader(file)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRepeatAndDelay(t *testing.T) {
	c, err := NewClip(ramp(2 * FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	// Two plays with 30 ms between them: the delay rounds up to two
	// silent packets.
	got := playAll(t, play(t, c, PlayOptions{Repeat: 2, Delay: 30 * time.Millisecond}), 100)
	silence := frameOfSilence()
	want := [][]byte{c.frames[0], c.frames[1], silence, silence, c.frames[0], c.frames[1]}
	if len(got) != len(want) {
		t.Fatalf("got %d payloads, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("payload %d differs", i)
		}
	}
}

func TestRepeatForeverStopsAtDuration(t *testing.T) {
	c, err := NewClip(ramp(3 * FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	// 210 ms rounds up to 11 packets: three plays of three frames and
	// two frames of the fourth.
	got := playAll(t, play(t, c, PlayOptions{Repeat: RepeatForever, Duration: 210 * time.Millisecond}), 100)
	if len(got) != 11 {
		t.Fatalf("got %d payloads, want 11", len(got))
	}
	for i, payload := range got {
		if !bytes.Equal(payload, c.frames[i%3]) {
			t.Errorf("payload %d is not frame %d of the clip", i, i%3)
		}
	}
}

func TestRepeatForeverWithoutDurationKeepsGoing(t *testing.T) {
	c, err := NewClip(ramp(FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	if got := playAll(t, play(t, c, PlayOptions{Repeat: RepeatForever}), 1000); len(got) != 1000 {
		t.Errorf("ended after %d payloads", len(got))
	}
}

func TestDurationCutsASinglePlay(t *testing.T) {
	c, err := NewClip(ramp(5 * FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	if got := playAll(t, play(t, c, PlayOptions{Duration: 40 * time.Millisecond}), 100); len(got) != 2 {
		t.Errorf("got %d payloads, want 2", len(got))
	}
}

func TestPayloadsCanBeChangedByTheCaller(t *testing.T) {
	c, err := NewClip(ramp(FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := play(t, c, PlayOptions{}).Next()
	first[0] ^= 0xFF
	second, _ := play(t, c, PlayOptions{}).Next()
	if second[0] == first[0] {
		t.Error("changing a payload changed the clip")
	}
}

func TestPlayRejectsBadOptions(t *testing.T) {
	c, err := NewClip(ramp(FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range []PlayOptions{{Repeat: -2}, {Delay: -time.Millisecond}, {Duration: -time.Millisecond}} {
		if _, err := c.Play(opts); err == nil {
			t.Errorf("%+v: no error", opts)
		}
	}
}

// TestOneClipManyPlaybacks plays one clip to several callers at once,
// as the MRFP does with a common announcement; run with -race.
func TestOneClipManyPlaybacks(t *testing.T) {
	c, err := NewClip(ramp(10 * FrameSize))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := c.Play(PlayOptions{Repeat: 3})
			if err != nil {
				t.Error(err)
				return
			}
			n := 0
			for {
				payload, ok := p.Next()
				if !ok {
					break
				}
				payload[0] = 0 // the caller owns what it gets
				n++
			}
			if n != 30 {
				t.Errorf("got %d payloads, want 30", n)
			}
		}()
	}
	wg.Wait()
}
