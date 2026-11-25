package sip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// defaultPort is the SIP port for UDP and TCP (RFC 3261 18.1.1).
const defaultPort = 5060

// Transport is a transport protocol as the Via header field names it
// (RFC 3261 20.42).
type Transport string

const (
	UDP Transport = "UDP"
	TCP Transport = "TCP"
)

// reliable reports whether the transport delivers in order and without
// loss, so that the transactions over it retransmit nothing (RFC 3261
// 17).
func (t Transport) reliable() bool { return t == TCP }

// Destination is where a message goes, or where it came from: the
// transport and the address and port at the far end. The user of the
// transport layer passes one with every request (RFC 3261 18.1.1).
type Destination struct {
	Transport Transport
	Addr      netip.AddrPort
}

func (d Destination) String() string { return string(d.Transport) + " " + d.Addr.String() }

// Capture is one message as it crossed the wire.
type Capture struct {
	Transport   Transport
	Sent        bool // false for a received message
	Source      netip.AddrPort
	Destination netip.AddrPort
	Message     *Message
	// Wire is the message exactly as it was sent or received. For a
	// received request it differs from Message.Bytes(), whose top Via
	// has received and rport already (RFC 3261 18.2.1, RFC 3581 4).
	Wire []byte
}

// Observer sees every message that the transport layer sends or
// receives, for example to copy it to a HEP collector. It runs
// on the goroutine that sends or receives, so it must not block.
type Observer func(Capture)

// TransportLayer is the transport layer of RFC 3261 18: UDP and TCP on
// the same address and port. Every element implements both (18), and a
// server that listens on a UDP port listens on the same TCP port
// (18.2.1). Messages from either transport come out of one Receive.
type TransportLayer struct {
	udp *UDPTransport
	// client is the socket on the protected client port of TS 33.203
	// 7.1, from which the requests to a protected peer leave; nil
	// everywhere else.
	client *UDPTransport
	// protected reports whether the far end is a peer with security
	// associations, whose requests go from the client port; nil means
	// every peer is one.
	protected   func(netip.AddrPort) bool
	listener    *net.TCPListener
	log         *slog.Logger
	observe     Observer // nil when nothing observes
	idleTimeout time.Duration

	received  chan incoming
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	// ctx ends with Close, and with it any connect under way.
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	conns map[netip.AddrPort]*net.TCPConn // nil once closed
	// queues holds, for each far end, the messages that wait to go to it
	// over TCP, oldest first. A far end is in the map while a goroutine
	// writes to it (writeQueued).
	queues map[netip.AddrPort][]tcpSend
}

type incoming struct {
	msg    *Message
	source Destination
}

// Listen opens UDP and TCP on address, e.g. ":5060". With port 0 the
// system picks a UDP port, and TCP listens on the same one.
func Listen(address string, log *slog.Logger) (*TransportLayer, error) {
	return newTransportLayer(address, log, nil, tcpIdleTimeout)
}

// ListenObserved is Listen with observe called for every message sent
// or received. A nil observe is the same as Listen.
func ListenObserved(address string, log *slog.Logger, observe Observer) (*TransportLayer, error) {
	return newTransportLayer(address, log, observe, tcpIdleTimeout)
}

// ListenProtected is ListenObserved on the protected server port of TS
// 33.203 7.1, with the protected client port of the same address bound
// as well. The requests this transport layer sends to a peer that
// protected reports on leave from the client port, over UDP and over
// TCP, and the responses to them come back there; the responses to the
// requests that arrive on the server port go back from the server port.
// That is how a UE and a P-CSCF send to each other once IMS AKA has set
// up IPsec ESP: each pair of security associations runs between one
// end's client port and the other end's server port, so a message that
// left the wrong port would match no security association.
//
// A nil protected takes every peer for one, which is what a UE needs,
// since it talks to its P-CSCF alone. A P-CSCF passes the UEs it has
// security associations with: 7.1 lets no unprotected message leave the
// client port, and the P-CSCF reaches the core from the port it listens
// on. A zero client port is ListenObserved, which sends everything from
// the port it listens on.
//
// The address has to be the node's own: the client port is bound to it,
// and the security associations are bound to it as well (7.1).
func ListenProtected(address string, clientPort uint16, protected func(netip.AddrPort) bool, log *slog.Logger, observe Observer) (*TransportLayer, error) {
	t, err := newTransportLayer(address, log, observe, tcpIdleTimeout)
	if err != nil || clientPort == 0 {
		return t, err
	}
	if addr := t.LocalAddr().Addr(); !addr.IsValid() || addr.IsUnspecified() {
		// A connection from an unspecified address takes a port of the
		// moment (connectTCP), so the protected client port would hold
		// for UDP alone and the requests over TCP would leave
		// unprotected.
		t.Close()
		return nil, fmt.Errorf("sip: a protected client port needs the node's own address, not %s", address)
	}
	client, err := ListenUDP(netip.AddrPortFrom(t.LocalAddr().Addr(), clientPort).String(), log)
	if err != nil {
		t.Close()
		return nil, err
	}
	client.observe = observe
	t.client, t.protected = client, protected
	t.wg.Add(1)
	go t.readUDP(client)
	return t, nil
}

// clientPort is the socket that a request to dst leaves from: the
// protected client port for a peer with security associations, and nil
// for every other peer and wherever no client port is bound.
func (t *TransportLayer) clientPort(dst netip.AddrPort) *UDPTransport {
	if t.client == nil || (t.protected != nil && !t.protected(dst)) {
		return nil
	}
	return t.client
}

func newTransportLayer(address string, log *slog.Logger, observe Observer, idleTimeout time.Duration) (*TransportLayer, error) {
	for attempt := 1; ; attempt++ {
		udp, err := ListenUDP(address, log)
		if err != nil {
			return nil, err
		}
		udp.observe = observe
		listener, err := listenTCP(address, udp.LocalAddr().Port())
		if err != nil {
			udp.Close()
			// The TCP twin of a port the system picked for UDP may be
			// taken; another port will do.
			if portOf(address) == 0 && attempt < 10 {
				continue
			}
			return nil, err
		}
		t := &TransportLayer{
			udp:         udp,
			listener:    listener,
			log:         log,
			observe:     observe,
			idleTimeout: idleTimeout,
			received:    make(chan incoming),
			closed:      make(chan struct{}),
			conns:       map[netip.AddrPort]*net.TCPConn{},
			queues:      map[netip.AddrPort][]tcpSend{},
		}
		t.ctx, t.cancel = context.WithCancel(context.Background())
		log.Info("sip listening", "transport", string(TCP), "address", unmapped(listener.Addr()).String())
		t.wg.Add(2)
		go t.readUDP(udp)
		go t.acceptTCP()
		return t, nil
	}
}

func (t *TransportLayer) LocalAddr() netip.AddrPort { return t.udp.LocalAddr() }

// Close closes the UDP socket, the TCP listener and every connection,
// and waits for their readers and writers to end. A Receive in progress
// returns an error, and so does every message that waits to go out over
// TCP.
func (t *TransportLayer) Close() error {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.cancel()
		t.mu.Lock()
		conns := t.conns
		t.conns = nil
		t.mu.Unlock()
		t.listener.Close()
		t.udp.Close()
		if t.client != nil {
			t.client.Close()
		}
		for _, c := range conns {
			c.Close()
		}
	})
	t.wg.Wait()
	return nil
}

// Receive returns the next message from either transport, and where it
// came from. A request has received and rport in its top Via already
// (RFC 3261 18.2.1, RFC 3581 4).
func (t *TransportLayer) Receive() (*Message, Destination, error) {
	select {
	case in := <-t.received:
		return in.msg, in.source, nil
	case <-t.closed:
		return nil, Destination{}, net.ErrClosed
	}
}

// deliver hands a message to Receive. It reports false once the layer
// is closed.
func (t *TransportLayer) deliver(in incoming) bool {
	select {
	case t.received <- in:
		return true
	case <-t.closed:
		return false
	}
}

// readUDP hands the messages of one of the layer's UDP sockets to
// Receive: those of the port it listens on, and those of the protected
// client port, which are the responses to the requests that left there.
func (t *TransportLayer) readUDP(u *UDPTransport) {
	defer t.wg.Done()
	for {
		m, src, err := u.Receive()
		if err != nil {
			select {
			case <-t.closed:
			default:
				t.log.Error("sip transport failed", "transport", string(UDP), "error", err.Error())
			}
			return
		}
		if !t.deliver(incoming{msg: m, source: Destination{Transport: UDP, Addr: src}}) {
			return
		}
	}
}

// SendRequest sends req to dst (RFC 3261 18.1.1), and returns once it
// has gone out. Over TCP it uses the open connection to dst, or opens
// one. The caller picks dst from the Route set or the Request-URI; the
// transport does not look up DNS.
func (t *TransportLayer) SendRequest(req *Message, dst Destination) error {
	return t.sendRequest(req, dst, nil)
}

// sendRequest is SendRequest. With written set, it does not wait for a
// TCP peer: req joins the queue of dst (queueTCP), and written gets the
// outcome later. Over UDP, and for an error that sendRequest returns,
// written is not called.
func (t *TransportLayer) sendRequest(req *Message, dst Destination, written func(error)) error {
	switch dst.Transport {
	case UDP:
		if client := t.clientPort(dst.Addr); client != nil {
			// TS 33.203 7.1: a request to a protected peer leaves from
			// the protected client port.
			return client.SendRequest(req, dst.Addr)
		}
		return t.udp.SendRequest(req, dst.Addr)
	case TCP:
		return t.sendTCP(req, dst.Addr, written)
	}
	return fmt.Errorf("sip: transport %s is not supported", dst.Transport)
}

// SendResponse sends resp the way its top Via says (RFC 3261 18.2.2),
// and returns once it has gone out. Over TCP the response goes back on
// the connection that source, where the request came from, names; if
// that connection has closed, on a new one to the address that
// tcpResponseAddress gives.
func (t *TransportLayer) SendResponse(resp *Message, source Destination) error {
	return t.sendResponse(resp, source, nil)
}

// sendResponse is SendResponse, with written as for sendRequest.
func (t *TransportLayer) sendResponse(resp *Message, source Destination, written func(error)) error {
	v, err := ParseVia(resp.Get("Via"))
	if err != nil {
		return err
	}
	switch Transport(v.Transport) {
	case UDP:
		return t.udp.SendResponse(resp)
	case TCP:
		remote := source.Addr
		if source.Transport != TCP || !t.hasConnection(remote) {
			if remote, err = tcpResponseAddress(v); err != nil {
				return err
			}
		}
		return t.sendTCP(resp, remote, written)
	}
	return fmt.Errorf("sip: response over %s is not supported", v.Transport)
}

// UDPTransport is the transport layer of RFC 3261 18 over one UDP
// socket. Requests and responses go out from the address it listens on,
// which RFC 3581 4 needs for responses to cross a NAT.
type UDPTransport struct {
	conn    *net.UDPConn
	log     *slog.Logger
	observe Observer // nil when nothing observes
	// buf holds one datagram. RFC 3261 18.1.1: a UDP message can be as
	// large as 65,535 bytes.
	buf []byte
}

// ListenUDP opens the socket, e.g. on ":5060".
func ListenUDP(address string, log *slog.Logger) (*UDPTransport, error) {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	t := &UDPTransport{conn: conn, log: log, buf: make([]byte, 65535)}
	log.Info("sip listening", "transport", string(UDP), "address", t.LocalAddr().String())
	return t, nil
}

func (t *UDPTransport) LocalAddr() netip.AddrPort {
	return t.conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

// Close closes the socket. A Receive in progress returns an error.
func (t *UDPTransport) Close() error { return t.conn.Close() }

// SendRequest sends req to dst (RFC 3261 18.1.1). The caller picks dst
// from the Route set or the Request-URI; the transport does not look up
// DNS.
func (t *UDPTransport) SendRequest(req *Message, dst netip.AddrPort) error {
	return t.send(req, dst)
}

// SendResponse sends resp to the address that its top Via names (RFC
// 3261 18.2.2 with RFC 3581 4).
func (t *UDPTransport) SendResponse(resp *Message) error {
	dst, err := responseDestination(resp)
	if err != nil {
		return err
	}
	return t.send(resp, dst)
}

func (t *UDPTransport) send(m *Message, dst netip.AddrPort) error {
	wire := m.Bytes()
	record(t.log, t.observe, Capture{Transport: UDP, Sent: true, Source: t.LocalAddr(), Destination: dst, Message: m, Wire: wire})
	_, err := t.conn.WriteToUDPAddrPort(wire, dst)
	return err
}

// Receive returns the next message and the address it came from. A
// request has its top Via updated with received and rport first (RFC
// 3261 18.2.1, RFC 3581 4). Datagrams that are not SIP messages are
// logged and skipped. Receive must not be called from two goroutines at
// once.
func (t *UDPTransport) Receive() (*Message, netip.AddrPort, error) {
	for {
		n, src, err := t.conn.ReadFromUDPAddrPort(t.buf)
		if err != nil {
			return nil, netip.AddrPort{}, err
		}
		// A dual-stack socket reports an IPv4 peer as ::ffff:a.b.c.d.
		src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		if len(bytes.TrimSpace(t.buf[:n])) == 0 {
			// Softphones such as pjsua send a datagram of only CRLF to
			// keep a NAT binding open. RFC 5626 4.4.1 defines CRLF
			// keepalives for streams, and RFC 3261 7.5 has a stream
			// receiver ignore CRLF before a start line; a datagram of
			// nothing else is ignored the same way, with no warning.
			continue
		}
		m, err := Parse(t.buf[:n])
		if err != nil {
			// SIMPLIFIED: RFC 3261 18.3. A request whose body is
			// shorter than its Content-Length SHOULD get a 400. It is
			// dropped like any other datagram that does not parse,
			// because only a broken peer sends one and the
			// registration scenarios have none.
			t.log.Warn("discarded datagram that is not a SIP message", "src", src.String(), "bytes", n, "error", err.Error())
			continue
		}
		if m.IsRequest() {
			if err := stampVia(m, src); err != nil {
				t.log.Warn("discarded request without a usable Via", "src", src.String(), "error", err.Error())
				continue
			}
		}
		record(t.log, t.observe, Capture{Transport: UDP, Source: src, Destination: t.LocalAddr(), Message: m, Wire: bytes.Clone(t.buf[:n])})
		return m, src, nil
	}
}

// stampVia records in the top Via where the request really came from,
// so that the response can go back there through a NAT.
func stampVia(req *Message, src netip.AddrPort) error {
	v, err := ParseVia(req.Get("Via"))
	if err != nil {
		return err
	}
	received := src.Addr().String()
	if rport, ok := v.Params.Get("rport"); ok && rport == "" {
		// RFC 3581 4: fill in rport, and add received even when it
		// equals the sent-by host.
		v.Params.Set("rport", strconv.Itoa(int(src.Port())))
		v.Params.Set("received", received)
	} else if sentBy, err := netip.ParseAddr(v.Host); err != nil || sentBy.Unmap() != src.Addr() {
		// RFC 3261 18.2.1: add received when sent-by is a domain name
		// or an address other than the source.
		v.Params.Set("received", received)
	} else {
		return nil
	}
	req.SetFirst("Via", v.String())
	return nil
}

// responseDestination applies RFC 3261 18.2.2 for unreliable unicast
// transports, with the step RFC 3581 4 adds: received and rport win
// over sent-by.
//
// SIMPLIFIED: RFC 3261 18.2.2. The maddr bullet (multicast) is not
// implemented, and neither is the last bullet, which resolves a sent-by
// domain name with RFC 3263 5. Neither case arises: no node sends
// multicast, and Receive adds received to every request whose sent-by
// is a domain name.
func responseDestination(resp *Message) (netip.AddrPort, error) {
	v, err := ParseVia(resp.Get("Via"))
	if err != nil {
		return netip.AddrPort{}, err
	}
	if v.Transport != "UDP" {
		return netip.AddrPort{}, fmt.Errorf("sip: response over %s is not supported", v.Transport)
	}
	port := v.Port
	if port == 0 {
		port = defaultPort
	}
	host := v.Host
	if received, ok := v.Params.Get("received"); ok {
		host = received
		if rport, ok := v.Params.Get("rport"); ok && rport != "" {
			if port, err = strconv.Atoi(rport); err != nil || port <= 0 || port > 65535 {
				return netip.AddrPort{}, fmt.Errorf("sip: malformed rport %q", rport)
			}
		}
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, errors.New("sip: response Via names no IP address to send to")
	}
	return netip.AddrPortFrom(ip, uint16(port)), nil
}

// record logs c and hands it to observe, if there is one.
func record(log *slog.Logger, observe Observer, c Capture) {
	direction := "recv"
	if c.Sent {
		direction = "send"
	}
	logMessage(log, c.Transport, direction, c.Message, c.Source, c.Destination)
	if observe != nil {
		observe(c)
	}
}

func logMessage(log *slog.Logger, transport Transport, direction string, m *Message, src, dst netip.AddrPort) {
	attrs := []any{"direction", direction, "transport", string(transport)}
	if m.IsRequest() {
		attrs = append(attrs, "method", string(m.Method))
	} else {
		attrs = append(attrs, "status", m.StatusCode)
	}
	branch := ""
	if v, err := ParseVia(m.Get("Via")); err == nil {
		branch = v.Branch()
	}
	attrs = append(attrs,
		"call_id", m.Get("Call-ID"),
		"cseq", m.Get("CSeq"),
		"branch", branch,
		"src", src.String(),
		"dst", dst.String(),
	)
	log.Info("sip message", attrs...)
}
