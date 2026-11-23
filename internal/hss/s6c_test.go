package hss

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// scAddress is the E.164 number of the service centre, a fictitious
// one, as it appears in the SC-Address of every S6c request.
const scAddress = "12025550100"

// scHost is the Diameter identity of the SC, which runs the SMS-GMSC
// that sends the SRR and the SMS-IWMSC that takes the ALR (TS 23.040
// 4.1).
const scHost = "sc." + realm

// s6cNetwork is the shNetwork with the SC connected to the HSS over S6c
// as well, as the two roles would run.
type s6cNetwork struct {
	shNetwork
	scNode *diameter.Node
	scPeer *diameter.Peer
}

func newS6cNetwork(t *testing.T) s6cNetwork {
	t.Helper()
	n := newShNetwork(t)
	node := diameter.NewNode(diameter.Identity{OriginHost: scHost, OriginRealm: realm},
		[]uint32{sgd.ApplicationIDS6c}, diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}, quiet())
	peer := node.Connect(diameter.PeerConfig{Address: n.server.Addr(), OriginHost: "hss." + realm})
	t.Cleanup(peer.Stop)
	return s6cNetwork{shNetwork: n, scNode: node, scPeer: peer}
}

// srr sends a routing info query for msisdn, as the SMS-GMSC of the SC
// does before each delivery attempt.
func (n s6cNetwork) srr(t *testing.T, msisdn string) sgd.SendRoutingInfoForSMAnswer {
	t.Helper()
	a, err := sgd.ParseSendRoutingInfoForSMAnswer(n.requestFromSC(t, sgd.SendRoutingInfoForSMRequest{
		Header:            diameter.NewHeader(n.scNode, "", realm),
		MSISDN:            msisdn,
		SupportedFeatures: sgd.SMSFSupport,
		SCAddress:         scAddress,
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// requestFromSC sends m from the SC to the HSS, waiting for the
// CER/CEA if the connection is not open yet.
func (n s6cNetwork) requestFromSC(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		answer, err := n.scPeer.Request(context.Background(), m)
		if errors.Is(err, diameter.ErrPeerUnavailable) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		return answer
	}
}

// subscriber reads what the HSS stored for the user of impi.
func (n s6cNetwork) subscriber(t *testing.T) Subscriber {
	t.Helper()
	u, ok, err := n.hss.store.Subscriber(context.Background(), impi)
	if err != nil || !ok {
		t.Fatalf("subscriber = %v, %v", ok, err)
	}
	return u
}

// TS 29.338 5.2.1.3: the HSS names the IP-SM-GW registered for the user
// as the serving node, by the Diameter identity the registration left
// behind, so that the SMS-GMSC can send it the TFR.
func TestSendRoutingInfoNamesTheRegisteredIPSMGW(t *testing.T) {
	n := newS6cNetwork(t)
	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, []sh.DataReference{sh.SMSRegistrationInfo},
		smsRegistrationOverS6c(ipsmgwNumber)); r != success {
		t.Fatalf("PUR = %v", r)
	}

	a := n.srr(t, msisdnA)
	if a.Result != success {
		t.Fatalf("SRA = %v", a.Result)
	}
	want := &sgd.ServingNode{IPSMGWNumber: ipsmgwNumber, IPSMGWName: ipsmgwHost, IPSMGWRealm: realm}
	if a.ServingNode == nil || *a.ServingNode != *want {
		t.Errorf("Serving-Node = %+v, want %+v", a.ServingNode, want)
	}
	// The IMSI lets the IP-SM-GW find the user again when the TFR
	// arrives (TS 24.341 5.3.3.4.2).
	if a.UserName != "001010000000001" {
		t.Errorf("User-Name = %q, want the IMSI", a.UserName)
	}
	// Nothing failed, so nothing waits at a service centre.
	if u := n.subscriber(t); len(u.MessageWaitingData) != 0 || u.UNRI {
		t.Errorf("message waiting data = %v, UNRI = %v", u.MessageWaitingData, u.UNRI)
	}
}

// TS 29.338 5.2.1.3: with no node registered for MT SMS the HSS answers
// DIAMETER_ERROR_ABSENT_USER and keeps the SC address, so that the
// alert can reach the SC later.
func TestSendRoutingInfoWithoutAServingNodeKeepsTheSCAddress(t *testing.T) {
	n := newS6cNetwork(t)
	a := n.srr(t, msisdnA)
	if want := diameter.Experimental(sgd.DiameterErrorAbsentUser); a.Result != want {
		t.Fatalf("SRA = %v, want %v", a.Result, want)
	}
	if a.ServingNode != nil {
		t.Errorf("Serving-Node = %+v, want none", a.ServingNode)
	}
	u := n.subscriber(t)
	if len(u.MessageWaitingData) != 1 || u.MessageWaitingData[0] != scAddress {
		t.Errorf("message waiting data = %v, want [%s]", u.MessageWaitingData, scAddress)
	}
	if !u.UNRI {
		t.Error("UNRI = false, want true")
	}

	// A second attempt from the same SC does not add the address twice.
	n.srr(t, msisdnA)
	if u := n.subscriber(t); len(u.MessageWaitingData) != 1 {
		t.Errorf("after a second query, message waiting data = %v", u.MessageWaitingData)
	}
}

// An IP-SM-GW whose Diameter identity the HSS does not have cannot be
// named to an SMS-GMSC, which would have nowhere to send the TFR.
func TestSendRoutingInfoNeedsTheIPSMGWDiameterIdentity(t *testing.T) {
	n := newS6cNetwork(t)
	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, []sh.DataReference{sh.SMSRegistrationInfo},
		smsRegistration(ipsmgwNumber)); r != success {
		t.Fatalf("PUR = %v", r)
	}
	if a := n.srr(t, msisdnA); a.Result != diameter.Experimental(sgd.DiameterErrorAbsentUser) {
		t.Errorf("SRA = %v, want absent user", a.Result)
	}
}

func TestSendRoutingInfoForAnUnknownNumber(t *testing.T) {
	n := newS6cNetwork(t)
	a := n.srr(t, "12025550188")
	if want := diameter.Experimental(sgd.DiameterErrorUserUnknown); a.Result != want {
		t.Errorf("SRA = %v, want %v", a.Result, want)
	}
}

// The IMSI of TS 23.003 13.3 is the user part of the private identity,
// and a private identity that is not derived from one has none.
func TestIMSI(t *testing.T) {
	for _, c := range []struct{ privateIdentity, want string }{
		{"001010000000001@" + realm, "001010000000001"},
		{"+12025550104@" + realm, ""},
		{"@" + realm, ""},
		{"0010100000000012345@" + realm, ""},
		{"001010000000001", ""},
	} {
		if got := imsi(c.privateIdentity); got != c.want {
			t.Errorf("imsi(%q) = %q, want %q", c.privateIdentity, got, c.want)
		}
	}
}
