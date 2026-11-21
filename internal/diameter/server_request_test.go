package diameter

import (
	"errors"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

func TestServerRequestReachesThePeerThatConnected(t *testing.T) {
	hssNode, _ := newTestNode(hss, testTimers)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	scscfNode, logs := newTestNode(scscf, testTimers)
	scscfNode.Handle(func(req *diam.Message) *diam.Message {
		ans := req.Answer(DiameterSuccess)
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(scscf))
		return ans
	})
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	t.Cleanup(peer.Stop)
	waitForLog(t, logs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})

	ans, err := server.Request(t.Context(), scscf, testRequest(hssNode))
	if err != nil {
		t.Fatal(err)
	}
	if resultCode(ans) != DiameterSuccess || originHost(ans) != scscf {
		t.Errorf("answer from %q with Result-Code %d", originHost(ans), resultCode(ans))
	}

	if _, err := server.Request(t.Context(), "other."+scscf, testRequest(hssNode)); !errors.Is(err, ErrPeerUnavailable) {
		t.Errorf("request to a host that never connected: %v, want ErrPeerUnavailable", err)
	}
}
