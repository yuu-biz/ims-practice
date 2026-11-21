package mixer

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/g711"
)

// frameOf is a payload where every sample has the same mu-law code.
func frameOf(code byte) []byte { return bytes.Repeat([]byte{code}, FrameSize) }

// tone is one frame of a sine wave of hz at amplitude, starting at
// sample offset, encoded as PCMU.
func tone(hz, amplitude float64, offset int) []byte {
	out := make([]byte, FrameSize)
	for i := range out {
		s := amplitude * math.Sin(2*math.Pi*hz*float64(offset+i)/SampleRate)
		out[i] = g711.EncodeMuLaw(int16(s))
	}
	return out
}

func conferenceOf(t *testing.T, ids ...string) *Conference {
	t.Helper()
	c := NewConference()
	for _, id := range ids {
		if err := c.Join(id); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func mix(t *testing.T, c *Conference, in map[string][]byte) map[string][]byte {
	t.Helper()
	out, err := c.Mix(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestSilenceIsTheCodeOfZero ties the silence that pads clips and that
// a lone participant hears to the encoder.
func TestSilenceIsTheCodeOfZero(t *testing.T) {
	if got := g711.EncodeMuLaw(0); got != muLawSilence {
		t.Errorf("g711.EncodeMuLaw(0) = %#02x, muLawSilence is %#02x", got, muLawSilence)
	}
	if got := g711.DecodeMuLaw(muLawSilence); got != 0 {
		t.Errorf("g711.DecodeMuLaw(muLawSilence) = %d, want 0", got)
	}
}

func TestTwoParticipantsHearEachOtherUnchanged(t *testing.T) {
	c := conferenceOf(t, "a", "b")
	a, b := tone(440, 12000, 0), tone(1000, 8000, 0)
	out := mix(t, c, map[string][]byte{"a": a, "b": b})
	// With one other speaker the mix is that speaker's payload itself,
	// up to the "negative zero" 0x7F, which comes back as 0xFF.
	if want := canonical(b); !bytes.Equal(out["a"], want) {
		t.Errorf("a hears %x, want b's frame %x", out["a"], want)
	}
	if want := canonical(a); !bytes.Equal(out["b"], want) {
		t.Errorf("b hears %x, want a's frame %x", out["b"], want)
	}
}

// canonical replaces 0x7F, the mu-law code for minus zero, with 0xFF.
func canonical(frame []byte) []byte {
	out := bytes.Clone(frame)
	for i, b := range out {
		if b == 0x7F {
			out[i] = muLawSilence
		}
	}
	return out
}

func TestThreeTonesAreSummedWithoutOwnVoice(t *testing.T) {
	c := conferenceOf(t, "a", "b", "c")
	in := map[string][]byte{
		"a": tone(440, 9000, 0),
		"b": tone(1000, 7000, 0),
		"c": tone(2500, 5000, 0),
	}
	out := mix(t, c, in)
	for listener := range in {
		for i := 0; i < FrameSize; i++ {
			var want int
			for speaker, frame := range in {
				if speaker != listener {
					want += int(g711.DecodeMuLaw(frame[i]))
				}
			}
			got := int(g711.DecodeMuLaw(out[listener][i]))
			// Encoding the sum loses at most half a mu-law step, which is
			// at most 1/32 of the value plus the smallest step of 8.
			if diff := abs(got - want); diff > 8+abs(want)/32 {
				t.Fatalf("%s, sample %d: got %d, want %d", listener, i, got, want)
			}
		}
	}
}

func TestOppositeSignalsCancel(t *testing.T) {
	c := conferenceOf(t, "a", "b", "c")
	out := mix(t, c, map[string][]byte{"a": frameOf(0x80), "b": frameOf(0x00)})
	// +32124 and -32124 sum to zero for c.
	if !bytes.Equal(out["c"], frameOf(muLawSilence)) {
		t.Errorf("c hears %x, want silence", out["c"][:8])
	}
}

func TestSumIsSaturated(t *testing.T) {
	c := conferenceOf(t, "a", "b", "c", "d")
	out := mix(t, c, map[string][]byte{"a": frameOf(0x80), "b": frameOf(0x80), "c": frameOf(0x80)})
	// d hears 3 x 32124, far above 32767: clipped to the loudest code,
	// not wrapped around to a negative value.
	if !bytes.Equal(out["d"], frameOf(0x80)) {
		t.Errorf("d hears %x, want the loudest positive code 0x80", out["d"][:8])
	}
	out = mix(t, c, map[string][]byte{"a": frameOf(0x00), "b": frameOf(0x00), "c": frameOf(0x00)})
	if !bytes.Equal(out["d"], frameOf(0x00)) {
		t.Errorf("d hears %x, want the loudest negative code 0x00", out["d"][:8])
	}
	// a hears b and c, 2 x 32124, also clipped, but not its own voice.
	if !bytes.Equal(out["a"], frameOf(0x00)) {
		t.Errorf("a hears %x, want 0x00", out["a"][:8])
	}
}

func TestNobodyHearsThemselves(t *testing.T) {
	c := conferenceOf(t, "a", "b", "c")
	out := mix(t, c, map[string][]byte{"a": tone(440, 16000, 0)})
	if !bytes.Equal(out["a"], frameOf(muLawSilence)) {
		t.Errorf("a, the only speaker, hears %x, want silence", out["a"][:8])
	}
	if bytes.Equal(out["b"], frameOf(muLawSilence)) {
		t.Error("b does not hear a")
	}
}

func TestAloneInTheConferenceIsSilence(t *testing.T) {
	c := conferenceOf(t, "a")
	out := mix(t, c, map[string][]byte{"a": tone(440, 16000, 0)})
	if len(out) != 1 || !bytes.Equal(out["a"], frameOf(muLawSilence)) {
		t.Errorf("got %v, want silence for a", out)
	}
}

func TestMissingFrameIsSilence(t *testing.T) {
	c := conferenceOf(t, "a", "b", "c")
	b := canonical(tone(1000, 8000, 0))
	// a sent nothing this tick and c is mapped to nil: only b is heard.
	out := mix(t, c, map[string][]byte{"b": b, "c": nil})
	if len(out) != 3 {
		t.Fatalf("got frames for %d participants, want 3", len(out))
	}
	if !bytes.Equal(out["a"], b) || !bytes.Equal(out["c"], b) {
		t.Errorf("a and c should hear b alone")
	}
	if !bytes.Equal(out["b"], frameOf(muLawSilence)) {
		t.Errorf("b should hear silence")
	}
}

func TestJoinAndLeaveBetweenTicks(t *testing.T) {
	c := conferenceOf(t, "a", "b")
	a := canonical(tone(440, 8000, 0))

	if err := c.Join("c"); err != nil {
		t.Fatal(err)
	}
	out := mix(t, c, map[string][]byte{"a": a})
	if !bytes.Equal(out["c"], a) {
		t.Error("c, who has just joined, does not hear a")
	}

	if err := c.Leave("a"); err != nil {
		t.Fatal(err)
	}
	out = mix(t, c, map[string][]byte{"b": frameOf(0x90)})
	if _, ok := out["a"]; ok {
		t.Error("a, who has left, still gets a frame")
	}
	if !bytes.Equal(out["c"], frameOf(0x90)) {
		t.Error("c should hear b alone")
	}
	if got, want := c.Participants(), []string{"b", "c"}; !slices.Equal(got, want) {
		t.Errorf("Participants() = %v, want %v", got, want)
	}
}

func TestErrors(t *testing.T) {
	c := conferenceOf(t, "a")
	if err := c.Join("a"); err == nil {
		t.Error("joining twice succeeded")
	}
	if err := c.Leave("x"); err == nil {
		t.Error("leaving without joining succeeded")
	}
	if _, err := c.Mix(map[string][]byte{"x": frameOf(0xFF)}); err == nil {
		t.Error("a frame from a stranger was mixed")
	}
	if _, err := c.Mix(map[string][]byte{"a": make([]byte, 240)}); err == nil {
		t.Error("a 30 ms frame was mixed as if it were 20 ms")
	}
}

// TestConcurrentControlAndMix has the control API join and leave while
// the media loop mixes; run with -race.
func TestConcurrentControlAndMix(t *testing.T) {
	c := conferenceOf(t, "a")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("p%d", i)
			if err := c.Join(id); err != nil {
				t.Error(err)
				return
			}
			if err := c.Leave(id); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if _, err := c.Mix(map[string][]byte{"a": tone(440, 8000, i*FrameSize)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
