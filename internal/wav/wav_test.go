package wav

import (
	"bytes"
	"runtime"
	"slices"
	"testing"
)

// FuzzRead checks that a file cannot make Read reserve more memory than
// the file holds, whatever its chunk sizes claim: the MRFP reads its
// announcements and the UE simulator the file it plays with Read.
func FuzzRead(f *testing.F) {
	var b bytes.Buffer
	if err := Write(&b, []int16{0, 1000, -1000}); err != nil {
		f.Fatal(err)
	}
	f.Add(b.Bytes())
	f.Fuzz(func(t *testing.T, file []byte) {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, _ = Read(bytes.NewReader(file))
		runtime.ReadMemStats(&after)
		// The slack covers the reader's own buffers and what the fuzzing
		// engine allocates meanwhile.
		if grew, limit := after.TotalAlloc-before.TotalAlloc, uint64(1<<20+8*len(file)); grew > limit {
			t.Errorf("Read of %d bytes allocated %d bytes, want at most %d", len(file), grew, limit)
		}
	})
}

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
