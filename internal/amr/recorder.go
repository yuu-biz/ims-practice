package amr

// Recorder rebuilds the frames of a received stream for the storage
// format. Each frame goes to its place by RTP timestamp; a frame that
// arrives twice is kept once, the version of the highest mode (RFC 4867
// 4.1, IR.92 3.2.5); and a frame that never arrived is stored as
// NO_DATA, so that the file keeps the timing of the call (RFC 4867
// 5.3). A Recorder belongs to one call.
type Recorder struct {
	codec   Codec
	started bool
	base    uint32 // the timestamp of the first frame
	frames  []Frame
}

func NewRecorder(c Codec) *Recorder { return &Recorder{codec: c} }

// Add files the frames of a payload whose RTP packet has timestamp:
// the first frame is at timestamp, the next 20 ms later (RFC 4867 4.1).
//
// SIMPLIFIED: IR.92 3.2.5. Frames from before the first packet that
// arrived, which only a reordered start of the stream would bring, are
// dropped rather than moving the start of the recording.
func (r *Recorder) Add(timestamp uint32, p Payload) {
	if !r.started {
		r.started, r.base = true, timestamp
	}
	// The difference as signed 32 bits lets the timestamp wrap.
	first := int(int32(timestamp-r.base)) / int(r.codec.SamplesPerFrame())
	for i, f := range p.Frames {
		n := first + i
		if n < 0 {
			continue
		}
		for len(r.frames) <= n {
			r.frames = append(r.frames, Frame{Type: NoData, Quality: true})
		}
		if r.better(f, r.frames[n]) {
			r.frames[n] = f
		}
	}
}

// better tells that f should replace the frame already filed: data
// beats NO_DATA, and of two speech frames the higher mode wins.
func (r *Recorder) better(f, filed Frame) bool {
	switch {
	case filed.Type == NoData || filed.Type == SpeechLost:
		return f.Type != NoData
	case r.codec.IsSpeech(f.Type) && r.codec.IsSpeech(filed.Type):
		return f.Type > filed.Type
	}
	return false
}

// Frames returns the frames so far, oldest first.
func (r *Recorder) Frames() []Frame { return r.frames }
