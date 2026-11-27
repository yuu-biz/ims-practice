package ue_test

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
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

// Encoding names are case-insensitive (RFC 4855 3), so a peer that
// writes a=rtpmap:0 pcmu/8000 offers the same format as PCMU/8000: the
// media takes the stream and carries the audio the same way.
func TestMediaTakesEncodingNamesInAnyCase(t *testing.T) {
	for _, base := range []sdp.Codec{pcmu, amrWB, sdp.VoLTECodecs()[2]} {
		for _, name := range []string{strings.ToLower(base.Name), strings.ToUpper(base.Name), base.Name} {
			codec := base
			codec.Name = name
			a, b := between(t, codec, nil)
			speech := ue.ToneSpeech(200 * time.Millisecond)
			if err := a.Start(speech); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			played(t, a, b)
			got, _ := b.Received()
			if sent := a.Sent(); len(sent) == 0 && len(a.SentFrames()) == 0 || !bytes.Equal(got, sent) ||
				!slices.EqualFunc(b.ReceivedFrames(), a.SentFrames(), sameFrame) {
				t.Errorf("%s: sent %d octets and %d frames, received %d octets and %d frames", name, len(sent), len(a.SentFrames()), len(got), len(b.ReceivedFrames()))
			}
			if err := a.SetStream(ue.Stream{Remote: loopback(b.Port()), Codec: base}); err != nil {
				t.Errorf("%s: a stream of %s after it: %v", name, base.Name, err)
			}
		}
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

// TestMediaPacksTheFramesInTheFormatOfTheStreamAtTheTick: a new offer and
// answer that changes octet-align of the same AMR-WB codec, with its own
// payload type, applies to the frames that have not gone out yet. The
// peer files each packet by its payload type, and what it gets back is
// the recording, frame for frame.
func TestMediaPacksTheFramesInTheFormatOfTheStreamAtTheTick(t *testing.T) {
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	remote := loopback(uint16(peer.LocalAddr().(*net.UDPAddr).Port))
	bandwidthEfficient, octetAligned := sdp.VoLTECodecs()[0], sdp.VoLTECodecs()[1]
	formats := map[uint8]sdp.Codec{uint8(bandwidthEfficient.PayloadType): bandwidthEfficient, uint8(octetAligned.PayloadType): octetAligned}

	a := listenMedia(t)
	a.SetFlow(ue.Flow{Send: true})
	if err := a.SetStream(ue.Stream{Remote: remote, Codec: bandwidthEfficient}); err != nil {
		t.Fatal(err)
	}
	speech := ue.ToneSpeech(time.Second)
	if err := a.Start(speech); err != nil {
		t.Fatal(err)
	}

	var got []amr.Frame
	var seen []uint8 // the payload type of each packet
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1500)
	for len(got) < len(speech.AMRWB) {
		peer.SetReadDeadline(deadline)
		n, err := peer.Read(buf)
		if err != nil {
			t.Fatalf("after %d of %d frames: %v", len(got), len(speech.AMRWB), err)
		}
		pkt, err := rtp.Unmarshal(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		codec, ok := formats[pkt.PayloadType]
		if !ok {
			t.Fatalf("a packet of payload type %d, which no stream had", pkt.PayloadType)
		}
		format, err := codec.AMRFormat()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := format.Unmarshal(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d of payload type %d: %v", len(seen), pkt.PayloadType, err)
		}
		got = append(got, payload.Frames...)
		seen = append(seen, pkt.PayloadType)
		if len(seen) == 10 {
			if err := a.SetStream(ue.Stream{Remote: remote, Codec: octetAligned}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !slices.EqualFunc(got, speech.AMRWB, sameFrame) {
		t.Errorf("the frames rebuilt from the packets are not the recording of %d frames", len(speech.AMRWB))
	}
	if seen[0] != 96 || seen[len(seen)-1] != 97 {
		t.Errorf("payload types %v, want 96 before the change and 97 after", seen)
	}
}

// TestMediaReadsWhatItReceivesInTheFormatOfItsOwnSDP: the peer answered
// octet-aligned, but the UE's own SDP offered bandwidth-efficient. The
// peer sends in the format of the UE's SDP (RFC 3264 5.1, 6.1), so the UE
// reads the packets in that format, whatever it sends in.
func TestMediaReadsWhatItReceivesInTheFormatOfItsOwnSDP(t *testing.T) {
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	own, theirs := sdp.VoLTECodecs()[0], sdp.VoLTECodecs()[1] // bandwidth-efficient 96, octet-aligned 97
	ownFormat, err := own.AMRFormat()
	if err != nil {
		t.Fatal(err)
	}

	a := listenMedia(t)
	a.SetFlow(ue.Flow{Receive: true})
	if err := a.SetStream(ue.Stream{Remote: loopback(uint16(peer.LocalAddr().(*net.UDPAddr).Port)),
		Codec: theirs, ReceiveCodec: own}); err != nil {
		t.Fatal(err)
	}
	speech := ue.ToneSpeech(time.Second)
	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(a.Port())}
	for i, f := range speech.AMRWB {
		payload, err := ownFormat.Marshal(amr.Payload{CMR: amr.NoModeRequest, Frames: []amr.Frame{f}})
		if err != nil {
			t.Fatal(err)
		}
		pkt := rtp.Packet{PayloadType: uint8(own.PayloadType), SequenceNumber: uint16(i), Timestamp: uint32(i * 320), SSRC: 1, Payload: payload}
		if _, err := peer.WriteToUDP(pkt.Marshal(), dst); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "every frame the peer sent to be read", func() bool { return len(a.ReceivedFrames()) == len(speech.AMRWB) })
	if !slices.EqualFunc(a.ReceivedFrames(), speech.AMRWB, sameFrame) {
		t.Errorf("the frames read are not the %d frames the peer sent", len(speech.AMRWB))
	}
	if got := a.Stats().FramesReceived; got != len(speech.AMRWB) {
		t.Errorf("FramesReceived = %d, want %d", got, len(speech.AMRWB))
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

// dtmfPeer is a socket that stands for the far end of a call, to read the
// RTP that a Media sends.
func dtmfPeer(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// readRTP reads one packet from c, or fails the test after the deadline.
func readRTP(t *testing.T, c *net.UDPConn, what string) rtp.Packet {
	t.Helper()
	buf := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("waiting for %s: %v", what, err)
	}
	p, err := rtp.Unmarshal(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// wantOnlyAudioFrom reads packets from c until it has read n of speech,
// and fails the test if one of them carries the payload type of the
// events.
func wantOnlyAudioFrom(t *testing.T, c *net.UDPConn, events sdp.Codec, n int) {
	t.Helper()
	for audio := 0; audio < n; {
		switch p := readRTP(t, c, "speech after telephone-event was dropped"); int(p.PayloadType) {
		case events.PayloadType:
			t.Fatalf("a telephone-event packet %+v went out after telephone-event left the stream", p)
		case sdp.PCMU:
			audio++
		}
	}
}

// TestMediaDropsPendingDTMFWhenTelephoneEventGoesAway is a new offer and
// answer that leave telephone-event out before the sender has begun the
// digits: the digits are dropped, no event goes out, and the speech
// carries on.
func TestMediaDropsPendingDTMFWhenTelephoneEventGoesAway(t *testing.T) {
	events := sdp.Codec{PayloadType: 101, Name: sdp.NameTelephoneEvent, ClockRate: 8000}
	a, peer := listenMedia(t), dtmfPeer(t)
	remote := peer.LocalAddr().(*net.UDPAddr).AddrPort()
	if err := a.SetStream(ue.Stream{Remote: remote, Codec: pcmu, Events: &events}); err != nil {
		t.Fatal(err)
	}
	a.SetFlow(ue.Flow{})
	if err := a.Start(ue.Speech{PCM: ue.Tone(440, 2*time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendDTMF("12"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetStream(ue.Stream{Remote: remote, Codec: pcmu}); err != nil {
		t.Fatal(err)
	}
	a.SetFlow(ue.Flow{Send: true, Receive: true})
	wantOnlyAudioFrom(t, peer, events, 10)
	if err := a.SendDTMF("3"); err != ue.ErrNoTelephoneEvent {
		t.Errorf("DTMF after telephone-event went away: %v, want ErrNoTelephoneEvent", err)
	}
	if sent, _ := a.DTMF(); sent != "" {
		t.Errorf("sent %q as DTMF, want none", sent)
	}
}

// TestMediaEndsEventWhenTelephoneEventGoesAway is the same new offer and
// answer in the middle of an event: the event stops at once, with no more
// packets in the format that is no longer agreed on.
func TestMediaEndsEventWhenTelephoneEventGoesAway(t *testing.T) {
	events := sdp.Codec{PayloadType: 101, Name: sdp.NameTelephoneEvent, ClockRate: 8000}
	a, peer := listenMedia(t), dtmfPeer(t)
	remote := peer.LocalAddr().(*net.UDPAddr).AddrPort()
	if err := a.SetStream(ue.Stream{Remote: remote, Codec: pcmu, Events: &events}); err != nil {
		t.Fatal(err)
	}
	a.SetFlow(ue.Flow{Send: true, Receive: true})
	if err := a.Start(ue.Speech{PCM: ue.Tone(440, 2*time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendDTMF("1"); err != nil {
		t.Fatal(err)
	}
	for p := readRTP(t, peer, "the first report of the event"); int(p.PayloadType) != events.PayloadType; {
		p = readRTP(t, peer, "the first report of the event")
	}
	// Hold the sender still so that the event stays half sent however slow
	// this test runs: the event takes seven packets.
	a.SetFlow(ue.Flow{Receive: true})
	if !ue.EventInProgress(a) {
		t.Fatal("the event was over before the test could stop it")
	}
	for {
		peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err := peer.Read(make([]byte, 1500)); err != nil {
			break // nothing more is in flight
		}
	}
	if err := a.SetStream(ue.Stream{Remote: remote, Codec: pcmu}); err != nil {
		t.Fatal(err)
	}
	if ue.EventInProgress(a) {
		t.Error("the event goes on after telephone-event left the stream")
	}
	a.SetFlow(ue.Flow{Send: true, Receive: true})
	wantOnlyAudioFrom(t, peer, events, 10)
}

// TestMediaSendsOnlyEventsThePeerListed is RFC 4733 2.5.1.1: the sender
// restricts itself to the events of the receiver's fmtp. A digit the peer
// did not list is refused before anything is queued, so the digits before
// it do not go out either.
func TestMediaSendsOnlyEventsThePeerListed(t *testing.T) {
	events := sdp.Codec{PayloadType: 101, Name: sdp.NameTelephoneEvent, ClockRate: 8000, Fmtp: "0-9"}
	a, b := between(t, pcmu, &events)
	digits := make(chan rune, 8)
	b.OnDigit(func(d rune) { digits <- d })
	if err := a.Start(ue.ToneSpeech(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, refused := range []string{"#", "1#", "*"} {
		if err := a.SendDTMF(refused); !errors.Is(err, ue.ErrEventNotAgreed) {
			t.Errorf("SendDTMF(%q) with events 0-9: %v, want ErrEventNotAgreed", refused, err)
		}
	}
	if err := a.SendDTMF("09"); err != nil {
		t.Fatal(err)
	}
	var got []rune
	for range 2 {
		select {
		case d := <-digits:
			got = append(got, d)
		case <-time.After(5 * time.Second):
			t.Fatalf("got %q, want 09", string(got))
		}
	}
	// Nothing else follows: the refused digits were never queued.
	select {
	case d := <-digits:
		t.Errorf("the digit %q went out as well", d)
	case <-time.After(300 * time.Millisecond):
	}
	if sent, _ := a.DTMF(); string(got) != "09" || sent != "09" {
		t.Errorf("peer heard %q; a sent %q; want 09", string(got), sent)
	}
}

// TestMediaChecksDTMFAgainstTheEventList covers a list of ranges and
// single numbers, and the list left out, which means 0-15 (RFC 4733
// 7.1.1). "#" is event 11 and "D" is event 15.
func TestMediaChecksDTMFAgainstTheEventList(t *testing.T) {
	for _, tc := range []struct {
		fmtp         string
		allowed, not string
	}{
		{"0-5,11", "05#", "6*A"},
		{"11,0-5", "#5", "6"},
		{"15", "D", "0#"},
		{"", "0123456789*#ABCD", ""},
	} {
		events := sdp.Codec{PayloadType: 101, Name: sdp.NameTelephoneEvent, ClockRate: 8000, Fmtp: tc.fmtp}
		a := listenMedia(t)
		if err := a.SetStream(ue.Stream{Remote: loopback(a.Port()), Codec: pcmu, Events: &events}); err != nil {
			t.Fatal(err)
		}
		for _, d := range tc.allowed {
			if err := a.SendDTMF(string(d)); err != nil {
				t.Errorf("fmtp %q: SendDTMF(%q): %v", tc.fmtp, d, err)
			}
		}
		for _, d := range tc.not {
			if err := a.SendDTMF(string(d)); !errors.Is(err, ue.ErrEventNotAgreed) {
				t.Errorf("fmtp %q: SendDTMF(%q): %v, want ErrEventNotAgreed", tc.fmtp, d, err)
			}
		}
	}
}
