package xcap_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

const (
	realm = "ims.mnc001.mcc001.3gppnetwork.org"
	impiA = "001010000000001@" + realm
	impuA = "sip:001010000000001@" + realm
	telA  = "tel:+12025550101"
	impiB = "001010000000002@" + realm
	impuB = "sip:001010000000002@" + realm
	// impuC has a subscription but neither a Ut credential nor service
	// data.
	impuC     = "sip:001010000000003@" + realm
	passwordA = "test-password-1"
	passwordB = "test-password-2"
	nonceKey  = "test-nonce-key-of-the-xcap-servers"
)

// mmtelServices is the first document an operator provisions: the
// rules of call forwarding and barring deactivated (GSMA IR.92 2.3.2).
const mmtelServices = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <complete-originating-identity-restriction>
    <originating-identity-presentation-restriction active="true">
      <default-behaviour>presentation-not-restricted</default-behaviour>
    </originating-identity-presentation-restriction>
    <operator-originating-identity-presentation-restriction authorized="true">
      <mode>temporary</mode>
      <restriction>only-identity</restriction>
    </operator-originating-identity-presentation-restriction>
  </complete-originating-identity-restriction>
  <complete-communication-diversion>
    <communication-diversion>
      <cp:ruleset>
        <cp:rule id="cfu">
          <cp:conditions><rule-deactivated/></cp:conditions>
          <cp:actions><forward-to><target/></forward-to></cp:actions>
        </cp:rule>
        <cp:rule id="cfb">
          <cp:conditions><busy/><rule-deactivated/></cp:conditions>
          <cp:actions><forward-to><target/></forward-to></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </communication-diversion>
    <operator-communication-diversion authorized="true">
      <communication-retention-on-invocation>clear-communication-on-invocation-of-diversion</communication-retention-on-invocation>
      <retention-when-diverting-rejected-at-diverted-to-user>no-action-at-diverting-user</retention-when-diverting-rejected-at-diverted-to-user>
      <total-number-of-diversions-for-each-communication>5</total-number-of-diversions-for-each-communication>
      <cdiv-indication-timer>0</cdiv-indication-timer>
      <communication-forwarding-on-no-reply-timer>20</communication-forwarding-on-no-reply-timer>
    </operator-communication-diversion>
  </complete-communication-diversion>
  <complete-communication-barring>
    <incoming-communication-barring>
      <cp:ruleset>
        <cp:rule id="baic">
          <cp:conditions><rule-deactivated/></cp:conditions>
          <cp:actions><allow>false</allow></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </incoming-communication-barring>
    <outgoing-communication-barring password-required="true">
      <cp:ruleset>
        <cp:rule id="baoc">
          <cp:conditions><rule-deactivated/></cp:conditions>
          <cp:actions><allow>false</allow></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </outgoing-communication-barring>
    <operator-incoming-communication-barring authorized="true"/>
    <operator-outgoing-communication-barring authorized="true"/>
  </complete-communication-barring>
</MMTelServices>`

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// network is the HSS stub with three subscriptions, an MMTel AS
// connected to it over Sh, and the XCAP server of that AS. A and B have
// a Ut credential and the service data of mmtelServices.
type network struct {
	sh     *sh.Client // the AS's
	server *httptest.Server
}

func newNetwork(t *testing.T) network {
	t.Helper()
	store, err := hss.NewMemoryStore(
		hss.Subscription{PrivateIdentity: impiA, PublicIdentities: []string{impuA, telA}, Password: passwordA},
		hss.Subscription{PrivateIdentity: impiB, PublicIdentities: []string{impuB}, Password: passwordB},
		hss.Subscription{PrivateIdentity: "001010000000003@" + realm, PublicIdentities: []string{impuC}, Password: "test-password-3"},
	)
	if err != nil {
		t.Fatal(err)
	}
	timers := diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, timers, quiet())
	h, err := hss.New(hss.Config{Realm: realm, VisitedNetworks: []string{realm}}, store, hssNode, quiet())
	if err != nil {
		t.Fatal(err)
	}
	hssNode.Handle(h.Handle)
	listener, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(listener.Stop)
	asNode := diameter.NewNode(diameter.Identity{OriginHost: "mmtel." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDSh}, timers, quiet())
	peer := asNode.Connect(diameter.PeerConfig{Address: listener.Addr(), OriginHost: "hss." + realm})
	t.Cleanup(peer.Stop)
	n := network{sh: sh.NewClient(asNode, peer, realm)}

	// The operator provisions what the HSS stub's configuration will:
	// the service data and the Ut credential.
	provision := func(publicID, serviceIndication string, data []byte) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := n.sh.CreateRepositoryData(context.Background(), publicID, serviceIndication, data)
			if errors.Is(err, diameter.ErrPeerUnavailable) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	provision(impuA, simservs.ServiceIndication, []byte(mmtelServices))
	provision(impuA, xcap.CredentialServiceIndication, xcap.NewCredential(impiA, realm, passwordA).Marshal())
	provision(impuB, simservs.ServiceIndication, []byte(mmtelServices))
	provision(impuB, xcap.CredentialServiceIndication, xcap.NewCredential(impiB, realm, passwordB).Marshal())

	n.server = httptest.NewServer(xcap.NewHandler(n.sh, "", []byte(nonceKey), quiet()))
	t.Cleanup(n.server.Close)
	return n
}

// client is the XCAP client of user A, asking for the document of xui.
func (n network) client(xui string) *xcap.Client {
	return &xcap.Client{Root: n.server.URL, XUI: xui, Username: impiA, Password: passwordA, UserAgent: "ims-practice-ue-test"}
}

// stored reads the MMTelServices of publicID over Sh, as the MMTel AS
// does for a call.
func (n network) stored(t *testing.T, publicID string) (simservs.MMTelServices, uint16) {
	t.Helper()
	d, found, err := n.sh.RepositoryData(context.Background(), publicID, simservs.ServiceIndication)
	if err != nil || !found {
		t.Fatalf("UDR: found %v, %v", found, err)
	}
	m, err := simservs.ParseMMTelServices(d.ServiceData.XML)
	if err != nil {
		t.Fatal(err)
	}
	return m, d.SequenceNumber
}
