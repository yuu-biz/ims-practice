package sip

import (
	"errors"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const callee = "001010000000002"

func inviteRequest() *Message {
	m := &Message{Method: INVITE, RequestURI: "sip:" + callee + "@" + imsDomain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<sip:001010000000001@"+imsDomain+">;tag="+NewTag())
	m.Add("To", "<sip:"+callee+"@"+imsDomain+">")
	m.Add("Call-ID", NewCallID())
	m.Add("CSeq", "1 INVITE")
	m.Add("Contact", "<sip:001010000000001@127.0.0.1>")
	return m
}

// rawInvite is an INVITE as a raw peer sends it, with the peer's own Via.
func rawInvite(peer *net.UDPConn) *Message {
	m := inviteRequest()
	via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	m.Prepend("Via", via.String())
	return m
}

// ackFor2xx is the ACK a UAC core sends for a 2xx. Without dialogs yet,
// it goes to the same place as the INVITE and copies what RFC 3261
// 13.2.2.4 keeps from it.
func ackFor2xx(invite, ok *Message) *Message {
	ack := &Message{Method: ACK, RequestURI: invite.RequestURI}
	ack.Add("Max-Forwards", "70")
	ack.Add("From", invite.Get("From"))
	ack.Add("To", ok.Get("To"))
	ack.Add("Call-ID", invite.Get("Call-ID"))
	cseq, _ := ParseCSeq(invite.Get("CSeq"))
	ack.Add("CSeq", CSeq{Number: cseq.Number, Method: ACK}.String())
	return ack
}

func send(t *testing.T, peer *net.UDPConn, m *Message, to *Stack) {
	t.Helper()
	if _, err := peer.WriteToUDPAddrPort(m.Bytes(), to.LocalAddr()); err != nil {
		t.Fatal(err)
	}
}

// expectSilence fails if peer receives anything within d.
func expectSilence(t *testing.T, peer *net.UDPConn, d time.Duration, why string) {
	t.Helper()
	peer.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 65535)
	if n, err := peer.Read(buf); err == nil {
		m, _ := Parse(buf[:n])
		t.Errorf("%s, but got %v", why, startLine(m))
	}
}

func startLine(m *Message) string {
	if m == nil {
		return "a datagram that does not parse"
	}
	if m.IsRequest() {
		return string(m.Method)
	}
	return ReasonPhrase(m.StatusCode)
}

// next returns the next response of ct, skipping 100 (Trying), which a
// slow TU may or may not cause.
func next(t *testing.T, ct *ClientTransaction) *Message {
	t.Helper()
	for {
		select {
		case m, ok := <-ct.Responses():
			if !ok {
				t.Fatalf("transaction ended, err = %v", ct.Err())
			}
			if m.StatusCode != 100 {
				return m
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no response")
		}
	}
}

func TestInviteClientSendsTheACKForAFailure(t *testing.T) {
	// A T1 long enough that no retransmitted INVITE gets between the
	// INVITE and its ACK.
	client := newStack(t, Timers{T1: 500 * time.Millisecond, T2: 4 * time.Second, T4: time.Second}, ignoreRequests)
	peer := rawSocket(t)
	invite := inviteRequest()
	invite.Add("Route", "<sip:orig@scscf."+imsDomain+";lr>")
	ct, err := client.SendRequest(invite, udpTo(addrOf(peer)))
	if err != nil {
		t.Fatal(err)
	}
	req := readDatagram(t, peer)
	busy := NewResponse(req, 486, "callee")
	send(t, peer, busy, client)

	ack := readDatagram(t, peer)
	// RFC 3261 17.1.1.3.
	if ack.Method != ACK || ack.RequestURI != req.RequestURI {
		t.Fatalf("got %s %s, want ACK %s", ack.Method, ack.RequestURI, req.RequestURI)
	}
	for _, h := range []string{"Via", "Route", "From", "Call-ID"} {
		if got, want := ack.GetAll(h), req.GetAll(h); !reflect.DeepEqual(got, want) {
			t.Errorf("ACK %s = %q, want the INVITE's %q", h, got, want)
		}
	}
	if got, want := ack.Get("To"), busy.Get("To"); got != want {
		t.Errorf("ACK To = %q, want the 486's %q", got, want)
	}
	if got := ack.Get("CSeq"); got != "1 ACK" {
		t.Errorf("ACK CSeq = %q, want 1 ACK", got)
	}

	// A retransmitted 486 gets the ACK again but does not reach the TU.
	send(t, peer, busy, client)
	if again := readDatagram(t, peer); !reflect.DeepEqual(again, ack) {
		t.Errorf("second ACK differs:\n%s\nfirst:\n%s", again, ack)
	}
	got := collect(t, ct)
	if len(got) != 1 || got[0].StatusCode != 486 || ct.Err() != nil {
		t.Errorf("TU got %d responses, err = %v; want the 486 only", len(got), ct.Err())
	}
}

func TestInviteServerAbsorbsTheACKForAFailure(t *testing.T) {
	var acks atomic.Int32
	server := newStackWith(t, Config{
		Timers:    slowTimers,
		OnRequest: func(st *ServerTransaction) { st.Respond(NewResponse(st.Request, 486, "callee")) },
		OnACK:     func(*Message) { acks.Add(1) },
	})
	peer := rawSocket(t)
	invite := rawInvite(peer)
	send(t, peer, invite, server)
	busy := readDatagram(t, peer)
	if busy.StatusCode != 486 {
		t.Fatalf("got %s, want 486", startLine(busy))
	}

	// Without an ACK, Timer G resends the 486 after T1.
	start := time.Now()
	if again := readDatagram(t, peer); again.StatusCode != 486 {
		t.Fatalf("got %s, want the 486 again", startLine(again))
	}
	if elapsed := time.Since(start); elapsed < slowTimers.T1/2 {
		t.Errorf("486 resent after %v, want about T1 (%v)", elapsed, slowTimers.T1)
	}

	send(t, peer, newMatchingRequest(invite, ACK, busy.Get("To")), server)
	expectSilence(t, peer, 6*slowTimers.T1, "the ACK should stop Timer G")
	if n := acks.Load(); n != 0 {
		t.Errorf("OnACK called %d times; the transaction should absorb the ACK", n)
	}
}

func TestInviteServerSendsTryingAfter200ms(t *testing.T) {
	release := make(chan struct{})
	server := newStack(t, testTimers, func(st *ServerTransaction) {
		<-release
		st.Respond(NewResponse(st.Request, 180, "callee"))
	})
	peer := rawSocket(t)
	start := time.Now()
	send(t, peer, rawInvite(peer), server)

	trying := readDatagram(t, peer)
	if elapsed := time.Since(start); trying.StatusCode != 100 || elapsed < 200*time.Millisecond {
		t.Fatalf("got %s after %v, want 100 Trying after 200 ms", startLine(trying), elapsed)
	}
	to, _ := ParseNameAddr(trying.Get("To"))
	if tag, ok := to.Params.Get("tag"); ok {
		t.Errorf("100 Trying has To tag %q; RFC 3261 17.2.1 says it SHOULD NOT", tag)
	}
	close(release)
	if ringing := readDatagram(t, peer); ringing.StatusCode != 180 {
		t.Errorf("got %s, want 180 from the TU", startLine(ringing))
	}
}

func TestInviteServerSendsNoTryingForAQuickTU(t *testing.T) {
	server := newStack(t, testTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 180, "callee"))
	})
	peer := rawSocket(t)
	send(t, peer, rawInvite(peer), server)
	if ringing := readDatagram(t, peer); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}
	expectSilence(t, peer, 300*time.Millisecond, "the TU answered within 200 ms, so no 100 Trying")
}

func TestInviteClientTimesOutWithTimerB(t *testing.T) {
	timers := Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}
	// The times come from the client's own transport as it sends, not
	// from the peer as it reads: a reader that wakes late on a busy
	// machine would see two datagrams closer together than they went
	// out.
	var mu sync.Mutex
	var sent []time.Time
	tr, err := ListenObserved("127.0.0.1:0", testLogger(t), func(c Capture) {
		if c.Sent && c.Message.Method == INVITE {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, time.Now())
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewStack(tr, Config{SentBy: tr.LocalAddr().String(), Timers: timers, OnRequest: ignoreRequests}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	peer := rawSocket(t)

	start := time.Now()
	ct, err := client.SendRequest(inviteRequest(), udpTo(addrOf(peer)))
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, ct); len(got) != 0 || !errors.Is(ct.Err(), ErrTimeout) {
		t.Fatalf("responses = %v, err = %v; want none and ErrTimeout", got, ct.Err())
	}
	if elapsed, b := time.Since(start), 64*timers.T1; elapsed < b {
		t.Errorf("timed out after %v, before Timer B (%v)", elapsed, b)
	}

	// RFC 3261 17.1.1.2: retransmissions at T1, 3*T1, 7*T1, ... with
	// intervals that double past T2, seven requests in all. Timers never
	// fire early, and each one starts after the send before it, so each
	// interval is at least its nominal value; the seventh may lose the
	// race with Timer B on a busy machine.
	mu.Lock()
	defer mu.Unlock()
	if len(sent) < 6 || len(sent) > 7 {
		t.Fatalf("%d INVITEs before Timer B, want 7", len(sent))
	}
	for i := 1; i < len(sent); i++ {
		if gap, want := sent[i].Sub(sent[i-1]), timers.T1<<(i-1); gap < want {
			t.Errorf("retransmission %d went out %v after the previous one, want at least %v", i, gap, want)
		}
	}
}

func TestInviteClientPassesEvery2xxUntilTimerM(t *testing.T) {
	client := newStack(t, testTimers, ignoreRequests)
	peer := rawSocket(t)
	start := time.Now()
	ct, err := client.SendRequest(inviteRequest(), udpTo(addrOf(peer)))
	if err != nil {
		t.Fatal(err)
	}
	req := readDatagram(t, peer)
	ok := NewResponse(req, 200, "callee")
	send(t, peer, ok, client)
	send(t, peer, ok, client)

	got := collect(t, ct)
	if len(got) != 2 || got[0].StatusCode != 200 || got[1].StatusCode != 200 || ct.Err() != nil {
		t.Fatalf("TU got %v, err = %v; want the 200 and its retransmission", got, ct.Err())
	}
	if elapsed, m := time.Since(start), 64*testTimers.T1; elapsed < m {
		t.Errorf("Responses closed after %v, before Timer M (%v)", elapsed, m)
	}
	// The transaction never acknowledges a 2xx; INVITE retransmissions
	// that crossed the 200 are all that may follow.
	peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 65535)
	for {
		n, err := peer.Read(buf)
		if err != nil {
			break
		}
		if m, _ := Parse(buf[:n]); m == nil || m.Method != INVITE {
			t.Errorf("client sent %s after the 200", startLine(m))
		}
	}
}

func TestInviteServerAcceptedAbsorbsRetransmittedInvites(t *testing.T) {
	var calls atomic.Int32
	transactions := make(chan *ServerTransaction, 1)
	acks := make(chan *Message, 1)
	server := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			calls.Add(1)
			st.Respond(NewResponse(st.Request, 200, "callee"))
			transactions <- st
		},
		OnACK: func(ack *Message) { acks <- ack },
	})
	peer := rawSocket(t)
	invite := rawInvite(peer)
	send(t, peer, invite, server)
	ok := readDatagram(t, peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	st := <-transactions

	send(t, peer, invite, server)
	expectSilence(t, peer, 5*testTimers.T1, "a retransmitted INVITE in Accepted is absorbed, and the transaction never resends a 2xx")
	if n := calls.Load(); n != 1 {
		t.Errorf("OnRequest called %d times, want 1", n)
	}

	// The TU's own retransmission of the 2xx goes out.
	st.Respond(NewResponse(st.Request, 200, "callee"))
	if again := readDatagram(t, peer); again.StatusCode != 200 {
		t.Errorf("got %s, want the TU's 200 again", startLine(again))
	}

	// The ACK for a 2xx is a new transaction, with a branch of its own.
	ack := ackFor2xx(invite, ok)
	ack.Prepend("Via", Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}.String())
	send(t, peer, ack, server)
	select {
	case got := <-acks:
		if got.Get("CSeq") != "1 ACK" || got.Get("Call-ID") != invite.Get("Call-ID") {
			t.Errorf("OnACK got CSeq %q Call-ID %q", got.Get("CSeq"), got.Get("Call-ID"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the ACK for the 2xx never reached OnACK")
	}
}

func TestInviteAnsweredEndToEnd(t *testing.T) {
	acks := make(chan *Message, 1)
	uas := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			st.Respond(NewResponse(st.Request, 180, "callee"))
			st.Respond(NewResponse(st.Request, 200, "callee"))
		},
		OnACK: func(ack *Message) { acks <- ack },
	})
	uac := newStack(t, testTimers, ignoreRequests)

	ct, err := uac.SendRequest(inviteRequest(), udpTo(uas.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if ringing := next(t, ct); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}
	ok := next(t, ct)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	if err := uac.SendACK(ackFor2xx(ct.Request, ok), udpTo(uas.LocalAddr())); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-acks:
		vias := ack.GetAll("Via")
		inviteVia, _ := ParseVia(ct.Request.Get("Via"))
		ackVia, _ := ParseVia(ack.Get("Via"))
		if len(vias) != 1 || ackVia.Branch() == inviteVia.Branch() {
			t.Errorf("ACK Via = %q; want one Via with a branch other than the INVITE's %s", vias, inviteVia.Branch())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the ACK never reached the UAS")
	}
}
