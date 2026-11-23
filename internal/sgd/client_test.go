package sgd

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// The clients are tested over a Diameter connection on the loopback
// address, the way the two nodes speak in a network: one node listens
// and the other connects to it, and each answers the requests of the
// other with its handler.

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testTimers are short, since every exchange of the tests is on the
// loopback address. The Answer timer is what a client with a timeout of
// its own replaces.
var testTimers = diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: time.Second}

// connection is two nodes of a test, with the Diameter connection that
// the second opened to the first.
type connection struct {
	listening  *diameter.Node
	server     *diameter.Server
	connecting *diameter.Node
	peer       *diameter.Peer
	// received carries every request that reached a handler, so that a
	// test can read what went out on the wire.
	received chan *diam.Message
}

// connect makes the two nodes and opens the connection. answer builds
// the answer to a request that reaches either node; a nil answer leaves
// the request unanswered until the client gives up.
func connect(t *testing.T, listening, connecting diameter.Identity, application uint32, answer func(*diam.Message) *diam.Message) *connection {
	t.Helper()
	c := &connection{received: make(chan *diam.Message, 8)}
	handle := func(request *diam.Message) *diam.Message {
		c.received <- request
		if a := answer(request); a != nil {
			return a
		}
		// RFC 6733 6.2 asks for an answer to every request; a test that
		// wants none lets the client's timeout pass first.
		time.Sleep(2 * testTimers.Answer)
		return diameter.CommandUnsupportedAnswer(listening, request)
	}
	c.listening = diameter.NewNode(listening, []uint32{application}, testTimers, quiet())
	c.listening.Handle(handle)
	server, err := c.listening.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.server = server
	t.Cleanup(server.Stop)
	c.connecting = diameter.NewNode(connecting, []uint32{application}, testTimers, quiet())
	c.connecting.Handle(handle)
	c.peer = c.connecting.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: listening.OriginHost})
	t.Cleanup(c.peer.Stop)
	until(t, "the connection to open", c.peer.Available)
	return c
}

// request is the one request that reached a handler.
func (c *connection) request(t *testing.T) *diam.Message {
	t.Helper()
	select {
	case m := <-c.received:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached the other node")
		return nil
	}
}

// noRequest reports that nothing reached the other node, which is how a
// client that refuses a request before it sends it shows.
func (c *connection) noRequest(t *testing.T) {
	t.Helper()
	select {
	case m := <-c.received:
		t.Fatalf("a request reached the other node: %v", m)
	default:
	}
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited in vain for %s", what)
}

// TestS6cClientSendsTheRoutingQuery: the SC asks the HSS which node
// serves a recipient, and the client fills in the Session-Id and the
// origin of its node.
func TestS6cClientSendsTheRoutingQuery(t *testing.T) {
	node := ServingNode{IPSMGWNumber: mmeNumber, IPSMGWName: "ipsmgw." + realm, IPSMGWRealm: realm}
	c := connect(t, hss, sc, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return SendRoutingInfoForSMAnswer{
			Result:      diameter.Base(diameter.DiameterSuccess),
			UserName:    imsi,
			ServingNode: &node,
		}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)

	sra, err := client.SendRoutingInfoForSM(t.Context(), SendRoutingInfoForSMRequest{
		Header:            diameter.Header{DestinationHost: hssHost, DestinationRealm: realm},
		MSISDN:            msisdn,
		SupportedFeatures: SMSFSupport,
		SCAddress:         scAddress,
	})
	if err != nil {
		t.Fatalf("SendRoutingInfoForSM: %v", err)
	}
	if !sra.Result.Success() || sra.UserName != imsi || sra.ServingNode == nil || *sra.ServingNode != node {
		t.Errorf("SRA = %+v, want the serving node of %s", sra, imsi)
	}

	srr, err := ParseSendRoutingInfoForSMRequest(c.request(t))
	if err != nil {
		t.Fatal(err)
	}
	if srr.OriginHost != scHost || srr.OriginRealm != realm || srr.DestinationHost != hssHost || srr.DestinationRealm != realm {
		t.Errorf("the SRR was routed %+v", srr.Header)
	}
	if !strings.HasPrefix(srr.SessionID, scHost+";") {
		t.Errorf("Session-Id = %q, want one of %s", srr.SessionID, scHost)
	}
	if srr.MSISDN != msisdn || srr.SCAddress != scAddress || srr.SupportedFeatures != SMSFSupport {
		t.Errorf("the SRR asked about %+v", srr)
	}
}

// TestS6cClientReturnsTheResultOfTheAnswer: an answer that says the user
// is absent is an answer, not a failure of the client: the SC reads the
// result and keeps the short message.
func TestS6cClientReturnsTheResultOfTheAnswer(t *testing.T) {
	c := connect(t, hss, sc, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return SendRoutingInfoForSMAnswer{Result: diameter.Experimental(DiameterErrorAbsentUser), UserName: imsi}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)

	sra, err := client.SendRoutingInfoForSM(t.Context(), SendRoutingInfoForSMRequest{
		Header:    diameter.Header{DestinationRealm: realm},
		MSISDN:    msisdn,
		SCAddress: scAddress,
	})
	if err != nil {
		t.Fatalf("SendRoutingInfoForSM: %v", err)
	}
	if sra.Result != diameter.Experimental(DiameterErrorAbsentUser) {
		t.Errorf("SRA result = %v, want DIAMETER_ERROR_ABSENT_USER", sra.Result)
	}
	// 5.3.1.1.3: a request that does not name the HSS of the user carries
	// Destination-Realm alone.
	srr, err := ParseSendRoutingInfoForSMRequest(c.request(t))
	if err != nil {
		t.Fatal(err)
	}
	if srr.DestinationHost != "" {
		t.Errorf("Destination-Host = %q, want none", srr.DestinationHost)
	}
}

// TestS6cClientReportsTheDeliveryStatus: the SC has the HSS keep its
// address in the message waiting data of an absent user.
func TestS6cClientReportsTheDeliveryStatus(t *testing.T) {
	c := connect(t, hss, sc, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return ReportSMDeliveryStatusAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)

	rda, err := client.ReportSMDeliveryStatus(t.Context(), ReportSMDeliveryStatusRequest{
		Header:         diameter.Header{DestinationHost: hssHost, DestinationRealm: realm},
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
		SCAddress:      scAddress,
		SMDeliveryOutcome: SMDeliveryOutcome{
			IPSMGWSMDeliveryOutcome: &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseAbsentUser},
		},
	})
	if err != nil {
		t.Fatalf("ReportSMDeliveryStatus: %v", err)
	}
	if !rda.Result.Success() {
		t.Errorf("RDA result = %v", rda.Result)
	}

	rdr, err := ParseReportSMDeliveryStatusRequest(c.request(t))
	if err != nil {
		t.Fatal(err)
	}
	if rdr.OriginHost != scHost || rdr.UserIdentifier.MSISDN != msisdn || rdr.SCAddress != scAddress {
		t.Errorf("the RDR reported %+v", rdr)
	}
	if o := rdr.SMDeliveryOutcome.IPSMGWSMDeliveryOutcome; o == nil || o.SMDeliveryCause != SMDeliveryCauseAbsentUser {
		t.Errorf("the outcome through the IP-SM-GW is %+v", o)
	}
}

// TestS6cClientRefusesANumberThatIsNotE164: a number that TBCD cannot
// hold never goes out, so that no node has to read octets that are not
// digits.
func TestS6cClientRefusesANumberThatIsNotE164(t *testing.T) {
	c := connect(t, hss, sc, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)
	header := diameter.Header{DestinationRealm: realm}

	if _, err := client.SendRoutingInfoForSM(t.Context(), SendRoutingInfoForSMRequest{
		Header: header, MSISDN: "+1 202 555 0112", SCAddress: scAddress,
	}); err == nil {
		t.Error("SendRoutingInfoForSM sent an MSISDN that is not E.164")
	}
	if _, err := client.ReportSMDeliveryStatus(t.Context(), ReportSMDeliveryStatusRequest{
		Header: header, UserIdentifier: UserIdentifier{MSISDN: msisdn}, SCAddress: "sc",
		SMDeliveryOutcome: SMDeliveryOutcome{IPSMGWSMDeliveryOutcome: &NodeSMDeliveryOutcome{}},
	}); err == nil {
		t.Error("ReportSMDeliveryStatus sent an SC address that is not E.164")
	}
	c.noRequest(t)
}

// TestS6cClientRefusesARequestTheFormatWouldNotCarry: an AVP that the
// command format of TS 29.338 marks mandatory, and that the caller left
// out, is refused before anything goes out; the receiver would answer
// such a request with DIAMETER_MISSING_AVP.
func TestS6cClientRefusesARequestTheFormatWouldNotCarry(t *testing.T) {
	c := connect(t, hss, sc, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)
	outcome := SMDeliveryOutcome{IPSMGWSMDeliveryOutcome: &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseAbsentUser}}

	// 5.3.2.3 and 5.3.2.7: { Destination-Realm }.
	if _, err := client.SendRoutingInfoForSM(t.Context(), SendRoutingInfoForSMRequest{
		MSISDN: msisdn, SCAddress: scAddress,
	}); err == nil {
		t.Error("SendRoutingInfoForSM sent an SRR that names no realm")
	}
	if _, err := client.ReportSMDeliveryStatus(t.Context(), ReportSMDeliveryStatusRequest{
		UserIdentifier: UserIdentifier{MSISDN: msisdn}, SCAddress: scAddress, SMDeliveryOutcome: outcome,
	}); err == nil {
		t.Error("ReportSMDeliveryStatus sent an RDR that names no realm")
	}
	// 5.3.2.7: { SC-Address }.
	if _, err := client.ReportSMDeliveryStatus(t.Context(), ReportSMDeliveryStatusRequest{
		Header:         diameter.Header{DestinationRealm: realm},
		UserIdentifier: UserIdentifier{MSISDN: msisdn}, SMDeliveryOutcome: outcome,
	}); err == nil {
		t.Error("ReportSMDeliveryStatus sent an RDR that carries no SC-Address")
	}
	c.noRequest(t)

	// An SRR, whose SC-Address 5.3.2.3 leaves optional, goes without one.
	if _, err := client.SendRoutingInfoForSM(t.Context(), SendRoutingInfoForSMRequest{
		Header: diameter.Header{DestinationRealm: realm}, MSISDN: msisdn,
	}); err != nil {
		t.Errorf("SendRoutingInfoForSM with no SC-Address: %v", err)
	}
	if srr, err := ParseSendRoutingInfoForSMRequest(c.request(t)); err != nil || srr.SCAddress != "" {
		t.Errorf("the SRR carried SC-Address %q, %v", srr.SCAddress, err)
	}
}

// TestSGdClientForwardsToTheServingNode: the SC forwards a short message
// over the connection that the serving node opened, and reads the
// acknowledgement of the UE out of the answer.
func TestSGdClientForwardsToTheServingNode(t *testing.T) {
	deliverReport := mustHex(t, smsDeliverReport)
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		return MTForwardShortMessageAnswer{
			Result: diameter.Base(diameter.DiameterSuccess),
			SMRPUI: deliverReport,
		}.Message(request, mme)
	})
	client := NewSGdClient(c.listening, c.server, 0)

	deliver := mustHex(t, smsDeliver)
	tfa, err := client.MTForwardShortMessage(t.Context(), MTForwardShortMessageRequest{
		Header:            diameter.Header{DestinationHost: mmeHost, DestinationRealm: realm},
		UserName:          imsi,
		SCAddress:         scAddress,
		SMRPUI:            deliver,
		MMENumberForMTSMS: mmeNumber,
	})
	if err != nil {
		t.Fatalf("MTForwardShortMessage: %v", err)
	}
	if !tfa.Result.Success() || !bytes.Equal(tfa.SMRPUI, deliverReport) {
		t.Errorf("TFA = %+v, want the SMS-DELIVER-REPORT", tfa)
	}

	tfr, err := ParseMTForwardShortMessageRequest(c.request(t))
	if err != nil {
		t.Fatal(err)
	}
	if tfr.OriginHost != scHost || tfr.DestinationHost != mmeHost || tfr.DestinationRealm != realm {
		t.Errorf("the TFR was routed %+v", tfr.Header)
	}
	if tfr.UserName != imsi || tfr.SCAddress != scAddress || !bytes.Equal(tfr.SMRPUI, deliver) || tfr.MMENumberForMTSMS != mmeNumber {
		t.Errorf("the TFR carried %+v", tfr)
	}
}

// TestSGdClientWaitsForTheDeliveryReport: the serving node answers the
// TFR only once the UE has acknowledged the short message, which takes
// longer than the Answer timer of the node. A client with a timeout of
// its own waits for it.
func TestSGdClientWaitsForTheDeliveryReport(t *testing.T) {
	report := 2 * testTimers.Answer
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		time.Sleep(report)
		return MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, mme)
	})
	tfr := MTForwardShortMessageRequest{
		Header:    diameter.Header{DestinationHost: mmeHost, DestinationRealm: realm},
		UserName:  imsi,
		SCAddress: scAddress,
		SMRPUI:    mustHex(t, smsDeliver),
	}

	patient := NewSGdClient(c.listening, c.server, 4*report)
	if tfa, err := patient.MTForwardShortMessage(t.Context(), tfr); err != nil || !tfa.Result.Success() {
		t.Errorf("the client that waits for the report: %+v, %v", tfa, err)
	}
	hasty := NewSGdClient(c.listening, c.server, 0)
	if _, err := hasty.MTForwardShortMessage(t.Context(), tfr); err == nil {
		t.Error("the client that waits the Answer timer of the node got an answer")
	}
}

// TestSGdClientRefusesATFRTheFormatWouldNotCarry: a TFR is refused
// before anything goes out when an AVP that 6.3.2.5 marks mandatory is
// missing. TS 29.338 6.3.1.2 makes Destination-Host mandatory in a TFR
// alone, and it is also where the client sends.
func TestSGdClientRefusesATFRTheFormatWouldNotCarry(t *testing.T) {
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		return MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, mme)
	})
	client := NewSGdClient(c.listening, c.server, 0)
	tpdu := mustHex(t, smsDeliver)

	for _, missing := range []struct {
		what string
		tfr  MTForwardShortMessageRequest
	}{
		{"Destination-Host", MTForwardShortMessageRequest{
			Header: diameter.Header{DestinationRealm: realm}, UserName: imsi, SCAddress: scAddress, SMRPUI: tpdu}},
		{"Destination-Realm", MTForwardShortMessageRequest{
			Header: diameter.Header{DestinationHost: mmeHost}, UserName: imsi, SCAddress: scAddress, SMRPUI: tpdu}},
		{"SC-Address", MTForwardShortMessageRequest{
			Header: diameter.Header{DestinationHost: mmeHost, DestinationRealm: realm}, UserName: imsi, SMRPUI: tpdu}},
	} {
		if _, err := client.MTForwardShortMessage(t.Context(), missing.tfr); err == nil {
			t.Errorf("MTForwardShortMessage sent a TFR that carries no %s", missing.what)
		}
	}
	c.noRequest(t)
}

// TestSGdClientReportsAServingNodeThatIsNotConnected: the HSS named a
// node that holds no connection to this SC, which the SC hears of as a
// failure of the attempt.
func TestSGdClientReportsAServingNodeThatIsNotConnected(t *testing.T) {
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		return MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, mme)
	})
	client := NewSGdClient(c.listening, c.server, 0)

	_, err := client.MTForwardShortMessage(t.Context(), MTForwardShortMessageRequest{
		Header: diameter.Header{DestinationHost: smsfHost, DestinationRealm: realm}, UserName: imsi,
		SCAddress: scAddress, SMRPUI: mustHex(t, smsDeliver),
	})
	if !errors.Is(err, diameter.ErrPeerUnavailable) {
		t.Errorf("MTForwardShortMessage to a node that never connected: %v, want ErrPeerUnavailable", err)
	}
}

// TestMOClientHandsTheShortMessageToTheServiceCentre: the serving node
// brings a short message of a UE to the SC and reads the
// SMS-SUBMIT-REPORT out of the answer.
func TestMOClientHandsTheShortMessageToTheServiceCentre(t *testing.T) {
	submitReport := mustHex(t, smsSubmitReport)
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		return MOForwardShortMessageAnswer{
			Result: diameter.Base(diameter.DiameterSuccess),
			SMRPUI: submitReport,
		}.Message(request, sc)
	})
	client := NewMOClient(c.connecting, c.peer)

	submit := mustHex(t, smsSubmit)
	ofa, err := client.MOForwardShortMessage(t.Context(), MOForwardShortMessageRequest{
		Header:         diameter.Header{DestinationHost: scHost, DestinationRealm: realm},
		SCAddress:      scAddress,
		UserIdentifier: UserIdentifier{UserName: imsi, MSISDN: msisdn},
		SMRPUI:         submit,
	})
	if err != nil {
		t.Fatalf("MOForwardShortMessage: %v", err)
	}
	if !ofa.Result.Success() || !bytes.Equal(ofa.SMRPUI, submitReport) {
		t.Errorf("OFA = %+v, want the SMS-SUBMIT-REPORT", ofa)
	}

	ofr, err := ParseMOForwardShortMessageRequest(c.request(t))
	if err != nil {
		t.Fatal(err)
	}
	if ofr.OriginHost != mmeHost || ofr.DestinationHost != scHost || ofr.DestinationRealm != realm {
		t.Errorf("the OFR was routed %+v", ofr.Header)
	}
	if ofr.SCAddress != scAddress || ofr.UserIdentifier.UserName != imsi || ofr.UserIdentifier.MSISDN != msisdn || !bytes.Equal(ofr.SMRPUI, submit) {
		t.Errorf("the OFR carried %+v", ofr)
	}
}

// TestMOClientReturnsTheRefusalOfTheServiceCentre: an SC that does not
// take the short message answers with a result and an
// SMS-SUBMIT-REPORT, which the serving node turns into the RP-ERROR for
// the UE.
func TestMOClientReturnsTheRefusalOfTheServiceCentre(t *testing.T) {
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		return MOForwardShortMessageAnswer{
			Result:                 diameter.Experimental(DiameterErrorSMDeliveryFailure),
			SMDeliveryFailureCause: &SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: UnknownServiceCentre},
			SMRPUI:                 mustHex(t, smsSubmitReport),
		}.Message(request, sc)
	})
	client := NewMOClient(c.connecting, c.peer)

	ofa, err := client.MOForwardShortMessage(t.Context(), MOForwardShortMessageRequest{
		Header:         diameter.Header{DestinationRealm: realm},
		SCAddress:      scAddress,
		UserIdentifier: UserIdentifier{UserName: imsi, MSISDN: msisdn},
		SMRPUI:         mustHex(t, smsSubmit),
	})
	if err != nil {
		t.Fatalf("MOForwardShortMessage: %v", err)
	}
	if ofa.Result != diameter.Experimental(DiameterErrorSMDeliveryFailure) || ofa.SMDeliveryFailureCause == nil ||
		ofa.SMDeliveryFailureCause.SMEnumeratedDeliveryFailureCause != UnknownServiceCentre {
		t.Errorf("OFA = %+v, want the refusal of the SC", ofa)
	}
}

// TestMOClientRefusesAnOFRTheFormatWouldNotCarry: the SC address the UE
// sent is the RP-DA of the short message, which 6.3.2.3 marks mandatory,
// and a serving node that names no realm has nowhere to send it.
func TestMOClientRefusesAnOFRTheFormatWouldNotCarry(t *testing.T) {
	c := connect(t, sc, mme, ApplicationIDSGd, func(request *diam.Message) *diam.Message {
		return MOForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, sc)
	})
	client := NewMOClient(c.connecting, c.peer)
	user := UserIdentifier{UserName: imsi, MSISDN: msisdn}
	tpdu := mustHex(t, smsSubmit)

	if _, err := client.MOForwardShortMessage(t.Context(), MOForwardShortMessageRequest{
		Header: diameter.Header{DestinationRealm: realm}, UserIdentifier: user, SMRPUI: tpdu,
	}); err == nil {
		t.Error("MOForwardShortMessage sent an OFR that carries no SC-Address")
	}
	if _, err := client.MOForwardShortMessage(t.Context(), MOForwardShortMessageRequest{
		SCAddress: scAddress, UserIdentifier: user, SMRPUI: tpdu,
	}); err == nil {
		t.Error("MOForwardShortMessage sent an OFR that names no realm")
	}
	c.noRequest(t)
}

// TestS6cClientReportsThatNoAnswerCame: the SC hears of a peer that does
// not answer, and keeps the short message.
func TestS6cClientReportsThatNoAnswerCame(t *testing.T) {
	c := connect(t, hss, sc, ApplicationIDS6c, func(*diam.Message) *diam.Message { return nil })
	client := NewS6cClient(c.connecting, c.peer)

	if _, err := client.SendRoutingInfoForSM(t.Context(), SendRoutingInfoForSMRequest{
		Header: diameter.Header{DestinationRealm: realm}, MSISDN: msisdn, SCAddress: scAddress,
	}); err == nil {
		t.Error("SendRoutingInfoForSM returned no error although no answer came")
	}
}
