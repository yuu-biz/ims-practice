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
	if v, _ := ParseVia(ct.Request.Get("Via")); v.Transport != "TCP" {
		t.Errorf("client transaction sent its request with Via %s, want TCP", v.Transport)
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

// The messages that the stack sends to one far end over TCP arrive in
// the order the TU handed them over, as they would over the one
// connection that RFC 3261 18 has them share: a peer sees, say, the 180
// (Ringing) of a call before its 200.
func TestMessagesToOneTCPFarEndKeepTheirOrder(t *testing.T) {
	const messages = 50
	t.Run("requests", func(t *testing.T) {
		addr, received := rawTCPServer(t)
		uac := newStack(t, testTimers, ignoreRequests)
		var sent []string
		for range messages {
			req := registerRequest()
			if _, err := uac.SendRequest(req, tcpTo(addr)); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, req.Get("Call-ID"))
		}
		for i, want := range sent {
			select {
			case m := <-received:
				if got := m.Get("Call-ID"); got != want {
					t.Fatalf("request %d arrived with Call-ID %s, want %s", i, got, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%d of %d requests arrived", i, messages)
			}
		}
	})
	t.Run("responses", func(t *testing.T) {
		// The reason phrase is free text (RFC 3261 21), so it numbers
		// the provisional responses.
		uas := newStack(t, testTimers, func(st *ServerTransaction) {
			tag := NewTag()
			for i := range messages {
				resp := NewResponse(st.Request, 183, tag)
				resp.Reason = "Session Progress " + strconv.Itoa(i)
				st.Respond(resp)
			}
			st.Respond(NewResponse(st.Request, 200, tag))
		})
		peer := dial(t, uas.LocalAddr())
		peer.write(t, tcpRequest(peer, INVITE))
		next := 0
		for next <= messages {
			resp, err := peer.read(t, 2*time.Second)
			if err != nil {
				t.Fatalf("%d of %d responses arrived: %v", next, messages+1, err)
			}
			want := "Session Progress " + strconv.Itoa(next)
			switch {
			case resp.StatusCode == 100:
				// The INVITE server transaction sends it itself when the
				// TU is slow (RFC 3261 17.2.1).
				continue
			case next == messages && resp.StatusCode != 200:
				t.Fatalf("got %d %s last, want the 200", resp.StatusCode, resp.Reason)
			case next < messages && resp.Reason != want:
				t.Fatalf("got %d %s, want %s", resp.StatusCode, resp.Reason, want)
			}
			next++
		}
	})
}

// A peer whose host has gone answers no TCP connect, and a connect to
// it waits tcpConnectTimeout before it fails. Meanwhile the stack goes
// on with its other peers, and its timers fire on time.
func TestTCPPeerThatAnswersNoConnectHoldsUpNothingElse(t *testing.T) {
	silent := silentTCPAddress(t)
	const requests = 3
	held := make(chan *ServerTransaction, requests)
	uas := newStack(t, testTimers, answerOptions(func(st *ServerTransaction) { held <- st }))
	// The peer sends its REGISTERs over a connection of its own and then
	// goes. Once that connection has closed, the responses go over a new
	// one to the sent-by (RFC 3261 18.2.2), where no connect is answered.
	peer := dial(t, uas.LocalAddr())
	var pending []*ServerTransaction
	for range requests {
		req := tcpRequest(peer, REGISTER)
		req.SetFirst("Via", "SIP/2.0/TCP "+silent.String()+";branch="+NewBranch())
		peer.write(t, req)
		select {
		case st := <-held:
			pending = append(pending, st)
		case <-time.After(2 * time.Second):
			t.Fatal("the REGISTER never reached the TU")
		}
	}
	peer.conn.Close()
	for deadline := time.Now().Add(2 * time.Second); uas.transport.hasConnection(pending[0].Source.Addr); {
		if time.Now().After(deadline) {
			t.Fatal("the stack kept the closed connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	within := start.Add(tcpConnectTimeout / 2)
	for _, st := range pending {
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	}
	other := newUDPPeer(t, uas.LocalAddr())
	options := other.request(OPTIONS)
	other.send(options)
	buf := make([]byte, 65535)
	other.conn.SetReadDeadline(within)
	if n, err := other.conn.Read(buf); err != nil {
		t.Fatalf("another peer got no answer to its OPTIONS within %v: %v", within.Sub(start), err)
	} else if resp, err := Parse(buf[:n]); err != nil || resp.StatusCode != 200 || resp.Get("Call-ID") != options.Get("Call-ID") {
		t.Fatalf("another peer got %q, want the 200 for its OPTIONS", buf[:n])
	}
	// Timer E sends the stack's own request again T1 after it went out.
	quiet := rawSocket(t)
	if _, err := uas.SendRequest(registerRequest(), udpTo(addrOf(quiet))); err != nil {
		t.Fatal(err)
	}
	quiet.SetReadDeadline(within)
	for _, what := range []string{"request", "retransmission of the request"} {
		if _, err := quiet.Read(buf); err != nil {
			t.Fatalf("the %s to a peer over UDP did not come within %v: %v", what, within.Sub(start), err)
		}
	}
}

// A request that the stack's loop hands to the transport without
// waiting has still failed at its target when it cannot go out over
// TCP, as a transport error (RFC 3261 17.1.4), and goes on to the next
// target (RFC 3263 4.3): when the queue to the far end is full, at
// once, and when its connection fails while it waits, once it has.
func TestRequestThatCannotGoOutOverTCPGoesToTheNextTarget(t *testing.T) {
	// Registered before the stack's own cleanup, it runs after the stack
	// has closed.
	bound := maxTCPQueue
	t.Cleanup(func() { maxTCPQueue = bound })
	maxTCPQueue = 1

	far, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { far.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := far.Accept(); err == nil {
			accepted <- c
		}
	}()
	answered := make(chan string, 2)
	next := newStack(t, testTimers, func(st *ServerTransaction) {
		answered <- st.Request.Get("Call-ID")
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})

	// The observer stands in for a write that takes long, as one to a
	// peer that has stopped reading does: it holds the write of slow
	// until released.
	slow := registerRequest()
	writing := make(chan struct{}, 1)
	release := make(chan struct{})
	tr, err := ListenObserved("127.0.0.1:0", testLogger(t), func(c Capture) {
		if c.Sent && c.Transport == TCP && c.Message.Get("Call-ID") == slow.Get("Call-ID") {
			writing <- struct{}{}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// Timer F fires 32 s after a request goes out, far later than this
	// test waits, so no request goes to the next target by timing out.
	uac, err := NewStack(tr, Config{SentBy: tr.LocalAddr().String(), OnRequest: ignoreRequests}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uac.Close)
	farEnd := unmapped(far.Addr())
	targets := []Destination{tcpTo(farEnd), udpTo(next.LocalAddr())}

	stalled, err := uac.SendRequest(slow, tcpTo(farEnd))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-writing:
	case <-time.After(2 * time.Second):
		t.Fatal("the transport never wrote the first request")
	}
	waiting, err := uac.SendRequest(registerRequest(), targets...)
	if err != nil {
		t.Fatal(err)
	}
	// Behind the request being written, the queue has room for one.
	refused, err := uac.SendRequest(registerRequest(), targets...)
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, refused); len(got) != 1 || got[0].StatusCode != 200 {
		t.Fatalf("the request that found the queue full got %v, err %v; want the 200 of the next target", got, refused.Err())
	}
	if callID := <-answered; callID != refused.Request.Get("Call-ID") {
		t.Fatalf("the next target answered %s, want the request that found the queue full", callID)
	}

	// The far end goes, and with it the connection that both requests
	// wait for.
	far.Close()
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the far end never accepted the connection")
	}
	for deadline := time.Now().Add(2 * time.Second); uac.transport.hasConnection(farEnd); {
		if time.Now().After(deadline) {
			t.Fatal("the stack kept the closed connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if collect(t, stalled); stalled.Err() == nil || errors.Is(stalled.Err(), ErrTimeout) {
		t.Errorf("the request whose write failed ended with %v, want the transport error", stalled.Err())
	}
	if got := collect(t, waiting); len(got) != 1 || got[0].StatusCode != 200 {
		t.Fatalf("the request that waited for the connection got %v, err %v; want the 200 of the next target", got, waiting.Err())
	}
	if callID := <-answered; callID != waiting.Request.Get("Call-ID") {
		t.Fatalf("the next target answered %s, want the request that waited for the connection", callID)
	}
}

// RFC 3261 18.2.2 has a response go back on the connection of its
// request while that is open, and over a new connection to the sent-by
// once it has closed. A response that waits behind another while the
// connection closes goes to the sent-by too.
func TestQueuedResponseGoesToTheSentByOnceItsConnectionCloses(t *testing.T) {
	sentBy, atSentBy := rawTCPServer(t)
	// The observer stands in for a write that takes long: it holds the
	// write of the response to slow until released.
	slow, waiting := registerRequest(), registerRequest()
	slowCallID := slow.Get("Call-ID")
	writing := make(chan struct{}, 1)
	release := make(chan struct{})
	tr, err := ListenObserved("127.0.0.1:0", testLogger(t), func(c Capture) {
		if c.Sent && c.Transport == TCP && !c.Message.IsRequest() && c.Message.Get("Call-ID") == slowCallID {
			writing <- struct{}{}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	arrived := make(chan *ServerTransaction, 2)
	onRequest := answerOptions(func(st *ServerTransaction) { arrived <- st })
	uas, err := NewStack(tr, Config{Timers: testTimers, SentBy: tr.LocalAddr().String(), OnRequest: onRequest}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uas.Close)

	peer := dial(t, uas.LocalAddr())
	byCallID := map[string]*ServerTransaction{}
	for _, req := range []*Message{slow, waiting} {
		req.Prepend("Via", "SIP/2.0/TCP "+sentBy.String()+";branch="+NewBranch())
		peer.write(t, req)
		select {
		case st := <-arrived:
			byCallID[st.Request.Get("Call-ID")] = st
		case <-time.After(2 * time.Second):
			t.Fatal("the REGISTER never reached the TU")
		}
	}
	first, second := byCallID[slowCallID], byCallID[waiting.Get("Call-ID")]
	first.Respond(NewResponse(first.Request, 200, NewTag()))
	select {
	case <-writing:
	case <-time.After(2 * time.Second):
		t.Fatal("the transport never wrote the first response")
	}
	second.Respond(NewResponse(second.Request, 200, NewTag()))
	// The stack takes what comes to it in order, so once it has answered
	// this OPTIONS it has handed the response over, while the connection
	// was still open.
	newUDPPeer(t, uas.LocalAddr()).sync()
	peer.conn.Close()
	for deadline := time.Now().Add(2 * time.Second); uas.transport.hasConnection(second.Source.Addr); {
		if time.Now().After(deadline) {
			t.Fatal("the stack kept the closed connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	select {
	case resp := <-atSentBy:
		if resp.StatusCode != 200 || resp.Get("Call-ID") != waiting.Get("Call-ID") {
			t.Errorf("the sent-by got %d for %s, want the 200 for the response that waited", resp.StatusCode, resp.Get("Call-ID"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the response that waited never reached the sent-by")
	}
}

// talkingTCPServer accepts connections, hands every message it reads to
// the returned channel, and answers the first message on a connection
// with an OPTIONS of its own over that connection.
func talkingTCPServer(t *testing.T) (netip.AddrPort, <-chan *Message) {
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
				for first := true; ; first = false {
					m, _, err := readStreamMessage(r)
					if err != nil {
						return
					}
					received <- m
					if first {
						options := registerRequest()
						options.Method = OPTIONS
						options.SetFirst("CSeq", "1 OPTIONS")
						options.Prepend("Via", "SIP/2.0/TCP "+unmapped(c.LocalAddr()).String()+";branch="+NewBranch())
						c.Write(options.Bytes())
					}
				}
			}()
		}
	}()
	return unmapped(l.Addr()), received
}

func receiveCallID(t *testing.T, received <-chan *Message, callID string) {
	t.Helper()
	select {
	case m := <-received:
		if got := m.Get("Call-ID"); got != callID {
			t.Fatalf("the far end got %s, want %s", got, callID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("the far end never got %s", callID)
	}
}

// breakWrites shuts the transport's own connection to remote for
// writing, so that the next write on it fails, as one does on a
// connection that the peer has reset. The connection stays open for
// reading.
func breakWrites(t *testing.T, tr *TransportLayer, remote netip.AddrPort) {
	t.Helper()
	tr.mu.Lock()
	c := tr.conns[remote]
	tr.mu.Unlock()
	if c == nil {
		t.Fatalf("no connection to %v", remote)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
}

// A write that fails leaves its connection of no more use, so the next
// message to the far end goes over a new connection, rather than fail
// on the broken one too.
func TestMessageAfterAFailedWriteOpensANewConnection(t *testing.T) {
	far, received := talkingTCPServer(t)
	tr, captures := listenObserved(t)
	first, second, third := mustParse(t, initialRegister), registerRequest(), registerRequest()
	first.SetFirst("Call-ID", NewCallID())
	if err := tr.SendRequest(first, tcpTo(far)); err != nil {
		t.Fatal(err)
	}
	receiveCallID(t, received, first.Get("Call-ID"))
	// The reader of the connection has read the OPTIONS of the far end
	// once it is observed, and then waits for a Receive that never comes,
	// so it does not see the connection close.
	for c := nextCapture(t, captures); c.Sent; c = nextCapture(t, captures) {
	}
	breakWrites(t, tr, far)
	if err := tr.SendRequest(second, tcpTo(far)); err == nil {
		t.Fatal("a write on a connection shut for writing went out")
	}
	if err := tr.SendRequest(third, tcpTo(far)); err != nil {
		t.Fatalf("the request after the failed write failed too: %v", err)
	}
	receiveCallID(t, received, third.Get("Call-ID"))
}

// A connect that gets no answer fails every message that waits for it,
// rather than one after the other, a connect for each.
func TestMessagesThatWaitForAConnectFailWithIt(t *testing.T) {
	silent := silentTCPAddress(t)
	tr := listenLayer(t, testLogger(t))
	const messages = 3
	failed := make(chan error, messages)
	start := time.Now()
	for range messages {
		req := mustParse(t, initialRegister)
		go func() { failed <- tr.SendRequest(req, tcpTo(silent)) }()
	}
	for range messages {
		select {
		case err := <-failed:
			if err == nil {
				t.Fatal("a message to an address that answers no connect went out")
			}
		case <-time.After(messages * tcpConnectTimeout):
			t.Fatal("a message to an address that answers no connect never failed")
		}
	}
	if elapsed := time.Since(start); elapsed > tcpConnectTimeout+tcpConnectTimeout/2 {
		t.Errorf("the last of %d messages failed after %v, want about one connect timeout, %v", messages, elapsed, tcpConnectTimeout)
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
