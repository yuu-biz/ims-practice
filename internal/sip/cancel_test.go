package sip

import (
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancelGoesOutAfterTheFirstProvisionalResponse(t *testing.T) {
	// A T1 long enough that no retransmitted INVITE shows up.
	client := newStack(t, Timers{T1: 500 * time.Millisecond, T2: 4 * time.Second, T4: time.Second}, ignoreRequests)
	peer := rawSocket(t)
	invite := inviteRequest()
	invite.Add("Route", "<sip:orig@scscf."+imsDomain+";lr>")
	invite.Add("Require", "100rel")
	ct, err := client.SendRequest(invite, udpTo(addrOf(peer)))
	if err != nil {
		t.Fatal(err)
	}
	req := readDatagram(t, peer)
	if err := ct.Cancel(); err != nil {
		t.Fatal(err)
	}
	expectSilence(t, peer, 100*time.Millisecond, "RFC 3261 9.1: no CANCEL before a provisional response")

	send(t, peer, NewResponse(req, 180, "callee"), client)
	cancel := readDatagram(t, peer)
	// RFC 3261 9.1.
	if cancel.Method != CANCEL || cancel.RequestURI != req.RequestURI {
		t.Fatalf("got %s %s, want CANCEL %s", startLine(cancel), cancel.RequestURI, req.RequestURI)
	}
	for _, h := range []string{"Via", "Route", "From", "To", "Call-ID"} {
		if got, want := cancel.GetAll(h), req.GetAll(h); !reflect.DeepEqual(got, want) {
			t.Errorf("CANCEL %s = %q, want the INVITE's %q", h, got, want)
		}
	}
	if got := cancel.Get("CSeq"); got != "1 CANCEL" {
		t.Errorf("CANCEL CSeq = %q, want 1 CANCEL", got)
	}
	if got := cancel.Get("Require"); got != "" {
		t.Errorf("CANCEL has Require %q", got)
	}

	send(t, peer, NewResponse(cancel, 200, "callee"), client)
	send(t, peer, NewResponse(req, 487, "callee"), client)
	if ack := readDatagram(t, peer); ack.Method != ACK || ack.Get("CSeq") != "1 ACK" {
		t.Errorf("got %s with CSeq %q, want the ACK for the 487", startLine(ack), ack.Get("CSeq"))
	}
	got := collect(t, ct)
	if len(got) != 2 || got[0].StatusCode != 180 || got[1].StatusCode != 487 {
		t.Errorf("TU got %v, want 180 and 487", got)
	}
}

// slowTimers leave room between the reads of a raw peer and Timer G, so
// that a retransmitted final response does not get in the way.
var slowTimers = Timers{T1: 100 * time.Millisecond, T2: 800 * time.Millisecond, T4: 500 * time.Millisecond}

func TestCancelIsAnsweredByTheStack(t *testing.T) {
	var calls atomic.Int32
	cancelled := make(chan bool, 1)
	server := newStack(t, slowTimers, func(st *ServerTransaction) {
		calls.Add(1)
		st.Respond(NewResponse(st.Request, 180, "callee"))
		select {
		case <-st.Cancelled():
			cancelled <- true
			st.Respond(NewResponse(st.Request, 487, "callee"))
		case <-time.After(2 * time.Second):
			cancelled <- false
		}
	})
	peer := rawSocket(t)
	invite := rawInvite(peer)
	send(t, peer, invite, server)
	if ringing := readDatagram(t, peer); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}

	send(t, peer, newMatchingRequest(invite, CANCEL, invite.Get("To")), server)
	byCSeq := map[string]*Message{}
	for range 2 {
		m := readDatagram(t, peer)
		byCSeq[m.Get("CSeq")] = m
	}
	ok, terminated := byCSeq["1 CANCEL"], byCSeq["1 INVITE"]
	if ok == nil || ok.StatusCode != 200 || terminated == nil || terminated.StatusCode != 487 {
		t.Fatalf("got %v, want 200 for the CANCEL and 487 for the INVITE", byCSeq)
	}
	if !<-cancelled {
		t.Error("the INVITE's TU never saw Cancelled")
	}
	// RFC 3261 9.2: the same To tag as the responses to the INVITE.
	if got, want := ok.Get("To"), terminated.Get("To"); got != want {
		t.Errorf("200 To = %q, want the 487's %q", got, want)
	}

	// A retransmitted CANCEL gets the 200 again from its own transaction.
	send(t, peer, newMatchingRequest(invite, ACK, terminated.Get("To")), server)
	send(t, peer, newMatchingRequest(invite, CANCEL, invite.Get("To")), server)
	if again := readDatagram(t, peer); again.StatusCode != 200 || again.Get("CSeq") != "1 CANCEL" {
		t.Errorf("got %s %q, want the 200 for the CANCEL again", startLine(again), again.Get("CSeq"))
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("OnRequest called %d times; the CANCEL is not for the TU", n)
	}
}

func TestCancelAfterTheFinalResponseChangesNothing(t *testing.T) {
	sawCancel := make(chan bool, 1)
	server := newStack(t, slowTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 486, "callee"))
		select {
		case <-st.Cancelled():
			sawCancel <- true
		case <-time.After(200 * time.Millisecond):
			sawCancel <- false
		}
	})
	peer := rawSocket(t)
	invite := rawInvite(peer)
	send(t, peer, invite, server)
	busy := readDatagram(t, peer)
	send(t, peer, newMatchingRequest(invite, ACK, busy.Get("To")), server)
	send(t, peer, newMatchingRequest(invite, CANCEL, invite.Get("To")), server)
	// The INVITE transaction still exists, in Confirmed, so the CANCEL
	// matches it (RFC 3261 9.2).
	if ok := readDatagram(t, peer); ok.StatusCode != 200 || ok.Get("CSeq") != "1 CANCEL" {
		t.Errorf("got %s %q, want 200 for the CANCEL", startLine(ok), ok.Get("CSeq"))
	}
	if <-sawCancel {
		t.Error("Cancelled closed after the final response")
	}
}

func TestCancelThatMatchesNothingGets481(t *testing.T) {
	server := newStack(t, testTimers, func(st *ServerTransaction) {
		t.Errorf("OnRequest got %s", st.Request.Method)
	})
	peer := rawSocket(t)
	invite := rawInvite(peer)
	send(t, peer, newMatchingRequest(invite, CANCEL, invite.Get("To")), server)
	if resp := readDatagram(t, peer); resp.StatusCode != 481 {
		t.Errorf("got %s, want 481", startLine(resp))
	}
}

// TestCancelOfANonInviteGets200 is RFC 3261 9.2: a CANCEL that matches a
// transaction of another method than INVITE gets 200, with the To tag of
// the response to that request once there is one, and changes nothing
// of what the request gets.
func TestCancelOfANonInviteGets200(t *testing.T) {
	answer := make(chan struct{})
	server := newStack(t, slowTimers, func(st *ServerTransaction) {
		<-answer
		st.Respond(NewResponse(st.Request, 200, "server"))
	})
	peer := rawSocket(t)
	rawOptions := func() *Message {
		m := rawInvite(peer)
		m.Method = OPTIONS
		m.Set("CSeq", "1 OPTIONS")
		return m
	}
	read := func(cseq string) *Message {
		t.Helper()
		m := readDatagram(t, peer)
		if m.StatusCode != 200 || m.Get("CSeq") != cseq {
			t.Fatalf("got %s %q, want 200 for %s", startLine(m), m.Get("CSeq"), cseq)
		}
		return m
	}

	// Before the final response.
	pending := rawOptions()
	send(t, peer, pending, server)
	send(t, peer, newMatchingRequest(pending, CANCEL, pending.Get("To")), server)
	read("1 CANCEL")
	close(answer)
	read("1 OPTIONS")

	// After it: the OPTIONS transaction waits in Completed for Timer J.
	answered := rawOptions()
	send(t, peer, answered, server)
	resp := read("1 OPTIONS")
	send(t, peer, newMatchingRequest(answered, CANCEL, answered.Get("To")), server)
	if got, want := read("1 CANCEL").Get("To"), resp.Get("To"); got != want {
		t.Errorf("200 To = %q, want the OPTIONS response's %q", got, want)
	}
}
