package sip

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"syscall"
	"time"
)

// This file holds the TCP half of the transport layer (RFC 3261 18).
// The layer keeps one table of open connections, keyed by the address
// and port at the far end, and shares it between requests and
// responses in both directions: a connection it opened to send a
// request also brings back the responses, and one it accepted carries
// the responses to the requests that came over it. Since every entry
// is a TCP connection, the transport is left out of the key that RFC
// 3261 18 describes.
//
// Each far end also has a queue of the messages that wait to go to it,
// and, while any wait, a goroutine of its own that opens the
// connection when there is none and writes them in the order they came.
// A connect to a host that has gone takes tcpConnectTimeout to fail, and
// so may a write to a peer that has stopped reading; only the messages
// to that far end wait for it. The stack's loop hands its messages over
// and goes on, and hears of a failure later (RFC 3261 18.4).

// tcpIdleTimeout is how long a connection stays open after the last
// message sent or received on it. RFC 3261 18 asks for at least the
// time a transaction may take, so that its request, responses and ACK
// share one connection. The longest here is an INVITE through a proxy:
// Timer C, 3.5 minutes by default, and then Timer L, 64*T1 = 32 s.
// Five minutes covers both.
const tcpIdleTimeout = 5 * time.Minute

// tcpConnectTimeout bounds opening a connection and writing one message
// to it. A peer that has gone away then shows up as a transport error
// (RFC 3261 18.4) within a few seconds, well before Timer B or F.
const tcpConnectTimeout = 2 * time.Second

// maxTCPQueue bounds the messages that wait to go to one far end over
// TCP, behind the one being written. They leave at once while the
// connection is open, and pile up only while a connect or a write
// waits. A node of the network is one far end for the messages of all
// its users, so the bound leaves room for a burst of them, as
// maxAdmitQueue does for the requests from one source. A message that
// finds the queue full fails at once, as a transport error.
var maxTCPQueue = 512

var errTCPQueueFull = errors.New("sip: too many messages wait for the TCP connection to this far end")

// tcpSend is a message in the queue of a far end, and what to call with
// the outcome once it has been written or has failed.
type tcpSend struct {
	m    *Message
	done func(error)
	// orElse is set for a response that goes back on the connection of
	// its request: where it goes instead, should that connection close
	// before the response is written (RFC 3261 18.2.2).
	orElse netip.AddrPort
}

// maxStreamMessageSize bounds one message on a stream. RFC 3261 18.1.1
// has every element handle messages up to 65,535 bytes, the UDP
// maximum; a peer that sends a longer one has its connection closed.
const maxStreamMessageSize = 65535

// errNoContentLength is a stream message without Content-Length, which
// leaves no way to tell where its body ends (RFC 3261 18.3).
var errNoContentLength = errors.New("sip: message on a stream without Content-Length")

// readStreamMessage reads the next message from a stream (RFC 3261
// 18.3): the header up to the empty line, and then as many body bytes
// as Content-Length says. Empty lines before the start line are
// skipped (RFC 3261 7.5). It returns the bytes of the message as well.
// Any error leaves the stream at an unknown place, so the caller closes
// the connection.
func readStreamMessage(r *bufio.Reader) (*Message, []byte, error) {
	var head []byte
	for {
		// r holds maxStreamMessageSize bytes, so a longer line fails
		// with bufio.ErrBufferFull.
		line, err := r.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && len(head) > 0 {
				return nil, nil, io.ErrUnexpectedEOF
			}
			return nil, nil, err
		}
		if len(head) == 0 && bytes.Equal(line, []byte("\r\n")) {
			continue
		}
		head = append(head, line...)
		if len(head) > maxStreamMessageSize {
			return nil, nil, fmt.Errorf("sip: header longer than %d bytes", maxStreamMessageSize)
		}
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
	}
	m, err := parseHeader(bytes.TrimSuffix(head, []byte("\r\n\r\n")))
	if err != nil {
		return nil, nil, err
	}
	n, ok, err := m.contentLength()
	switch {
	case err != nil:
		return nil, nil, err
	case !ok:
		return nil, nil, errNoContentLength
	// n can be as large as an int goes, so len(head)+n could overflow.
	case n > maxStreamMessageSize-len(head):
		return nil, nil, fmt.Errorf("sip: message longer than %d bytes", maxStreamMessageSize)
	}
	m.Body = make([]byte, n)
	if _, err := io.ReadFull(r, m.Body); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, io.ErrUnexpectedEOF
		}
		return nil, nil, err
	}
	return m, append(head, m.Body...), nil
}

func (t *TransportLayer) acceptTCP() {
	defer t.wg.Done()
	for {
		c, err := t.listener.AcceptTCP()
		if err != nil {
			select {
			case <-t.closed:
			default:
				t.log.Error("sip transport failed", "transport", string(TCP), "error", err.Error())
			}
			return
		}
		t.addConnection(c, unmapped(c.RemoteAddr()), "accepted")
	}
}

// addConnection puts c in the table and starts reading it. When a
// connection to remote is in the table already, because two senders
// opened one at the same time, c is closed and the other one kept.
func (t *TransportLayer) addConnection(c *net.TCPConn, remote netip.AddrPort, event string) (*net.TCPConn, error) {
	t.mu.Lock()
	if t.conns == nil {
		t.mu.Unlock()
		c.Close()
		return nil, net.ErrClosed
	}
	if existing, ok := t.conns[remote]; ok {
		t.mu.Unlock()
		c.Close()
		return existing, nil
	}
	t.conns[remote] = c
	t.wg.Add(1)
	t.mu.Unlock()
	t.logConnection(event, c, remote, "")
	go t.readTCP(c, remote)
	return c, nil
}

func (t *TransportLayer) dropConnection(c *net.TCPConn, remote netip.AddrPort, reason string) {
	c.Close()
	t.mu.Lock()
	if t.conns[remote] == c {
		delete(t.conns, remote)
	}
	t.mu.Unlock()
	t.logConnection("closed", c, remote, reason)
}

// readTCP reads the messages of one connection until it closes. A
// connection on which nothing was sent or received for tcpIdleTimeout
// is closed here too, by the read deadline that every message moves.
func (t *TransportLayer) readTCP(c *net.TCPConn, remote netip.AddrPort) {
	defer t.wg.Done()
	r := bufio.NewReaderSize(c, maxStreamMessageSize)
	for {
		c.SetReadDeadline(time.Now().Add(t.idleTimeout))
		m, wire, err := readStreamMessage(r)
		if err != nil {
			t.dropConnection(c, remote, closeReason(err))
			return
		}
		if m.IsRequest() {
			if err := stampVia(m, remote); err != nil {
				t.log.Warn("discarded request without a usable Via", "src", remote.String(), "error", err.Error())
				continue
			}
		}
		record(t.log, t.observe, Capture{Transport: TCP, Source: remote, Destination: unmapped(c.LocalAddr()), Message: m, Wire: wire})
		if !t.deliver(incoming{msg: m, source: Destination{Transport: TCP, Addr: remote}}) {
			return
		}
	}
}

func closeReason(err error) string {
	switch {
	case errors.Is(err, io.EOF):
		return "closed by the peer"
	case errors.Is(err, os.ErrDeadlineExceeded):
		return "idle"
	case errors.Is(err, net.ErrClosed):
		return "closed locally"
	}
	return err.Error()
}

// connectTCP returns the open connection to remote, or opens one.
func (t *TransportLayer) connectTCP(remote netip.AddrPort) (*net.TCPConn, error) {
	t.mu.Lock()
	c, ok := t.conns[remote]
	t.mu.Unlock()
	if ok {
		return c, nil
	}
	dialer := net.Dialer{Timeout: tcpConnectTimeout}
	if local := unmapped(t.listener.Addr()).Addr(); !local.IsUnspecified() && local.Is4() == remote.Addr().Is4() {
		// RFC 3261 18.1.1: should this connection close, the peer may
		// open one to the address the request came from, at the port of
		// the sent-by, so the connection comes from the address the
		// transport listens on. The port stays one of the moment (18),
		// except on a protected client port, from which TS 33.203 7.1
		// has the connections to the other end's protected server port
		// come.
		addr := &net.TCPAddr{IP: local.AsSlice()}
		if client := t.clientPort(remote); client != nil {
			addr.Port = int(client.LocalAddr().Port())
			// The protected client port of a P-CSCF carries a
			// connection to each UE at once, which the systems that
			// have it let one port do only with SO_REUSEADDR. A
			// connection to a UE that this one had and closed less than
			// a couple of minutes ago is refused all the same, since
			// the pair of addresses and ports is the same one and is
			// still in TIME_WAIT; the request fails, and the UE or the
			// network sends it again.
			dialer.Control = func(_, _ string, c syscall.RawConn) error {
				var set error
				if err := c.Control(func(fd uintptr) { set = setReuseAddr(fd) }); err != nil {
					return err
				}
				return set
			}
		}
		dialer.LocalAddr = addr
	}
	nc, err := dialer.DialContext(t.ctx, "tcp", remote.String())
	if err != nil {
		return nil, err
	}
	return t.addConnection(nc.(*net.TCPConn), remote, "opened")
}

func (t *TransportLayer) hasConnection(remote netip.AddrPort) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.conns[remote]
	return ok
}

// sendTCP puts s in the queue of remote (queueTCP). With s.done nil, it
// then waits for the write and returns its outcome. Otherwise it returns
// at once, and s.done gets the outcome from the goroutine that writes.
func (t *TransportLayer) sendTCP(s tcpSend, remote netip.AddrPort) error {
	if s.done != nil {
		return t.queueTCP(s, remote)
	}
	outcome := make(chan error, 1)
	s.done = func(err error) { outcome <- err }
	if err := t.queueTCP(s, remote); err != nil {
		return err
	}
	return <-outcome
}

// queueTCP puts s at the end of the queue of remote, and starts the
// goroutine that writes the queue when there is none. s.done gets the
// outcome from that goroutine: nil once s.m has been written. When
// queueTCP returns an error, s is not queued and s.done is not called.
func (t *TransportLayer) queueTCP(s tcpSend, remote netip.AddrPort) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conns == nil {
		return net.ErrClosed
	}
	queue, writing := t.queues[remote]
	if len(queue) >= maxTCPQueue {
		return errTCPQueueFull
	}
	t.queues[remote] = append(queue, s)
	if !writing {
		t.wg.Add(1)
		go t.writeQueued(remote)
	}
	return nil
}

// writeQueued writes the messages in the queue of remote, oldest first,
// and ends once the queue is empty, so that a far end has a goroutine
// only while messages wait for it.
func (t *TransportLayer) writeQueued(remote netip.AddrPort) {
	defer t.wg.Done()
	for {
		next, ok := t.nextTCP(remote)
		if !ok {
			return
		}
		if next.orElse.IsValid() && !t.hasConnection(remote) {
			t.redirect(next)
			continue
		}
		c, err := t.connectTCP(remote)
		if err != nil {
			// Every message in the queue waits for this connection, and
			// each would wait as long again for a connect of its own, so
			// they all fail with this one. A response that was to go back
			// on the connection of its request goes elsewhere instead.
			t.mu.Lock()
			waiting := append([]tcpSend{next}, t.queues[remote]...)
			t.queues[remote] = nil
			t.mu.Unlock()
			for _, s := range waiting {
				if s.orElse.IsValid() {
					t.redirect(s)
				} else {
					s.done(err)
				}
			}
			continue
		}
		next.done(t.writeTCP(c, next.m, remote))
	}
}

// redirect sends s, a response whose request came over a connection that
// has closed, over a new connection to s.orElse (RFC 3261 18.2.2).
func (t *TransportLayer) redirect(s tcpSend) {
	if err := t.queueTCP(tcpSend{m: s.m, done: s.done}, s.orElse); err != nil {
		s.done(err)
	}
}

// nextTCP takes the oldest message out of the queue of remote. Once the
// queue is empty, it takes remote out of t.queues and reports false, so
// that the next message for remote starts a goroutine again.
func (t *TransportLayer) nextTCP(remote netip.AddrPort) (tcpSend, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	queue := t.queues[remote]
	if len(queue) == 0 {
		delete(t.queues, remote)
		return tcpSend{}, false
	}
	t.queues[remote] = queue[1:]
	return queue[0], true
}

// writeTCP writes m to c, the connection to remote.
func (t *TransportLayer) writeTCP(c *net.TCPConn, m *Message, remote netip.AddrPort) error {
	wire := m.Bytes()
	record(t.log, t.observe, Capture{Transport: TCP, Sent: true, Source: unmapped(c.LocalAddr()), Destination: remote, Message: m, Wire: wire})
	c.SetWriteDeadline(time.Now().Add(tcpConnectTimeout))
	if _, err := c.Write(wire); err != nil {
		// The connection is of no more use. It leaves the table at once,
		// so that the next message to remote opens a new one; the reader
		// sees it closed, and logs that, once it reads again.
		t.mu.Lock()
		if t.conns[remote] == c {
			delete(t.conns, remote)
		}
		t.mu.Unlock()
		c.Close()
		return err
	}
	c.SetReadDeadline(time.Now().Add(t.idleTimeout))
	return nil
}

// tcpResponseAddress is where RFC 3261 18.2.2 opens a new connection
// for a response whose request came over a connection that has closed
// since: the address in received, or else the sent-by host, and the
// sent-by port.
//
// SIMPLIFIED: RFC 3261 18.2.2. When that connection cannot be opened
// either, the server SHOULD look the sent-by up with RFC 3263 5 and try
// the servers it gives. Here the response is lost, as it would be over
// UDP: every sent-by in this project is an IP address, or has a
// received parameter next to it, so DNS would give the same address.
func tcpResponseAddress(v Via) (netip.AddrPort, error) {
	host := v.Host
	if received, ok := v.Params.Get("received"); ok {
		host = received
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, errors.New("sip: response Via names no IP address to connect to")
	}
	port := v.Port
	if port == 0 {
		port = defaultPort
	}
	return netip.AddrPortFrom(ip, uint16(port)), nil
}

// unmapped is the address of one end of a connection. A dual-stack
// socket reports an IPv4 peer as ::ffff:a.b.c.d.
func unmapped(a net.Addr) netip.AddrPort {
	ap := a.(*net.TCPAddr).AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

func (t *TransportLayer) logConnection(event string, c *net.TCPConn, remote netip.AddrPort, reason string) {
	attrs := []any{"transport", string(TCP), "event", event, "local", unmapped(c.LocalAddr()).String(), "remote", remote.String()}
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	t.log.Info("sip connection", attrs...)
}

// listenTCP listens on the host of address and on port, the port that
// UDP listens on (RFC 3261 18.2.1).
func listenTCP(address string, port uint16) (*net.TCPListener, error) {
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}
	addr.Port = int(port)
	return net.ListenTCP("tcp", addr)
}

// portOf is the port in address, 0 when the system is to choose it.
func portOf(address string) int {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}
