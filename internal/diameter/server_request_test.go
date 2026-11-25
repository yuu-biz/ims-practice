package diameter

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestServerHostsAreThePeersOfARealmThatServeAnApplication: of the
// peers connected to a server, Hosts gives those whose CER named the
// realm and advertised the application, and forgets each one once its
// connection is gone.
func TestServerHostsAreThePeersOfARealmThatServeAnApplication(t *testing.T) {
	logs := &logBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	server, err := NewNode(Identity{OriginHost: hss, OriginRealm: realm}, []uint32{ApplicationIDCx, ApplicationIDSh}, testTimers, log).
		Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	const otherRealm = "epc.mnc001.mcc001.3gppnetwork.org"
	connect := func(host, peerRealm string, application uint32) *Peer {
		logs := &logBuffer{}
		node := NewNode(Identity{OriginHost: host, OriginRealm: peerRealm}, []uint32{application}, testTimers,
			slog.New(slog.NewJSONHandler(logs, nil)))
		peer := node.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
		t.Cleanup(peer.Stop)
		waitForLog(t, logs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})
		return peer
	}
	second := connect("scscf-2."+realm, realm, ApplicationIDCx)
	connect(scscf, realm, ApplicationIDCx)
	connect("ipsmgw."+realm, realm, ApplicationIDSh)
	connect("mme."+otherRealm, otherRealm, ApplicationIDCx)

	if got, want := server.Hosts(strings.ToUpper(realm), ApplicationIDCx), []string{"scscf-2." + realm, scscf}; !slices.Equal(got, want) {
		t.Errorf("Hosts = %v, want %v", got, want)
	}
	second.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for got := server.Hosts(realm, ApplicationIDCx); !slices.Equal(got, []string{scscf}); got = server.Hosts(realm, ApplicationIDCx) {
		if time.Now().After(deadline) {
			t.Fatalf("Hosts = %v after the second S-CSCF disconnected, want only %s", got, scscf)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
