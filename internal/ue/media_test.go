package ue_test

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/amr"
	"github.com/yuu-biz/ims-practice/internal/rtcp"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

func listenMedia(t *testing.T) *ue.Media {
	t.Helper()
	m, err := ue.ListenMedia("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func loopback(port uint16) netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)
}

var (
	pcmu  = sdp.Codec{PayloadType: sdp.PCMU, Name: sdp.NamePCMU, ClockRate: 8000}
	amrWB = sdp.VoLTECodecs()[0] // AMR-WB, bandwidth-efficient
)

// between joins two Media with the same stream, each sending to the
// other, as the offer and the answer of a call do.
func between(t *testing.T, codec sdp.Codec, events *sdp.Codec) (a, b *ue.Media) {
	t.Helper()
	a, b = listenMedia(t), listenMedia(t)
	for _, p := range []struct{ from, to *ue.Media }{{a, b}, {b, a}} {
		if err := p.from.SetStream(ue.Stream{Remote: loopback(p.to.Port()), Codec: codec, Events: events,
			RS: sdp.RTCPSenders, RR: sdp.RTCPReceivers}); err != nil {
			t.Fatal(err)
		}
		p.from.SetFlow(ue.Flow{Send: true, Receive: true})
	}
	return a, b
}

// played waits until from has sent all its speech, and to has received
// all of it: every octet of PCMU, and every AMR frame in its place.
func played(t *testing.T, from, to *ue.Media) {
	t.Helper()
	select {
	case <-from.Played():
	case <-time.After(5 * time.Second):
		t.Fatal("the speech never all went out")
	}
	waitFor(t, "all the speech sent to arrive", func() bool {
		got, _ := to.Received()
		return len(got) == len(from.Sent()) && len(to.ReceivedFrames()) == len(from.SentFrames())
	})
}

// TestMediaSendsSymmetricRTP is RFC 4961: the UE sends its RTP from the
// port it receives on, and its RTCP from the port above (RFC 3550 11).
func TestMediaSendsSymmetricRTP(t *testing.T) {
	ue.SetRTCPInterval(t, 50*time.Millisecond)
	a := listenMedia(t)
	if a.Port()%2 != 0 {
		t.Fatalf("RTP on the odd port %d", a.Port())
	}
	// The peer needs a port and the one above it. The system chooses the
	// first, so the second may be taken: try again with another.
	var peer, peerRTCP *net.UDPConn
	for try := 0; try < 50 && peerRTCP == nil; try++ {
		rtpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		rtcpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: rtpConn.LocalAddr().(*net.UDPAddr).Port + 1})
		if err != nil {
			rtpConn.Close()
			continue
		}
		peer, peerRTCP = rtpConn, rtcpConn
	}
	if peerRTCP == nil {
		t.Fatal("no port with a free port above it for the peer after 50 tries")
	}
	defer peer.Close()
	defer peerRTCP.Close()
	if err := a.SetStream(ue.Stream{Remote: peer.LocalAddr().(*net.UDPAddr).AddrPort(), Codec: pcmu, RS: 0, RR: 4000}); err != nil {
		t.Fatal(err)
	}
	a.SetFlow(ue.Flow{Send: true, Receive: true})
	if err := a.Start(ue.Speech{PCM: ue.Tone(440, 100*time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, from, err := peer.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	if from.Port() != a.Port() {
		t.Errorf("RTP from port %d, want %d", from.Port(), a.Port())
	}
	if p, err := rtp.Unmarshal(buf[:n]); err != nil || p.PayloadType != sdp.PCMU || !p.Marker || len(p.Payload) != 160 {
		t.Errorf("first packet %+v, %v; want 160 octets of PCMU with the marker", p, err)
	}
	peerRTCP.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, from, err = peerRTCP.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	if from.Port() != a.Port()+1 {
		t.Errorf("RTCP from port %d, want %d", from.Port(), a.Port()+1)
	}
	packets, err := rtcp.Unmarshal(buf[:n])
	if err != nil || len(packets) < 2 || packets[0].Type() != rtcp.TypeSR || packets[1].Type() != rtcp.TypeSDES {
		t.Errorf("compound packet %+v, %v; want an SR and an SDES (IR.92 3.2.4)", packets, err)
	}
}

func TestMediaCarriesPCMU(t *testing.T) {
	a, b := between(t, pcmu, nil)
	if err := a.Start(ue.Speech{PCM: ue.Tone(440, 200*time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	played(t, a, b)
	got, packets := b.Received()
	if packets != 10 || !bytes.Equal(got, a.Sent()) {
		t.Errorf("received %d packets, %d bytes; want the 10 packets and %d bytes sent", packets, len(got), len(a.Sent()))
	}
}

// TestMediaCarriesAMRWB sends the frames of the embedded tone one per
// packet (IR.92 3.2.5), and the other side files them by timestamp: the
// two match bit for bit.
func TestMediaCarriesAMRWB(t *testing.T) {
	for _, codec := range []sdp.Codec{sdp.VoLTECodecs()[0], sdp.VoLTECodecs()[1], sdp.VoLTECodecs()[2], sdp.VoLTECodecs()[3]} {
		a, b := between(t, codec, nil)
		speech := ue.ToneSpeech(400 * time.Millisecond)
		if err := a.Start(speech); err != nil {
			t.Fatal(err)
		}
		played(t, a, b)
		want := speech.AMRWB
		if codec.Name == sdp.NameAMR {
			want = speech.AMR
		}
		sent, received := a.SentFrames(), b.ReceivedFrames()
		if len(want) != 20 || !slices.EqualFunc(sent, want, sameFrame) || !slices.EqualFunc(received, want, sameFrame) {
			t.Errorf("%s %s: sent %d frames, received %d; want the %d of the tone", codec.Name, codec.Fmtp, len(sent), len(received), len(want))
		}
		if s := b.Stats(); s.PacketsReceived != 20 || s.FramesReceived != 20 {
			t.Errorf("%s: stats %+v", codec.Name, s)
		}
	}
}

func sameFrame(a, b amr.Frame) bool {
	return a.Type == b.Type && a.Quality == b.Quality && bytes.Equal(a.Data, b.Data)
}

// A listener of StreamReceivedTo hears the call while it runs: the
// frames reach the writer before the call ends, and what it has at the
// end is the file that -received-out would write.
func TestMediaStreamsWhatItReceivesWhileTheCallRuns(t *testing.T) {
	for _, codec := range []sdp.Codec{amrWB, pcmu} {
		a, b := between(t, codec, nil)
		var heard syncBuffer
		b.StreamReceivedTo(&heard)
		if err := a.Start(ue.ToneSpeech(400 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		// The start of the call reaches the listener while the rest of
		// the 20 packets is still to go out.
		waitFor(t, "the listener to hear the start of the call", func() bool { return heard.len() > 0 })
		early := heard.len()
		select {
		case <-a.Played():
			t.Errorf("%s: the listener heard nothing before the last packet went out", codec.Name)
		default:
		}
		played(t, a, b)
		if err := b.StreamError(); err != nil {
			t.Errorf("%s: %v", codec.Name, err)
		}
		var want bytes.Buffer
		if codec.Name == sdp.NamePCMU {
			received, _ := b.Received()
			want.Write(received)
		} else if err := amr.WriteStorage(&want, amr.AMRWB, b.ReceivedFrames()); err != nil {
			t.Fatal(err)
		}
		if got := heard.bytes(); !bytes.Equal(got, want.Bytes()) {
			t.Errorf("%s: the stream holds %d bytes, the file of the same call %d", codec.Name, len(got), want.Len())
		}
		if heard.len() <= early {
			t.Errorf("%s: the stream stopped growing after %d bytes", codec.Name, early)
		}
	}
}

// A listener that stops reading ends the stream, not the call.
func TestMediaKeepsTheCallWhenTheListenerFails(t *testing.T) {
	a, b := between(t, amrWB, nil)
	b.StreamReceivedTo(errorWriter{})
	if err := a.Start(ue.ToneSpeech(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	played(t, a, b)
	if b.StreamError() == nil {
		t.Error("the failed stream is not reported")
	}
	if frames := b.ReceivedFrames(); len(frames) != 5 {
		t.Errorf("the call received %d frames; want the 5 of the tone", len(frames))
	}
}

// syncBuffer is read by the test while the media goroutine writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

func (s *syncBuffer) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("the listener stopped reading") }

// TestMediaPausesWithoutLosingTheTiming: while the flow allows no
// sending, as on hold, the speech waits and the RTP clock runs on, so
// that the receiving side files NO_DATA for the pause (RFC 4867 5.3).
func TestMediaPausesWithoutLosingTheTiming(t *testing.T) {
	a, b := between(t, amrWB, nil)
	speech := ue.ToneSpeech(time.Second)
	a.SetFlow(ue.Flow{})
	if err := a.Start(speech); err != nil {
		t.Fatal(err)
	}
	a.SetFlow(ue.Flow{Send: true, Receive: true})
	waitFor(t, "the first frames of the tone to go out", func() bool { return len(a.SentFrames()) >= 2 })
	a.SetFlow(ue.Flow{Receive: true})
	select {
	case <-a.Played():
		t.Fatal("the whole tone went out before the pause")
	default:
	}
	packetTimes(t, a, 3)
	a.SetFlow(ue.Flow{Send: true, Receive: true})
	played(t, a, b)
	sent, received := a.SentFrames(), b.ReceivedFrames()
	speechFrames := 0
	for _, f := range received {
		if f.Type != amr.NoData {
			speechFrames++
		}
	}
	if len(sent) <= 50 || speechFrames != 50 || !slices.EqualFunc(sent, received, sameFrame) {
		t.Errorf("sent %d frames, received %d with %d of speech; want the 50 of the tone with a pause of NO_DATA", len(sent), len(received), speechFrames)
	}
}

// packetTimes waits until the RTP clock of m has moved on n times: n
// packet times have gone by for its sender.
func packetTimes(t *testing.T, m *ue.Media, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for last, moved := ue.RTPClock(m), 0; moved < n; time.Sleep(time.Millisecond) {
		if now := ue.RTPClock(m); now != last {
			last, moved = now, moved+1
		}
		if time.Now().After(deadline) {
			t.Fatalf("the RTP clock moved on %d times in 5 s, want %d", moved, n)
		}
	}
}

// TestMediaCarriesDTMF is RFC 4733 in the audio stream (TS 26.114
// Annex G): each digit arrives once.
func TestMediaCarriesDTMF(t *testing.T) {
	events := sdp.VoLTECodecs()[5] // telephone-event/16000
	a, b := between(t, amrWB, &events)
	digits := make(chan rune, 8)
	b.OnDigit(func(d rune) { digits <- d })
	if err := a.SendDTMF("1x"); err == nil {
		t.Error("a letter went out as a digit")
	}
	if err := a.Start(ue.ToneSpeech(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := a.SendDTMF("1#"); err != nil {
		t.Fatal(err)
	}
	var got []rune
	for range 2 {
		select {
		case d := <-digits:
			got = append(got, d)
		case <-time.After(5 * time.Second):
			t.Fatalf("got %q, want 1#", string(got))
		}
	}
	if sent, received := a.DTMF(); string(got) != "1#" || sent != "1#" || received != "" {
		t.Errorf("heard %q; a sent %q", string(got), sent)
	}
	if _, received := b.DTMF(); received != "1#" {
		t.Errorf("b keeps %q", received)
	}
	noEvents, _ := between(t, pcmu, nil)
	if err := noEvents.SendDTMF("1"); err != ue.ErrNoTelephoneEvent {
		t.Errorf("DTMF without telephone-event: %v, want ErrNoTelephoneEvent (IR.92 3.3)", err)
	}
}

// TestRTCPGoesBothWaysAndEndsWithBYE is IR.92 3.2.4 and RFC 3550 6.6.
func TestRTCPGoesBothWaysAndEndsWithBYE(t *testing.T) {
	ue.SetRTCPInterval(t, 20*time.Millisecond)
	a, b := between(t, pcmu, nil)
	a.Start(ue.Speech{PCM: ue.Tone(440, time.Second)})
	b.Start(ue.Speech{})
	if !came(watch, func() bool { return a.Stats().RTCPReceived > 1 && b.Stats().RTCPReceived > 1 }) {
		t.Fatalf("no RTCP both ways within %v", watch)
	}
	before := b.Stats().RTCPReceived
	a.Stop()
	waitFor(t, "the RTCP BYE", func() bool { return b.Stats().RTCPReceived > before })

	off, other := between(t, pcmu, nil)
	off.SetStream(ue.Stream{Remote: loopback(other.Port()), Codec: pcmu})
	off.Start(ue.Speech{PCM: ue.Tone(440, 100*time.Millisecond)})
	// The watch that saw RTCP both ways above.
	if came(watch, func() bool { return off.Stats().RTCPSent != 0 }) {
		t.Errorf("%d RTCP packets with b=RS:0 and b=RR:0, want none (IR.92 3.2.4)", off.Stats().RTCPSent)
	}
}
