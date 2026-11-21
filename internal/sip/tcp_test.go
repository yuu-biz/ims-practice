package sip

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func listenLayer(t *testing.T, log *slog.Logger) *TransportLayer {
	t.Helper()
	tr, err := Listen("127.0.0.1:0", log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

func tcpTo(addr netip.AddrPort) Destination { return Destination{Transport: TCP, Addr: addr} }

// stream is a raw TCP connection standing in for a peer.
type stream struct {
	conn net.Conn
	r    *bufio.Reader
}

func dial(t *testing.T, addr netip.AddrPort) *stream {
	t.Helper()
	c, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &stream{conn: c, r: bufio.NewReaderSize(c, maxStreamMessageSize)}
}

func (s *stream) localAddr() netip.AddrPort { return unmapped(s.conn.LocalAddr()) }

func (s *stream) write(t *testing.T, m *Message) {
	t.Helper()
	if _, err := s.conn.Write(m.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func (s *stream) read(t *testing.T, within time.Duration) (*Message, error) {
	t.Helper()
	s.conn.SetReadDeadline(time.Now().Add(within))
	m, _, err := readStreamMessage(s.r)
	return m, err
}

// rawTCPServer accepts connections and reads messages from them, which
// it hands to received, without ever answering.
func rawTCPServer(t *testing.T) (netip.AddrPort, <-chan *Message) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	received := make(chan *Message, 16)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
			go func() {
				r := bufio.NewReaderSize(c, maxStreamMessageSize)
				for {
					m, _, err := readStreamMessage(r)
					if err != nil {
						return
					}
					received <- m
				}
			}()
		}
	}()
	return unmapped(l.Addr()), received
}

func tcpRequest(from *stream, method Method) *Message {
	m := registerRequest()
	if method == INVITE {
		m = inviteRequest()
	}
	via := Via{Transport: "TCP", Host: "127.0.0.1", Port: int(from.localAddr().Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	m.Prepend("Via", via.String())
	return m
}

// chunks returns its parts one Read at a time, the way TCP may cut a
// stream into segments anywhere.
type chunks [][]byte

func (c *chunks) Read(p []byte) (int, error) {
	if len(*c) == 0 {
		return 0, io.EOF
	}
	n := copy(p, (*c)[0])
	if n == len((*c)[0]) {
		*c = (*c)[1:]
	} else {
		(*c)[0] = (*c)[0][n:]
	}
	return n, nil
}

func TestStreamFraming(t *testing.T) {
	first := mustParse(t, initialRegister)
	second := registerRequest()
	second.Body = []byte("v=0\r\n")
	second.Add("Content-Type", "application/sdp")
	both := append(first.Bytes(), second.Bytes()...)

	readAll := func(t *testing.T, parts ...[]byte) []*Message {
		t.Helper()
		c := chunks(parts)
		r := bufio.NewReaderSize(&c, maxStreamMessageSize)
		var got []*Message
		for {
			m, _, err := readStreamMessage(r)
			if errors.Is(err, io.EOF) {
				return got
			}
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, m)
		}
	}
	wantMessages := func(t *testing.T, got []*Message) {
		t.Helper()
		if len(got) != 2 || got[0].String() != first.String() || got[1].String() != second.String() {
			t.Fatalf("read %d messages, want the two that were written", len(got))
		}
	}

	t.Run("two messages in one read", func(t *testing.T) {
		wantMessages(t, readAll(t, both))
	})
	t.Run("messages split across reads", func(t *testing.T) {
		// Cut inside the header, inside the empty line, and inside the
		// body of the second message.
		head := len(first.Bytes()) + 20
		emptyLine := bytes.Index(both[head:], []byte("\r\n\r\n")) + head + 3
		wantMessages(t, readAll(t, both[:20], both[20:head], both[head:emptyLine], both[emptyLine:len(both)-2], both[len(both)-2:]))
	})
	t.Run("CRLF keepalives between messages", func(t *testing.T) {
		wantMessages(t, readAll(t, []byte("\r\n\r\n"), first.Bytes(), []byte("\r\n"), second.Bytes()))
	})

	for name, data := range map[string]string{
		"no Content-Length":          "OPTIONS sip:a SIP/2.0\r\nVia: SIP/2.0/TCP 127.0.0.1;branch=z9hG4bK1\r\n\r\n",
		"malformed Content-Length":   "OPTIONS sip:a SIP/2.0\r\nContent-Length: x\r\n\r\n",
		"body cut short":             "OPTIONS sip:a SIP/2.0\r\nl: 10\r\n\r\nabc",
		"header cut short":           "OPTIONS sip:a SIP/2.0\r\nCall-ID: x\r\n",
		"Content-Length past limit":  "OPTIONS sip:a SIP/2.0\r\nContent-Length: 70000\r\n\r\n",
		"Content-Length of MaxInt64": "OPTIONS sip:a SIP/2.0\r\nContent-Length: 9223372036854775807\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := bufio.NewReaderSize(strings.NewReader(data), maxStreamMessageSize)
			if m, _, err := readStreamMessage(r); err == nil || errors.Is(err, io.EOF) {
				t.Errorf("read %v, err %v; want a framing error", m, err)
			}
		})
	}
}

func TestRequestAndResponseOverTCP(t *testing.T) {
	type seen struct {
		via    Via
		source Destination
	}
	server := make(chan seen, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		v, _ := ParseVia(st.Request.Get("Via"))
		server <- seen{v, st.Source}
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	uac := newStack(t, testTimers, ignoreRequests)

	ct, err := uac.SendRequest(registerRequest(), tcpTo(uas.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, ct)
	if len(got) != 1 || got[0].StatusCode != 200 || ct.Err() != nil {
		t.Fatalf("responses = %v, err = %v; want the 200", got, ct.Err())
	}
	s := <-server
	if s.via.Transport != "TCP" || s.source.Transport != TCP {
		t.Errorf("Via transport %s, source %v; want both TCP", s.via.Transport, s.source)
	}
	if s.source.Addr == uac.LocalAddr() {
		t.Errorf("source %v is the client's listening port, want the port of the connection it opened", s.source)
	}
}

// RFC 3261 18.1.1: should the connection close, the server may open one
// to the address the request came from and the port of the sent-by,
// so a connection that the transport opens comes from the address it
// listens on, whatever address the host would pick for the route. Its
// port is one of the moment (18).
func TestConnectionComesFromTheListeningAddress(t *testing.T) {
	tr, err := Listen("127.0.0.2:0", testLogger(t))
	if err != nil {
		t.Skipf("no second loopback address on this host: %v", err)
	}
	t.Cleanup(func() { tr.Close() })
	uac, err := NewStack(tr, Config{Timers: testTimers, SentBy: tr.LocalAddr().String(), OnRequest: ignoreRequests}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uac.Close)
	sources := make(chan Destination, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		sources <- st.Source
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	ct, err := uac.SendRequest(registerRequest(), tcpTo(uas.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ct)
	if source := <-sources; source.Addr.Addr() != tr.LocalAddr().Addr() || source.Addr == tr.LocalAddr() {
		t.Errorf("connection came from %v, want the address %v with a port of the moment", source.Addr, tr.LocalAddr().Addr())
	}
}

// RFC 3261 18.2.2: the response goes back on the connection the request
// came on, not to the sent-by.
func TestResponseOverTheConnectionOfTheRequest(t *testing.T) {
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	peer := dial(t, uas.LocalAddr())
	req := tcpRequest(peer, REGISTER)
	// Nothing listens at this sent-by.
	req.SetFirst("Via", "SIP/2.0/TCP 127.0.0.1:9;branch="+NewBranch())
	peer.write(t, req)

	resp, err := peer.read(t, 2*time.Second)
	if err != nil {
		t.Fatalf("no response on the connection: %v", err)
	}
	if resp.StatusCode != 200 || resp.Get("Call-ID") != req.Get("Call-ID") {
		t.Errorf("got %d for %s, want the 200 for the REGISTER", resp.StatusCode, resp.Get("Call-ID"))
	}
}

// RFC 3261 18.2.2: when the connection of the request has closed, the
// response goes over a new one to the received address and the sent-by
// port.
func TestResponseOpensANewConnectionWhenTheOldOneClosed(t *testing.T) {
	answer := make(chan *ServerTransaction, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) { answer <- st })
	sentBy, received := rawTCPServer(t)
	peer := dial(t, uas.LocalAddr())
	req := tcpRequest(peer, REGISTER)
	req.SetFirst("Via", "SIP/2.0/TCP 127.0.0.1:"+strconv.Itoa(int(sentBy.Port()))+";branch="+NewBranch())
	peer.write(t, req)
	st := <-answer
	peer.conn.Close()
	// Wait until the stack has seen the connection close.
	for deadline := time.Now().Add(2 * time.Second); uas.transport.hasConnection(st.Source.Addr); {
		if time.Now().After(deadline) {
			t.Fatal("the stack kept the closed connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st.Respond(NewResponse(st.Request, 200, NewTag()))
	select {
	case resp := <-received:
		if resp.StatusCode != 200 {
			t.Errorf("got %d, want the 200", resp.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response at the sent-by")
	}
}

// RFC 3261 17.1.1.2 and 17.1.2.2: over TCP the client transaction sends
// its request once.
func TestNoRequestRetransmissionsOverTCP(t *testing.T) {
	addr, received := rawTCPServer(t)
	uac := newStack(t, testTimers, ignoreRequests)
	var sent []*ClientTransaction
	for _, req := range []*Message{registerRequest(), inviteRequest()} {
		ct, err := uac.SendRequest(req, tcpTo(addr))
		if err != nil {
			t.Fatal(err)
		}
		sent = append(sent, ct)
	}
	counts := map[Method]int{}
	for range sent {
		select {
		case m := <-received:
			counts[m.Method]++
		case <-time.After(2 * time.Second):
			t.Fatalf("server got %v, want both requests", counts)
		}
	}
	// Nobody answers, so each transaction ends with Timer F or B, 64*T1
	// later. Over UDP, Timers A and E would have resent each request
	// several times by then.
	for _, ct := range sent {
		if collect(t, ct); !errors.Is(ct.Err(), ErrTimeout) {
			t.Errorf("err = %v, want ErrTimeout", ct.Err())
		}
	}
	for len(received) > 0 {
		counts[(<-received).Method]++
	}
	if counts[REGISTER] != 1 || counts[INVITE] != 1 {
		t.Errorf("received %v, want each request once", counts)
	}
}

// RFC 3261 17.2.1: over TCP the INVITE server transaction sends its
// 300-699 response once, without Timer G.
func TestNoResponseRetransmissionsOverTCP(t *testing.T) {
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 486, NewTag()))
	})
	peer := dial(t, uas.LocalAddr())
	peer.write(t, tcpRequest(peer, INVITE))
	var busy int
	for {
		resp, err := peer.read(t, 400*time.Millisecond)
		if err != nil {
			break
		}
		if resp.StatusCode == 486 {
			busy++
		}
	}
	if busy != 1 {
		t.Errorf("got the 486 %d times, want once", busy)
	}
}

// largeRequest is a REGISTER that RFC 3261 18.1.1 keeps off UDP.
func largeRequest() *Message {
	req := registerRequest()
	req.Add("Content-Type", "text/plain")
	req.Body = bytes.Repeat([]byte("0123456789abcdef\r\n"), 100)
	return req
}

func TestLargeRequestGoesOverTCP(t *testing.T) {
	arrived := make(chan *ServerTransaction, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		arrived <- st
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	uac := newStack(t, testTimers, ignoreRequests)
	req := largeRequest()

	ct, err := uac.SendRequest(req, udpTo(uas.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, ct); len(got) != 1 || got[0].StatusCode != 200 {
		t.Fatalf("responses = %v, err = %v; want the 200", got, ct.Err())
	}
	st := <-arrived
	v, _ := ParseVia(st.Request.Get("Via"))
	if st.Source.Transport != TCP || v.Transport != "TCP" {
		t.Errorf("came over %v with Via %s, want TCP for both", st.Source, v.Transport)
	}
	if !bytes.Equal(st.Request.Body, req.Body) {
		t.Errorf("body of %d bytes arrived as %d bytes", len(req.Body), len(st.Request.Body))
	}
	if ct.destination.Transport != TCP {
		t.Errorf("client transaction runs over %v, want TCP", ct.destination)
	}
}

func TestSmallRequestStaysOnUDP(t *testing.T) {
	arrived := make(chan Destination, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		arrived <- st.Source
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	uac := newStack(t, testTimers, ignoreRequests)
	ct, err := uac.SendRequest(registerRequest(), udpTo(uas.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ct)
	if source := <-arrived; source.Transport != UDP {
		t.Errorf("came over %v, want UDP", source)
	}
}

// RFC 3261 18.1.1: when the peer refuses the TCP connection, the large
// request goes over UDP after all.
func TestLargeRequestFallsBackToUDP(t *testing.T) {
	peer := rawSocket(t)
	uac := newStack(t, testTimers, ignoreRequests)
	req := largeRequest()
	if _, err := uac.SendRequest(req, udpTo(addrOf(peer))); err != nil {
		t.Fatal(err)
	}
	got := readDatagram(t, peer)
	v, _ := ParseVia(got.Get("Via"))
	if v.Transport != "UDP" || !bytes.Equal(got.Body, req.Body) {
		t.Errorf("got Via %s and %d body bytes, want UDP and all %d", v.Transport, len(got.Body), len(req.Body))
	}
}

func TestIdleConnectionCloses(t *testing.T) {
	tr, err := newTransportLayer("127.0.0.1:0", testLogger(t), nil, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	// The idle timeout runs from the moment the transport accepts the
	// connection, which is before dial returns.
	start := time.Now()
	peer := dial(t, tr.LocalAddr())
	if _, err := peer.read(t, 2*time.Second); !errors.Is(err, io.EOF) {
		t.Fatalf("read: %v, want the connection closed", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("closed after %v, before the idle timeout", elapsed)
	}
}

func TestTCPLogsEachMessage(t *testing.T) {
	var mu sync.Mutex
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(lockedWriter{&mu, &logs}, nil))
	tr, err := Listen("127.0.0.1:0", log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	addr, received := rawTCPServer(t)
	if err := tr.SendRequest(mustParse(t, initialRegister), tcpTo(addr)); err != nil {
		t.Fatal(err)
	}
	<-received

	mu.Lock()
	defer mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if entry["msg"] == "sip message" {
			if entry["transport"] != "TCP" || entry["direction"] != "send" || entry["dst"] != addr.String() {
				t.Errorf("log %v, want a send over TCP to %v", entry, addr)
			}
			return
		}
	}
	t.Error("no sip message logged")
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
