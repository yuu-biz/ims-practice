package agw

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/rtp"
)

// Announcements are the announcements provisioned in an MRFP, by name.
// An MRFC asks for one by its name, as the an parameter of the signal
// an/apf names it (TS 29.333 5.14.3.18) and as the provisioned
// prompt-url /provisioned/<name> of RFC 4240 3.3 does.
type Announcements map[string]*mixer.Clip

// LoadAnnouncements reads each WAV file of files, keyed by the name of
// its announcement.
func LoadAnnouncements(files map[string]string) (Announcements, error) {
	out := make(Announcements, len(files))
	for name, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("agw: announcement %q: %w", name, err)
		}
		clip, err := mixer.ReadWAV(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("agw: announcement %q in %s: %w", name, path, err)
		}
		out[name] = clip
	}
	return out, nil
}

// Announcement is a provisioned announcement as the API shows it.
type Announcement struct {
	Name string `json:"name"`
	// DurationMS is how long one play lasts.
	DurationMS int `json:"duration_ms"`
}

// Announcement tells whether the relay has the announcement name, as an
// MRFC finds out with Audit Capability (TS 23.333 6.1.8.2) before it
// accepts a request to play it.
func (r *Relay) Announcement(name string) (Announcement, bool) {
	clip, ok := r.announcements[name]
	if !ok {
		return Announcement{}, false
	}
	return Announcement{Name: name, DurationMS: clip.Frames() * mixer.Ptime}, true
}

// ErrUnknownAnnouncement means the relay has no announcement of that
// name.
var ErrUnknownAnnouncement = errors.New("agw: no such announcement")

// Signals is the Signals descriptor of a termination: the media that
// the node itself sends to the party of the termination (H.248.1
// (03/2013) 7.1.11). A new Signals descriptor replaces the one before,
// and an empty one stops what plays (7.1.11.11). While a signal plays,
// the termination sends it in place of what the context would send.
type Signals struct {
	// Play plays a provisioned announcement: the signal an/apf of the
	// Start Announcement procedure (TS 29.333 5.14.3.18, 5.17.2.9).
	Play *Play `json:"play,omitempty"`
}

// RepeatForever is the Repeat that plays until the duration runs out,
// or until the controller stops the signal.
const RepeatForever = mixer.RepeatForever

// Play is the announcement to play, with the parameters of RFC 4240 3
// that shape it.
type Play struct {
	Announcement string `json:"announcement"`
	// Repeat is the number of plays in all, the Announcement Cycles of
	// TS 29.333 5.17.2.9. Zero plays once.
	Repeat int `json:"repeat,omitzero"`
	// DelayMS is the silence between two plays, and DurationMS the most
	// the announcement may last; zero means no limit.
	DelayMS    int `json:"delay_ms,omitzero"`
	DurationMS int `json:"duration_ms,omitzero"`
}

// EventName names an event that a termination can report.
type EventName string

// SignalCompleted is the event g/sc, Signal Completion (H.248.1
// (03/2013) E.1.2.2): a signal has ended on its own, as when an
// announcement has played its last cycle or its duration has run out.
// It is the Announcement Completed procedure of TS 29.333 5.17.2.11.
const SignalCompleted EventName = "signal_completed"

// Events is the Events descriptor of a termination (H.248.1 (03/2013)
// 7.1.9): the events to report, and the RequestID that each report
// carries back. A new Events descriptor replaces the one before.
type Events struct {
	RequestID uint32      `json:"request_id"`
	Detect    []EventName `json:"detect"`
	// Notify is the URL that the node posts each Notify to. A media
	// gateway sends Notify to the one controller it has registered
	// with; this API serves several controllers at once, so each
	// request says where its events go.
	Notify string `json:"notify"`
}

// Notify is what the node posts to Events.Notify when an event that the
// controller asked for has happened: the Notify command, with its
// ObservedEvents descriptor (H.248.1 (03/2013) 7.2.7).
type Notify struct {
	Context     string        `json:"context"`
	Termination TerminationID `json:"termination"`
	RequestID   uint32        `json:"request_id"`
	Event       EventName     `json:"event"`
}

// String is the signal for the log.
func (s *Signals) String() string {
	if s == nil || s.Play == nil {
		return ""
	}
	return "play " + s.Play.Announcement
}

func (e *Events) detects(name EventName) bool {
	if e == nil {
		return false
	}
	for _, d := range e.Detect {
		if d == name {
			return true
		}
	}
	return false
}

func (e *Events) check() error {
	if e == nil || len(e.Detect) == 0 {
		return nil
	}
	for _, d := range e.Detect {
		if d != SignalCompleted {
			return fmt.Errorf("%w: event %q", ErrBadRequest, d)
		}
	}
	if !strings.HasPrefix(e.Notify, "http://") {
		return fmt.Errorf("%w: notify %q is not an http:// URL", ErrBadRequest, e.Notify)
	}
	return nil
}

// playback starts what s asks for, or returns nil for an empty Signals
// descriptor, which stops what plays.
func (r *Relay) playback(s *Signals) (*mixer.Playback, error) {
	if s.Play == nil {
		return nil, nil
	}
	clip, ok := r.announcements[s.Play.Announcement]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAnnouncement, s.Play.Announcement)
	}
	p, err := clip.Play(mixer.PlayOptions{
		Repeat:   s.Play.Repeat,
		Delay:    time.Duration(s.Play.DelayMS) * time.Millisecond,
		Duration: time.Duration(s.Play.DurationMS) * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return p, nil
}

// notify posts n in a goroutine of its own, so that a controller that
// is slow to answer holds up no media.
func (r *Relay) notify(url string, n Notify) {
	body, err := json.Marshal(n)
	if err != nil {
		return
	}
	go func() {
		resp, err := r.notifier.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			r.log.Warn("Notify not delivered", "context", n.Context, "termination", string(n.Termination), "url", url, "error", err.Error())
			return
		}
		resp.Body.Close()
		r.log.Info("Notify sent", "context", n.Context, "termination", string(n.Termination), "event", string(n.Event), "status", resp.StatusCode)
	}()
}

// NotifyHandler serves the URL of Events.Notify in a controller: it
// decodes each Notify for f, and answers 200 once f has returned.
func NotifyHandler(f func(Notify)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var n Notify
		if err := json.NewDecoder(req.Body).Decode(&n); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f(n)
	})
}

// source writes the RTP packets of the media that the node makes
// itself for one termination, as an RTP source of its own (RFC 3550 5.1):
// its own SSRC, and a sequence number and timestamp that start at
// random values.
//
// SIMPLIFIED: RFC 3550 6. The node sends no RTCP sender reports for the
// media it makes, and so no RTCP at all for an announcement. The UE
// simulator uses a sender report only for the statistics of its
// receiver reports, and its call goes on without one.
type source struct {
	ssrc uint32
	seq  uint16
	// timestamp is that of the next packet, if it follows the last one
	// without a gap.
	timestamp uint32
	// sent is the tick of the context's clock in which the last packet
	// went out, or zero before the first (relayContext.ticks).
	sent uint64
	// talkspurt marks the first packet after silence (RFC 3551 4.1): a
	// tick in which the source sent nothing, or the start of a signal.
	talkspurt bool
}

func newSource() *source {
	return &source{ssrc: rand.Uint32(), seq: uint16(rand.Uint32()), timestamp: rand.Uint32(), talkspurt: true}
}

// packet carries one 20 ms PCMU payload under payload type pt, sent in
// the tick now.
//
// The timestamp counts the frames of the sampling clock, whether the
// source sent them or not (RFC 3550 5.1). After silence it goes on by
// the ticks that the silence lasted: the packets are made one a tick,
// and the nominal sampling instant is what counts, not the time of day
// at which a tick came.
func (s *source) packet(pt uint8, payload []byte, now uint64) []byte {
	if s.talkspurt && s.sent != 0 && now-s.sent > 1 {
		s.timestamp += uint32(now-s.sent-1) * mixer.FrameSize
	}
	p := rtp.Packet{Marker: s.talkspurt, PayloadType: pt, SequenceNumber: s.seq, Timestamp: s.timestamp, SSRC: s.ssrc, Payload: payload}
	s.seq++
	s.timestamp += uint32(len(payload))
	s.sent = now
	s.talkspurt = false
	return p.Marshal()
}

// idle tells the source of the tick now. If it sent no packet in that
// tick, its next packet starts a talkspurt.
func (s *source) idle(now uint64) {
	if s.sent != now {
		s.talkspurt = true
	}
}
