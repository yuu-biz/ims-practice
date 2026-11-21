package amr

import (
	"testing"
)

func TestRecorder(t *testing.T) {
	r := NewRecorder(AMRWB)
	low, high := frame(t, AMRWB, 2, 1), frame(t, AMRWB, 8, 2)
	sid := frame(t, AMRWB, 9, 3)
	base := uint32(0xffffff00) // the timestamp wraps during the call
	step := AMRWB.SamplesPerFrame()

	r.Add(base, Payload{Frames: []Frame{low}})
	r.Add(base+2*step, Payload{Frames: []Frame{sid}})            // frame 1 never arrives
	r.Add(base, Payload{Frames: []Frame{high}})                  // a redundant copy at a higher mode
	r.Add(base+2*step, Payload{Frames: []Frame{{Type: NoData}}}) // nothing new
	r.Add(base+3*step, Payload{Frames: []Frame{low, high}})      // two frames in one packet
	r.Add(base-step, Payload{Frames: []Frame{high}})             // before the start: dropped

	got := r.Frames()
	want := []FrameType{8, NoData, 9, 2, 8}
	if len(got) != len(want) {
		t.Fatalf("%d frames, want %d", len(got), len(want))
	}
	for i, ft := range want {
		if got[i].Type != ft {
			t.Errorf("frame %d is type %d, want %d", i, got[i].Type, ft)
		}
	}
}
