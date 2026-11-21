// Package mixer is the audio part of the MRFP (TS 23.228 4.7): a mixer
// that gives each participant of a conference the sum of everybody
// else, and a player that turns an audio file into the payloads of an
// announcement. Both work on G.711 mu-law (PCMU, payload type 0 of RFC
// 3551 Table 4), 8000 samples per second in packets of 20 ms, which is
// the codec every UE and media node of this project can send.
//
// Neither part reads a clock, starts a goroutine or touches the
// network. The caller keeps the 20 ms tick, collects the RTP payloads
// that arrived, and writes the RTP headers (internal/rtp). That keeps
// the arithmetic testable on its own and lets the same code sit behind
// the HTTP control API and behind H.248.
package mixer

import (
	"fmt"
	"slices"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/g711"
)

const (
	// SampleRate is the G.711 sampling rate (RFC 3551 4.5.14, Table 4).
	SampleRate = 8000
	// Ptime is the packet time in milliseconds, the default of RFC 3551
	// 4.2 and the value this project puts in SDP.
	Ptime = 20
	// FrameSize is the number of samples in one packet. G.711 has one
	// byte per sample, so it is also the length of a PCMU payload.
	FrameSize = SampleRate * Ptime / 1000
)

// muLawSilence is the mu-law code of a zero sample. A frame of it is
// what a participant hears when nobody else is talking, and what pads
// the last frame of a clip.
const muLawSilence byte = 0xFF

// Conference is the mix of one conference: the set of participants
// whose audio is summed.
//
// A mix has to live in one MRFP process, because every
// participant's audio must meet in one place to be added. Only the
// membership is kept here, and it is small. Which MRFP and context hold
// which conference is the MRFC's state and belongs in its Store, so any
// MRFC instance can carry on with the conference.
//
// A Conference is safe for concurrent use, so that the control API can
// add and remove participants while the media loop mixes. The lock
// covers this conference only.
type Conference struct {
	mu           sync.Mutex
	participants map[string]bool
}

// NewConference returns a conference with no participants.
func NewConference() *Conference {
	return &Conference{participants: make(map[string]bool)}
}

// Join adds a participant, identified by an id the caller chooses,
// such as the termination ID of its RTP stream. From the next Mix on,
// the participant hears the others and the others hear it.
func (c *Conference) Join(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.participants[id] {
		return fmt.Errorf("mixer: %q is already in the conference", id)
	}
	c.participants[id] = true
	return nil
}

// Leave removes a participant. From the next Mix on, it is neither
// heard nor given a frame.
func (c *Conference) Leave(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.participants[id] {
		return fmt.Errorf("mixer: %q is not in the conference", id)
	}
	delete(c.participants, id)
	return nil
}

// Participants returns the ids of the participants in sorted order.
func (c *Conference) Participants() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.participants))
	for id := range c.participants {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Mix makes one 20 ms tick of the conference. in holds the latest PCMU
// payload received from each participant, FrameSize bytes long. A
// participant missing from in, or mapped to nil, sent nothing in this
// tick (a lost packet, or silence suppression of RFC 3551 4.1) and adds
// silence.
//
// The result has one PCMU payload for every participant: the sum of
// all the other participants, never its own voice, which would come
// back to the speaker as an echo. A participant alone in the
// conference gets silence.
func (c *Conference) Mix(in map[string][]byte) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, frame := range in {
		if !c.participants[id] {
			return nil, fmt.Errorf("mixer: frame from %q, who is not in the conference", id)
		}
		if frame != nil && len(frame) != FrameSize {
			return nil, fmt.Errorf("mixer: frame from %q has %d bytes, want %d", id, len(frame), FrameSize)
		}
	}

	// The total is kept in int32, wide enough for any number of int16
	// samples a conference will have, so that taking one participant
	// out of it again is exact. Clipping happens only on the result.
	var total [FrameSize]int32
	own := make(map[string]*[FrameSize]int32, len(in))
	for id, frame := range in {
		if frame == nil {
			continue
		}
		var linear [FrameSize]int32
		for i, b := range frame {
			linear[i] = int32(g711.DecodeMuLaw(b))
			total[i] += linear[i]
		}
		own[id] = &linear
	}

	out := make(map[string][]byte, len(c.participants))
	for id := range c.participants {
		frame := make([]byte, FrameSize)
		for i := range frame {
			s := total[i]
			if mine := own[id]; mine != nil {
				s -= mine[i]
			}
			frame[i] = g711.EncodeMuLaw(saturate(s))
		}
		out[id] = frame
	}
	return out, nil
}

// saturate clips a sum to the 16-bit range. Wrapping around instead
// would turn a loud passage into a loud click.
func saturate(s int32) int16 {
	switch {
	case s > 32767:
		return 32767
	case s < -32768:
		return -32768
	default:
		return int16(s)
	}
}
