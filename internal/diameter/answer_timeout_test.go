package diameter

import (
	"context"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// slowTimers answer a request later than the node waits by default, as a
// serving node that holds a TFA until the UE has sent its delivery
// report does.
var slowTimers = Timers{
	Watchdog:  300 * time.Millisecond,
	Reconnect: 200 * time.Millisecond,
	Answer:    100 * time.Millisecond,
}

// answerAfter is a handler that waits before it answers.
func answerAfter(d time.Duration) Handler {
	return func(req *diam.Message) *diam.Message {
		time.Sleep(d)
		ans := req.Answer(DiameterSuccess)
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(hss))
		return ans
	}
}

// TestAnswerTimeoutOfOneRequest: a request waits the Answer timer of its
// node, and the timeout of WithAnswerTimeout where the context carries
// one.
func TestAnswerTimeoutOfOneRequest(t *testing.T) {
	peer, _ := openSlowPair(t, answerAfter(300*time.Millisecond))

	if _, err := peer.Request(t.Context(), testRequest(peer.node)); err == nil {
		t.Error("the request got an answer within the Answer timer of the node")
	}
	ctx := WithAnswerTimeout(t.Context(), 5*time.Second)
	if _, err := peer.Request(ctx, testRequest(peer.node)); err != nil {
		t.Errorf("the request with a timeout of its own: %v", err)
	}
}

// TestAnswerTimeoutOfARequestToAConnectedPeer: the same for a request
// that a server sends to the peer that connected to it, which is how the
// SC reaches a serving node over SGd.
func TestAnswerTimeoutOfARequestToAConnectedPeer(t *testing.T) {
	hssNode, _ := newTestNode(hss, slowTimers)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	scscfNode, logs := newTestNode(scscf, slowTimers)
	scscfNode.Handle(answerAfter(300 * time.Millisecond))
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	t.Cleanup(peer.Stop)
	waitForLog(t, logs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})

	if _, err := server.Request(t.Context(), scscf, testRequest(hssNode)); err == nil {
		t.Error("the request got an answer within the Answer timer of the node")
	}
	ctx := WithAnswerTimeout(t.Context(), 5*time.Second)
	if _, err := server.Request(ctx, scscf, testRequest(hssNode)); err != nil {
		t.Errorf("the request with a timeout of its own: %v", err)
	}
}

// TestAnswerTimeoutOfAGroupsRequest: a group gives the timeout to all of
// its attempts together, as it does the Answer timer.
func TestAnswerTimeoutOfAGroupsRequest(t *testing.T) {
	peer, _ := openSlowPair(t, answerAfter(300*time.Millisecond))
	group, err := NewPeerGroup(nil, "scscf", peer)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := group.Request(t.Context(), testRequest(peer.node)); err == nil {
		t.Error("the request got an answer within the Answer timer of the node")
	}
	ctx := WithAnswerTimeout(t.Context(), 5*time.Second)
	if _, err := group.Request(ctx, testRequest(peer.node)); err != nil {
		t.Errorf("the request with a timeout of its own: %v", err)
	}
}

// TestAnswerTimeoutKeepsTheCallersDeadline: the timeout of a request
// lengthens the wait of the node, not a deadline that the caller set.
func TestAnswerTimeoutKeepsTheCallersDeadline(t *testing.T) {
	peer, _ := openSlowPair(t, answerAfter(300*time.Millisecond))

	ctx, cancel := context.WithTimeout(WithAnswerTimeout(t.Context(), 5*time.Second), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := peer.Request(ctx, testRequest(peer.node)); err == nil {
		t.Error("the request outlived the deadline of its caller")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("the request waited %v, past the deadline of its caller", waited)
	}
}

// TestAnswerTimeoutOfZeroKeepsTheNodesTimer: a role that has no timer of
// its own passes zero, which leaves the node's.
func TestAnswerTimeoutOfZeroKeepsTheNodesTimer(t *testing.T) {
	peer, _ := openSlowPair(t, answerAfter(300*time.Millisecond))

	if _, err := peer.Request(WithAnswerTimeout(t.Context(), 0), testRequest(peer.node)); err == nil {
		t.Error("the request got an answer within the Answer timer of the node")
	}
}

// openSlowPair is openPair with the timers of a node whose peer answers
// late.
func openSlowPair(t *testing.T, handler Handler) (*Peer, *logBuffer) {
	t.Helper()
	hssNode, _ := newTestNode(hss, slowTimers)
	hssNode.Handle(handler)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	scscfNode, logs := newTestNode(scscf, slowTimers)
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	t.Cleanup(peer.Stop)
	waitForLog(t, logs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})
	return peer, logs
}
