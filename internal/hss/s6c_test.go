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
	// alerts carries the ALRs that reach the SC.
	alerts chan sgd.AlertServiceCentreRequest
}

func newS6cNetwork(t *testing.T) s6cNetwork {
	t.Helper()
	n := newShNetwork(t)
	node := diameter.NewNode(diameter.Identity{OriginHost: scHost, OriginRealm: realm},
		[]uint32{sgd.ApplicationIDS6c}, diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}, quiet())
	sc := s6cNetwork{shNetwork: n, scNode: node, alerts: make(chan sgd.AlertServiceCentreRequest, 8)}
	// The SMS-IWMSC side of the SC: it takes the alerts of the HSS and
	// answers them.
	node.Handle(func(request *diam.Message) *diam.Message {
		alr, err := sgd.ParseAlertServiceCentreRequest(request)
		if err != nil {
			return sgd.AlertServiceCentreAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}.Message(request, node.Identity())
		}
		sc.alerts <- alr
		return sgd.AlertServiceCentreAnswer{Result: success}.Message(request, node.Identity())
	})
	sc.scPeer = node.Connect(diameter.PeerConfig{Address: n.server.Addr(), OriginHost: "hss." + realm})
	t.Cleanup(sc.scPeer.Stop)
	return sc
}

// alert waits for one ALR to reach the SC.
func (n s6cNetwork) alert(t *testing.T) sgd.AlertServiceCentreRequest {
	t.Helper()
	select {
	case alr := <-n.alerts:
		return alr
	case <-time.After(5 * time.Second):
		t.Fatal("no ALR reached the service centre")
		return sgd.AlertServiceCentreRequest{}
	}
}

// noAlert reports that no ALR reaches the SC for a while.
func (n s6cNetwork) noAlert(t *testing.T) {
	t.Helper()
	select {
	case alr := <-n.alerts:
		t.Fatalf("an ALR reached the service centre: %+v", alr)
	case <-time.After(200 * time.Millisecond):
	}
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

// rdr reports how a delivery through the IP-SM-GW ended, as the
// SMS-GMSC of the SC does.
func (n s6cNetwork) rdr(t *testing.T, msisdn string, cause sgd.SMDeliveryCause) sgd.ReportSMDeliveryStatusAnswer {
	t.Helper()
	return n.rdrWithFlags(t, msisdn, cause, 0)
}

func (n s6cNetwork) rdrWithFlags(t *testing.T, msisdn string, cause sgd.SMDeliveryCause, flags sgd.RDRFlags) sgd.ReportSMDeliveryStatusAnswer {
	t.Helper()
	a, err := sgd.ParseReportSMDeliveryStatusAnswer(n.requestFromSC(t, sgd.ReportSMDeliveryStatusRequest{
		Header:         diameter.NewHeader(n.scNode, "", realm),
		UserIdentifier: sgd.UserIdentifier{MSISDN: msisdn},
		SCAddress:      scAddress,
		SMDeliveryOutcome: sgd.SMDeliveryOutcome{
			IPSMGWSMDeliveryOutcome: &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: cause},
		},
		RDRFlags: flags,
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// register puts the IP-SM-GW of the test network in the HSS for the
// user, as the Sh update after an IMS registration does.
func (n s6cNetwork) register(t *testing.T) {
	t.Helper()
	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, []sh.DataReference{sh.SMSRegistrationInfo},
		smsRegistrationOverS6c(ipsmgwNumber)); r != success {
		t.Fatalf("PUR = %v", r)
	}
}

// waitForSubscriber waits until the HSS holds a subscriber that want
// accepts, which the alerts in the background need.
func (n s6cNetwork) waitForSubscriber(t *testing.T, what string, want func(Subscriber) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		u := n.subscriber(t)
		if want(u) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: message waiting data = %v, UNRI = %v, MCEF = %v", what, u.MessageWaitingData, u.UNRI, u.MCEF)
		}
		time.Sleep(10 * time.Millisecond)
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

// TS 23.040 3.2.8 case 1: a delivery that found the user absent puts
// the SC address into the message waiting data and sets UNRI; one that
// the UE refused for want of memory sets MCEF and clears UNRI, because
// the UE is reachable and only free memory helps.
func TestReportSMDeliveryStatusKeepsTheSCAddress(t *testing.T) {
	n := newS6cNetwork(t)
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseAbsentUser); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	u := n.subscriber(t)
	if len(u.MessageWaitingData) != 1 || u.MessageWaitingData[0] != scAddress || !u.UNRI || u.MCEF {
		t.Fatalf("after an absent user: MWD %v, UNRI %v, MCEF %v", u.MessageWaitingData, u.UNRI, u.MCEF)
	}

	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseUEMemoryCapacityExceeded); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	u = n.subscriber(t)
	if len(u.MessageWaitingData) != 1 || u.UNRI || !u.MCEF {
		t.Fatalf("after a full memory: MWD %v, UNRI %v, MCEF %v", u.MessageWaitingData, u.UNRI, u.MCEF)
	}

	// A transfer that succeeded takes the address out again (TS 29.338
	// 5.2.3.1).
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseSuccessfulTransfer); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	if u := n.subscriber(t); len(u.MessageWaitingData) != 0 {
		t.Errorf("after a successful transfer: MWD %v", u.MessageWaitingData)
	}
}

// TS 29.338 5.2.3.4: a message waiting list that is full is refused, so
// that the SC knows that only its own retries are left.
func TestReportSMDeliveryStatusWithAFullMessageWaitingList(t *testing.T) {
	n := newS6cNetwork(t)
	u := n.subscriber(t)
	// Other service centres, none of them the one that now reports.
	for i := range maxMessageWaitingData {
		u.MessageWaitingData = append(u.MessageWaitingData, "1202555020"+string(rune('0'+i)))
	}
	if err := n.hss.store.Put(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseAbsentUser)
	if want := diameter.Experimental(sgd.DiameterErrorMWDListFull); a.Result != want {
		t.Errorf("RDA = %v, want %v", a.Result, want)
	}
	// The flag is still set, so that the user's return still alerts the
	// service centres that did fit.
	if u := n.subscriber(t); !u.UNRI {
		t.Error("UNRI = false, want true")
	}
}

// A cause the HSS does not know leaves the message waiting data as it
// is, rather than counting as a delivery that succeeded.
func TestReportSMDeliveryStatusWithAnUnknownCause(t *testing.T) {
	n := newS6cNetwork(t)
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseAbsentUser); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCause(99)); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	if u := n.subscriber(t); len(u.MessageWaitingData) != 1 || !u.UNRI {
		t.Errorf("message waiting data = %v, UNRI = %v", u.MessageWaitingData, u.UNRI)
	}
}

func TestReportSMDeliveryStatusForAnUnknownNumber(t *testing.T) {
	n := newS6cNetwork(t)
	a := n.rdr(t, "12025550188", sgd.SMDeliveryCauseAbsentUser)
	if want := diameter.Experimental(sgd.DiameterErrorUserUnknown); a.Result != want {
		t.Errorf("RDA = %v, want %v", a.Result, want)
	}
}

// The whole store and deliver round inside the home network, as far as
// the HSS sees it: a delivery finds no node, the SC address stays, the
// user registers again, and the HSS alerts the SC (TS 23.040 3.2.8 case
// 1 item 2c, TS 23.204 6.5b step 2, TS 29.338 5.2.2).
func TestRegistrationAlertsTheServiceCentre(t *testing.T) {
	n := newS6cNetwork(t)
	if a := n.srr(t, msisdnA); a.Result != diameter.Experimental(sgd.DiameterErrorAbsentUser) {
		t.Fatalf("SRA = %v", a.Result)
	}
	// Waiting for the first answer opens the SC's connection, over which
	// the alert then travels back.
	n.noAlert(t)

	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, []sh.DataReference{sh.SMSRegistrationInfo},
		smsRegistrationOverS6c(ipsmgwNumber)); r != success {
		t.Fatalf("PUR = %v", r)
	}
	alr := n.alert(t)
	if alr.SCAddress != scAddress || alr.UserIdentifier.MSISDN != msisdnA {
		t.Errorf("ALR = %+v, want SC-Address %s and MSISDN %s", alr, scAddress, msisdnA)
	}
	// The SC took the alert, so its address leaves the message waiting
	// data, and UNRI goes with the registration.
	deadline := time.Now().Add(5 * time.Second)
	for {
		u := n.subscriber(t)
		if len(u.MessageWaitingData) == 0 && !u.UNRI {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message waiting data = %v, UNRI = %v", u.MessageWaitingData, u.UNRI)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The SC now gets a serving node and delivers.
	if a := n.srr(t, msisdnA); a.Result != success || a.ServingNode == nil {
		t.Errorf("SRA = %v, %+v", a.Result, a.ServingNode)
	}
}

// TS 23.040 3.2.8 case 1 item 2c: while MCEF is set, the return of the
// user alerts nobody. Only free memory in the UE helps then, so the
// message waiting data stays as it is.
func TestRegistrationDoesNotAlertWhileTheMemoryIsFull(t *testing.T) {
	n := newS6cNetwork(t)
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseUEMemoryCapacityExceeded); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, []sh.DataReference{sh.SMSRegistrationInfo},
		smsRegistrationOverS6c(ipsmgwNumber)); r != success {
		t.Fatalf("PUR = %v", r)
	}
	n.noAlert(t)
	if u := n.subscriber(t); len(u.MessageWaitingData) != 1 || !u.MCEF {
		t.Errorf("message waiting data = %v, MCEF = %v", u.MessageWaitingData, u.MCEF)
	}
}

// A deregistration leaves the message waiting data alone: the user is
// not reachable, so there is nothing to alert about.
func TestDeregistrationSendsNoAlert(t *testing.T) {
	n := newS6cNetwork(t)
	if a := n.srr(t, msisdnA); a.Result != diameter.Experimental(sgd.DiameterErrorAbsentUser) {
		t.Fatalf("SRA = %v", a.Result)
	}
	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, []sh.DataReference{sh.SMSRegistrationInfo},
		smsRegistrationOverS6c("")); r != success {
		t.Fatalf("PUR = %v", r)
	}
	n.noAlert(t)
	if u := n.subscriber(t); len(u.MessageWaitingData) != 1 || !u.UNRI {
		t.Errorf("message waiting data = %v, UNRI = %v", u.MessageWaitingData, u.UNRI)
	}
}

// TS 29.338 5.2.1.3 leaves out a serving node that is marked with the
// mobile not reachable flag. Over IP that flag is UNRI, so a user whose
// last delivery found it absent gets no serving node, however the
// IP-SM-GW is registered, until it is reachable again.
func TestSendRoutingInfoLeavesOutANodeMarkedNotReachable(t *testing.T) {
	n := newS6cNetwork(t)
	n.register(t)
	if a := n.srr(t, msisdnA); a.Result != success {
		t.Fatalf("SRA = %v", a.Result)
	}

	// The delivery through that very IP-SM-GW found the user absent.
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseAbsentUser); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	if u := n.subscriber(t); !u.UNRI || u.IPSMGW.OriginHost != ipsmgwHost {
		t.Fatalf("UNRI = %v, IP-SM-GW = %q", u.UNRI, u.IPSMGW.OriginHost)
	}
	a := n.srr(t, msisdnA)
	if want := diameter.Experimental(sgd.DiameterErrorAbsentUser); a.Result != want {
		t.Errorf("SRA = %v, want %v", a.Result, want)
	}
	if a.ServingNode != nil {
		t.Errorf("Serving-Node = %+v, want none", a.ServingNode)
	}

	// The user is reachable again, so the flag goes and the node comes
	// back.
	n.register(t)
	n.waitForSubscriber(t, "after the registration", func(u Subscriber) bool { return !u.UNRI })
	if a := n.srr(t, msisdnA); a.Result != success || a.ServingNode == nil {
		t.Errorf("SRA = %v, %+v", a.Result, a.ServingNode)
	}
}

// TS 23.040 3.2.6 items 2g and 2h: a short message that arrived proves
// that the UE is reachable and has memory, so the flags go and the
// service centres that are left are alerted. Without this the memory
// flag would stop every later alert.
func TestDeliveredShortMessageClearsTheMemoryFlag(t *testing.T) {
	n := newS6cNetwork(t)
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseUEMemoryCapacityExceeded); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	// Another service centre holds a short message for the user as well.
	other := "12025550200"
	u := n.subscriber(t)
	u.MessageWaitingData = append(u.MessageWaitingData, other)
	if err := n.hss.store.Put(context.Background(), u); err != nil {
		t.Fatal(err)
	}

	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseSuccessfulTransfer); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	n.waitForSubscriber(t, "after the delivery", func(u Subscriber) bool { return !u.MCEF && !u.UNRI })
	// The other service centre has no Diameter identity in the
	// configuration, so the alert it cannot get purges its address (TS
	// 29.338 5.2.2.2) rather than filling the list for good.
	n.waitForSubscriber(t, "the purge of an address that cannot be alerted",
		func(u Subscriber) bool { return len(u.MessageWaitingData) == 0 })

	// With the flag gone, the return of the user alerts again.
	if a := n.rdr(t, msisdnA, sgd.SMDeliveryCauseAbsentUser); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	n.register(t)
	if alr := n.alert(t); alr.SCAddress != scAddress {
		t.Errorf("ALR = %+v", alr)
	}
}

// TS 29.338 5.2.1.3 and 5.2.3.4: for a short message that gets one
// delivery attempt the HSS does not keep the service centre address,
// because an alert would come too late to be of use.
func TestSingleAttemptDeliveryKeepsNoSCAddress(t *testing.T) {
	n := newS6cNetwork(t)
	a, err := sgd.ParseSendRoutingInfoForSMAnswer(n.requestFromSC(t, sgd.SendRoutingInfoForSMRequest{
		Header: diameter.NewHeader(n.scNode, "", realm), MSISDN: msisdnA, SCAddress: scAddress,
		SRRFlags: sgd.SRRSingleAttemptDelivery,
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if want := diameter.Experimental(sgd.DiameterErrorAbsentUser); a.Result != want {
		t.Fatalf("SRA = %v, want %v", a.Result, want)
	}
	if u := n.subscriber(t); len(u.MessageWaitingData) != 0 {
		t.Errorf("after the query, message waiting data = %v", u.MessageWaitingData)
	}

	if a := n.rdrWithFlags(t, msisdnA, sgd.SMDeliveryCauseAbsentUser, sgd.RDRSingleAttemptDelivery); a.Result != success {
		t.Fatalf("RDA = %v", a.Result)
	}
	if u := n.subscriber(t); len(u.MessageWaitingData) != 0 {
		t.Errorf("after the report, message waiting data = %v", u.MessageWaitingData)
	}
}

// A registration that lands between the read of the subscriber and the
// write of the message waiting data must not leave an address behind
// that no alert will ever reach: the query is answered with the serving
// node that the registration brought.
func TestSendRoutingInfoSeesARegistrationUnderTheLock(t *testing.T) {
	n := newS6cNetwork(t)
	// Open the connection of the SC first, so that the query below is
	// the only thing the HSS waits for.
	n.srr(t, msisdnA)
	n.waitForSubscriber(t, "the first query", func(u Subscriber) bool { return len(u.MessageWaitingData) == 1 })

	// Hold the lock of the subscriber, register the IP-SM-GW behind it
	// and clear what the first query left, as a PUR would, and only then
	// let the query that is waiting go on.
	unlock, err := n.hss.users.Lock(context.Background(), impi)
	if err != nil {
		t.Fatal(err)
	}
	answers := make(chan sgd.SendRoutingInfoForSMAnswer, 1)
	go func() { answers <- n.srr(t, msisdnA) }()
	// The query is past its unlocked read by the time it blocks on the
	// lock; waiting makes the interleaving the one under test.
	time.Sleep(200 * time.Millisecond)
	u := n.subscriber(t)
	u.IPSMGWNumber, u.IPSMGW = ipsmgwNumber, diameter.Identity{OriginHost: ipsmgwHost, OriginRealm: realm}
	u.MessageWaitingData, u.UNRI = nil, false
	if err := n.hss.store.Put(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	unlock()

	a := <-answers
	if a.Result != success || a.ServingNode == nil {
		t.Fatalf("SRA = %v, %+v, want the serving node that the registration brought", a.Result, a.ServingNode)
	}
	if u := n.subscriber(t); len(u.MessageWaitingData) != 0 || u.UNRI {
		t.Errorf("message waiting data = %v, UNRI = %v, want nothing waiting", u.MessageWaitingData, u.UNRI)
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
