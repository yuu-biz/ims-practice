package diameter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// Timers are the durations behind SetTimer.
type Timers struct {
	// Watchdog is Tw before jitter, Twinit in RFC 3539 3.4.1 [1]. It
	// also bounds a connection attempt, as RFC 3539 3.4.1 [5] allows.
	Watchdog time.Duration
	// WatchdogJitter spreads each Tw evenly over Watchdog plus or minus
	// this much. RFC 3539 3.4.1 [1] uses 2 seconds.
	WatchdogJitter time.Duration
	// Reconnect is Tc (RFC 6733 2.1 and 12).
	Reconnect time.Duration
	// Answer bounds the wait for the CER on a new connection, for a CEA,
	// for a DPA, and for the peer to disconnect after a DPA. RFC 6733
	// 5.6.2 leaves this timer to the application.
	Answer time.Duration
}

// PeerConfig is a peer that this node connects to.
type PeerConfig struct {
	Address    string // host:port
	OriginHost string // the Origin-Host the peer must send in its CEA
}

// Handler answers a request that a peer sent to this node, such as a
// UAR arriving at the HSS. It runs on its own goroutine and must return
// an answer; RFC 6733 6.2 requires one for every request.
type Handler func(request *diam.Message) *diam.Message

// Capture is one message as it went out on, or came in on, a
// connection.
type Capture struct {
	Sent        bool // false for a received message
	Source      netip.AddrPort
	Destination netip.AddrPort
	Message     *diam.Message
}

// Observer sees every message that a node sends or receives, for
// example to copy it to a HEP collector. It runs on the
// goroutine of the connection, so it must not block.
type Observer func(Capture)

// Node is one Diameter node: its identity, its timers, its handler for
// incoming requests, and the End-to-End Identifiers and Session-Ids
// shared by all of its connections.
type Node struct {
	identity Identity
	// applications are the Application-Ids that the node advertises in
	// its CER and CEA, and that a peer must share (RFC 6733 5.3).
	applications []uint32
	timers       Timers
	log          *slog.Logger
	handler      Handler
	observe      Observer // nil when nothing observes
	// followUps holds, by request, what AfterAnswer asked to run once the
	// request's answer is on its way.
	followUps sync.Map // *diam.Message -> func()
	endToEnd  atomic.Uint32
	// sessionHigh and sessionLow are the two counters of a Session-Id
	// (RFC 6733 8.8).
	sessionHigh uint32
	sessionLow  atomic.Uint32
}

// NewNode makes a node that supports the given applications, such as
// ApplicationIDCx.
func NewNode(identity Identity, applications []uint32, timers Timers, log *slog.Logger) *Node {
	n := &Node{identity: identity, applications: slices.Clone(applications), timers: timers, log: log}
	// RFC 6733 3: the high-order 12 bits come from the current time and
	// the low-order 20 bits are random, so identifiers stay unique across
	// restarts.
	n.endToEnd.Store(uint32(time.Now().Unix())<<20 | rand.Uint32()&0xfffff)
	// RFC 6733 8.8 suggests the boot time for the high 32 bits, so that
	// Session-Ids stay unique across restarts.
	n.sessionHigh = uint32(time.Now().Unix())
	return n
}

// Handle sets the handler for requests from peers. Call it before
// Listen and Connect. Without a handler every request is answered with
// DIAMETER_COMMAND_UNSUPPORTED.
func (n *Node) Handle(h Handler) { n.handler = h }

// AfterAnswer runs f once the answer to request has been handed to its
// connection, so that whatever f sends goes out after that answer. A
// handler calls it for the request it is answering: an AF, for example,
// must answer an ASR before it ends the session with an STR (TS 29.214
// 4.4.6.1). f runs on the handler's goroutine and may wait for answers.
func (n *Node) AfterAnswer(request *diam.Message, f func()) { n.followUps.Store(request, f) }

// Observe sets the observer of every message on every connection. Call
// it before Listen and Connect.
func (n *Node) Observe(o Observer) { n.observe = o }

// Identity is how this node names itself in its messages.
func (n *Node) Identity() Identity { return n.identity }

// NewSessionID returns a new Session-Id in the format of RFC 6733 8.8:
// <DiameterIdentity>;<high 32 bits>;<low 32 bits>.
func (n *Node) NewSessionID() string {
	return fmt.Sprintf("%s;%d;%d", n.identity.OriginHost, n.sessionHigh, n.sessionLow.Add(1))
}

// ErrPeerUnavailable means the connection to the peer is not open, or
// is SUSPECT, so the request was not sent.
var ErrPeerUnavailable = errors.New("diameter: peer is not available")

// errConnectionClosed means the connection closed before the answer
// arrived.
var errConnectionClosed = errors.New("diameter: connection closed before the answer arrived")

// outgoing is an application message on its way to the loop. A request
// carries the channel its answer goes to. An answer carries the
// connection its request came on, so it is never sent on a newer one.
type outgoing struct {
	message *diam.Message
	answer  chan answerResult // requests only
	gen     uint64            // answers only
}

func (o *outgoing) isRequest() bool { return o.answer != nil }

type answerResult struct {
	message *diam.Message
	err     error
}

// Peer runs the state machine for one peer in its own goroutine.
type Peer struct {
	node     *Node
	address  string // initiator only
	host     string // the peer's Origin-Host
	log      *slog.Logger
	inbox    chan input
	outbox   chan *outgoing
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// input is what the dialer and reader goroutines hand to the loop. gen
// names the connection it belongs to, so that input from a connection
// the loop has already given up on is dropped.
type input struct {
	gen   uint64
	event Event         // RcvConnAck, RcvConnNack or PeerDisc; nil for msg
	conn  net.Conn      // with RcvConnAck
	msg   *diam.Message // a received message
	err   error         // why the dial or the read failed
}

// Connect starts an initiator peer. It connects, retries every Tc, and
// keeps going until Stop.
func (n *Node) Connect(peer PeerConfig) *Peer {
	p := n.newPeer(peer.OriginHost)
	p.address = peer.Address
	go p.run(NewInitiatorState(peer.OriginHost, n.applications), Start{}, nil)
	return p
}

func (n *Node) newPeer(peerHost string) *Peer {
	return &Peer{
		node:   n,
		host:   peerHost,
		log:    n.log.With("peer", peerHost),
		inbox:  make(chan input),
		outbox: make(chan *outgoing),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Request sends an application request to the peer and waits for its
// answer, at most the Answer timer. The peer sets the Hop-by-Hop and
// End-to-End Identifiers.
func (p *Peer) Request(ctx context.Context, request *diam.Message) (*diam.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, p.node.timers.Answer)
	defer cancel()
	out := &outgoing{message: request, answer: make(chan answerResult, 1)}
	select {
	case p.outbox <- out:
	case <-p.done:
		return nil, ErrPeerUnavailable
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-out.answer:
		return r.message, r.err
	case <-ctx.Done():
		return nil, fmt.Errorf("no answer to %s: %w", commandName(request), ctx.Err())
	}
}

// Stop sends a DPR if the connection is open, waits for the DPA, and
// returns once the peer has finished.
func (p *Peer) Stop() {
	p.stopOnce.Do(func() { close(p.stop) })
	<-p.done
}

func (p *Peer) post(in input) bool {
	select {
	case p.inbox <- in:
		return true
	case <-p.done:
		return false
	}
}

func (p *Peer) dial(gen uint64) {
	d := net.Dialer{Timeout: p.node.timers.Watchdog}
	conn, err := d.Dial("tcp", p.address)
	if err != nil {
		p.post(input{gen: gen, event: RcvConnNack{}, err: err})
		return
	}
	if !p.post(input{gen: gen, event: RcvConnAck{}, conn: conn}) {
		conn.Close()
	}
}

func (p *Peer) read(gen uint64, conn net.Conn) {
	for {
		m, err := diam.ReadMessage(conn, dict.Default)
		if err != nil {
			resetIfUnparseable(conn, err)
			p.post(input{gen: gen, event: PeerDisc{}, err: err})
			return
		}
		if !p.post(input{gen: gen, msg: m}) {
			return
		}
	}
}

// resetIfUnparseable makes the coming Close send a TCP RST when the
// stream held bytes that are not a Diameter message. RFC 6733 2.1: such
// a stream cannot be recovered and is closed with a reset. EOF and
// network errors mean the connection is already gone.
func resetIfUnparseable(conn net.Conn, err error) {
	var netErr net.Error
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &netErr) {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetLinger(0)
	}
}

// loop is the state the I/O loop owns. Only the peer's goroutine
// touches it.
type loop struct {
	peer  *Peer
	stop  <-chan struct{}
	timer *time.Timer
	conn  net.Conn
	gen   uint64
	// hopByHop is the last Hop-by-Hop Identifier sent. RFC 6733 3: it
	// normally increases by one from a random start.
	hopByHop uint32
	// outstanding maps the Hop-by-Hop Identifier of each request sent
	// on the current connection to what is waiting for its answer.
	outstanding map[uint32]pending
}

type pending struct {
	command string
	answer  chan answerResult // nil for CER, DWR and DPR
}

func (p *Peer) run(s State, first Event, conn net.Conn) {
	defer close(p.done)
	l := &loop{
		peer:        p,
		stop:        p.stop,
		timer:       time.NewTimer(time.Hour),
		hopByHop:    rand.Uint32(),
		outstanding: map[uint32]pending{},
	}
	l.timer.Stop()
	defer l.disconnect()
	if conn != nil {
		l.gen++
		l.adopt(conn)
	}
	ev := first
	for {
		next, actions := Transition(s, ev)
		if next.Phase != s.Phase || next.Watchdog != s.Watchdog {
			p.log.Info("peer state", "event", ev.eventName(), "from", describe(s), "to", describe(next))
		}
		s = next
		for _, a := range actions {
			if _, finish := a.(Finish); finish {
				return
			}
			l.do(a)
		}
		ev = l.wait()
	}
}

func describe(s State) string {
	if s.Phase == Open {
		return s.String() + " (" + s.Watchdog.String() + ")"
	}
	return s.String()
}

func (l *loop) wait() Event {
	for {
		select {
		case <-l.stop:
			l.stop = nil
			return Stop{}
		case <-l.timer.C:
			return Timeout{}
		case out := <-l.peer.outbox:
			if !out.isRequest() && out.gen != l.gen {
				// The connection the request came on is gone, and with
				// it the peer that waits for this answer.
				continue
			}
			return SendMessage{Outgoing: out}
		case in := <-l.peer.inbox:
			if in.gen != l.gen {
				if in.conn != nil {
					in.conn.Close()
				}
				continue
			}
			if in.msg != nil {
				if ev, ok := l.received(in.msg); ok {
					return ev
				}
				continue
			}
			switch in.event.(type) {
			case RcvConnAck:
				l.adopt(in.conn)
			case RcvConnNack:
				l.peer.log.Info("connection failed", "address", l.peer.address, "error", in.err.Error())
			case PeerDisc:
				l.peer.log.Info("connection lost", "error", in.err.Error())
			}
			return in.event
		}
	}
}

func (l *loop) adopt(conn net.Conn) {
	l.conn = conn
	l.peer.log.Info("connection up", "local_addr", conn.LocalAddr().String(), "remote_addr", conn.RemoteAddr().String())
	go l.peer.read(l.gen, conn)
}

func (l *loop) received(m *diam.Message) (Event, bool) {
	l.peer.node.record(l.peer.log, l.conn, false, m)
	if !isRequest(m) {
		p, ok := l.outstanding[m.Header.HopByHopID]
		if !ok {
			// RFC 6733 3: an answer with an unknown Hop-by-Hop
			// Identifier MUST be discarded.
			l.peer.log.Warn("discarded answer with unknown Hop-by-Hop Identifier",
				"hop_by_hop", fmt.Sprintf("0x%08x", m.Header.HopByHopID))
			return nil, false
		}
		if p.answer == nil {
			delete(l.outstanding, m.Header.HopByHopID)
		}
	}
	return parseEvent(m), true
}

func (l *loop) do(a Action) {
	local, applications := l.peer.node.identity, l.peer.node.applications
	switch a := a.(type) {
	case Connect:
		l.gen++
		go l.peer.dial(l.gen)
	case SendCER:
		l.sendRequest(capabilitiesExchangeRequest(local, applications, l.localIP(), l.nextIDs()))
	case SendCEA:
		l.send(capabilitiesExchangeAnswer(local, applications, l.localIP(), a.Request, a.ResultCode))
	case SendDWR:
		l.sendRequest(deviceWatchdogRequest(local, l.nextIDs()))
	case SendDWA:
		l.send(deviceWatchdogAnswer(local, a.Request))
	case SendDPR:
		l.sendRequest(disconnectPeerRequest(local, l.nextIDs(), a.Cause))
	case SendDPA:
		l.send(disconnectPeerAnswer(local, a.Request))
	case Process:
		l.process(a.Message)
	case SndMessage:
		if a.Outgoing.isRequest() {
			ids := l.nextIDs()
			a.Outgoing.message.Header.HopByHopID = ids.HopByHop
			// The Hop-by-Hop Identifier belongs to the connection, so every
			// attempt gets a new one. A retransmission to another peer, the
			// T flag of RFC 6733 5.5.4, keeps the End-to-End Identifier it
			// came with: that and the Origin-Host are what tell the
			// receiver that it is a duplicate.
			if a.Outgoing.message.Header.CommandFlags&diam.RetransmittedFlag == 0 {
				a.Outgoing.message.Header.EndToEndID = ids.EndToEnd
			}
			l.outstanding[ids.HopByHop] = pending{command: commandName(a.Outgoing.message), answer: a.Outgoing.answer}
		}
		l.send(a.Outgoing.message)
	case RefuseMessage:
		if a.Outgoing.isRequest() {
			a.Outgoing.answer <- answerResult{err: ErrPeerUnavailable}
		} else {
			l.peer.log.Info("dropped answer, connection is closing", "command", commandName(a.Outgoing.message))
		}
	case Disconnect:
		l.disconnect()
	case SetTimer:
		l.timer.Reset(l.duration(a.Timer))
	}
}

// process gives an answer to the request waiting for it, and a request
// to the handler. The handler runs on its own goroutine so that a slow
// answer never holds up the watchdog.
func (l *loop) process(m *diam.Message) {
	if !isRequest(m) {
		p, ok := l.outstanding[m.Header.HopByHopID]
		if !ok || p.answer == nil {
			// The Hop-by-Hop Identifier named a CER, DWR or DPR, but the
			// command code is an application's: nothing waits for it.
			l.peer.log.Warn("discarded answer that does not match its request",
				"command", commandName(m), "hop_by_hop", fmt.Sprintf("0x%08x", m.Header.HopByHopID))
			return
		}
		delete(l.outstanding, m.Header.HopByHopID)
		p.answer <- answerResult{message: m}
		return
	}
	node, gen, outbox, done := l.peer.node, l.gen, l.peer.outbox, l.peer.done
	go func() {
		var answer *diam.Message
		if node.handler == nil {
			answer = CommandUnsupportedAnswer(node.identity, m)
		} else {
			answer = node.handler(m)
		}
		select {
		case outbox <- &outgoing{message: answer, gen: gen}:
		case <-done:
		}
		// The loop writes the answer before it takes the next message from
		// the outbox, so a request sent from here follows it.
		if f, ok := node.followUps.LoadAndDelete(m); ok {
			f.(func())()
		}
	}()
}

func (l *loop) nextIDs() MessageIDs {
	l.hopByHop++
	return MessageIDs{HopByHop: l.hopByHop, EndToEnd: l.peer.node.endToEnd.Add(1)}
}

// localIP is the address this end of the connection uses, which is the
// Host-IP-Address to advertise. A wildcard listen address such as
// 0.0.0.0 never appears here.
func (l *loop) localIP() net.IP {
	return l.conn.LocalAddr().(*net.TCPAddr).IP
}

func (l *loop) sendRequest(m *diam.Message) {
	l.outstanding[m.Header.HopByHopID] = pending{command: commandName(m)}
	l.send(m)
}

func (l *loop) send(m *diam.Message) {
	l.peer.node.record(l.peer.log, l.conn, true, m)
	if _, err := m.WriteTo(l.conn); err != nil {
		// Closing without bumping gen lets the reader report PeerDisc,
		// so the state machine sees the failure.
		l.peer.log.Info("write failed", "error", err.Error())
		l.conn.Close()
	}
}

func (l *loop) disconnect() {
	if l.conn == nil {
		return
	}
	l.conn.Close()
	l.conn = nil
	l.gen++
	for _, p := range l.outstanding {
		if p.answer != nil {
			p.answer <- answerResult{err: errConnectionClosed}
		}
	}
	clear(l.outstanding)
	l.peer.log.Info("connection closed")
}

func (l *loop) duration(t Timer) time.Duration {
	timers := l.peer.node.timers
	switch t {
	case TimerWatchdog:
		if timers.WatchdogJitter <= 0 {
			return timers.Watchdog
		}
		jitter := rand.N(2*timers.WatchdogJitter+1) - timers.WatchdogJitter
		return timers.Watchdog + jitter
	case TimerReconnect:
		return timers.Reconnect
	}
	return timers.Answer
}

// record logs m and hands it to the node's observer, if there is one.
// conn is the connection that m came in on or goes out on.
func (n *Node) record(log *slog.Logger, conn net.Conn, sent bool, m *diam.Message) {
	direction := "recv"
	if sent {
		direction = "send"
	}
	logMessage(log, direction, m)
	if n.observe == nil {
		return
	}
	c := Capture{Sent: sent, Message: m, Source: addrPort(conn.RemoteAddr()), Destination: addrPort(conn.LocalAddr())}
	if sent {
		c.Source, c.Destination = c.Destination, c.Source
	}
	n.observe(c)
}

// addrPort is the address of one end of a TCP connection. A dual-stack
// socket reports an IPv4 peer as ::ffff:a.b.c.d.
func addrPort(a net.Addr) netip.AddrPort {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return netip.AddrPort{}
	}
	ap := tcp.AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

func logMessage(log *slog.Logger, direction string, m *diam.Message) {
	attrs := []any{
		"direction", direction,
		"command", commandName(m),
		"r", isRequest(m),
		"hop_by_hop", fmt.Sprintf("0x%08x", m.Header.HopByHopID),
		"end_to_end", fmt.Sprintf("0x%08x", m.Header.EndToEndID),
	}
	if !isRequest(m) {
		attrs = append(attrs, "result_code", resultCode(m))
	}
	log.Info("diameter message", attrs...)
}

// Server accepts connections from peers. It is the responder side.
type Server struct {
	node     *Node
	listener net.Listener
	wg       sync.WaitGroup // the accept loop and connections still waiting for a CER

	mu     sync.Mutex
	closed bool
	peers  map[string]*Peer // by lower-case Origin-Host
}

// Listen starts accepting connections on address, e.g. ":3868" (RFC
// 6733 2.1).
func (n *Node) Listen(address string) (*Server, error) {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	s := &Server{node: n, listener: ln, peers: map[string]*Peer{}}
	n.log.Info("listening", "address", ln.Addr().String())
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr is the address the server listens on.
func (s *Server) Addr() string { return s.listener.Addr().String() }

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			s.node.log.Warn("accept failed", "error", err.Error())
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go s.handshake(conn)
	}
}

// handshake is the logic that RFC 6733 5.6.1 puts in front of the state
// machines: wait for the CER, then hand the connection to the state
// machine of the Origin-Host the CER names.
func (s *Server) handshake(conn net.Conn) {
	defer s.wg.Done()
	log := s.node.log.With("remote_addr", conn.RemoteAddr().String())
	conn.SetReadDeadline(time.Now().Add(s.node.timers.Answer))
	m, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		log.Info("closed connection that sent no CER", "error", err.Error())
		resetIfUnparseable(conn, err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})
	cer, ok := parseEvent(m).(RConnCER)
	if !ok {
		s.node.record(log, conn, false, m)
		log.Info("closed connection whose first message was not a CER")
		conn.Close()
		return
	}
	s.node.record(log.With("peer", cer.OriginHost), conn, false, m)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		conn.Close()
		return
	}
	key := strings.ToLower(cer.OriginHost)
	if _, exists := s.peers[key]; exists {
		// R-Reject of RFC 6733 5.6: this peer already has a connection.
		log.Warn("rejected second connection from peer", "peer", cer.OriginHost)
		conn.Close()
		return
	}
	p := s.node.newPeer(cer.OriginHost)
	s.peers[key] = p
	go func() {
		p.run(NewResponderState(s.node.applications), cer, conn)
		s.mu.Lock()
		delete(s.peers, key)
		s.mu.Unlock()
	}()
}

// Stop closes the listener and stops every peer, each with a DPR.
func (s *Server) Stop() {
	s.mu.Lock()
	s.closed = true
	s.listener.Close()
	peers := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	s.wg.Wait()
	StopAll(peers)
}

// StopAll stops peers in parallel, so that their DPR/DPA exchanges
// overlap instead of adding up.
func StopAll(peers []*Peer) {
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Go(p.Stop)
	}
	wg.Wait()
}
