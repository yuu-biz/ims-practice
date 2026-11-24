package ipsmgw_test

import (
	"context"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// This file runs the three nodes that carry a short message in one
// process, each the one the network has: the IP-SM-GW of this package,
// the Service Centre of package smsc and the HSS of package hss, on
// real Diameter connections between them. What the tests here check is
// the path that no single package sees, from the short message of a UE
// to the same UE and back (TS 23.204 6.3, 6.4, 6.5a and 6.5b).
//
// Alice is the only registered user, so she sends to herself: the short
// message goes out over SGd and comes back over it, and one registration
// serves both ends of the flow.

// hssDiameterHost is the Diameter identity of the HSS of the chain.
const hssDiameterHost = "hss." + domain

// chain is the IP-SM-GW of a harness with a real Service Centre and a
// real HSS behind it.
type chain struct {
	*harness
	store  hss.Store
	centre *smsc.SMSC
	// reports are the submit reports and status reports that reached the
	// entry point while the tests waited for a delivery.
	reports []*sip.Message
}

// aliceSubscription is Alice as the HSS holds her: the private identity
// of TS 23.003 13.3 carries her IMSI, and the tel URI among her public
// identities is her MSISDN (TS 29.328 7.6.9).
func aliceSubscription() hss.Subscription {
	return hss.Subscription{PrivateIdentity: aliceIMSI + "@" + domain,
		PublicIdentities: []string{alice, aliceTel}, Password: "test-password-1"}
}

// newChain starts the HSS, the Service Centre and the IP-SM-GW, each on
// a Diameter node of its own, and opens the connections between them:
// the SC to the HSS, so that the alert comes back over it (TS 29.338
// 5.2.2), and the IP-SM-GW to both, as the end that brings the mobile
// originated short messages (6.3.1.1).
func newChain(t *testing.T, answer func(*sip.Message) int) *chain {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	if err := sgd.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	hssID := diameter.Identity{OriginHost: hssDiameterHost, OriginRealm: domain}
	hssNode := diameter.NewNode(hssID, []uint32{diameter.ApplicationIDSh, sgd.ApplicationIDS6c}, sgdTimers, quiet())
	store, err := hss.NewMemoryStore(aliceSubscription())
	if err != nil {
		t.Fatal(err)
	}
	hssRole, err := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain},
		ServiceCentres: []hss.ServiceCentre{{Address: scNumber, Host: scDiameterHost, Realm: domain}}},
		store, hssNode, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hssRole.Close)
	hssNode.Handle(hssRole.Handle)
	hssServer, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hssServer.Stop)
	hssRole.SetPeers(hssServer)

	scID := diameter.Identity{OriginHost: scDiameterHost, OriginRealm: domain}
	scNode := diameter.NewNode(scID, []uint32{sgd.ApplicationIDS6c, sgd.ApplicationIDSGd}, sgdTimers, quiet())
	scServer, err := scNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scServer.Stop)
	scToHSS := scNode.Connect(diameter.PeerConfig{Address: hssServer.Addr(), OriginHost: hssID.OriginHost})
	t.Cleanup(scToHSS.Stop)
	// One retry an hour from now, so that a short message the SC holds
	// goes out again only because the HSS alerted it.
	centre := smsc.New(smsc.Config{Address: scNumber, HSSRealm: domain,
		RetrySchedule: []time.Duration{time.Hour}, PollInterval: 20 * time.Millisecond},
		smsc.NewMemoryStore(), sgd.NewSGdClient(scNode, scServer, 30*time.Second),
		sgd.NewS6cClient(scNode, scToHSS), smsc.SystemClock, quiet())
	scNode.Handle(func(request *diam.Message) *diam.Message { return serviceCentreAnswer(t, scID, centre, request) })
	ctx, stop := context.WithCancel(context.Background())
	running := make(chan struct{})
	go func() {
		centre.Run(ctx)
		close(running)
	}()
	t.Cleanup(func() {
		stop()
		<-running
	})

	gwNode := diameter.NewNode(diameter.Identity{OriginHost: gwDiameterHost, OriginRealm: domain},
		[]uint32{diameter.ApplicationIDSh, sgd.ApplicationIDSGd}, sgdTimers, quiet())
	c := &chain{store: store, centre: centre}
	gwNode.Handle(func(request *diam.Message) *diam.Message { return c.harness.g.Handle(request) })
	gwToSC := gwNode.Connect(diameter.PeerConfig{Address: scServer.Addr(), OriginHost: scID.OriginHost})
	t.Cleanup(gwToSC.Stop)
	gwToHSS := gwNode.Connect(diameter.PeerConfig{Address: hssServer.Addr(), OriginHost: hssID.OriginHost})
	t.Cleanup(gwToHSS.Stop)

	c.harness = newHarnessParts(t, ipsmgw.NewMemoryStore(), time.Minute, answer, ipsmgw.Config{},
		ipsmgw.NewSMSIWMSC(sgd.NewMOClient(gwNode, gwToSC), scNumber, domain, quiet()),
		sh.NewClient(gwNode, gwToHSS, domain))
	waitForPeers(t, gwToSC, gwToHSS, scToHSS)
	return c
}

// serviceCentreAnswer hands an OFR or an ALR to the SC and writes its
// answer, which is what the sc role of the ims command does.
func serviceCentreAnswer(t *testing.T, identity diameter.Identity, centre *smsc.SMSC, request *diam.Message) *diam.Message {
	t.Helper()
	if request.Header.CommandCode == sgd.CommandMOForwardShortMessage {
		ofr, err := sgd.ParseMOForwardShortMessageRequest(request)
		if err != nil {
			t.Errorf("the SC cannot read the OFR: %v", err)
			return diameter.CommandUnsupportedAnswer(identity, request)
		}
		return centre.MOForwardShortMessage(context.Background(), ofr).Message(request, identity)
	}
	alr, err := sgd.ParseAlertServiceCentreRequest(request)
	if err != nil {
		t.Errorf("the SC cannot read the ALR: %v", err)
		return diameter.CommandUnsupportedAnswer(identity, request)
	}
	return centre.AlertServiceCentre(context.Background(), alr).Message(request, identity)
}

// waitForPeers waits until every connection is open, so that the first
// request of a test is not the one that finds a peer still connecting.
func waitForPeers(t *testing.T, peers ...*diameter.Peer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		open := true
		for _, p := range peers {
			open = open && p.Available()
		}
		if open {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the Diameter connections of the chain never opened")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// registerAlice runs Alice's registration through the IP-SM-GW and waits
// until the HSS holds this IP-SM-GW for her, which is what makes the
// Service Centre's routing query name it (TS 29.338 5.2.1.2). It returns
// the SUBSCRIBE, which a later NOTIFY of a test answers within.
func (c *chain) registerAlice() *sip.Message {
	c.t.Helper()
	c.thirdPartyRegister(3600)
	subscribe := c.subscribed()
	c.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	c.waitForSubscriber("the IP-SM-GW is registered for Alice", func(u hss.Subscriber) bool {
		return u.IPSMGWNumber == number && u.IPSMGW.OriginHost == gwDiameterHost
	})
	return subscribe
}

// waitForSubscriber waits until Alice is as want says in the store of
// the HSS. The IP-SM-GW writes over Sh beside the request it answers,
// and the HSS alerts the service centres beside the request it answers,
// so a test waits for what it needs rather than for a message.
func (c *chain) waitForSubscriber(what string, want func(hss.Subscriber) bool) hss.Subscriber {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		u, ok, err := c.store.Subscriber(context.Background(), aliceIMSI+"@"+domain)
		if err != nil {
			c.t.Fatalf("the store of the HSS: %v", err)
		}
		if ok && want(u) {
			return u
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("the HSS never came to the state where %s: %+v", what, u)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// toAlice is a short message from Alice to her own number, which the SC
// takes over SGd and brings back to the same IP-SM-GW.
func toAlice(mr uint8) sms.Submit {
	return sms.Submit{MessageReference: mr, DestinationAddress: sms.InternationalNumber("+12025550101"),
		UserData: hello.UserData}
}

// nextDelivery is the next MESSAGE at the entry point that carries a
// short message for the UE. The submit report of a short message and the
// delivery of it are two MESSAGEs that the nodes send at the same time,
// so the reports before it are kept in c.reports for a test to read.
func (c *chain) nextDelivery() (*sip.Message, sms.RPDataNetworkToMS) {
	c.t.Helper()
	for {
		m := c.next()
		if data, ok := parseRP(c.t, m).(sms.RPDataNetworkToMS); ok {
			return m, data
		}
		c.reports = append(c.reports, m)
	}
}

// nextReportRP is the next answer to a short message of Alice's that
// reached the entry point, from the ones nextDelivery kept and then from
// the entry point itself.
func (c *chain) nextReportRP() sms.RPMessage {
	c.t.Helper()
	if len(c.reports) > 0 {
		m := c.reports[0]
		c.reports = c.reports[1:]
		return parseRP(c.t, m)
	}
	return parseRP(c.t, c.next())
}

// TestShortMessageThroughTheServiceCentre is TS 23.204 6.3 and 6.4: the
// short message of a UE reaches the Service Centre over SGd, the Service
// Centre asks the HSS which node serves the recipient and brings it back
// to the IP-SM-GW, and the UE's delivery report ends the delivery.
func TestShortMessageThroughTheServiceCentre(t *testing.T) {
	c := newChain(t, answerOK)
	c.registerAlice()

	if resp := c.send(message(submitBody(t, 5, scAddress, toAlice(5)))); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	mt, data := c.nextDelivery()
	if mt.RequestURI != aliceTel {
		t.Errorf("Request-URI = %s, want %s", mt.RequestURI, aliceTel)
	}
	if data.OriginatorAddress != scAddress {
		t.Errorf("RP-OA = %v, want the SC", data.OriginatorAddress)
	}
	tpdu, err := sms.ParseTPDU(data)
	got, ok := tpdu.(sms.Deliver)
	if err != nil || !ok || got.OriginatingAddress != sms.InternationalNumber("+12025550101") {
		t.Fatalf("the UE got %+v, %v; want an SMS-DELIVER from Alice", tpdu, err)
	}
	// The SC accepted the short message before it delivered it, so the
	// submit report of TS 24.341 5.3.3.4.3 is on its way or here already.
	if rp, isAck := c.nextReportRP().(sms.RPAck); !isAck || rp.MessageReference != 5 {
		t.Errorf("the sender got %+v, want an RP-ACK with RP-MR 5", rp)
	}

	report := deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
		UserData: marshal(t, sms.DeliverReport{})})
	if resp := c.send(report); resp.StatusCode != 202 {
		t.Fatalf("delivery report answered %d, want 202", resp.StatusCode)
	}
	// Nothing is left waiting for the user: the HSS holds no service
	// centre for her, and the SC heard that the short message arrived.
	c.waitForSubscriber("no service centre waits for Alice", func(u hss.Subscriber) bool {
		return len(u.MessageWaitingData) == 0 && !u.UNRI
	})
}

// TestAbsentUserIsAlertedThroughTheServiceCentre is TS 23.204 6.5a and
// 6.5b with TS 23.040 3.2.6 item 2c: a UE that answers 480 leaves the
// short message at the Service Centre, whose address the HSS keeps; the
// next request from the UE tells the HSS that it can take short messages
// again, the HSS alerts the Service Centre, and the short message is
// delivered then.
func TestAbsentUserIsAlertedThroughTheServiceCentre(t *testing.T) {
	// The UE takes no short message until the test lets it: every
	// MESSAGE that carries one for it is answered 480, as a UE that is
	// reached but cannot take it answers (TS 24.341 5.3.3.4.2).
	available := make(chan struct{})
	answer := func(m *sip.Message) int {
		if _, forUE := parseRPOf(m).(sms.RPDataNetworkToMS); !forUE {
			return 200
		}
		select {
		case <-available:
			return 200
		default:
			return 480
		}
	}
	c := newChain(t, answer)
	c.registerAlice()

	if resp := c.send(message(submitBody(t, 6, scAddress, toAlice(6)))); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	if _, data := c.nextDelivery(); data.OriginatorAddress != scAddress {
		t.Errorf("RP-OA = %v, want the SC", data.OriginatorAddress)
	}
	// The delivery failed with an absent subscriber, so the SC reported
	// it to the HSS, which keeps the SC's address and marks the user
	// unreachable for short messages.
	c.waitForSubscriber("the SC waits for Alice", func(u hss.Subscriber) bool {
		return len(u.MessageWaitingData) == 1 && u.MessageWaitingData[0] == scNumber && u.UNRI
	})

	// The UE is back. Any request of hers says so, and this one is a
	// short message of her own, which the Service Centre takes as well.
	close(available)
	if resp := c.send(message(submitBody(t, 7, scAddress, toAlice(7)))); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	// The notification cleared the flag and the alert took the address
	// out of the message waiting data.
	c.waitForSubscriber("no service centre waits for Alice anymore", func(u hss.Subscriber) bool {
		return len(u.MessageWaitingData) == 0 && !u.UNRI
	})
	// Both short messages reach the UE: the one the SC held, which the
	// alert set off, and the one she has just sent. They come one after
	// another, since TS 23.040 6.2 allows the Service Centre one
	// outstanding SMS-DELIVER to a UE at a time, so each one's delivery
	// report is what lets the next go out.
	for i := 0; i < 2; i++ {
		mt, data := c.nextDelivery()
		if data.OriginatorAddress != scAddress {
			t.Errorf("RP-OA = %v, want the SC", data.OriginatorAddress)
		}
		c.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
			UserData: marshal(t, sms.DeliverReport{})}))
	}
}

// TestMemoryFullIsAlertedThroughTheServiceCentre is TS 23.204 6.6 with
// TS 23.040 3.2.6 item 2f: a UE whose memory is full answers the short
// message with RP-ERROR, RP-Cause 22, so the Service Centre keeps it and
// the HSS marks the memory capacity exceeded; the UE's notification that
// it has memory again clears the mark, the HSS alerts the Service
// Centre, and the short message is delivered then.
func TestMemoryFullIsAlertedThroughTheServiceCentre(t *testing.T) {
	c := newChain(t, answerOK)
	c.registerAlice()

	if resp := c.send(message(submitBody(t, 8, scAddress, toAlice(8)))); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	mt, data := c.nextDelivery()
	// The UE took the MESSAGE and then reported that it had no memory
	// for the short message (TS 24.011 table 8.4 part 2).
	c.send(deliveryReport(t, mt, sms.RPError{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
		Cause: sms.RPCauseMemoryCapacityExceeded, UserData: marshal(t, sms.DeliverReport{FailureCause: sms.FCSMemoryCapacityExceeded})}))
	c.waitForSubscriber("the SC waits for Alice and her memory is full", func(u hss.Subscriber) bool {
		return len(u.MessageWaitingData) == 1 && u.MessageWaitingData[0] == scNumber && u.MCEF
	})

	// The UE has memory again and says so in an RP-SMMA.
	if resp := c.send(message(marshal(t, sms.RPSMMA{MessageReference: 2}))); resp.StatusCode != 202 {
		t.Fatalf("the notification was answered %d, want 202", resp.StatusCode)
	}
	c.waitForSubscriber("no service centre waits for Alice anymore", func(u hss.Subscriber) bool {
		return len(u.MessageWaitingData) == 0 && !u.MCEF && !u.UNRI
	})
	mt, data = c.nextDelivery()
	c.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
		UserData: marshal(t, sms.DeliverReport{})}))
}

// parseRPOf reads the RP message of a MESSAGE, or nil where there is
// none. The entry point of a chain reads it to tell a short message for
// the UE from the reports that go to it.
func parseRPOf(m *sip.Message) sms.RPMessage {
	rp, err := sms.ParseRP(m.Body)
	if err != nil {
		return nil
	}
	return rp
}
