package sip

import (
	"errors"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	offer  = Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 1 IN IP4 127.0.0.1\r\n")}
	answer = Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 1 IN IP4 127.0.0.1\r\n")}
)

// newUA runs a UA on loopback whose Contact names user at the address
// it listens on.
func newUA(t *testing.T, timers Timers, user string, onCall func(*IncomingCall)) *UA {
	t.Helper()
	return newUAWith(t, UAConfig{Timers: timers, OnCall: onCall}, user)
}

// newUAWith is newUA for a UA with more in its configuration; SentBy
// and Contact are filled in.
func newUAWith(t *testing.T, cfg UAConfig, user string) *UA {
	t.Helper()
	tr := listenLayer(t, testLogger(t))
	cfg.SentBy = tr.LocalAddr().String()
	cfg.Contact = "sip:" + user + "@" + tr.LocalAddr().String()
	ua, err := NewUA(tr, cfg, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua
}

func noCalls(c *IncomingCall) { c.Reject(480) }

// invitation calls the user of to straight at its Contact.
func invitation(to *UA) Invitation {
	return Invitation{RequestURI: to.cfg.Contact, From: caller, To: calleeURI, Body: offer}
}

// responses reads the responses of an outgoing call until Responses is
// closed.
func responses(t *testing.T, c *OutgoingCall) []*Message {
	t.Helper()
	var got []*Message
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-c.Responses():
			if !ok {
				return got
			}
			got = append(got, m)
		case <-timeout:
			t.Fatalf("Responses still open after %d responses", len(got))
		}
	}
}

func codes(ms []*Message) []int {
	var c []int
	for _, m := range ms {
		c = append(c, m.StatusCode)
	}
	return c
}

func waitDone(t *testing.T, done <-chan struct{}, who string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the %s's call never ended", who)
	}
}

type accepted struct {
	ack *Message
	err error
}

func TestCallIsAnsweredAndAcknowledged(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		calls <- c
		if err := c.Progress(180, Body{}); err != nil {
			t.Error(err)
		}
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)

	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{180, 200}) {
		t.Fatalf("caller got %v, want 180 and 200", codes(got))
	}
	if ok := got[1]; string(ok.Body) != string(answer.Content) || ok.Get("Content-Type") != answer.Type {
		t.Errorf("200 carries %q of type %q, want the answer", ok.Body, ok.Get("Content-Type"))
	}
	a := <-acks
	if a.err != nil {
		t.Fatal(a.err)
	}
	// RFC 3261 13.2.2.4: the ACK goes to the remote target with the
	// INVITE's CSeq number.
	if a.ack.RequestURI != calleeUA.cfg.Contact || a.ack.Get("CSeq") != "1 ACK" {
		t.Errorf("ACK is %s with CSeq %q, want %s with 1 ACK", a.ack.RequestURI, a.ack.Get("CSeq"), calleeUA.cfg.Contact)
	}

	in := <-calls
	if string(in.Request.Body) != string(offer.Content) {
		t.Errorf("INVITE carries %q, want the offer", in.Request.Body)
	}
	uac, _ := out.Dialog()
	uas, _ := in.Dialog()
	if uac.State != DialogConfirmed || uas.State != DialogConfirmed {
		t.Errorf("dialogs are %s at the caller and %s at the callee, want both Confirmed", uac.State, uas.State)
	}
	if uac.ID.LocalTag != uas.ID.RemoteTag || uac.ID.RemoteTag != uas.ID.LocalTag || uac.ID.CallID != uas.ID.CallID {
		t.Errorf("dialog IDs do not mirror: caller %+v, callee %+v", uac.ID, uas.ID)
	}
	if uac.RemoteTarget != calleeUA.cfg.Contact || uas.RemoteTarget != callerUA.cfg.Contact {
		t.Errorf("remote targets are %s and %s, want each other's Contact", uac.RemoteTarget, uas.RemoteTarget)
	}
}

// rawINVITE is an INVITE from a raw peer that wants to be reached at its
// own address.
func rawINVITE(peer *net.UDPConn) *Message {
	m := rawInvite(peer)
	m.Set("Contact", "<sip:001010000000001@"+addrOf(peer).String()+">")
	return m
}

// rawACK is the ACK a raw peer sends for ok, with a Via of its own.
func rawACK(peer *net.UDPConn, invite, ok *Message) *Message {
	ack := ackFor2xx(invite, ok)
	via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	ack.Prepend("Via", via.String())
	return ack
}

// readSkippingTrying reads the next datagram that is not a 100 (Trying).
func readSkippingTrying(t *testing.T, peer *net.UDPConn) *Message {
	t.Helper()
	for {
		if m := readDatagram(t, peer); m.StatusCode != 100 {
			return m
		}
	}
}

func TestUASRetransmitsThe2xxUntilTheACK(t *testing.T) {
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, slowTimers, callee, func(c *IncomingCall) {
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	send(t, peer, invite, calleeUA.Stack())

	ok := readSkippingTrying(t, peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	// RFC 6026 8.1: the first ACK is lost, so the 200 comes again after
	// T1 and then after 2*T1.
	start := time.Now()
	var gaps []time.Duration
	for range 2 {
		again := readDatagram(t, peer)
		if again.StatusCode != 200 || again.Get("To") != ok.Get("To") {
			t.Fatalf("got %s with To %q, want the 200 again", startLine(again), again.Get("To"))
		}
		gaps = append(gaps, time.Since(start))
		start = time.Now()
	}
	if gaps[0] < slowTimers.T1/2 || gaps[1] < slowTimers.T1+slowTimers.T1/2 {
		t.Errorf("200 retransmitted after %v, want about T1 and then 2*T1 (T1 = %v)", gaps, slowTimers.T1)
	}

	send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
	select {
	case a := <-acks:
		if a.err != nil || a.ack == nil || a.ack.Get("CSeq") != "1 ACK" {
			t.Errorf("Accept returned %v, %v; want the ACK", a.ack, a.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return after the ACK")
	}
	expectSilence(t, peer, 6*slowTimers.T1, "the ACK should stop the retransmissions")
}

func TestUASEndsTheCallWithoutAnACK(t *testing.T) {
	timers := Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}
	acks := make(chan accepted, 1)
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, timers, callee, func(c *IncomingCall) {
		calls <- c
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	peer := rawSocket(t)
	start := time.Now()
	send(t, peer, rawINVITE(peer), calleeUA.Stack())

	// RFC 3261 13.3.1.4: after 64*T1 of 200s without an ACK, a BYE.
	var bye *Message
	for bye == nil {
		if m := readDatagram(t, peer); m.Method == BYE {
			bye = m
		} else if m.StatusCode != 200 && m.StatusCode != 100 {
			t.Fatalf("got %s, want 200s and then BYE", startLine(m))
		}
	}
	if elapsed := time.Since(start); elapsed < 64*timers.T1 {
		t.Errorf("BYE after %v, before 64*T1", elapsed)
	}
	if bye.RequestURI != "sip:001010000000001@"+addrOf(peer).String() || bye.Get("CSeq") != "1 BYE" {
		t.Errorf("BYE is %s with CSeq %q, want the caller's Contact with 1 BYE", bye.RequestURI, bye.Get("CSeq"))
	}
	send(t, peer, NewResponse(bye, 200, ""), calleeUA.Stack())

	if a := <-acks; !errors.Is(a.err, ErrNoACK) {
		t.Errorf("Accept returned %v, %v; want ErrNoACK", a.ack, a.err)
	}
	c := <-calls
	waitDone(t, c.Done(), "callee")
	if !errors.Is(c.Err(), ErrNoACK) {
		t.Errorf("call ended with %v, want ErrNoACK", c.Err())
	}
}

func TestUACAcknowledgesEach2xx(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)
	if got := invite.Get("Contact"); got != "<"+callerUA.cfg.Contact+">" {
		t.Errorf("INVITE Contact = %q, want the UA's", got)
	}
	ok := NewResponse(invite, 200, "callee")
	ok.Add("Contact", "<"+target+">")
	send(t, peer, ok, callerUA.Stack())

	ack := readDatagram(t, peer)
	// RFC 3261 13.2.2.4.
	if ack.Method != ACK || ack.RequestURI != target || ack.Get("CSeq") != "1 ACK" {
		t.Fatalf("got %s %s with CSeq %q, want ACK %s with 1 ACK", startLine(ack), ack.RequestURI, ack.Get("CSeq"), target)
	}
	if tagOf(ack.Get("To")) != "callee" {
		t.Errorf("ACK To = %q, want the callee's tag", ack.Get("To"))
	}
	if tagOf(ack.Get("From")) != tagOf(invite.Get("From")) || ack.Get("Call-ID") != invite.Get("Call-ID") {
		t.Errorf("ACK From %q, Call-ID %q do not match the INVITE", ack.Get("From"), ack.Get("Call-ID"))
	}

	// The 200 comes again: the ACK goes again, and the user sees one 200.
	send(t, peer, ok, callerUA.Stack())
	again := readDatagram(t, peer)
	again.Remove("Via")
	ack.Remove("Via")
	if !reflect.DeepEqual(again, ack) {
		t.Errorf("second ACK differs:\n%s\nfirst:\n%s", again, ack)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Errorf("caller got %v, want one 200", got)
	}
}

func TestRejectedCall(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		calls <- c
		c.Progress(180, Body{})
		if err := c.Reject(486); err != nil {
			t.Error(err)
		}
		if _, err := c.Accept(answer); !errors.Is(err, ErrAnswered) {
			t.Errorf("Accept after Reject: %v, want ErrAnswered", err)
		}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{180, 486}) {
		t.Fatalf("caller got %v, want 180 and 486", got)
	}
	waitDone(t, out.Done(), "caller")
	in := <-calls
	waitDone(t, in.Done(), "callee")
	// RFC 3261 12.3: the 486 ends the early dialog at both ends.
	if d, _ := out.Dialog(); d.State != DialogTerminated || out.Err() != nil {
		t.Errorf("caller: dialog %s, err %v; want Terminated and nil", d.State, out.Err())
	}
	if d, _ := in.Dialog(); d.State != DialogTerminated || in.Err() != nil {
		t.Errorf("callee: dialog %s, err %v; want Terminated and nil", d.State, in.Err())
	}
}

func TestUnknownExtensionIsRefused(t *testing.T) {
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) { t.Error("OnCall called for an INVITE that requires an unknown extension") })
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	invite.Add("Require", "no-such-extension")
	send(t, peer, invite, calleeUA.Stack())
	// RFC 3261 8.2.2.3.
	if resp := readSkippingTrying(t, peer); resp.StatusCode != 420 || resp.Get("Unsupported") != "no-such-extension" {
		t.Errorf("got %s with Unsupported %q, want 420 naming the extension", startLine(resp), resp.Get("Unsupported"))
	}
}

func TestCallerCancelsWhileRinging(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Progress(180, Body{})
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	if ringing := <-out.Responses(); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}
	if err := out.Cancel(); err != nil {
		t.Fatal(err)
	}
	// RFC 3261 9.2: the callee answers the INVITE with 487.
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{487}) {
		t.Errorf("after the CANCEL the caller got %v, want 487", got)
	}
	in := <-calls
	waitDone(t, in.Done(), "callee")
	if !errors.Is(in.Err(), ErrCancelled) {
		t.Errorf("callee's call ended with %v, want ErrCancelled", in.Err())
	}
	if _, err := in.Accept(answer); !errors.Is(err, ErrAnswered) {
		t.Errorf("Accept after the CANCEL: %v, want ErrAnswered", err)
	}
	waitDone(t, out.Done(), "caller")
}

func TestA2xxThatCrossesTheCANCELGetsABYE(t *testing.T) {
	callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)
	ringing := NewResponse(invite, 180, "callee")
	ringing.Add("Contact", "<"+target+">")
	send(t, peer, ringing, callerUA.Stack())
	<-out.Responses()
	out.Cancel()
	cancel := readDatagram(t, peer)
	if cancel.Method != CANCEL {
		t.Fatalf("got %s, want CANCEL", startLine(cancel))
	}
	// The callee had answered already.
	ok := NewResponse(invite, 200, "callee")
	ok.Add("Contact", "<"+target+">")
	send(t, peer, ok, callerUA.Stack())
	send(t, peer, NewResponse(cancel, 481, "callee"), callerUA.Stack())

	if ack := readDatagram(t, peer); ack.Method != ACK {
		t.Fatalf("got %s, want the ACK for the 200", startLine(ack))
	}
	bye := readDatagram(t, peer)
	if bye.Method != BYE || bye.RequestURI != target || bye.Get("CSeq") != "2 BYE" {
		t.Fatalf("got %s %s with CSeq %q, want BYE %s with 2 BYE", startLine(bye), bye.RequestURI, bye.Get("CSeq"), target)
	}
	send(t, peer, NewResponse(bye, 200, ""), callerUA.Stack())
	waitDone(t, out.Done(), "caller")
}

// established is a call that the callee has accepted and the caller has
// acknowledged.
type established struct {
	callerUA, calleeUA *UA
	out                *OutgoingCall
	in                 *IncomingCall
}

func establish(t *testing.T) established {
	t.Helper()
	calls := make(chan *IncomingCall, 1)
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("caller got %v, want 200", got)
	}
	if a := <-acks; a.err != nil {
		t.Fatal(a.err)
	}
	return established{callerUA: callerUA, calleeUA: calleeUA, out: out, in: <-calls}
}

func TestCallerHangsUp(t *testing.T) {
	e := establish(t)
	if err := e.out.Bye(); err != nil {
		t.Fatalf("Bye: %v", err)
	}
	waitDone(t, e.out.Done(), "caller")
	waitDone(t, e.in.Done(), "callee")
	uas, _ := e.in.Dialog()
	// RFC 3261 12.2.1.1: the BYE takes the caller's next CSeq number.
	if uas.State != DialogTerminated || uas.RemoteSeq != 2 || e.in.Err() != nil {
		t.Errorf("callee: dialog %s, remote seq %d, err %v; want Terminated, 2, nil", uas.State, uas.RemoteSeq, e.in.Err())
	}
	if err := e.out.Bye(); !errors.Is(err, ErrCallEnded) {
		t.Errorf("second Bye: %v, want ErrCallEnded", err)
	}
}

func TestCalleeHangsUp(t *testing.T) {
	e := establish(t)
	if err := e.in.Bye(); err != nil {
		t.Fatalf("Bye: %v", err)
	}
	waitDone(t, e.out.Done(), "caller")
	uac, _ := e.out.Dialog()
	// The callee's local sequence number was empty, so its BYE has 1.
	if uac.State != DialogTerminated || uac.RemoteSeq != 1 || e.out.Err() != nil {
		t.Errorf("caller: dialog %s, remote seq %d, err %v; want Terminated, 1, nil", uac.State, uac.RemoteSeq, e.out.Err())
	}
}

func TestCalleeCannotHangUpAnEarlyDialog(t *testing.T) {
	errs := make(chan error, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Progress(180, Body{})
		errs <- c.Bye()
		c.Reject(480)
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	// RFC 3261 15: the callee MUST NOT send a BYE in an early dialog.
	if err := <-errs; err == nil {
		t.Error("Bye in an early dialog succeeded")
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{180, 480}) {
		t.Errorf("caller got %v, want 180 and 480", got)
	}
}

// sendInDialog sends req from the caller's UA straight to the callee
// and returns the final response.
func sendInDialog(t *testing.T, e established, req *Message) *Message {
	t.Helper()
	ct, err := e.callerUA.Stack().SendRequest(req, udpTo(e.calleeUA.Stack().LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	got := collect(t, ct)
	if len(got) == 0 {
		t.Fatalf("no response, err = %v", ct.Err())
	}
	return got[len(got)-1]
}

func TestRequestOutOfOrderGets500(t *testing.T) {
	e := establish(t)
	d, _ := e.out.Dialog()
	// The callee's remote sequence number is the INVITE's, 1.
	late := d.NewRequest(BYE)
	late.Set("CSeq", "0 BYE")
	if resp := sendInDialog(t, e, late); resp.StatusCode != 500 {
		t.Errorf("BYE with CSeq 0 got %s, want 500 (RFC 3261 12.2.2)", startLine(resp))
	}
	select {
	case <-e.in.Done():
		t.Fatal("a BYE out of order ended the call")
	default:
	}
	if err := e.out.Bye(); err != nil {
		t.Errorf("Bye after the rejected one: %v", err)
	}
	waitDone(t, e.in.Done(), "callee")
}

func TestRequestForAnUnknownDialogGets481(t *testing.T) {
	e := establish(t)
	d, _ := e.out.Dialog()
	d.ID.RemoteTag = "no-such-tag"
	if resp := sendInDialog(t, e, d.NewRequest(BYE)); resp.StatusCode != 481 {
		t.Errorf("BYE for another dialog got %s, want 481 (RFC 3261 12.2.2)", startLine(resp))
	}
	select {
	case <-e.in.Done():
		t.Error("a BYE for another dialog ended the call")
	default:
	}
}

func TestCloseEndsTheCalls(t *testing.T) {
	t.Run("incoming call not yet answered", func(t *testing.T) {
		ringing := make(chan *IncomingCall, 1)
		calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) { ringing <- c })
		callerUA := newUA(t, testTimers, "001010000000001", noCalls)
		if _, err := callerUA.Invite(invitation(calleeUA)); err != nil {
			t.Fatal(err)
		}
		var in *IncomingCall
		select {
		case in = <-ringing:
		case <-time.After(5 * time.Second):
			t.Fatal("the INVITE never reached the callee")
		}
		calleeUA.Close()
		waitDone(t, in.Done(), "callee")
		if !errors.Is(in.Err(), ErrStackClosed) {
			t.Errorf("callee ended with %v, want ErrStackClosed", in.Err())
		}
	})
	t.Run("confirmed call", func(t *testing.T) {
		e := establish(t)
		e.callerUA.Close()
		waitDone(t, e.out.Done(), "caller")
		d, _ := e.out.Dialog()
		if d.State != DialogTerminated || !errors.Is(e.out.Err(), ErrStackClosed) {
			t.Errorf("caller: dialog %s, err %v; want Terminated, ErrStackClosed", d.State, e.out.Err())
		}
		if _, ok := e.callerUA.dialog(d.ID); ok {
			t.Error("the closed UA still routes requests to the dialog")
		}
	})
}

// message is a MESSAGE outside a dialog to the UA ua (RFC 3428 4).
func message(ua *UA) *Message {
	m := &Message{Method: MESSAGE, RequestURI: ua.cfg.Contact}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+caller+">;tag="+NewTag())
	m.Add("To", "<"+calleeURI+">")
	m.Add("Call-ID", NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("Content-Type", "text/plain")
	m.Body = []byte("hello")
	return m
}

// finalTo sends req to ua and returns the final response.
func finalTo(t *testing.T, ua *UA, req *Message) *Message {
	t.Helper()
	ct, err := newStack(t, testTimers, ignoreRequests).SendRequest(req, udpTo(ua.Stack().LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	var final *Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no final response: %v", ct.Err())
	}
	return final
}

// RFC 3428 7: a UA that takes MESSAGE gets each one outside a dialog in
// OnMessage, which answers it; one that does not answers 405, with an
// Allow that leaves MESSAGE out (RFC 3261 8.2.1).
func TestMessageOutsideADialog(t *testing.T) {
	got := make(chan *Message, 1)
	tr := listenLayer(t, testLogger(t))
	taker, err := NewUA(tr, UAConfig{
		SentBy: tr.LocalAddr().String(), Timers: testTimers, Contact: "sip:taker@" + tr.LocalAddr().String(), OnCall: noCalls,
		OnMessage: func(st *ServerTransaction) {
			got <- st.Request
			st.Respond(NewResponse(st.Request, 200, NewTag()))
		},
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(taker.Close)
	if r := finalTo(t, taker, message(taker)); r.StatusCode != 200 {
		t.Errorf("MESSAGE got %d, want the 200 of OnMessage", r.StatusCode)
	}
	if m := <-got; string(m.Body) != "hello" {
		t.Errorf("OnMessage got body %q", m.Body)
	}
	if allow := taker.allowHeader(); allow != "INVITE, ACK, CANCEL, BYE, PRACK, UPDATE, OPTIONS, NOTIFY, MESSAGE" {
		t.Errorf("Allow = %q, want MESSAGE among the methods", allow)
	}

	other := newUA(t, testTimers, "other", noCalls)
	r := finalTo(t, other, message(other))
	if allow := strings.Join(r.GetAll("Allow"), ", "); r.StatusCode != 405 || allow != "INVITE, ACK, CANCEL, BYE, PRACK, UPDATE, OPTIONS, NOTIFY" {
		t.Errorf("MESSAGE to a UA without OnMessage got %d with Allow %q, want 405 without MESSAGE", r.StatusCode, allow)
	}
}

// slowLog is a log that takes its time with each line once slow is set,
// and keeps the lines that it finishes after closed is set.
type slowLog struct {
	mu           sync.Mutex
	slow, closed bool
	late         []string
}

func (l *slowLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	slow := l.slow
	l.mu.Unlock()
	if slow {
		time.Sleep(20 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		l.late = append(l.late, string(p))
	}
	return len(p), nil
}

func (l *slowLog) set(slow, closed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.slow, l.closed = slow, closed
}

// Close returns once the UA has stopped: nothing that the UA runs by
// itself uses its stack or its logger after that, so that the owner of
// the logger may close it. Here the BYE that the UA sends by itself,
// for a 2xx that crossed the CANCEL, still waits for its answer when
// the UA closes, and the BYE fails then.
func TestCloseWaitsForWhatTheUARunsByItself(t *testing.T) {
	log := &slowLog{}
	tr := listenLayer(t, testLogger(t))
	callerUA, err := NewUA(tr, UAConfig{SentBy: tr.LocalAddr().String(), Timers: testTimers, OnCall: noCalls,
		Contact: "sip:001010000000001@" + tr.LocalAddr().String()}, slog.New(slog.NewTextHandler(log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callerUA.Close)
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)
	send(t, peer, forked(invite, 180, "callee", target, 0), callerUA.Stack())
	<-out.Responses()
	out.Cancel()
	if cancel := readDatagram(t, peer); cancel.Method != CANCEL {
		t.Fatalf("got %s, want CANCEL", startLine(cancel))
	}
	send(t, peer, forked(invite, 200, "callee", target, 0), callerUA.Stack())
	for {
		if m := readDatagram(t, peer); m.Method == BYE {
			break
		}
	}

	log.set(true, false)
	callerUA.Close()
	log.set(false, true)
	time.Sleep(100 * time.Millisecond)
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.late) > 0 {
		t.Errorf("the UA logged after Close had returned:\n%s", strings.Join(log.late, ""))
	}
}
