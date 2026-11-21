package wav

import (
	"bytes"
	"slices"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	samples := []int16{0, 1000, -1000, 32767, -32768}
	var b bytes.Buffer
	if err := Write(&b, samples); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 44+2*len(samples) {
		t.Errorf("file is %d bytes", b.Len())
	}
	got, err := Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, samples) {
		t.Errorf("got %v, want %v", got, samples)
	}
}

func TestRejectsOtherSampleRates(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, []int16{1}); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	raw[24], raw[25] = 0x80, 0x3e // 16000 Hz
	if _, err := Read(bytes.NewReader(raw)); err == nil {
		t.Error("accepted a sample rate other than 8000")
	}
}
