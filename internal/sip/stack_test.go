package sip

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Short timers keep the tests fast. Retransmissions then go out at 20,
// 60, 140, 300 ms, ... and Timer F fires at 1.28 s.
var testTimers = Timers{T1: 20 * time.Millisecond, T2: 160 * time.Millisecond, T4: 100 * time.Millisecond}

func newStack(t *testing.T, timers Timers, onRequest func(*ServerTransaction)) *Stack {
	t.Helper()
	return newStackWith(t, Config{Timers: timers, OnRequest: onRequest})
}

// newStackWith fills in SentBy with the address the stack listens on.
func newStackWith(t *testing.T, cfg Config) *Stack {
	t.Helper()
	tr := listenLayer(t, testLogger(t))
	cfg.SentBy = tr.LocalAddr().String()
	s, err := NewStack(tr, cfg, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func ignoreRequests(*ServerTransaction) {}

func udpTo(addr netip.AddrPort) Destination { return Destination{Transport: UDP, Addr: addr} }

func registerRequest() *Message {
	m := &Message{Method: REGISTER, RequestURI: "sip:" + imsDomain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<sip:001010000000001@"+imsDomain+">;tag="+NewTag())
	m.Add("To", "<sip:001010000000001@"+imsDomain+">")
	m.Add("Call-ID", NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<sip:001010000000001@127.0.0.1>")
	return m
}

// relay forwards datagrams between one client and a server, like a NAT
// on the path, and can drop the first requests or responses other than
// 100 (Trying) that it sees.
type relay struct {
	conn   *net.UDPConn
	server netip.AddrPort

	mu            sync.Mutex
	client        netip.AddrPort
	dropRequests  int
	dropResponses int
	requests      int // seen, dropped or not
	responses     int
}

func newRelay(t *testing.T, server netip.AddrPort, dropRequests, dropResponses int) *relay {
	r := &relay{conn: rawSocket(t), server: server, dropRequests: dropRequests, dropResponses: dropResponses}
	go r.run()
	return r
}

func (r *relay) addr() netip.AddrPort { return addrOf(r.conn) }

func (r *relay) run() {
	buf := make([]byte, 65535)
	for {
		n, src, err := r.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		isResponse := bytes.HasPrefix(buf[:n], []byte(SIPVersion+" "))
		r.mu.Lock()
		var dst netip.AddrPort
		drop := false
		if isResponse {
			r.responses++
			dst = r.client
			// A 100 (Trying) goes through: a server sends it only when
			// it is slow to answer, so it is not the response a test
			// wants to lose.
			if r.dropResponses > 0 && !bytes.HasPrefix(buf[:n], []byte(SIPVersion+" 100 ")) {
				r.dropResponses--
				drop = true
			}
		} else {
			r.requests++
			r.client, dst = src, r.server
			if r.dropRequests > 0 {
				r.dropRequests--
				drop = true
			}
		}
		r.mu.Unlock()
		if !drop {
			r.conn.WriteToUDPAddrPort(buf[:n], dst)
		}
	}
}

func (r *relay) counts() (requests, responses int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests, r.responses
}

// collect reads a client transaction to its end.
func collect(t *testing.T, ct *ClientTransaction) []*Message {
	t.Helper()
	var got []*Message
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-ct.Responses():
			if !ok {
				return got
			}
			got = append(got, m)
		case <-timeout:
			t.Fatal("client transaction did not end")
		}
	}
}

func TestRetransmissionRecoversLostDatagrams(t *testing.T) {
	var calls atomic.Int32
	viaSeen := make(chan string, 1)
	server := newStack(t, testTimers, func(st *ServerTransaction) {
		calls.Add(1)
		viaSeen <- st.Request.Get("Via")
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	r := newRelay(t, server.transport.LocalAddr(), 1, 1)
	client := newStack(t, testTimers, ignoreRequests)

	start := time.Now()
	ct, err := client.SendRequest(registerRequest(), udpTo(r.addr()))
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, ct)
	elapsed := time.Since(start)

	if ct.Err() != nil || len(got) != 1 || got[0].StatusCode != 200 {
		t.Fatalf("responses = %v, err = %v; want one 200", got, ct.Err())
	}
	// The first request is lost, so the server first sees the Timer E
	// retransmission at T1. Its 200 is lost too, so the client only gets
	// the 200 that the server resends for the next retransmission, at
	// T1 + 2*T1.
	if second := 3 * testTimers.T1; elapsed < second {
		t.Errorf("200 arrived after %v, before the second retransmission at %v", elapsed, second)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("OnRequest called %d times, want 1: retransmissions belong to the server transaction", n)
	}
	if requests, responses := r.counts(); requests < 3 || responses < 2 {
		t.Errorf("relay saw %d requests and %d responses, want at least 3 and 2", requests, responses)
	}
	if got[0].Get("Call-ID") != ct.Request.Get("Call-ID") {
		t.Errorf("response Call-ID %q, want %q", got[0].Get("Call-ID"), ct.Request.Get("Call-ID"))
	}

	// The server saw the relay as the source, which is what rport is for.
	via, err := ParseVia(<-viaSeen)
	if err != nil {
		t.Fatal(err)
	}
	wantRPort := strconv.Itoa(int(r.addr().Port()))
	if rport, _ := via.Params.Get("rport"); rport != wantRPort {
		t.Errorf("rport = %q, want the relay's port %s", rport, wantRPort)
	}
	if received, _ := via.Params.Get("received"); received != "127.0.0.1" {
		t.Errorf("received = %q, want 127.0.0.1", received)
	}
	if via.Branch() == "" || via.SentBy() != client.cfg.SentBy {
		t.Errorf("top Via %v is not the client's", via)
	}
}

// udpPeer is a plain UDP socket that sends requests to a stack by
// hand, so that a test decides what reaches the stack and in which
// order instead of leaving it to the timers.
type udpPeer struct {
	t     *testing.T
	conn  *net.UDPConn
	stack netip.AddrPort
}

func newUDPPeer(t *testing.T, stack netip.AddrPort) *udpPeer {
	return &udpPeer{t: t, conn: rawSocket(t), stack: stack}
}

// request builds a request outside a dialog with a Via of its own, which
// asks for rport so that the answer comes back to this socket.
func (p *udpPeer) request(method Method) *Message {
	m := &Message{Method: method, RequestURI: "sip:" + imsDomain}
	m.Add("Via", Via{Transport: string(UDP), Host: "127.0.0.1", Port: int(addrOf(p.conn).Port()),
		Params: Params{{Name: "branch", Value: NewBranch()}, {Name: "rport"}}}.String())
	m.Add("Max-Forwards", "70")
	m.Add("From", "<sip:001010000000001@"+imsDomain+">;tag="+NewTag())
	m.Add("To", "<sip:001010000000002@"+imsDomain+">")
	m.Add("Call-ID", NewCallID())
	m.Add("CSeq", "1 "+string(method))
	return m
}

func (p *udpPeer) send(m *Message) {
	p.t.Helper()
	if _, err := p.conn.WriteToUDPAddrPort(m.Bytes(), p.stack); err != nil {
		p.t.Fatal(err)
	}
}

func (p *udpPeer) read() *Message {
	p.t.Helper()
	return readDatagram(p.t, p.conn)
}

// sync sends an OPTIONS, which the stack's TU must answer at once, and
// returns what arrived before its answer. The stack reads one socket in
// order and handles each message to the end before the next, so
// everything it sent for the requests before the OPTIONS is in the
// returned list. With Config.Ordered, the TU has also seen those
// requests by then. With Config.Admit, both hold only for the requests
// from other sockets: the OPTIONS has a Call-ID of its own, so Admit
// may let it in while it still decides on earlier requests from this
// one.
func (p *udpPeer) sync() []*Message {
	p.t.Helper()
	options := p.request(OPTIONS)
	p.send(options)
	var before []*Message
	for {
		m := p.read()
		if m.Get("Call-ID") == options.Get("Call-ID") {
			return before
		}
		before = append(before, m)
	}
}

// answerOptions answers an OPTIONS at once, for udpPeer.sync, and passes
// any other request to next.
func answerOptions(next func(*ServerTransaction)) func(*ServerTransaction) {
	return func(st *ServerTransaction) {
		if st.Request.Method == OPTIONS {
			st.Respond(NewResponse(st.Request, 200, NewTag()))
			return
		}
		next(st)
	}
}

// RFC 3261 17.2.2: in Trying the transaction discards retransmissions,
// and in Completed it answers each with its final response again.
func TestServerTransactionAbsorbsRetransmissions(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	server := newStack(t, testTimers, answerOptions(func(st *ServerTransaction) {
		calls.Add(1)
		<-release
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	}))
	peer := newUDPPeer(t, server.transport.LocalAddr())

	register := peer.request(REGISTER)
	for range 3 {
		peer.send(register)
	}
	if got := peer.sync(); len(got) != 0 {
		t.Fatalf("server sent %v in Trying, want nothing", got)
	}
	close(release)
	if got := peer.read(); got.StatusCode != 200 || got.Get("Call-ID") != register.Get("Call-ID") {
		t.Fatalf("got %v, want the 200 to the REGISTER", got)
	}
	peer.send(register)
	if got := peer.read(); got.StatusCode != 200 || got.Get("Call-ID") != register.Get("Call-ID") {
		t.Fatalf("got %v for the retransmission in Completed, want the 200 again", got)
	}
	if got := peer.sync(); len(got) != 0 {
		t.Errorf("server sent %v besides, want nothing", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("OnRequest called %d times, want 1: retransmissions belong to the server transaction", n)
	}
}

// FinalStatus tells the TU which final response it gave a request: none
// before it has answered, and not a provisional response.
func TestFinalStatusIsTheFinalResponseOfTheTU(t *testing.T) {
	seen := make(chan [3]int, 1)
	server := newStack(t, testTimers, func(st *ServerTransaction) {
		before := st.FinalStatus()
		st.Respond(NewResponse(st.Request, 100, ""))
		provisional := st.FinalStatus()
		st.Respond(NewResponse(st.Request, 403, NewTag()))
		seen <- [3]int{before, provisional, st.FinalStatus()}
	})
	peer := newUDPPeer(t, server.transport.LocalAddr())
	peer.send(peer.request(REGISTER))
	select {
	case got := <-seen:
		if got != [3]int{0, 0, 403} {
			t.Errorf("FinalStatus before, after the 100 and after the 403 = %v, want [0 0 403]", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the REGISTER never reached the TU")
	}
}

// ClaimFirstSuccess picks out of the responses of a transaction the one
// that first told the sender that the session was getting through: the
// 180 (Ringing), or the 2xx when no 180 went before it. TS 32.409
// 4.1.2.2 counts a successful session establishment on that response,
// under the message type that carried it, so neither the 100 nor the
// 183 is it, a 2xx after a 180 does not take its place, and nor does a
// retransmitted 2xx.
func TestClaimFirstSuccessIsTheFirstResponseThatTellsOfASuccess(t *testing.T) {
	for _, c := range []struct {
		name string
		sent []int
		want []bool
	}{
		{"a session that rings", []int{100, 183, 180, 200, 200}, []bool{false, false, true, false, false}},
		{"a session answered without ringing", []int{100, 183, 200}, []bool{false, false, true}},
		{"a session that the callee refuses", []int{100, 486}, []bool{false, false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			seen := make(chan []bool, 1)
			server := newStack(t, testTimers, func(st *ServerTransaction) {
				var got []bool
				for _, code := range c.sent {
					tag := NewTag()
					if code == 100 {
						tag = ""
					}
					resp := NewResponse(st.Request, code, tag)
					st.Respond(resp)
					got = append(got, st.ClaimFirstSuccess(resp))
				}
				seen <- got
			})
			peer := newUDPPeer(t, server.transport.LocalAddr())
			peer.send(peer.request(INVITE))
			select {
			case got := <-seen:
				if !slices.Equal(got, c.want) {
					t.Errorf("ClaimFirstSuccess for each of %v = %v, want %v", c.sent, got, c.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the INVITE never reached the TU")
			}
		})
	}
}

// The transaction gives the success away once: a second call returns
// false even for the response that took it, so a TU that asks twice
// about one response still counts one successful establishment.
func TestClaimFirstSuccessGivesTheSuccessAwayOnce(t *testing.T) {
	seen := make(chan [2]bool, 1)
	server := newStack(t, testTimers, func(st *ServerTransaction) {
		ringing := NewResponse(st.Request, 180, NewTag())
		st.Respond(ringing)
		seen <- [2]bool{st.ClaimFirstSuccess(ringing), st.ClaimFirstSuccess(ringing)}
	})
	peer := newUDPPeer(t, server.transport.LocalAddr())
	peer.send(peer.request(INVITE))
	select {
	case got := <-seen:
		if got != [2]bool{true, false} {
			t.Errorf("the two calls for the one 180 = %v, want [true false]", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the INVITE never reached the TU")
	}
}

func TestClientTransactionTimesOut(t *testing.T) {
	timers := Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}
	client := newStack(t, timers, ignoreRequests)
	peer := rawSocket(t)

	start := time.Now()
	ct, err := client.SendRequest(registerRequest(), udpTo(addrOf(peer)))
	if err != nil {
		t.Fatal(err)
	}

	// The peer answers, but with responses the client must not take: one
	// for another sent-by, and one for an unknown branch.
	req := readDatagram(t, peer)
	other := NewResponse(req, 200, "x")
	v, _ := ParseVia(other.Get("Via"))
	v.Port++
	other.SetFirst("Via", v.String())
	stray := NewResponse(req, 200, "x")
	v, _ = ParseVia(stray.Get("Via"))
	v.Params.Set("branch", NewBranch())
	stray.SetFirst("Via", v.String())
	for _, m := range []*Message{other, stray} {
		peer.WriteToUDPAddrPort(m.Bytes(), client.transport.LocalAddr())
	}

	got := collect(t, ct)
	if len(got) != 0 || !errors.Is(ct.Err(), ErrTimeout) {
		t.Fatalf("responses = %v, err = %v; want none and ErrTimeout", got, ct.Err())
	}
	if elapsed, f := time.Since(start), 64*timers.T1; elapsed < f {
		t.Errorf("timed out after %v, before Timer F (%v)", elapsed, f)
	}
	retransmissions := 0
	for {
		peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if _, err := peer.Read(make([]byte, 65535)); err != nil {
			break
		}
		retransmissions++
	}
	// Timer E fires at 10, 30 and 70 ms, then every 40 ms up to Timer F
	// at 640 ms, but a loaded machine runs every timer late and gets
	// fewer of them in. That the request went out again proves that the
	// stack runs Timer E; the schedule itself is the state machine's,
	// which TestNonInviteClientRetransmitsWithBackoff checks.
	if retransmissions == 0 {
		t.Error("no retransmission before Timer F")
	}
}

// With the default T1, Timer F would fire after 32 s; the operator's
// TransactionTimeout ends the transaction long before.
func TestClientTransactionTimesOutAfterTheTransactionTimeout(t *testing.T) {
	timers := DefaultTimers()
	timers.TransactionTimeout = 200 * time.Millisecond
	client := newStack(t, timers, ignoreRequests)
	start := time.Now()
	ct, err := client.SendRequest(registerRequest(), udpTo(addrOf(rawSocket(t))))
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, ct); len(got) != 0 || !errors.Is(ct.Err(), ErrTimeout) {
		t.Fatalf("responses = %v, err = %v; want none and ErrTimeout", got, ct.Err())
	}
	if elapsed := time.Since(start); elapsed < timers.TransactionTimeout {
		t.Errorf("timed out after %v, before the transaction timeout (%v)", elapsed, timers.TransactionTimeout)
	}
}

func TestNewStackRefusesANegativeTransactionTimeout(t *testing.T) {
	tr := listenLayer(t, testLogger(t))
	timers := DefaultTimers()
	timers.TransactionTimeout = -time.Second
	if _, err := NewStack(tr, Config{SentBy: tr.LocalAddr().String(), Timers: timers, OnRequest: ignoreRequests}, testLogger(t)); err == nil {
		t.Error("NewStack took a negative TransactionTimeout")
	}
}

func TestCloseEndsRunningClientTransactions(t *testing.T) {
	tr := listenLayer(t, testLogger(t))
	client, err := NewStack(tr, Config{SentBy: tr.LocalAddr().String(), OnRequest: ignoreRequests}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := client.SendRequest(registerRequest(), udpTo(addrOf(rawSocket(t))))
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if got := collect(t, ct); len(got) != 0 || !errors.Is(ct.Err(), ErrStackClosed) {
		t.Errorf("responses = %v, err = %v; want none and ErrStackClosed", got, ct.Err())
	}
	if _, err := client.SendRequest(registerRequest(), udpTo(addrOf(rawSocket(t)))); !errors.Is(err, ErrStackClosed) {
		t.Errorf("SendRequest after Close: err = %v, want ErrStackClosed", err)
	}
}

func TestSendRequestRejectsACKAndCANCEL(t *testing.T) {
	client := newStack(t, testTimers, ignoreRequests)
	for _, method := range []Method{ACK, CANCEL} {
		req := registerRequest()
		req.Method = method
		if _, err := client.SendRequest(req, udpTo(addrOf(rawSocket(t)))); err == nil {
			t.Errorf("SendRequest(%s) succeeded", method)
		}
	}
	if err := client.SendACK(registerRequest(), udpTo(addrOf(rawSocket(t)))); err == nil {
		t.Error("SendACK(REGISTER) succeeded")
	}
}
