package sip

import (
	"strconv"
	"testing"
)

// RFC 3261 12.2.2: a request within a dialog whose CSeq number is lower
// than the last one is out of order and gets 500. A peer may send
// requests back to back, such as a PRACK and then an UPDATE, so the UA
// has to take them in the order they arrive, or it would refuse the
// first one itself.
func TestRequestsWithinADialogKeepTheirOrder(t *testing.T) {
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) { r.Accept(Body{}) })
		c.Accept(answer)
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	send(t, peer, invite, calleeUA.Stack())
	ok := readSkippingTrying(t, peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	d, err := NewUACDialog(invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	// The ACK and the first requests go out before the UA has taken any
	// of them.
	send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
	const count = 50
	for range count {
		info := d.NewRequest("INFO")
		via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
		info.Prepend("Via", via.String())
		send(t, peer, info, calleeUA.Stack())
	}
	answers := map[string]int{}
	for len(answers) < count {
		resp := readDatagram(t, peer)
		if resp.Get("CSeq") == "1 INVITE" {
			continue // a retransmission of the 200 that crossed the ACK
		}
		answers[resp.Get("CSeq")] = resp.StatusCode
	}
	for seq := 2; seq < 2+count; seq++ {
		if code := answers[strconv.Itoa(seq)+" INFO"]; code != 200 {
			t.Errorf("INFO with CSeq %d got %d, want 200", seq, code)
		}
	}
}
