package agw

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// ramp is a clip of n frames whose samples all differ, so that a frame
// out of place shows.
func ramp(t *testing.T, frames int) (*mixer.Clip, [][]byte) {
	t.Helper()
	samples := make([]int16, frames*mixer.FrameSize)
	for i := range samples {
		samples[i] = int16(i*37%20000 - 10000)
	}
	clip, err := mixer.NewClip(samples)
	if err != nil {
		t.Fatal(err)
	}
	var payloads [][]byte
	for f := range frames {
		payloads = append(payloads, g711.Encode(samples[f*mixer.FrameSize:(f+1)*mixer.FrameSize]))
	}
	return clip, payloads
}

// notifications is a controller's Notify URL that keeps what it gets.
func notifications(t *testing.T) (string, <-chan Notify) {
	t.Helper()
	got := make(chan Notify, 10)
	server := httptest.NewServer(NotifyHandler(func(n Notify) { got <- n }))
	t.Cleanup(server.Close)
	return server.URL + "/notify", got
}

func readRTP(t *testing.T, c interface {
	SetReadDeadline(time.Time) error
	Read([]byte) (int, error)
}, within time.Duration) (rtp.Packet, bool) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return rtp.Packet{}, false
	}
	p, err := rtp.Unmarshal(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	p.Payload = bytes.Clone(p.Payload)
	return p, true
}

// TestPlayIsSentAsRTPAndNotified is Start Announcement and Announcement
// Completed (TS 29.333 5.17.2.9, 5.17.2.11): the announcement goes out
// one PCMU payload per 20 ms packet, twice with a packet of silence
// between, and its end is reported to the controller.
func TestPlayIsSentAsRTPAndNotified(t *testing.T) {
	clip, frames := ramp(t, 4)
	r := newMRFP(t, 20, Announcements{"ramp": clip})
	api := httptest.NewServer(Handler(r))
	defer api.Close()
	client := NewClient(api.URL)
	url, notified := notifications(t)
	party := udp(t)

	c, err := client.Add(t.Context(), TerminationRequest{Mode: SendOnly, Remote: addr(party)})
	if err != nil {
		t.Fatal(err)
	}
	tid := TerminationID("t1")
	_, err = client.Modify(t.Context(), c.ID, tid, TerminationRequest{
		Signals: &Signals{Play: &Play{Announcement: "ramp", Repeat: 2, DelayMS: 20}},
		Events:  &Events{RequestID: 7, Detect: []EventName{SignalCompleted}, Notify: url},
	})
	if err != nil {
		t.Fatal(err)
	}

	silence := bytes.Repeat([]byte{0xFF}, mixer.FrameSize)
	want := append(append(append([][]byte{}, frames...), silence), frames...)
	var first rtp.Packet
	for i, payload := range want {
		p, ok := readRTP(t, party, time.Second)
		if !ok {
			t.Fatalf("packet %d did not come", i)
		}
		if i == 0 {
			first = p
			if !p.Marker {
				t.Error("the first packet of the announcement has no marker bit")
			}
		}
		if p.PayloadType != 0 || p.SSRC != first.SSRC || p.SequenceNumber != first.SequenceNumber+uint16(i) ||
			p.Timestamp != first.Timestamp+uint32(i*mixer.FrameSize) {
			t.Errorf("packet %d: PT %d SSRC %x seq %d ts %d; want PCMU from one source in order", i, p.PayloadType, p.SSRC, p.SequenceNumber, p.Timestamp)
		}
		if !bytes.Equal(p.Payload, payload) {
			t.Errorf("packet %d carries the wrong payload", i)
		}
	}
	select {
	case n := <-notified:
		if n != (Notify{Context: c.ID, Termination: tid, RequestID: 7, Event: SignalCompleted}) {
			t.Errorf("Notify %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("no Notify after the last packet")
	}
	if extra := rtptest.Queued(t, party); len(extra) != 0 {
		t.Errorf("%d packets came after the announcement ended", len(extra))
	}
	if s := r.Contexts()[0].Terminations[tid].Signals; s != nil {
		t.Errorf("the termination still shows %+v playing", s)
	}
}

// TestEmptySignalsStopAPlay is H.248.1 (03/2013) 7.1.11.11 and Stop
// Announcement (TS 29.333 5.17.2.10): an empty Signals descriptor stops
// an announcement that plays forever. A stop is no completion, so no
// Notify comes.
func TestEmptySignalsStopAPlay(t *testing.T) {
	clip, _ := ramp(t, 2)
	r := newMRFP(t, 20, Announcements{"ramp": clip})
	url, notified := notifications(t)
	party := udp(t)
	c, err := r.Add(TerminationRequest{
		Mode: SendOnly, Remote: addr(party),
		Signals: &Signals{Play: &Play{Announcement: "ramp", Repeat: RepeatForever}},
		Events:  &Events{RequestID: 1, Detect: []EventName{SignalCompleted}, Notify: url},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, ok := readRTP(t, party, time.Second); !ok {
			t.Fatalf("packet %d of the loop did not come", i)
		}
	}
	if _, err := r.Modify(c.ID, "t1", TerminationRequest{Signals: &Signals{}}); err != nil {
		t.Fatal(err)
	}
	// What the loop sent before the Modify returned is in the socket;
	// nothing may follow it.
	rtptest.Queued(t, party)
	if _, ok := readRTP(t, party, 100*time.Millisecond); ok {
		t.Error("packets still come after the empty Signals descriptor")
	}
	select {
	case n := <-notified:
		t.Errorf("got %+v for a signal that the controller stopped", n)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestPlayTakesThePlaceOfRelayedMedia: while a termination plays, what
// comes from the other termination of the context does not reach its
// party; afterwards it does again.
func TestPlayTakesThePlaceOfRelayedMedia(t *testing.T) {
	clip, frames := ramp(t, 3)
	r := newMRFP(t, 20, Announcements{"ramp": clip})
	url, notified := notifications(t)
	caller, callee := udp(t), udp(t)
	c, err := r.Add(TerminationRequest{ID: "caller", Remote: addr(caller)}, TerminationRequest{ID: "callee", Remote: addr(callee)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Modify(c.ID, "caller", TerminationRequest{
		Signals: &Signals{Play: &Play{Announcement: "ramp"}},
		Events:  &Events{RequestID: 1, Detect: []EventName{SignalCompleted}, Notify: url},
	}); err != nil {
		t.Fatal(err)
	}
	callee.WriteToUDPAddrPort((rtp.Packet{PayloadType: 0, Payload: []byte("speech")}).Marshal(), c.Terminations["callee"].Local)
	for i, want := range frames {
		p, ok := readRTP(t, caller, time.Second)
		if !ok || !bytes.Equal(p.Payload, want) {
			t.Fatalf("packet %d to the caller: %v, %q; want frame %d of the announcement", i, ok, p.Payload, i)
		}
	}
	// The play is over once the MRFP says so.
	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Fatal("no Notify of the end of the announcement")
	}
	callee.WriteToUDPAddrPort((rtp.Packet{PayloadType: 0, Payload: []byte("speech")}).Marshal(), c.Terminations["callee"].Local)
	if p, ok := readRTP(t, caller, time.Second); !ok || string(p.Payload) != "speech" {
		t.Errorf("after the announcement the caller got %v, %q; want the callee's packet", ok, p.Payload)
	}
}

func TestUnknownAnnouncement(t *testing.T) {
	clip, _ := ramp(t, 1)
	r := newMRFP(t, 4, Announcements{"ramp": clip})
	api := httptest.NewServer(Handler(r))
	defer api.Close()
	client := NewClient(api.URL)

	if a, ok, err := client.Announcement(t.Context(), "ramp"); err != nil || !ok || a.DurationMS != 20 {
		t.Errorf("Announcement(ramp) = %+v, %v, %v; want 20 ms", a, ok, err)
	}
	if _, ok, err := client.Announcement(t.Context(), "busy"); err != nil || ok {
		t.Errorf("Announcement(busy) = %v, %v; want not found", ok, err)
	}
	_, err := client.Add(t.Context(), TerminationRequest{Signals: &Signals{Play: &Play{Announcement: "busy"}}})
	if !errors.Is(err, ErrUnknownAnnouncement) {
		t.Errorf("Add with an unknown announcement got %v", err)
	}
	// The range holds two terminations; the failed Add must have given
	// its ports back.
	if _, err := r.Add(TerminationRequest{}, TerminationRequest{}); err != nil {
		t.Errorf("after the failed Add: %v", err)
	}
	if _, err := r.Add(TerminationRequest{Events: &Events{Detect: []EventName{SignalCompleted}, Notify: "mrfc:8081"}}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Events without an http:// URL got %v", err)
	}
}

func TestNotifyHandlerTakesPOSTOnly(t *testing.T) {
	server := httptest.NewServer(NotifyHandler(func(Notify) { t.Error("a GET reached the handler") }))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET got %d", resp.StatusCode)
	}
	resp, err = http.Post(server.URL, "application/json", bytes.NewReader([]byte("{")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a broken body got %d", resp.StatusCode)
	}
}
