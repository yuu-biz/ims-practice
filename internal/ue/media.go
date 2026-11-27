package ue

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	mathrand "math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/amr"
	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/rtcp"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/wav"
)

// packetTime is the audio in each RTP packet: one 20 ms frame (sdp.Ptime,
// IR.92 3.2.5).
const packetTime = sdp.Ptime * time.Millisecond

// Stream is the audio stream of a call as the offer and the answer
// settled it (RFC 3264).
type Stream struct {
	// Remote is where RTP goes. RTCP goes to the port above it (RFC 3550
	// 11).
	Remote netip.AddrPort
	// Codec is the speech payload format as the peer's SDP numbers it,
	// and Events the telephone-event of the same clock rate when both
	// sides took one (TS 26.114 G.3). The media sends with these numbers.
	Codec  sdp.Codec
	Events *sdp.Codec
	// ReceiveCodec and ReceiveEvents are the same formats as this side's
	// own SDP numbers them. The media accepts only these numbers: each
	// side's SDP lists the payload types it expects to receive (RFC 3264
	// 5.1, 6.1). Left zero, they are Codec and Events, for a peer that
	// numbers the formats the same way.
	ReceiveCodec  sdp.Codec
	ReceiveEvents *sdp.Codec
	// RS and RR are the RTCP bandwidth in bit/s that the peer's SDP
	// gives (RFC 3556 2, IR.92 3.2.4).
	RS, RR int
}

// Flow is which way RTP may go now. The call works it out from the
// direction of the stream, hold, the resources of the access network
// and the early media authorization.
type Flow struct {
	Send, Receive bool
}

// Media is the RTP and RTCP side of a call: RTP on an even port and RTCP
// on the odd one above it (RFC 3550 11), each one socket that both sends
// and receives, as symmetric RTP and RTCP ask (RFC 4961 3, IR.92 3.2.3
// and 3.2.4). A media relay that latches onto the address it receives
// from can then send back to the same place, even through NAT.
//
// Until the call gives it a Stream, Media drops what arrives. Start
// starts the clock of the RTP it sends, and RTCP with it.
type Media struct {
	rtpConn, rtcpConn *net.UDPConn
	// cname names this side in RTCP for the whole call. It is random, so
	// that it is unique as RFC 3550 6.5.1 asks and tells nothing of the
	// user.
	cname string
	ssrc  uint32

	mu     sync.Mutex
	stream *Stream
	flow   Flow
	format amr.Format // of an AMR or AMR-WB stream

	// What goes out: the speech, one packet per 20 ms, and the next of it.
	// PCMU is kept as payloads. AMR and AMR-WB are kept as frames, and each
	// is packed at its tick in the format and payload type of the stream
	// then, so that a new offer and answer that changes octet-align applies
	// to the frames that have not gone out yet.
	started bool
	stopped chan struct{}
	// senders counts the goroutines that send, so that Stop waits for
	// them.
	senders  sync.WaitGroup
	speech   [][]byte
	frames   []amr.Frame
	next     int
	played   chan struct{} // closed when the speech has all gone out
	seq      uint16
	ts       uint32
	tsAt     time.Time // when the clock was at ts
	talking  bool      // the last tick sent speech, so no marker
	dtmf     []rune    // digits waiting to go out
	reports  []rtp.DTMFReport
	eventTS  uint32
	pause    int // ticks of silence left after a digit (TS 26.114 G.2)
	sentDTMF []rune

	// What was sent and received, for the e2e test and for listening
	// afterwards.
	pcmuSent, pcmuReceived []byte
	sentFrames             []amr.Frame
	firstSentTS            uint32
	recorder               *amr.Recorder
	stats                  MediaStats
	receivedDTMF           []rune
	dtmfReceiver           rtp.DTMFReceiver
	onDigit                func(rune)

	// Where what arrives also goes while the call runs
	// (StreamReceivedTo), how much of the recording has gone there, and
	// the first error of writing to it.
	live       io.Writer
	liveFrames *amr.StreamWriter
	liveFiled  int // frames of the recording written
	liveBytes  int // PCMU payload bytes written
	liveErr    error

	// RTCP (RFC 3550 6).
	receiver       *rtcp.Receiver
	lastRTPSent    time.Time
	lastRTPArrival time.Time
	avgRTCPSize    float64
	octetsSent     uint32
}

// MediaStats counts what went through the media of a call.
type MediaStats struct {
	PacketsSent     int `json:"packets_sent"`
	PacketsReceived int `json:"packets_received"`
	// FramesSent and FramesReceived count AMR or AMR-WB frames.
	FramesSent     int `json:"frames_sent,omitempty"`
	FramesReceived int `json:"frames_received,omitempty"`
	RTCPSent       int `json:"rtcp_sent"`
	RTCPReceived   int `json:"rtcp_received"`
}

// ListenMedia opens the RTP and RTCP sockets on addr, such as
// "0.0.0.0:0", where port 0 picks a free pair of ports and any other
// port must be even.
func ListenMedia(addr string) (*Media, error) {
	a, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	rtpConn, rtcpConn, err := listenPair(a)
	if err != nil {
		return nil, err
	}
	var ssrc [4]byte
	rand.Read(ssrc[:])
	m := &Media{rtpConn: rtpConn, rtcpConn: rtcpConn, cname: rand.Text(), stopped: make(chan struct{}),
		ssrc: uint32(ssrc[0])<<24 | uint32(ssrc[1])<<16 | uint32(ssrc[2])<<8 | uint32(ssrc[3]),
		// RFC 3550 5.1: random initial sequence number and timestamp.
		seq: uint16(mathrand.Uint32()), ts: mathrand.Uint32(),
		played: make(chan struct{}),
	}
	go m.receiveRTP()
	go m.receiveRTCP()
	return m, nil
}

// listenPair binds an even port for RTP and the one above it for RTCP.
func listenPair(a *net.UDPAddr) (rtpConn, rtcpConn *net.UDPConn, err error) {
	if a.Port%2 != 0 {
		return nil, nil, fmt.Errorf("RTP port %d is odd; RTCP takes the port above an even one (RFC 3550 11)", a.Port)
	}
	for attempt := 0; attempt < 100; attempt++ {
		rtpConn, err = net.ListenUDP("udp4", a)
		if err != nil {
			return nil, nil, err
		}
		port := rtpConn.LocalAddr().(*net.UDPAddr).Port
		if port%2 == 0 {
			rtcpConn, err = net.ListenUDP("udp4", &net.UDPAddr{IP: a.IP, Port: port + 1})
			if err == nil {
				return rtpConn, rtcpConn, nil
			}
		}
		rtpConn.Close()
		if a.Port != 0 {
			return nil, nil, fmt.Errorf("RTCP port %d: %w", port+1, err)
		}
	}
	return nil, nil, errors.New("no free pair of RTP and RTCP ports")
}

// Port is the local RTP port, for the SDP.
func (m *Media) Port() uint16 { return m.rtpConn.LocalAddr().(*net.UDPAddr).AddrPort().Port() }

// Close stops the media, with an RTCP BYE when RTCP runs (RFC 3550
// 6.6), and closes the sockets.
func (m *Media) Close() error {
	m.Stop()
	return errors.Join(m.rtpConn.Close(), m.rtcpConn.Close())
}

// SetStream gives the media the stream that the offer and answer
// settled, again after each new offer and answer.
func (m *Media) SetStream(s Stream) error {
	var format amr.Format
	if isAMR(s.Codec) {
		f, err := s.Codec.AMRFormat()
		if err != nil {
			return err
		}
		format = f
	} else if !s.Codec.Is(sdp.NamePCMU) {
		return fmt.Errorf("codec %s is none the UE plays", s.Codec.Name)
	}
	if s.ReceiveCodec.Name == "" {
		s.ReceiveCodec, s.ReceiveEvents = s.Codec, s.Events
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream != nil && !m.stream.Codec.Is(s.Codec.Name) && m.started {
		return fmt.Errorf("the codec of the call cannot change from %s to %s", m.stream.Codec.Name, s.Codec.Name)
	}
	if s.Events == nil && (len(m.dtmf) > 0 || len(m.reports) > 0) {
		// A telephone event goes out only in a format that the offer and
		// answer settled (RFC 4733). With none left, the digits that wait
		// are dropped and the event on its way ends here, without the
		// reports that remain: the format of the packets already sent is no
		// longer the call's, so the end of the event cannot go in it.
		slog.Warn("telephone-event left the stream, so the DTMF that was not sent is dropped",
			"waiting_digits", string(m.dtmf), "event_in_progress", len(m.reports) > 0)
		m.dtmf, m.reports = nil, nil
	}
	m.stream, m.format = &s, format
	if isAMR(s.Codec) && m.recorder == nil {
		m.recorder = amr.NewRecorder(format.Codec)
	}
	return nil
}

func isAMR(c sdp.Codec) bool { return c.Is(sdp.NameAMR) || c.Is(sdp.NameAMRWB) }

// SetFlow says which way RTP may go now.
func (m *Media) SetFlow(f Flow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flow = f
}

// OnDigit has f hear each DTMF digit that arrives, once each.
func (m *Media) OnDigit(f func(rune)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDigit = f
}

// StreamReceivedTo also writes what the call receives to w while the
// call runs, so that a player reading w hears the call as it happens:
// the AMR or AMR-WB frames in the storage format of RFC 4867 5, or the
// PCMU payloads as they are. w gets one write per packet.
//
// The write happens on the goroutine that reads RTP, so a w that
// blocks keeps the call from reading its packets, and the socket drops
// what arrives meanwhile: a listener that can be slower than the call
// needs a queue of its own, as the player of "ue -play" has.
//
// The call goes on when a write fails, which is what a listener that
// has gone looks like; StreamError tells what happened.
func (m *Media) StreamReceivedTo(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.live = w
}

// StreamError is the first error of writing to the stream of
// StreamReceivedTo, which stopped the stream.
func (m *Media) StreamError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.liveErr
}

// streamReceived writes what has arrived since the last packet to the
// stream of StreamReceivedTo. The write is made without the lock, so
// that a listener holds up nothing that needs it: the sending of RTP,
// RTCP, and the call's own view of what it has received. It still holds
// up the reading of the next packet, which is the listener's to avoid
// (StreamReceivedTo).
func (m *Media) streamReceived() {
	m.mu.Lock()
	var (
		frames []amr.Frame
		pcmu   []byte
	)
	switch {
	case m.live == nil || m.liveErr != nil:
		m.mu.Unlock()
		return
	case m.recorder != nil:
		if m.liveFrames == nil {
			m.liveFrames = amr.NewStreamWriter(m.live, m.format.Codec)
		}
		filed := m.recorder.Frames()
		frames = append(frames, filed[m.liveFiled:]...)
		m.liveFiled = len(filed)
	default:
		pcmu = append(pcmu, m.pcmuReceived[m.liveBytes:]...)
		m.liveBytes = len(m.pcmuReceived)
	}
	w, stream := m.live, m.liveFrames
	m.mu.Unlock()

	var err error
	switch {
	case len(frames) > 0:
		err = stream.Write(frames)
	case len(pcmu) > 0:
		_, err = w.Write(pcmu)
	}
	if err != nil {
		m.mu.Lock()
		m.liveErr = err
		m.mu.Unlock()
	}
}

// Start starts the clock of the RTP this side sends, which plays speech
// in the codec of the stream, and starts RTCP (IR.92 3.2.4). It does
// nothing the second time.
//
// SIMPLIFIED: IR.92 3.2.1 and RFC 4867 8.3.1. The UE sends the frames
// of its recording in the modes they were encoded with: it neither
// follows the CMR of the frames it receives nor keeps to a mode-set of
// the peer's SDP, since it cannot encode. The
// UE simulator offers no mode-set and sends no CMR, so between two of
// them every mode is allowed.
func (m *Media) Start(speech Speech) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}
	if m.stream == nil {
		return errors.New("no stream to send")
	}
	switch {
	case m.stream.Codec.Is(sdp.NamePCMU):
		n := wav.SampleRate * sdp.Ptime / 1000
		for i := 0; i+n <= len(speech.PCM); i += n {
			m.speech = append(m.speech, g711.Encode(speech.PCM[i:i+n]))
		}
	default:
		frames := speech.AMR
		if m.format.Codec == amr.AMRWB {
			frames = speech.AMRWB
		}
		for _, f := range frames {
			// A frame that cannot be packed is refused now, not at its tick.
			if _, err := m.packFrame(f); err != nil {
				return err
			}
		}
		m.frames = frames
	}
	m.started, m.tsAt = true, time.Now()
	if m.speechLen() == 0 {
		close(m.played)
	}
	m.senders.Add(2)
	go m.sendRTP()
	go m.sendRTCP()
	return nil
}

// packFrame is the payload of one frame in the format of the stream, or
// nil for NO_DATA, which sends nothing. m.mu is held.
func (m *Media) packFrame(f amr.Frame) ([]byte, error) {
	if f.Type == amr.NoData {
		return nil, nil
	}
	return m.format.Marshal(amr.Payload{CMR: amr.NoModeRequest, Frames: []amr.Frame{f}})
}

// speechLen is how many packet times the speech takes. m.mu is held.
func (m *Media) speechLen() int { return len(m.speech) + len(m.frames) }

// Played is closed once the speech has all gone out.
func (m *Media) Played() <-chan struct{} { return m.played }

// Stop stops sending, with an RTCP BYE if RTCP runs (RFC 3550 6.6).
func (m *Media) Stop() {
	m.mu.Lock()
	select {
	case <-m.stopped:
		m.mu.Unlock()
		return
	default:
	}
	close(m.stopped)
	if m.started && !m.rtcpDisabled() {
		m.writeRTCP(true)
	}
	m.mu.Unlock()
	m.senders.Wait()
}

// ErrNoTelephoneEvent means the call has no telephone-event to send DTMF
// with: IR.92 3.3 forbids it then.
var ErrNoTelephoneEvent = errors.New("the call has not negotiated telephone-event, so it cannot send DTMF")

// SendDTMF queues digits to go out as telephone events (RFC 4733, TS
// 26.114 Annex G). The speech pauses while they go.
func (m *Media) SendDTMF(digits string) error {
	for _, d := range digits {
		if _, ok := rtp.DTMFEvent(d); !ok {
			return fmt.Errorf("%q is no DTMF digit", d)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream == nil || m.stream.Events == nil {
		return ErrNoTelephoneEvent
	}
	m.dtmf = append(m.dtmf, []rune(digits)...)
	return nil
}

// sendRTP sends a packet every 20 ms. The RTP clock runs on whether a
// packet goes out or not, so that a pause for hold or for DTMF keeps
// the timing (RFC 3550 5.1).
func (m *Media) sendRTP() {
	defer m.senders.Done()
	ticker := time.NewTicker(packetTime)
	defer ticker.Stop()
	for {
		if p, ok := m.tick(); ok {
			b := p.Marshal()
			if _, err := m.rtpConn.WriteToUDPAddrPort(b, m.remote()); err == nil {
				m.mu.Lock()
				m.stats.PacketsSent++
				m.octetsSent += uint32(len(p.Payload))
				m.lastRTPSent = time.Now()
				m.mu.Unlock()
			}
		}
		select {
		case <-ticker.C:
		case <-m.stopped:
			return
		}
	}
}

func (m *Media) remote() netip.AddrPort {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stream.Remote
}

// tick is one 20 ms of the sender: the packet to send, if any.
func (m *Media) tick() (rtp.Packet, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.stream
	ts := m.ts
	samples := uint32(s.Codec.ClockRate) * sdp.Ptime / 1000
	m.ts += samples
	m.tsAt = time.Now()
	p := rtp.Packet{SSRC: m.ssrc, Timestamp: ts}
	switch {
	case !m.flow.Send:
		m.talking = false
		return p, false
	case len(m.reports) > 0:
		r := m.reports[0]
		m.reports = m.reports[1:]
		p.Marker, p.PayloadType, p.Timestamp, p.Payload = r.Marker, uint8(s.Events.PayloadType), m.eventTS, r.Payload.Marshal()
		if len(m.reports) == 0 {
			m.pause = int(rtp.DTMFMinPause / packetTime)
		}
	case m.pause > 0:
		m.pause--
		return p, false
	case len(m.dtmf) > 0:
		digit := m.dtmf[0]
		m.dtmf = m.dtmf[1:]
		reports, err := rtp.DTMF(digit, s.Events.ClockRate, rtp.DTMFDuration)
		if err != nil {
			return p, false
		}
		m.sentDTMF = append(m.sentDTMF, digit)
		m.eventTS, m.reports = ts, reports[1:]
		m.talking = false
		r := reports[0]
		p.Marker, p.PayloadType, p.Payload = r.Marker, uint8(s.Events.PayloadType), r.Payload.Marshal()
	case m.next < m.speechLen():
		var payload []byte
		if isAMR(s.Codec) {
			// Cannot fail: Start packed every frame in the format of the
			// same codec, and octet-align does not change what a frame needs.
			payload, _ = m.packFrame(m.frames[m.next])
		} else {
			payload = m.speech[m.next]
		}
		m.next++
		if m.next == m.speechLen() {
			close(m.played)
		}
		if payload == nil {
			// A NO_DATA frame: nothing goes out, and the next speech frame
			// starts a talkspurt.
			m.talking = false
			return p, false
		}
		// RFC 3551 4.1: the marker bit starts a talkspurt.
		p.Marker, p.PayloadType, p.Payload = !m.talking, uint8(s.Codec.PayloadType), payload
		m.talking = true
		m.recordSent(ts, payload)
	default:
		m.talking = false
		return p, false
	}
	p.SequenceNumber = m.seq
	m.seq++
	return p, true
}

// recordSent keeps what went out, in the form the receiving side keeps
// it, so that the two can be compared: G.711 payloads one after the
// other, and AMR frames by their place in time, with NO_DATA where none
// went out (RFC 4867 5.3). m.mu is held.
func (m *Media) recordSent(ts uint32, payload []byte) {
	if !isAMR(m.stream.Codec) {
		m.pcmuSent = append(m.pcmuSent, payload...)
		return
	}
	p, err := m.format.Unmarshal(payload)
	if err != nil {
		return
	}
	if len(m.sentFrames) == 0 {
		m.firstSentTS = ts
	}
	slot := int((ts - m.firstSentTS) / m.format.Codec.SamplesPerFrame())
	for len(m.sentFrames) < slot {
		m.sentFrames = append(m.sentFrames, amr.Frame{Type: amr.NoData, Quality: true})
	}
	m.sentFrames = append(m.sentFrames, p.Frames...)
	m.stats.FramesSent += len(p.Frames)
}

func (m *Media) receiveRTP() {
	buf := make([]byte, 1500)
	for {
		n, _, err := m.rtpConn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		p, err := rtp.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		digit, isDigit := m.receivedPacket(p, time.Now())
		m.streamReceived()
		if isDigit {
			m.mu.Lock()
			onDigit := m.onDigit
			m.mu.Unlock()
			if onDigit != nil {
				onDigit(digit)
			}
		}
	}
}

// receivedPacket files an RTP packet, and returns the DTMF digit it
// completes, if any.
func (m *Media) receivedPacket(p rtp.Packet, arrival time.Time) (rune, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.stream
	if s == nil || !m.flow.Receive {
		return 0, false
	}
	if m.receiver == nil || m.receiver.SSRC != p.SSRC {
		// SIMPLIFIED: RFC 3550 8.2. A new SSRC starts the statistics
		// afresh instead of being checked for a collision: the peer is
		// the only source of the call.
		m.receiver = &rtcp.Receiver{SSRC: p.SSRC, ClockRate: s.Codec.ClockRate}
	}
	m.receiver.Packet(p.SequenceNumber, p.Timestamp, arrival)
	m.lastRTPArrival = arrival
	switch {
	case int(p.PayloadType) == s.ReceiveCodec.PayloadType:
		m.stats.PacketsReceived++
		if !isAMR(s.ReceiveCodec) {
			m.pcmuReceived = append(m.pcmuReceived, p.Payload...)
			return 0, false
		}
		payload, err := m.format.Unmarshal(p.Payload)
		if err != nil {
			return 0, false
		}
		m.recorder.Add(p.Timestamp, payload)
		m.stats.FramesReceived += len(payload.Frames)
	case s.ReceiveEvents != nil && int(p.PayloadType) == s.ReceiveEvents.PayloadType:
		e, err := rtp.UnmarshalTelephoneEvent(p.Payload)
		if err != nil {
			return 0, false
		}
		if digit, ok := m.dtmfReceiver.Report(p.Timestamp, e); ok {
			m.receivedDTMF = append(m.receivedDTMF, digit)
			return digit, true
		}
	}
	return 0, false
}

// Received returns the PCMU payloads received so far and the number of
// speech packets they came in.
func (m *Media) Received() ([]byte, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.pcmuReceived...), m.stats.PacketsReceived
}

// Sent returns the PCMU payloads sent so far.
func (m *Media) Sent() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.pcmuSent...)
}

// ReceivedFrames returns the AMR or AMR-WB frames received so far, each
// in its place in time (amr.Recorder).
func (m *Media) ReceivedFrames() []amr.Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recorder == nil {
		return nil
	}
	return append([]amr.Frame(nil), m.recorder.Frames()...)
}

// SentFrames returns the AMR or AMR-WB frames sent so far, in the same
// form as ReceivedFrames.
func (m *Media) SentFrames() []amr.Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]amr.Frame(nil), m.sentFrames...)
}

// Stats counts what went through the media so far.
func (m *Media) Stats() MediaStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// DTMF returns the digits sent and received so far.
func (m *Media) DTMF() (sent, received string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.sentDTMF), string(m.receivedDTMF)
}

// Stream returns the stream of the call, and false before it has one.
func (m *Media) Stream() (Stream, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream == nil {
		return Stream{}, false
	}
	return *m.stream, true
}

// rtcpDisabled is IR.92 3.2.4: b=RS and b=RR of 0 turn RTCP off. m.mu
// is held.
func (m *Media) rtcpDisabled() bool { return m.stream.RS+m.stream.RR == 0 }

// sendRTCP sends a compound packet at the interval of RFC 3550 6.3.1.
//
// The interval is capped at 5 s. IR.92 3.2.4 asks that the far end hear
// from the UE at least every 5 s, so that it can tell a dead call
// quickly. With b=RS and b=RR as the UE writes them, RFC 3550 gives the
// 5 s minimum, spread between 0.5 and 1.5 times it and divided by
// e-3/2: between 2.05 and 6.16 s, so that some intervals would break
// that rule. The cap keeps the spread below 5 s and costs a few more
// packets than RFC 3550 alone.
func (m *Media) sendRTCP() {
	defer m.senders.Done()
	initial := true
	for {
		m.mu.Lock()
		disabled := m.rtcpDisabled()
		params := rtcp.IntervalParams{
			Members: 2, Senders: m.activeSenders(time.Now()), WeSent: m.weSent(time.Now()),
			RS: m.stream.RS, RR: m.stream.RR, AvgSize: m.avgRTCPSize, Initial: initial,
		}
		if params.AvgSize == 0 {
			// RFC 3550 6.3.2: the probable size of the first packet, with
			// the UDP and IP headers: an SR with one report block and an
			// SDES with the CNAME.
			params.AvgSize = 28 + 52 + 12 + float64(len(m.cname))
		}
		m.mu.Unlock()
		wait := time.Second
		if !disabled {
			wait = rtcpInterval(params)
		}
		select {
		case <-time.After(wait):
		case <-m.stopped:
			return
		}
		if disabled {
			continue
		}
		m.mu.Lock()
		select {
		case <-m.stopped:
			m.mu.Unlock()
			return
		default:
		}
		m.writeRTCP(false)
		m.mu.Unlock()
		initial = false
	}
}

// rtcpInterval is the wait before the next compound packet. A test
// shortens it.
var rtcpInterval = func(p rtcp.IntervalParams) time.Duration {
	return min(p.Interval(mathrand.Float64()), rtcp.MinInterval)
}

// activeSenders counts the participants that sent RTP in the last two
// intervals (RFC 3550 6.3.3), taking an interval to be the 5 s minimum.
// m.mu is held.
func (m *Media) activeSenders(now time.Time) int {
	n := 0
	if m.weSent(now) {
		n++
	}
	if now.Sub(m.lastRTPArrival) < 2*rtcp.MinInterval {
		n++
	}
	return n
}

func (m *Media) weSent(now time.Time) bool { return now.Sub(m.lastRTPSent) < 2*rtcp.MinInterval }

// writeRTCP sends one compound packet: an SR when this side has sent
// RTP lately, an RR otherwise, then the SDES with the CNAME (IR.92
// 3.2.4), and a BYE when the call ends (RFC 3550 6.6). m.mu is held.
func (m *Media) writeRTCP(bye bool) {
	now := time.Now()
	var blocks []rtcp.ReportBlock
	if m.receiver != nil {
		if b, ok := m.receiver.Block(now); ok {
			blocks = append(blocks, b)
		}
	}
	var report rtcp.Packet = rtcp.ReceiverReport{SSRC: m.ssrc, Reports: blocks}
	if m.weSent(now) {
		elapsed := now.Sub(m.tsAt)
		report = rtcp.SenderReport{
			SSRC: m.ssrc, NTPTime: rtcp.NTPTime(now),
			RTPTime:     m.ts + uint32(elapsed.Seconds()*float64(m.stream.Codec.ClockRate)),
			PacketCount: uint32(m.stats.PacketsSent), OctetCount: m.octetsSent, Reports: blocks,
		}
	}
	packets := []rtcp.Packet{report, rtcp.SourceDescription{Chunks: []rtcp.Chunk{{SSRC: m.ssrc, CNAME: m.cname}}}}
	if bye {
		packets = append(packets, rtcp.Goodbye{SSRCs: []uint32{m.ssrc}})
	}
	b, err := rtcp.Marshal(packets...)
	if err != nil {
		return
	}
	remote := netip.AddrPortFrom(m.stream.Remote.Addr(), m.stream.Remote.Port()+1)
	if _, err := m.rtcpConn.WriteToUDPAddrPort(b, remote); err != nil {
		return
	}
	m.stats.RTCPSent++
	m.avgRTCPSize = rtcp.UpdateAvgSize(m.avgRTCPSize, len(b)+28)
}

func (m *Media) receiveRTCP() {
	buf := make([]byte, 1500)
	for {
		n, _, err := m.rtcpConn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		// IR.92 3.2.4: every packet type is accepted, and a compound
		// packet that fails the checks of RFC 3550 A.2 is dropped.
		packets, err := rtcp.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		m.mu.Lock()
		m.stats.RTCPReceived++
		m.avgRTCPSize = rtcp.UpdateAvgSize(m.avgRTCPSize, n+28)
		for _, p := range packets {
			if sr, ok := p.(rtcp.SenderReport); ok && m.receiver != nil && sr.SSRC == m.receiver.SSRC {
				m.receiver.SenderReport(sr, time.Now())
			}
		}
		m.mu.Unlock()
	}
}

// Tone is a sine wave of hz for d, at half of full scale, to play when
// no WAV file is given.
func Tone(hz float64, d time.Duration) []int16 {
	n := int(d.Seconds() * wav.SampleRate)
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(16000 * math.Sin(2*math.Pi*hz*float64(i)/wav.SampleRate))
	}
	return out
}

// CodecInfo describes the speech codec of a call, for logs and for the
// controller.
type CodecInfo struct {
	Name        string `json:"name"`
	PayloadType int    `json:"payload_type"`
	ClockRate   int    `json:"clock_rate"`
	// PayloadFormat is bandwidth-efficient or octet-aligned for AMR and
	// AMR-WB (RFC 4867 4.3, 4.4), "" for G.711.
	PayloadFormat string `json:"payload_format,omitempty"`
	// TelephoneEvent is the payload type of DTMF, 0 for none.
	TelephoneEvent int `json:"telephone_event,omitempty"`
}

// CodecInfo describes the speech codec of the stream.
func (s Stream) CodecInfo() CodecInfo { return codecInfo(s) }

func codecInfo(s Stream) CodecInfo {
	c := CodecInfo{Name: s.Codec.Name, PayloadType: s.Codec.PayloadType, ClockRate: s.Codec.ClockRate}
	if f, err := s.Codec.AMRFormat(); err == nil {
		c.PayloadFormat = "bandwidth-efficient"
		if f.OctetAligned {
			c.PayloadFormat = "octet-aligned"
		}
	}
	if s.Events != nil {
		c.TelephoneEvent = s.Events.PayloadType
	}
	return c
}

func (c CodecInfo) String() string {
	s := c.Name + "/" + strconv.Itoa(c.ClockRate) + " PT " + strconv.Itoa(c.PayloadType)
	if c.PayloadFormat != "" {
		s += " " + c.PayloadFormat
	}
	return s
}

// ErrNoMedia means an answered call ended without any RTP from the
// other side, which was to send some: one-way audio.
var ErrNoMedia = errors.New("no RTP received")
