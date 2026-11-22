package sip

import (
	"errors"
	"net"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// failoverTimers end a transaction that gets no response after 200 ms,
// so that a request soon goes on to the next target.
var failoverTimers = Timers{T1: testTimers.T1, T2: testTimers.T2, T4: testTimers.T4, TransactionTimeout: 200 * time.Millisecond}

// closedTCPPort is an address where nothing takes TCP connections: the
// port of a listener that has closed.
func closedTCPPort(t *testing.T) netip.AddrPort {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.TCPAddr).AddrPort()
	l.Close()
	return addr
}

// silence reports whether c receives nothing within d.
func silence(c *net.UDPConn, d time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(d))
	_, err := c.Read(make([]byte, 65535))
	return err != nil
}

// answering runs a stack that answers every request with code, and passes
// on each request that it takes.
func answering(t *testing.T, code int) (*Stack, <-chan *Message) {
	t.Helper()
	got := make(chan *Message, 4)
	s := newStack(t, failoverTimers, func(st *ServerTransaction) {
		got <- st.Request
		st.Respond(NewResponse(st.Request, code, NewTag()))
	})
	return s, got
}

func takeRequest(t *testing.T, got <-chan *Message, what string) *Message {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never came", what)
		return nil
	}
}

// RFC 3263 4.3: a target that does not answer before Timer F has
// failed, and the same request goes to the next one in a new client
// transaction, with a Via of a new branch.
func TestRequestGoesToTheNextTargetWhenTheFirstIsSilent(t *testing.T) {
	silent := rawSocket(t)
	second, got := answering(t, 200)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(registerRequest(), udpTo(addrOf(silent)), udpTo(second.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	first := readDatagram(t, silent)
	if responses := collect(t, ct); len(responses) != 1 || responses[0].StatusCode != 200 {
		t.Fatalf("responses = %v, err = %v; want the 200 of the second target", codes(responses), ct.Err())
	}
	again := takeRequest(t, got, "the request at the second target")
	firstVia, _ := ParseVia(first.Get("Via"))
	againVia, _ := ParseVia(again.Get("Via"))
	if firstVia.Branch() == againVia.Branch() {
		t.Errorf("both targets got branch %s, want a new one for the second", againVia.Branch())
	}
	for _, name := range []string{"Call-ID", "CSeq", "From", "To"} {
		if first.Get(name) != again.Get(name) {
			t.Errorf("%s is %q at the second target, %q at the first; want the same request", name, again.Get(name), first.Get(name))
		}
	}
}

// A 503 is a failure of its target alone (RFC 3263 4.3): the TU sees the
// answer of the next target, and the 503 only when no target is left.
func TestRequestGoesToTheNextTargetAfterA503(t *testing.T) {
	unavailable, _ := answering(t, 503)
	second, got := answering(t, 200)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(registerRequest(), udpTo(unavailable.LocalAddr()), udpTo(second.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if responses := collect(t, ct); !reflect.DeepEqual(codes(responses), []int{200}) {
		t.Fatalf("TU got %v, err = %v; want only the 200 of the second target", codes(responses), ct.Err())
	}
	takeRequest(t, got, "the request at the second target")

	ct, err = client.SendRequest(registerRequest(), udpTo(unavailable.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if responses := collect(t, ct); !reflect.DeepEqual(codes(responses), []int{503}) || ct.Err() != nil {
		t.Errorf("with one target the TU got %v, err = %v; want the 503", codes(responses), ct.Err())
	}
}

// A target that refuses the TCP connection has failed with a transport
// error before the request could go out, and the request goes to the
// next one at once.
func TestRequestGoesToTheNextTargetWhenTheFirstRefusesTCP(t *testing.T) {
	second, got := answering(t, 200)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(registerRequest(), tcpTo(closedTCPPort(t)), udpTo(second.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if responses := collect(t, ct); len(responses) != 1 || responses[0].StatusCode != 200 {
		t.Fatalf("responses = %v, err = %v; want the 200 of the second target", codes(responses), ct.Err())
	}
	takeRequest(t, got, "the request at the second target")

	if _, err := client.SendRequest(registerRequest(), tcpTo(closedTCPPort(t))); err == nil {
		t.Error("SendRequest to a single target that refuses TCP succeeded")
	}
}

// When the target after a 503 cannot be reached either, the TU gets
// the 503, not the transport error of the target that never took the
// request.
func TestA503OutlivesATargetAfterItThatRefusesTCP(t *testing.T) {
	unavailable, _ := answering(t, 503)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(registerRequest(), udpTo(unavailable.LocalAddr()), tcpTo(closedTCPPort(t)))
	if err != nil {
		t.Fatal(err)
	}
	if responses := collect(t, ct); !reflect.DeepEqual(codes(responses), []int{503}) || ct.Err() != nil {
		t.Errorf("TU got %v, err = %v; want the 503 of the first target", codes(responses), ct.Err())
	}
}

// A target that has sent a response has the request: when Timer F fires
// after it, the request goes nowhere else (RFC 3263 4.3).
func TestTimeoutAfterAResponseKeepsTheRequestAtItsTarget(t *testing.T) {
	trying := rawSocket(t)
	next := rawSocket(t)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(registerRequest(), udpTo(addrOf(trying)), udpTo(addrOf(next)))
	if err != nil {
		t.Fatal(err)
	}
	req := readDatagram(t, trying)
	trying.WriteToUDPAddrPort(NewResponse(req, 100, "").Bytes(), client.LocalAddr())
	if responses := collect(t, ct); !reflect.DeepEqual(codes(responses), []int{100}) || !errors.Is(ct.Err(), ErrTimeout) {
		t.Fatalf("TU got %v, err = %v; want the 100 and ErrTimeout", codes(responses), ct.Err())
	}
	if !silence(next, 100*time.Millisecond) {
		t.Error("the request went on to the next target after a response")
	}
}

// RFC 3263 4: the CANCEL and the ACK for the 487 go to the target that
// took the INVITE, not to the one that failed before it.
func TestCancelAndACKGoWhereTheINVITEWent(t *testing.T) {
	silent := rawSocket(t)
	callee := rawSocket(t)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(inviteRequest(), udpTo(addrOf(silent)), udpTo(addrOf(callee)))
	if err != nil {
		t.Fatal(err)
	}
	readDatagram(t, silent)
	invite := readDatagram(t, callee)
	callee.WriteToUDPAddrPort(NewResponse(invite, 180, "callee").Bytes(), client.LocalAddr())
	if resp := <-ct.Responses(); resp.StatusCode != 180 {
		t.Fatalf("TU got %d, want the 180", resp.StatusCode)
	}
	if err := ct.Cancel(); err != nil {
		t.Fatal(err)
	}
	cancel := readDatagram(t, callee)
	inviteVia, _ := ParseVia(invite.Get("Via"))
	cancelVia, _ := ParseVia(cancel.Get("Via"))
	if cancel.Method != CANCEL || cancelVia.Branch() != inviteVia.Branch() {
		t.Fatalf("callee got %s with branch %s, want the CANCEL of the INVITE with branch %s", cancel.Method, cancelVia.Branch(), inviteVia.Branch())
	}
	callee.WriteToUDPAddrPort(NewResponse(cancel, 200, "callee").Bytes(), client.LocalAddr())
	callee.WriteToUDPAddrPort(NewResponse(invite, 487, "callee").Bytes(), client.LocalAddr())
	if ack := readDatagram(t, callee); ack.Method != ACK {
		t.Fatalf("callee got %s after the 487, want the ACK", ack.Method)
	}
	if got := collect(t, ct); !reflect.DeepEqual(codes(got), []int{487}) {
		t.Errorf("TU got %v after the 180, want the 487", codes(got))
	}
	// The silent target gets its INVITE again from Timer A until Timer B,
	// and nothing else.
	for {
		m, ok := readWithin(silent, 100*time.Millisecond)
		if !ok {
			break
		}
		if m.Method != INVITE {
			t.Errorf("the silent target got %s, want the INVITE alone", m.Method)
		}
	}
}

func readWithin(c *net.UDPConn, d time.Duration) (*Message, bool) {
	buf := make([]byte, 65535)
	c.SetReadDeadline(time.Now().Add(d))
	n, err := c.Read(buf)
	if err != nil {
		return nil, false
	}
	m, err := Parse(buf[:n])
	return m, err == nil
}

// A cancelled INVITE goes to no other target: here the TU cancels it
// before any response, and Timer B ends it.
func TestCancelledINVITEGoesToNoOtherTarget(t *testing.T) {
	silent := rawSocket(t)
	next := rawSocket(t)
	client := newStack(t, failoverTimers, ignoreRequests)

	ct, err := client.SendRequest(inviteRequest(), udpTo(addrOf(silent)), udpTo(addrOf(next)))
	if err != nil {
		t.Fatal(err)
	}
	if err := ct.Cancel(); err != nil {
		t.Fatal(err)
	}
	if got := collect(t, ct); len(got) != 0 || !errors.Is(ct.Err(), ErrTimeout) {
		t.Fatalf("TU got %v, err = %v; want none and ErrTimeout", codes(got), ct.Err())
	}
	if !silence(next, 100*time.Millisecond) {
		t.Error("the cancelled INVITE went on to the next target")
	}
}

// The ACK for a 2xx has no response that could tell of a failure; a
// target that refuses its TCP connection is skipped.
func TestACKFor2xxSkipsATargetThatRefusesTCP(t *testing.T) {
	peer := rawSocket(t)
	client := newStack(t, failoverTimers, ignoreRequests)
	ack := inviteRequest()
	ack.Method = ACK
	ack.Set("CSeq", "1 ACK")
	if err := client.SendACK(ack, tcpTo(closedTCPPort(t)), udpTo(addrOf(peer))); err != nil {
		t.Fatal(err)
	}
	if got := readDatagram(t, peer); got.Method != ACK {
		t.Errorf("second target got %s, want the ACK", got.Method)
	}
}

// A proxy forwards to the next target that DNS gives once the first
// fails, and the UAC sees the answer of that target (RFC 3261 16.6 step
// 7).
func TestProxyForwardsToTheNextTarget(t *testing.T) {
	silent := rawSocket(t)
	uas, _ := answering(t, 200)
	proxy := newStack(t, failoverTimers, func(st *ServerTransaction) {
		final, err := st.Proxy(st.Request.Clone(), udpTo(addrOf(silent)), udpTo(uas.LocalAddr()))
		if err != nil {
			st.Respond(NewResponse(st.Request, 504, NewTag()))
			return
		}
		st.Respond(final)
	})
	uac := newStack(t, testTimers, ignoreRequests)

	ct, err := uac.SendRequest(registerRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, ct); len(got) != 1 || got[0].StatusCode != 200 {
		t.Fatalf("UAC got %v, err = %v; want the 200 of the second target", codes(got), ct.Err())
	}
}

// A UA sends its INVITE to the next target that Locate gives when the
// first does not answer, and the call goes on there.
func TestUAInvitesTheNextTargetWhenTheFirstIsSilent(t *testing.T) {
	silent := rawSocket(t)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		if _, err := c.Accept(answer); err != nil {
			t.Error(err)
		}
	})
	// DNS gives the silent server first when the INVITE goes out; the
	// ACK and the BYE find the callee alone.
	var looked atomic.Bool
	callerUA := newUAWith(t, UAConfig{Timers: failoverTimers, OnCall: noCalls, Locate: func(u URI) ([]Destination, error) {
		targets, err := locateIPAddress(u)
		if !looked.Swap(true) {
			targets = append([]Destination{udpTo(addrOf(silent))}, targets...)
		}
		return targets, err
	}}, "001010000000001")

	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	if got := responses(t, out); !reflect.DeepEqual(codes(got), []int{200}) {
		t.Fatalf("caller got %v, err = %v; want the 200 of the callee", codes(got), out.Err())
	}
	if m := readDatagram(t, silent); m.Method != INVITE {
		t.Errorf("the silent target got %s, want the INVITE", m.Method)
	}
	if err := out.Bye(); err != nil {
		t.Error(err)
	}
}
