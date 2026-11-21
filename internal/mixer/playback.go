package mixer

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/wav"
)

// Clip is an announcement encoded once as PCMU and cut into payloads of
// FrameSize bytes. It does not change after it is made, so the MRFP can
// fetch a file once and play it to many callers at the same time.
type Clip struct {
	frames [][]byte
}

// NewClip encodes linear samples at 8 kHz. The last payload is padded
// with mu-law silence to a full 20 ms, so that every packet has the
// same length and the RTP timestamp advances evenly (RFC 3551 4.2).
func NewClip(samples []int16) (*Clip, error) {
	if len(samples) == 0 {
		return nil, errors.New("mixer: a clip needs at least one sample")
	}
	c := &Clip{}
	for start := 0; start < len(samples); start += FrameSize {
		frame := make([]byte, FrameSize)
		for i := range frame {
			if start+i < len(samples) {
				frame[i] = g711.EncodeMuLaw(samples[start+i])
			} else {
				frame[i] = muLawSilence
			}
		}
		c.frames = append(c.frames, frame)
	}
	return c, nil
}

// ReadWAV reads a clip from a WAV file of 16-bit PCM, mono, 8000
// samples per second, the format internal/wav reads and writes.
func ReadWAV(r io.Reader) (*Clip, error) {
	samples, err := wav.Read(r)
	if err != nil {
		return nil, err
	}
	return NewClip(samples)
}

// Frames is the number of payloads in one play of the clip.
func (c *Clip) Frames() int { return len(c.frames) }

// RepeatForever is the "forever" value of the repeat parameter of RFC
// 4240 3.
const RepeatForever = -1

// PlayOptions are the parameters of the announcement service of RFC
// 4240 3 that shape the audio itself. The MRFC reads them from the
// Request-URI and hands them to the MRFP.
type PlayOptions struct {
	// Repeat is how many times the clip is played in all. Zero means
	// once, as when the parameter is absent. RepeatForever plays until
	// Duration runs out or the caller stops asking for payloads; RFC
	// 4240 3 recommends a local limit on "forever", which the MRFC sets
	// as Duration.
	Repeat int
	// Delay is the silence between two plays.
	Delay time.Duration
	// Duration is the most the announcement may last, delays included.
	// Zero means no limit.
	Duration time.Duration
}

// Play starts a playback of the clip. Delay and Duration are rounded up
// to whole packets, the smallest unit the caller sends.
func (c *Clip) Play(opts PlayOptions) (*Playback, error) {
	if opts.Repeat < RepeatForever {
		return nil, fmt.Errorf("mixer: repeat %d", opts.Repeat)
	}
	if opts.Delay < 0 || opts.Duration < 0 {
		return nil, fmt.Errorf("mixer: negative delay %v or duration %v", opts.Delay, opts.Duration)
	}
	plays := opts.Repeat
	if plays == 0 {
		plays = 1
	}
	return &Playback{
		clip:        c,
		plays:       plays,
		delayFrames: framesIn(opts.Delay),
		limitFrames: framesIn(opts.Duration),
	}, nil
}

func framesIn(d time.Duration) int {
	packet := Ptime * time.Millisecond
	return int((d + packet - 1) / packet)
}

// Playback is one announcement being played, driven by the caller's 20
// ms tick: one call to Next for each RTP packet to send. It is not safe
// for concurrent use; it belongs to the loop that sends its packets.
type Playback struct {
	clip        *Clip
	plays       int // RepeatForever, or the number of plays in all
	delayFrames int
	limitFrames int // 0: no limit

	next   int // index of the next frame of the clip
	played int // plays finished
	gap    int // silent frames still to send before the next play
	sent   int
	ended  bool
}

// Next returns the payload for the next packet. When the playback has
// ended, because every play is done or Duration has run out, it returns
// false. That is the moment the MRFP tells the MRFC that the
// announcement is over, and the MRFC sends BYE (RFC 4240 3).
func (p *Playback) Next() ([]byte, bool) {
	if p.ended || (p.limitFrames > 0 && p.sent == p.limitFrames) {
		p.ended = true
		return nil, false
	}
	p.sent++
	if p.gap > 0 {
		p.gap--
		return frameOfSilence(), true
	}
	payload := append([]byte(nil), p.clip.frames[p.next]...)
	p.next++
	if p.next == len(p.clip.frames) {
		p.next = 0
		p.played++
		if p.plays != RepeatForever && p.played == p.plays {
			p.ended = true
		} else {
			p.gap = p.delayFrames
		}
	}
	return payload, true
}

func frameOfSilence() []byte {
	frame := make([]byte, FrameSize)
	for i := range frame {
		frame[i] = muLawSilence
	}
	return frame
}
