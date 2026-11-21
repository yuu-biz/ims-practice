package evs

import (
	"bytes"
	"reflect"
	"testing"
)

func TestStorage(t *testing.T) {
	p132 := frame(t, Primary, Primary13200, 1)
	pSID := frame(t, Primary, PrimarySID, 2)
	io66 := frame(t, AMRWBIO, AMRWBIO6600, 3)
	io66.Quality = false
	frames := []Frame{p132, pSID, {Mode: Primary, Type: NoData, Quality: true}, {Mode: Primary, Type: SpeechLost, Quality: true}, io66}

	var b bytes.Buffer
	if err := WriteStorage(&b, frames); err != nil {
		t.Fatal(err)
	}
	// Figures A.7 and A.8: magic number, 32-bit channel count, then
	// ToC byte and data for each frame. F is 0 in every ToC byte.
	want := cat([]byte("#!EVS_MC1.0\n"), []byte{0, 0, 0, 1},
		[]byte{0x04}, p132.Data, []byte{0x0c}, pSID.Data, []byte{0x0f}, []byte{0x0e},
		[]byte{0b0_0_1_0_0000}, io66.Data)
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("got  %x\nwant %x", b.Bytes(), want)
	}
	got, err := ReadStorage(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, frames) {
		t.Errorf("read back\n%+v\nwant\n%+v", got, frames)
	}
}

func TestReadStorageMalformed(t *testing.T) {
	head := cat([]byte("#!EVS_MC1.0\n"), []byte{0, 0, 0, 1})
	for name, b := range map[string][]byte{
		"empty":           nil,
		"AMR-WB file":     []byte("#!AMR-WB\n"),
		"no channels":     []byte("#!EVS_MC1.0\n"),
		"two channels":    cat([]byte("#!EVS_MC1.0\n"), []byte{0, 0, 0, 2}),
		"CMR byte":        cat(head, []byte{0xff}),
		"future use":      cat(head, []byte{0x0d}),
		"frame cut short": cat(head, []byte{0x04, 1, 2}),
	} {
		if f, err := ReadStorage(bytes.NewReader(b)); err == nil {
			t.Errorf("%s: accepted as %+v", name, f)
		}
	}
	if f, err := ReadStorage(bytes.NewReader(head)); err != nil || len(f) != 0 {
		t.Errorf("a file without frames: %+v, %v", f, err)
	}
}
