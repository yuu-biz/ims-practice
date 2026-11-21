package announcements

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/wav"
)

// TestFilesAreGenerated: each WAV file in the repository is what Clips
// makes, and there is no other. After a change to Clips, run go
// generate ./deploy/announcements.
func TestFilesAreGenerated(t *testing.T) {
	clips := Clips()
	files, err := filepath.Glob("*.wav")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(clips) {
		t.Errorf("files %v, want one per announcement of Clips", files)
	}
	for name, samples := range clips {
		got, err := os.ReadFile(name + ".wav")
		if err != nil {
			t.Errorf("%s: %v (run go generate ./deploy/announcements)", name, err)
			continue
		}
		var want bytes.Buffer
		if err := wav.Write(&want, samples); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Errorf("%s.wav differs from what Clips makes (run go generate ./deploy/announcements)", name)
		}
	}
}
