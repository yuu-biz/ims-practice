package sip

import (
	"errors"
	"reflect"
	"testing"
)

// establishWith is establish for a call whose two users answer the
// requests within the dialog: fromCallee answers those that the callee
// sends, at the caller, and fromCaller those that the caller sends.
func establishWith(t *testing.T, fromCallee, fromCaller func(*InDialogRequest)) established {
	t.Helper()
	calls := make(chan *IncomingCall, 1)
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(fromCaller)
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.OnRequest = fromCallee
	out, err := callerUA.Invite(inv)
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

// A B2BUA relays whatever its one leg received to the other leg, so
// both ends of a call send and answer requests of any method within the
// dialog.
func TestRequestOfAnotherMethodWithinTheDialog(t *testing.T) {
	info := Body{Type: "application/dtmf-relay", Content: []byte("Signal=5\r\n")}
	got := make(chan *InDialogRequest, 2)
	user := func(r *InDialogRequest) {
		got <- r
		if r.Request.Method == "INFO" {
			if _, err := r.Accept(Body{}, HeaderField{Name: "X-Seen", Value: "yes"}); err != nil {
				t.Error(err)
			}
			if err := r.Reject(500); !errors.Is(err, ErrRequestAnswered) {
				t.Errorf("Reject after Accept: %v, want ErrRequestAnswered", err)
			}
			return
		}
		r.Reject(603, HeaderField{Name: "Warning", Value: `399 example "no transfer"`})
	}
	e := establishWith(t, user, user)

	resp, err := e.out.Send("INFO", info, HeaderField{Name: "Info-Package", Value: "dtmf"})
	if err != nil || resp.StatusCode != 200 || resp.Get("X-Seen") != "yes" {
		t.Fatalf("INFO from the caller got %v, %v; want the 200 of the callee's user", resp, err)
	}
	r := <-got
	if r.Dialog() != &e.in.CallDialog || string(r.Request.Body) != string(info.Content) ||
		r.Request.Get("Content-Type") != info.Type || r.Request.Get("Info-Package") != "dtmf" || r.Request.Get("CSeq") != "2 INFO" {
		t.Errorf("callee's user got\n%s", r.Request)
	}

	// The callee's first request in the dialog has CSeq 1 (RFC 3261
	// 12.2.1.1).
	resp, err = e.in.Send("REFER", Body{}, ReferTo{URI: "sip:conf@" + imsDomain}.Header())
	if err != nil || resp.StatusCode != 603 || resp.Get("Warning") == "" {
		t.Fatalf("REFER from the callee got %v, %v; want the 603 of the caller's user", resp, err)
	}
	if r := <-got; r.Request.Get("CSeq") != "1 REFER" || r.Request.Get("Refer-To") != "<sip:conf@"+imsDomain+">" {
		t.Errorf("caller's user got\n%s", r.Request)
	}

	if _, err := e.out.Send(BYE, Body{}); err == nil {
		t.Error("Send sent a BYE, which has a method of its own")
	}
}

// RFC 3261 8.2.1: without a user that takes it, a method that the UA
// does not know gets 405, and the dialog goes on.
func TestRequestOfAnUnknownMethodGets405(t *testing.T) {
	e := establish(t)
	resp, err := e.out.Send("INFO", Body{})
	if err != nil || resp.StatusCode != 405 || resp.Get("Allow") == "" {
		t.Fatalf("INFO got %v, %v; want 405 with Allow", resp, err)
	}
	select {
	case <-e.in.Done():
		t.Error("the 405 ended the call")
	default:
	}
}

// RFC 3261 12.2.1.2: a 481 to a request within the dialog tells that
// the peer has lost the dialog, and the UA ends it with a BYE.
func TestA481WithinTheDialogEndsTheCall(t *testing.T) {
	// The test answers for the peer, by hand. With the short timers of
	// the other tests, Timer B or F (64*T1) would end the INVITE or the
	// INFO whenever the test took longer than that to answer; this test
	// is about the 481, so the UA keeps the timers of RFC 3261.
	callerUA := newUA(t, DefaultTimers(), "001010000000001", noCalls)
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)
	send(t, peer, forked(invite, 200, "callee", target, 0), callerUA.Stack())
	if ack := readDatagram(t, peer); ack.Method != ACK {
		t.Fatalf("got %s, want ACK", startLine(ack))
	}
	responses(t, out)

	done := make(chan error, 1)
	go func() {
		_, err := out.Send("INFO", Body{})
		done <- err
	}()
	info := readDatagram(t, peer)
	send(t, peer, NewResponse(info, 481, ""), callerUA.Stack())
	bye := readDatagram(t, peer)
	if bye.Method != BYE || bye.Get("CSeq") != "3 BYE" {
		t.Fatalf("got %s with CSeq %q, want BYE with 3 BYE", startLine(bye), bye.Get("CSeq"))
	}
	send(t, peer, NewResponse(bye, 200, ""), callerUA.Stack())
	if err := <-done; err != nil {
		t.Errorf("Send: %v, want the 481 without an error", err)
	}
	waitDone(t, out.Done(), "caller")
	if !errors.Is(out.Err(), ErrDialogLost) {
		t.Errorf("call ended with %v, want ErrDialogLost", out.Err())
	}
}
