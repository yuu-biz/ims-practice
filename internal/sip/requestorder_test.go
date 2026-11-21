package sip

import (
	"net"
	"testing"
	"time"
)

// TestRequestsWithinADialogGoOneAtATime: an UPDATE that the user sends
// while the PRACK of the dialog has no final response waits for it. Had
// both gone out, the UPDATE could reach the callee first, over TCP
// while the PRACK took UDP, and the callee would then reject the PRACK
// for its lower CSeq (RFC 3261 12.2.2). The UA sends the PRACK itself,
// or the user does, as a B2BUA that relays the caller's PRACK does.
func TestRequestsWithinADialogGoOneAtATime(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "PRACK of the UA"
		if manual {
			name = "PRACK of the user"
		}
		t.Run(name, func(t *testing.T) {
			callerUA := newUA(t, slowTimers, "001010000000001", noCalls)
			peer := rawSocket(t)
			target := "sip:" + callee + "@" + addrOf(peer).String()
			out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer, ManualPRACK: manual})
			if err != nil {
				t.Fatal(err)
			}
			invite := readDatagram(t, peer)
			progress := reliable(invite, 183, 1, target)
			answer.addTo(progress)
			send(t, peer, progress, callerUA.Stack())
			var prov *Message
			select {
			case prov = <-out.Responses():
			case <-time.After(2 * time.Second):
				t.Fatal("no 183 for the user")
			}
			if manual {
				go out.PRACK(prov, Body{})
			}
			prack := readDatagram(t, peer)
			if prack.Method != PRACK {
				t.Fatalf("got %s, want the PRACK of the 183", startLine(prack))
			}

			updated := make(chan *Message, 1)
			go func() {
				resp, _ := out.Update(Body{})
				updated <- resp
			}()
			onlyRetransmissionsOf(t, peer, prack, 3*slowTimers.T1)
			send(t, peer, NewResponse(prack, 200, ""), callerUA.Stack())

			update := nextOtherThan(t, peer, prack)
			prackSeq, _ := ParseCSeq(prack.Get("CSeq"))
			updateSeq, _ := ParseCSeq(update.Get("CSeq"))
			if update.Method != UPDATE || updateSeq.Number <= prackSeq.Number {
				t.Fatalf("got %s with CSeq %q after the PRACK with %q, want the UPDATE with a higher number",
					startLine(update), update.Get("CSeq"), prack.Get("CSeq"))
			}
			send(t, peer, NewResponse(update, 200, ""), callerUA.Stack())
			select {
			case resp := <-updated:
				if resp == nil || resp.StatusCode != 200 {
					t.Errorf("Update returned %v, want the 200", resp)
				}
			case <-time.After(2 * time.Second):
				t.Error("Update did not return after its 200")
			}
			send(t, peer, NewResponse(invite, 486, "callee"), callerUA.Stack())
		})
	}
}

// onlyRetransmissionsOf fails when, within d, peer gets anything but
// req again.
func onlyRetransmissionsOf(t *testing.T, peer *net.UDPConn, req *Message, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	buf := make([]byte, 65535)
	for {
		peer.SetReadDeadline(deadline)
		n, err := peer.Read(buf)
		if err != nil {
			return
		}
		m, _ := Parse(buf[:n])
		if m == nil || m.Get("CSeq") != req.Get("CSeq") {
			t.Fatalf("%s went out while the %s had no final response", startLine(m), req.Method)
		}
	}
}

// nextOtherThan reads past the retransmissions of req to the next
// message.
func nextOtherThan(t *testing.T, peer *net.UDPConn, req *Message) *Message {
	t.Helper()
	for {
		m := readDatagram(t, peer)
		if m.Get("CSeq") != req.Get("CSeq") {
			return m
		}
	}
}
