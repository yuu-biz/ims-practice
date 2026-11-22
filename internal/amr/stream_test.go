package amr

import (
	"bytes"
	"testing"
)

// A listener hears the frames of a call while it runs: each packet adds
// to the stream, the stream holds what the storage format says it
// should at every point, and at the end it is the file of the whole
// call.
func TestStreamWriterWritesEachPacketAsItArrives(t *testing.T) {
	const codec = AMRWB
	r := NewRecorder(codec)
	var got bytes.Buffer
	stream := NewStreamWriter(&got, codec)

	if err := stream.Write(nil); err != nil || got.Len() != 0 {
		t.Errorf("a call that received nothing wrote %x, %v; want nothing, not even the magic number", got.Bytes(), err)
	}
	step := codec.SamplesPerFrame()
	base := uint32(0xfffffe00) // the timestamp wraps during the call
	arrivals := []struct {
		at     uint32 // in frames from the first one
		frames []Frame
	}{
		{0, []Frame{frame(t, codec, 8, 1)}},
		{1, []Frame{frame(t, codec, 8, 2)}},
		{4, []Frame{frame(t, codec, 2, 3)}}, // the two before it were lost
		{5, []Frame{frame(t, codec, 9, 4)}}, // comfort noise
	}
	written := 0
	for i, a := range arrivals {
		r.Add(base+a.at*step, Payload{CMR: NoModeRequest, Frames: a.frames})
		filed := r.Frames()
		if err := stream.Write(filed[written:]); err != nil {
			t.Fatalf("packet %d: %v", i+1, err)
		}
		written = len(filed)
		// What has gone out is readable on its own, and holds every
		// frame filed so far, the lost ones as NO_DATA.
		c, heard, err := ReadStorage(bytes.NewReader(got.Bytes()))
		if err != nil {
			t.Fatalf("after packet %d the stream does not read back: %v", i+1, err)
		}
		if c != codec || len(heard) != written {
			t.Fatalf("after packet %d the stream holds %s with %d frames; want %s with %d", i+1, c, len(heard), codec, written)
		}
		if heard[len(heard)-1].Type != a.frames[len(a.frames)-1].Type {
			t.Errorf("after packet %d the newest frame is type %d; want %d", i+1, heard[len(heard)-1].Type, a.frames[0].Type)
		}
	}
	if types := frameTypes(r.Frames()); !equalTypes(types, []FrameType{8, 8, NoData, NoData, 2, 9}) {
		t.Fatalf("the recording holds the frame types %v", types)
	}

	// The same frames written at the end of the call, as -received-out
	// writes them to a file, are the same bytes.
	var file bytes.Buffer
	if err := WriteStorage(&file, codec, r.Frames()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), file.Bytes()) {
		t.Errorf("the stream of the call differs from the file of the call:\n%x\n%x", got.Bytes(), file.Bytes())
	}
}

// A frame whose place has gone out is left out of the stream, while the
// file of the whole call still holds it (Recorder keeps the better of
// two frames in one place).
func TestStreamWriterLeavesOutWhatComesTooLate(t *testing.T) {
	const codec = AMR
	r := NewRecorder(codec)
	var got bytes.Buffer
	stream := NewStreamWriter(&got, codec)
	step := codec.SamplesPerFrame()
	written := 0
	flush := func(t *testing.T) {
		t.Helper()
		filed := r.Frames()
		if err := stream.Write(filed[written:]); err != nil {
			t.Fatal(err)
		}
		written = len(filed)
	}

	r.Add(0, Payload{Frames: []Frame{frame(t, codec, 7, 1)}})
	flush(t)
	// The third packet comes before the second, so the stream has
	// already said that the second place holds nothing.
	r.Add(2*step, Payload{Frames: []Frame{frame(t, codec, 7, 2)}})
	flush(t)
	r.Add(step, Payload{Frames: []Frame{frame(t, codec, 5, 3)}})
	flush(t)

	_, heard, err := ReadStorage(bytes.NewReader(got.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if types := frameTypes(heard); !equalTypes(types, []FrameType{7, NoData, 7}) {
		t.Errorf("the stream holds %v; want the late frame's place left as NO_DATA", types)
	}
	if types := frameTypes(r.Frames()); !equalTypes(types, []FrameType{7, 5, 7}) {
		t.Errorf("the recording holds %v; want the late frame in its place", types)
	}
}

func frameTypes(frames []Frame) []FrameType {
	types := make([]FrameType, len(frames))
	for i, f := range frames {
		types[i] = f.Type
	}
	return types
}

func equalTypes(got, want []FrameType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
