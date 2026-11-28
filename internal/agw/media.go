package agw

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/rtp"
)

// relayContext is a context and its terminations. mu guards terms,
// closed and what each termination keeps under it, and covers this
// context only: packets of other contexts never wait for it.
type relayContext struct {
	id    string
	relay *Relay

	// writes orders the store writes of this context: an operation takes
	// it before it changes the context and gives it back once the change
	// is in the store, so that two changes cannot reach the store the
	// other way round. The media path takes mu alone, and no store
	// call is made under mu, so a store that is slow holds up the
	// controller of this context and never its packets.
	writes sync.Mutex

	mu     sync.Mutex
	terms  map[TerminationID]*relayTermination
	closed bool
	// chosen counts the termination IDs that the relay chose itself.
	chosen int
	// ticking is true while the clock of the context runs.
	ticking bool
	// ticks numbers the ticks of the clock: each step counts one, and
	// a clock that starts again counts the ticks that it missed since
	// it stopped, at stopped. The timestamps of the node's own media
	// count in these ticks.
	ticks   uint64
	stopped time.Time
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
	mode Mode
	// latchOff tells that the termination latches no more: it was
	// added or modified with NAPTOff or StopLatching.
	latchOff bool
	signals  *Signals
	events   *Events
	// playback is the announcement that plays, or nil.
	playback *mixer.Playback
	// own is the RTP source of what the node sends itself.
	own *source
	// receivePT and sendPT are the payload types of PCMU in what the
	// termination receives and in what it sends: those of its own SDP,
	// the Local descriptor, and of its party's SDP, the Remote
	// descriptor. Each side of a session receives with the payload types
	// of its own SDP, and sends with those of the other side's (RFC 3264
	// 5.1, 6.1). Until a descriptor says otherwise, PCMU has its static
	// payload type 0 (RFC 3551 6).
	receivePT, sendPT uint8
	// heard holds the PCMU payloads that arrived since the last tick of
	// a conference, oldest first.
	heard [][]byte
}

// channel is one socket of a termination, with the address it sends to.
type channel struct {
	conn  *net.UDPConn
	local netip.AddrPort
	mu    sync.Mutex
	// remote is the address of the SDP, and latched the source that the
	// channel latched onto, or the zero address. latching is NAPTLatch
	// or NAPTRelatch while it waits for its packet, and empty otherwise.
	remote, latched netip.AddrPort
	latching        Latch
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
	latch := req.Latch
	if latch == "" {
		// SIMPLIFIED: RFC 4961. A termination added without a latching
		// latches once, and follows its party afterwards only when the
		// controller asks it to latch again. Such a termination faces a
		// UE, and the P-CSCF asks for NAPTRelatch for each move that a
		// new SDP of the UE announces. A UE that moves without telling
		// the network, as one whose NAT gives its media another port,
		// is not followed. A termination whose party changes within a
		// call, as on the network side of the P-CSCF, is added with
		// NAPTOff.
		latch = NAPTLatch
	}
	if !latch.valid() {
		return nil, fmt.Errorf("%w: latching %q", ErrBadRequest, latch)
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
	t.setPayloadTypes(req.LocalMedia, req.RemoteMedia)
	t.setRemote(req.Remote)
	t.setLatch(latch)
	t.latchOff = latch == NAPTOff || latch == StopLatching
	rc.terms[id] = t
	rc.mix.Join(string(id))
	go t.read(rtpChannel, t.rtp)
	go t.read(rtcpChannel, t.rtcp)
	return t, nil
}

// reopen makes a termination of a stored context again, on the port it
// had, and starts reading its sockets. It sends where the stored
// address says, which is the address the earlier process latched onto
// only if a change of the context was stored after the latch. A
// termination that latched latches again as one added without a
// latching does: the first packet that arrives is answered where it
// comes from, so a party behind NAT is found again whatever was stored.
// One that had latching off keeps it off, and goes on following the
// SDP that its controller gives. rc.mu is held.
func (rc *relayContext) reopen(saved SavedTermination) error {
	if !saved.Mode.valid() {
		return fmt.Errorf("%w: mode %q", ErrBadRequest, saved.Mode)
	}
	rtp, rtcp, err := rc.relay.reopenPorts(saved.LocalRTP)
	if err != nil {
		return err
	}
	t := &relayTermination{id: saved.ID, ctx: rc, rtp: rtp, rtcp: rtcp, mode: saved.Mode, own: newSource(), latchOff: saved.Latch == NAPTOff,
		receivePT: saved.ReceivePT, sendPT: saved.SendPT}
	t.setRemote(saved.Remote)
	if t.latchOff {
		t.setLatch(NAPTOff)
	} else {
		t.setLatch(NAPTLatch)
	}
	rc.terms[saved.ID] = t
	rc.mix.Join(string(saved.ID))
	go t.read(rtpChannel, t.rtp)
	go t.read(rtcpChannel, t.rtcp)
	return nil
}

// remove takes a termination out of the context and closes its ports,
// undoing an add. rc.mu is held.
func (rc *relayContext) remove(t *relayTermination) {
	delete(rc.terms, t.id)
	rc.mix.Leave(string(t.id))
	t.close()
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
	if req.Latch != "" && !req.Latch.valid() {
		return fmt.Errorf("%w: latching %q", ErrBadRequest, req.Latch)
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
	t.setPayloadTypes(req.LocalMedia, req.RemoteMedia)
	t.setRemote(req.Remote)
	t.setLatch(req.Latch)
	switch req.Latch {
	case NAPTOff, StopLatching:
		// After StopLatching the termination sends where it does now,
		// and a relay created again sends there too.
		t.latchOff = true
	case NAPTLatch, NAPTRelatch:
		t.latchOff = false
	}
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

// setPayloadTypes takes the payload types of PCMU from the media of the
// Local and the Remote descriptor. Media without PCMU keeps what the
// termination has. ctx.mu is held.
func (t *relayTermination) setPayloadTypes(local, remote Media) {
	if pt, ok := local.pcmu(); ok {
		t.receivePT = pt
	}
	if pt, ok := remote.pcmu(); ok {
		t.sendPT = pt
	}
}

// setRemote takes the RTP address of the SDP, and the RTCP address one
// port above it. Nothing changes for a zero address. A channel that has
// latched keeps sending to the address it latched (H.248.37 (06/2008)
// 6.6.2).
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

// setLatch sets the latching of RTP and RTCP alike. An empty l keeps
// it.
func (t *relayTermination) setLatch(l Latch) {
	t.rtp.setLatch(l)
	t.rtcp.setLatch(l)
}

// read takes the packets that arrive on one socket of the termination,
// and latches onto the source of a packet as the latching of the
// channel asks.
//
// SIMPLIFIED: H.248.37 (06/2008) 6.6.7.1. After a latch, the packets of
// other sources pass as well, where H.248.37 has them discarded (6.6.2,
// 6.6.3). The filter guards a termination against packets that another
// source injects into the call, which a lab network has none of: every
// packet that reaches a termination comes from the party it faces.
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
			log.Info("latched", "context", t.ctx.id, "termination", string(t.id), "channel", kind, "before", before.String(), "source", src.String())
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
	// A packet read from the socket of a termination just before a
	// Subtract took it out waits here for rc.mu, and arrives after the
	// Subtract has returned. It belongs to the context no more. A
	// Subtract of the whole context leaves the terminations in it and
	// marks it closed, before it closes the sockets.
	if rc.closed || rc.terms[from.id] != from {
		return
	}
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
	if err != nil || p.PayloadType != t.receivePT || len(p.Payload) != mixer.FrameSize {
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

// tick starts the clock once a change is in the store, so that a change
// which was given up for a store that failed sends nothing. The caller
// holds neither lock.
func (rc *relayContext) tick() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.startClock()
}

// startClock starts the clock of the context if it needs one and has
// none. rc.mu is held.
func (rc *relayContext) startClock() {
	if rc.ticking || rc.closed || !rc.needsClock() {
		return
	}
	if !rc.stopped.IsZero() {
		// No clock counts the ticks while none runs, so the time of day
		// does, once.
		rc.ticks += uint64(time.Since(rc.stopped) / tick)
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
	rc.ticks++
	now := rc.ticks
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
			packets = append(packets, outgoing{t.rtp, t.own.packet(t.sendPT, payload, now)})
		}
	}
	packets = append(packets, rc.mixTick(now)...)
	for _, t := range rc.terms {
		t.own.idle(now)
	}
	more := !rc.closed && rc.needsClock()
	rc.ticking = more
	if !more {
		rc.stopped = time.Now()
	}
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
	c.remote = remote
}

// setLatch carries out what l asks. An empty l changes nothing.
func (c *channel) setLatch(l Latch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch l {
	case NAPTOff:
		c.latched, c.latching = netip.AddrPort{}, ""
	case NAPTLatch, NAPTRelatch:
		c.latching = l
	case StopLatching:
		c.latching = ""
	}
}

// latch takes src as the address to send to, if the latching that
// waits asks for it: NAPTLatch for any packet, NAPTRelatch for one from
// another source than the address the channel sends to. That latching
// is then done. latch tells where the channel sent before, and whether
// it latched.
func (c *channel) latch(src netip.AddrPort) (before netip.AddrPort, latched bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	before = c.destination()
	if c.latching == NAPTLatch || (c.latching == NAPTRelatch && src != before) {
		c.latched, c.latching = src, ""
		return before, true
	}
	return before, false
}

// destination is where the channel sends: the address it latched, or
// else that of the SDP. c.mu is held.
func (c *channel) destination() netip.AddrPort {
	if c.latched.IsValid() {
		return c.latched
	}
	return c.remote
}

// send writes b to the destination, once the channel has one.
func (c *channel) send(b []byte) {
	c.mu.Lock()
	dst := c.destination()
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
func (rc *relayContext) mixTick(now uint64) []outgoing {
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
			packets = append(packets, outgoing{t.rtp, t.own.packet(t.sendPT, mixed[string(id)], now)})
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

// saved is the context as the store keeps it, in the order of the
// termination IDs. Of each termination it holds what a relay created
// again under the same name needs to carry the media on: its ports, its
// mode, and the address it sends to now, which is the one it latched
// onto once it has, whether it latches, and the payload types of PCMU
// in what it receives and sends. What a termination plays is
// not kept (see restore).
// rc.mu is held.
func (rc *relayContext) saved() SavedContext {
	c := SavedContext{ID: rc.id, Terminations: make([]SavedTermination, 0, len(rc.terms))}
	for id, t := range rc.terms {
		_, remote, _, _, _ := t.rtp.view()
		saved := SavedTermination{ID: id, LocalRTP: t.rtp.local.Port(), Remote: remote, Mode: t.mode, ReceivePT: t.receivePT, SendPT: t.sendPT}
		if t.latchOff {
			saved.Latch = NAPTOff
		}
		c.Terminations = append(c.Terminations, saved)
	}
	slices.SortFunc(c.Terminations, func(a, b SavedTermination) int { return strings.Compare(string(a.ID), string(b.ID)) })
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
	return c.local, c.destination(), c.latched.IsValid(), c.received.Load(), c.sent.Load()
}
