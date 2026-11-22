package sip

import (
	"net"
	"sync"
	"testing"
)

// arrivals records the CSeq number and the Arrival of each request
// within a dialog that the user of a call gets, and accepts it.
type arrivals struct {
	mu  sync.Mutex
	got map[uint32]uint64
}

func (a *arrivals) handle(r *InDialogRequest) {
	cseq, _ := ParseCSeq(r.Request.Get("CSeq"))
	a.mu.Lock()
	a.got[cseq.Number] = r.Arrival
	a.mu.Unlock()
	r.Accept(Body{})
}

// sendInOrderButOne sends count INFOs back to back from peer within d,
// and among them, after the first half, one whose CSeq number is lower
// than that of the INFO before it, which the UA refuses with 500 (RFC
// 3261 12.2.2). It waits for the final response of each, and returns
// the CSeq numbers of the INFOs in order and that of the one out of
// order.
func sendInOrderButOne(t *testing.T, peer *net.UDPConn, d *Dialog, to *Stack, count int) (inOrder []uint32, outOfOrder uint32) {
	t.Helper()
	info := func() *Message {
		req := d.NewRequest("INFO")
		via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
		req.Prepend("Via", via.String())
		return req
	}
	var lateVia string
	for i := range count {
		req := info()
		cseq, _ := ParseCSeq(req.Get("CSeq"))
		inOrder = append(inOrder, cseq.Number)
		send(t, peer, req, to)
		if i == count/2 {
			late := info()
			d.LocalSeq--
			outOfOrder = cseq.Number - 1
			late.Set("CSeq", CSeq{Number: outOfOrder, Method: "INFO"}.String())
			lateVia = late.Get("Via")
			send(t, peer, late, to)
		}
	}
	answered := map[string]int{}
	for len(answered) < count+1 {
		resp := readDatagram(t, peer)
		if resp.IsRequest() || resp.Get("CSeq") == "1 INVITE" {
			continue // an ACK, or a retransmission of the 200 that crossed the ACK
		}
		answered[resp.Get("Via")] = resp.StatusCode
	}
	if code := answered[lateVia]; code != 500 {
		t.Errorf("the INFO out of order got %d, want 500", code)
	}
	return inOrder, outOfOrder
}

// checkArrivals checks that the user got the requests with the CSeq
// numbers inOrder, numbered 1, 2 and so on in that order, and not the
// one out of order.
func checkArrivals(t *testing.T, a *arrivals, inOrder []uint32, outOfOrder uint32) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, seq := range inOrder {
		if arrival, ok := a.got[seq]; !ok || arrival != uint64(i+1) {
			t.Errorf("the INFO with CSeq %d has Arrival %d (handed over: %t), want %d", seq, arrival, ok, i+1)
		}
	}
	if len(a.got) != len(inOrder) {
		t.Errorf("the user got %d INFOs, want %d: the one out of order with CSeq %d too", len(a.got), len(inOrder), outOfOrder)
	}
}

// The UA hands each request within a dialog to the user in a goroutine
// of its own, so they may run in any order; Arrival tells the order in
// which the UA took them, that of their CSeq numbers. A request that it
// refuses for its order takes no number, so that a user that relays the
// requests in that order does not wait for one that never comes. Both
// sides of a call number the requests of their peer.
func TestArrivalNumbersTheRequestsInTheOrderTheyCame(t *testing.T) {
	const count = 40
	t.Run("incoming call", func(t *testing.T) {
		a := &arrivals{got: map[uint32]uint64{}}
		calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
			c.Handle(a.handle)
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
		send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
		inOrder, outOfOrder := sendInOrderButOne(t, peer, &d, calleeUA.Stack(), count)
		checkArrivals(t, a, inOrder, outOfOrder)
	})
	t.Run("outgoing call", func(t *testing.T) {
		a := &arrivals{got: map[uint32]uint64{}}
		callerUA := newUA(t, testTimers, "001010000000001", noCalls)
		_, p := rawAnswer(t, callerUA, Invitation{OnRequest: a.handle})
		inOrder, outOfOrder := sendInOrderButOne(t, p.peer, &p.dialog, callerUA.Stack(), count)
		checkArrivals(t, a, inOrder, outOfOrder)
	})
}
