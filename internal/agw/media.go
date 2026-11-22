package agw

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// relayContext is a context and its terminations. mu guards terms,
// closed and what each termination keeps under it, and covers this
// context only: packets of other contexts never wait for it.
type relayContext struct {
	id    string
	relay *Relay

	mu     sync.Mutex
	terms  map[TerminationID]*relayTermination
	closed bool
	// chosen counts the termination IDs that the relay chose itself.
	chosen int
	// ticking is true while the clock of the context runs.
	ticking bool
	// mix is the mixer of the context, with every termination as a
	// participant. It mixes while the context is a conference.
	mix *mixer.Conference
}

// relayTermination is one termination with its two sockets.
type relayTermination struct {
	id        TerminationID
	ctx       *relayContext
	rtp, rtcp *channel
	// Guarded by ctx.mu.
	mode    Mode
	signals *Signals
	events  *Events
	// playback is the announcement that plays, or nil.
	playback *mixer.Playback
	// own is the RTP source of what the node sends itself.
	own *source
	// heard holds the PCMU payloads that arrived since the last tick of
	// a conference, oldest first.
	heard [][]byte
}

// channel is one socket of a termination, with the address it sends to.
type channel struct {
	conn    *net.UDPConn
	local   netip.AddrPort
	mu      sync.Mutex
	remote  netip.AddrPort
	latched bool
	// received and sent count packets.
	received, sent atomic.Uint64
}

// The channels in logs.
const (
	rtpChannel  = "rtp"
	rtcpChannel = "rtcp"
)

// add opens a termination for req and starts reading its sockets.
// rc.mu is held.
func (rc *relayContext) add(req TerminationRequest) (*relayTermination, error) {
	id, err := rc.terminationID(req.ID)
	if err != nil {
		return nil, err
	}
	mode := req.Mode
	if mode == "" {
		mode = SendReceive
	}
	if !mode.valid() {
		return nil, fmt.Errorf("%w: mode %q", ErrBadRequest, mode)
	}
	if err := req.Events.check(); err != nil {
		return nil, err
	}
	var playback *mixer.Playback
	if req.Signals != nil {
		if playback, err = rc.relay.playback(req.Signals); err != nil {
			return nil, err
		}
	}
	rtp, rtcp, err := rc.relay.open()
	if err != nil {
		return nil, err
	}
	t := &relayTermination{id: id, ctx: rc, rtp: rtp, rtcp: rtcp, mode: mode, events: req.Events, own: newSource()}
	t.setSignals(req.Signals, playback)
	t.setRemote(req.Remote)
	rc.terms[id] = t
	rc.mix.Join(string(id))
	go t.read(rtpChannel, t.rtp)
	go t.read(rtcpChannel, t.rtcp)
	return t, nil
}

// terminationID checks the ID that a controller asks for, or chooses
// one. rc.mu is held.
func (rc *relayContext) terminationID(asked TerminationID) (TerminationID, error) {
	if asked == "" {
		for {
			rc.chosen++
			id := TerminationID(fmt.Sprintf("t%d", rc.chosen))
			if _, taken := rc.terms[id]; !taken {
				return id, nil
			}
		}
	}
	for _, c := range asked {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_') {
			return "", fmt.Errorf("%w: termination ID %q may hold letters, digits, - and _ only", ErrBadRequest, asked)
		}
	}
	if _, taken := rc.terms[asked]; taken {
		return "", fmt.Errorf("%w: the context has a termination %q already", ErrBadRequest, asked)
	}
	return asked, nil
}

// modify applies req, all of it or none. ctx.mu is held.
func (t *relayTermination) modify(req TerminationRequest) error {
	if req.Mode != "" && !req.Mode.valid() {
		return fmt.Errorf("%w: mode %q", ErrBadRequest, req.Mode)
	}
	if err := req.Events.check(); err != nil {
		return err
	}
	var playback *mixer.Playback
	if req.Signals != nil {
		var err error
		if playback, err = t.ctx.relay.playback(req.Signals); err != nil {
			return err
		}
	}
	if req.Mode != "" {
		t.mode = req.Mode
	}
	if req.Events != nil {
		t.events = req.Events
	}
	if req.Signals != nil {
		t.setSignals(req.Signals, playback)
	}
	t.setRemote(req.Remote)
	return nil
}

// setSignals replaces what the termination plays. An empty Signals
// descriptor stops it. ctx.mu is held.
func (t *relayTermination) setSignals(s *Signals, playback *mixer.Playback) {
	t.signals, t.playback = nil, playback
	if playback != nil {
		t.signals = s
		t.own.talkspurt = true
	}
}

// setRemote takes the RTP address of the SDP, and the RTCP address one
// port above it. Nothing changes for a zero address, or for a channel
// that has latched.
//
// SIMPLIFIED: RFC 3605 and RFC 5761. RTCP is taken to be on the port
// above RTP (RFC 3550 11, RFC 3264 6.1); a=rtcp and rtcp-mux are not
// read. No UE of this project sends either.
func (t *relayTermination) setRemote(remote netip.AddrPort) {
	if !remote.IsValid() {
		return
	}
	t.rtp.setRemote(remote)
	t.rtcp.setRemote(netip.AddrPortFrom(remote.Addr(), remote.Port()+1))
}

// read takes the packets that arrive on one socket of the termination.
// The first packet from a new source latches the remote address:
// a UE behind NAT sends from an address its SDP cannot know. RTP and
// RTCP latch each on their own, by the same rule.
//
// SIMPLIFIED: RFC 4961. A latched address is kept for the whole call.
// Re-latching only matters when a UE changes address in the middle of
// a call.
func (t *relayTermination) read(kind string, in *channel) {
	log := t.ctx.relay.log
	buf := make([]byte, 1500)
	for {
		n, src, err := in.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		if before, latched := in.latch(src); latched && before != src {
			log.Info("latched", "context", t.ctx.id, "termination", string(t.id), "channel", kind, "sdp_remote", before.String(), "source", src.String())
		}
		t.ctx.receive(t, kind, in, buf[:n])
	}
}

// conference tells that the context mixes: it has three or more
// terminations (TS 23.333 6.2.9.1). rc.mu is held.
func (rc *relayContext) conference() bool { return len(rc.terms) >= 3 }

// heardFrames is how many payloads a termination of a conference keeps
// for the next ticks: enough for packets that come in a burst after a
// short delay, and few enough that a party heard late is not heard much
// later than it spoke.
const heardFrames = 3

// receive takes a packet that arrived on channel in of termination
// from, and sends it on as it is where it goes: the same channel of the
// other termination of a context of two. RTP goes only from a termination that receives
// to one that sends (H.248.1 (03/2013) 7.1.7.1.1), and not to one that
// plays a signal; RTCP goes whatever the modes. In a conference the RTP
// payload waits for the mixer instead, and RTCP goes nowhere.
//
// The packet is counted, and sent on, while rc.mu is held. A
// controller that reads the counts and then changes the mode knows
// that the packets counted went by the mode it had set before, and the
// counts that a Subtract reports are the packets that went on the
// wire, as a capture shows them, with none still on its way.
//
// SIMPLIFIED: TS 23.333 5.10 and RFC 3550 6 and 7.1. The mixer takes
// PCMU only and drops other payloads, telephone-event among
// them. It keeps no more than heardFrames for jitter, writes no CSRC
// list of the parties it mixed, and sends no RTCP of its own. That is
// enough for a conference of a few UEs on one bridge.
func (rc *relayContext) receive(from *relayTermination, kind string, in *channel, packet []byte) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	in.received.Add(1)
	if out := rc.forwardTo(from, kind, packet); out != nil {
		out.send(packet)
	}
}

// forwardTo tells where a packet that arrived on termination from goes
// on as it is, if anywhere. rc.mu is held.
func (rc *relayContext) forwardTo(from *relayTermination, kind string, packet []byte) *channel {
	if rc.conference() {
		if kind == rtpChannel && from.mode.receives() {
			from.hear(packet)
		}
		return nil
	}
	if len(rc.terms) != 2 {
		return nil
	}
	var to *relayTermination
	for _, t := range rc.terms {
		if t != from {
			to = t
		}
	}
	if kind == rtcpChannel {
		return to.rtcp
	}
	if !from.mode.receives() || !to.mode.sends() || to.playback != nil {
		return nil
	}
	return to.rtp
}

// The media that the node makes itself goes out on a clock of 20 ms,
// one packet of PCMU each tick. A context has its clock only
// while it needs one.
const tick = mixer.Ptime * time.Millisecond

// hear keeps the payload of an RTP packet of PCMU for the mixer.
// ctx.mu is held.
func (t *relayTermination) hear(packet []byte) {
	p, err := rtp.Unmarshal(packet)
	if err != nil || p.PayloadType != sdp.PCMU || len(p.Payload) != mixer.FrameSize {
		return
	}
	if len(t.heard) == heardFrames {
		t.heard = t.heard[1:]
	}
	t.heard = append(t.heard, bytes.Clone(p.Payload))
}

// needsClock tells whether the context mixes, or a termination of it
// plays a signal. rc.mu is held.
func (rc *relayContext) needsClock() bool {
	if rc.conference() {
		return true
	}
	for _, t := range rc.terms {
		if t.playback != nil {
			return true
		}
	}
	return false
}

// startClock starts the clock of the context if it needs one and has
// none. rc.mu is held.
func (rc *relayContext) startClock() {
	if rc.ticking || rc.closed || !rc.needsClock() {
		return
	}
	rc.ticking = true
	go rc.run()
}

// run is the clock of the context. It stops once the context no longer
// needs it, or is gone.
func (rc *relayContext) run() {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for range ticker.C {
		if !rc.step() {
			return
		}
	}
}

// outgoing is a packet that a tick sends.
type outgoing struct {
	to     *channel
	packet []byte
}

// step sends the packets of one tick and the Notify of each signal that
// has ended, and tells whether the clock goes on.
//
// The packets go out while rc.mu is held, so that once a Modify or a
// Subtract has returned, no packet of what the context did before goes
// out after it: a controller that stops an announcement, or takes a
// party out of a conference, sees the change take effect by the time it
// has the reply. The Notify goes out after, since it waits for the
// controller.
func (rc *relayContext) step() bool {
	rc.mu.Lock()
	if rc.closed {
		rc.ticking = false
		rc.mu.Unlock()
		return false
	}
	var packets []outgoing
	var notifies []func()
	for _, t := range rc.terms {
		if t.playback == nil {
			continue
		}
		payload, ok := t.playback.Next()
		if !ok {
			t.playback, t.signals = nil, nil
			if t.events.detects(SignalCompleted) {
				url, n := t.events.Notify, Notify{Context: rc.id, Termination: t.id, RequestID: t.events.RequestID, Event: SignalCompleted}
				notifies = append(notifies, func() { rc.relay.notify(url, n) })
			}
			continue
		}
		if t.mode.sends() {
			packets = append(packets, outgoing{t.rtp, t.own.packet(payload)})
		}
	}
	packets = append(packets, rc.mixTick()...)
	more := !rc.closed && rc.needsClock()
	rc.ticking = more
	for _, p := range packets {
		p.to.send(p.packet)
	}
	rc.mu.Unlock()
	for _, notify := range notifies {
		notify()
	}
	return more
}

func (c *channel) setRemote(remote netip.AddrPort) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.latched {
		c.remote = remote
	}
}

// latch keeps src as the remote address if the channel has not latched
// yet, and tells what the remote address was before.
func (c *channel) latch(src netip.AddrPort) (before netip.AddrPort, latched bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latched {
		return c.remote, false
	}
	before, c.remote, c.latched = c.remote, src, true
	return before, true
}

// send writes b to the remote address, once the channel has one: from
// the SDP or from latching.
func (c *channel) send(b []byte) {
	c.mu.Lock()
	dst := c.remote
	c.mu.Unlock()
	if !dst.IsValid() {
		return
	}
	if _, err := c.conn.WriteToUDPAddrPort(b, dst); err == nil {
		c.sent.Add(1)
	}
}

// mixTick mixes one tick of a conference: each termination that sends
// and plays nothing gets the sum of the others that receive, never its
// own voice (mixer.Conference.Mix). rc.mu is held.
func (rc *relayContext) mixTick() []outgoing {
	if !rc.conference() {
		for _, t := range rc.terms {
			t.heard = nil
		}
		return nil
	}
	in := make(map[string][]byte, len(rc.terms))
	for id, t := range rc.terms {
		if len(t.heard) > 0 {
			in[string(id)], t.heard = t.heard[0], t.heard[1:]
		}
	}
	mixed, err := rc.mix.Mix(in)
	if err != nil {
		rc.relay.log.Warn("mixing failed", "context", rc.id, "error", err.Error())
		return nil
	}
	var packets []outgoing
	for id, t := range rc.terms {
		if t.playback == nil && t.mode.sends() {
			packets = append(packets, outgoing{t.rtp, t.own.packet(mixed[string(id)])})
		}
	}
	return packets
}

func (t *relayTermination) close() {
	t.rtp.conn.Close()
	t.rtcp.conn.Close()
	t.ctx.relay.ports.give(t.rtp.local.Port())
}

func (rc *relayContext) close() {
	for _, t := range rc.terms {
		t.close()
	}
}

// view is the context as the API shows it. rc.mu is held.
func (rc *relayContext) view() Context {
	c := Context{ID: rc.id, Terminations: make(map[TerminationID]*Termination, len(rc.terms))}
	for id, t := range rc.terms {
		view := t.view()
		c.Terminations[id] = &view
	}
	return c
}

// view is the termination as the API shows it. ctx.mu is held.
func (t *relayTermination) view() Termination {
	v := Termination{ID: t.id, Mode: t.mode, Signals: t.signals, Events: t.events}
	v.Local, v.Remote, v.Latched, v.Packets, v.PacketsSent = t.rtp.view()
	v.RTCPLocal, v.RTCPRemote, v.RTCPLatched, v.RTCPPackets, _ = t.rtcp.view()
	return v
}

func (c *channel) view() (local, remote netip.AddrPort, latched bool, received, sent uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.local, c.remote, c.latched, c.received.Load(), c.sent.Load()
}
