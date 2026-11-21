package diameter

import (
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// A request that a handler sends from AfterAnswer reaches the peer
// after the answer, as TS 29.214 4.4.6.1 needs for the STR that follows
// an ASA. The handler here waits before it answers, so a follow-up sent
// any earlier would overtake the answer.
func TestAfterAnswerSendsAfterTheAnswer(t *testing.T) {
	hssNode, _ := newTestNode(hss, testTimers)
	received := make(chan string, 8)
	hssNode.Observe(func(c Capture) {
		if !c.Sent && c.Message.Header.CommandCode == 303 {
			received <- map[bool]string{true: "request", false: "answer"}[isRequest(c.Message)]
		}
	})
	hssNode.Handle(func(req *diam.Message) *diam.Message {
		ans := req.Answer(DiameterSuccess)
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(hss))
		return ans
	})
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)

	scscfNode, logs := newTestNode(scscf, testTimers)
	var peer *Peer
	followedUp := make(chan error, 1)
	scscfNode.Handle(func(req *diam.Message) *diam.Message {
		scscfNode.AfterAnswer(req, func() {
			_, err := peer.Request(t.Context(), testRequest(scscfNode))
			followedUp <- err
		})
		time.Sleep(50 * time.Millisecond)
		ans := req.Answer(DiameterSuccess)
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(scscf))
		return ans
	})
	peer = scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	t.Cleanup(peer.Stop)
	waitForLog(t, logs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})

	if _, err := server.Request(t.Context(), scscf, testRequest(hssNode)); err != nil {
		t.Fatal(err)
	}
	if err := <-followedUp; err != nil {
		t.Fatalf("follow-up request: %v", err)
	}
	if first, second := <-received, <-received; first != "answer" || second != "request" {
		t.Errorf("the HSS received the %s, then the %s; want the answer before the follow-up request", first, second)
	}
}
