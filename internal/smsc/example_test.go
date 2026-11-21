package smsc_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// The identities of the example are fictitious: PLMN 001/01 and numbers
// from +1 202 555 0100 to 0199.
const (
	realm  = "epc.mnc001.mcc001.3gppnetwork.org"
	scHost = "sc." + realm
	mme    = "mme." + realm
	hss    = "hss." + realm
)

// overTheWire stands for a Diameter connection: it serializes m and
// reads it back, as the peer would.
func overTheWire(m *diam.Message) *diam.Message {
	b, err := m.Serialize()
	if err != nil {
		panic(err)
	}
	received, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		panic(err)
	}
	return received
}

// diameterNode is what the role puts between the SC and its Diameter
// peers. A real role sends each request on the peer that
// Destination-Host names and waits for the answer; this one hands it to
// the stand-in HSS and MME below.
type diameterNode struct {
	sessions int
	mu       sync.Mutex
	mme      *standInMME
}

func (n *diameterNode) header(h sgd.Header) sgd.Header {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sessions++
	h.SessionID = fmt.Sprintf("%s;1;%d", scHost, n.sessions)
	h.OriginHost, h.OriginRealm = scHost, realm
	return h
}

func (n *diameterNode) SendRoutingInfoForSM(_ context.Context, req sgd.SendRoutingInfoForSMRequest) (sgd.SendRoutingInfoForSMAnswer, error) {
	req.Header = n.header(req.Header)
	// The stand-in HSS: an MME is registered for MT SMS for Bob.
	request := overTheWire(req.Message())
	sra := sgd.SendRoutingInfoForSMAnswer{
		Result:      sgd.Base(sgd.DiameterSuccess),
		UserName:    "001010000000002",
		ServingNode: &sgd.ServingNode{MMEName: mme, MMERealm: realm, MMENumberForMTSMS: "12025550190"},
	}.Message(request, sgd.Identity{OriginHost: hss, OriginRealm: realm})
	return sgd.ParseSendRoutingInfoForSMAnswer(overTheWire(sra))
}

func (n *diameterNode) ReportSMDeliveryStatus(_ context.Context, req sgd.ReportSMDeliveryStatusRequest) (sgd.ReportSMDeliveryStatusAnswer, error) {
	req.Header = n.header(req.Header)
	rda := sgd.ReportSMDeliveryStatusAnswer{Result: sgd.Base(sgd.DiameterSuccess)}.
		Message(overTheWire(req.Message()), sgd.Identity{OriginHost: hss, OriginRealm: realm})
	return sgd.ParseReportSMDeliveryStatusAnswer(overTheWire(rda))
}

func (n *diameterNode) MTForwardShortMessage(_ context.Context, req sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
	req.Header = n.header(req.Header)
	tfa := n.mme.receive(overTheWire(req.Message()))
	return sgd.ParseMTForwardShortMessageAnswer(overTheWire(tfa))
}

// standInMME takes the TFR and tells what it would send the UE.
type standInMME struct {
	mu  sync.Mutex
	log []string
}

func (m *standInMME) receive(request *diam.Message) *diam.Message {
	tfr, err := sgd.ParseMTForwardShortMessageRequest(request)
	if err != nil {
		panic(err)
	}
	tpdu, err := sms.ParseTPDU(sms.RPDataNetworkToMS{UserData: tfr.SMRPUI})
	if err != nil {
		panic(err)
	}
	deliver := tpdu.(sms.Deliver)
	text, _ := sms.DecodeText(deliver.DataCodingScheme, deliver.UserData)
	m.mu.Lock()
	m.log = append(m.log, fmt.Sprintf("MME: SMS-DELIVER for IMSI %s from %s: %q", tfr.UserName, deliver.OriginatingAddress, text))
	m.mu.Unlock()
	return sgd.MTForwardShortMessageAnswer{Result: sgd.Base(sgd.DiameterSuccess)}.
		Message(request, sgd.Identity{OriginHost: mme, OriginRealm: realm})
}

// Example shows how a role wires the SC: the requests that its Diameter
// server takes go to the methods of the SC, the requests of the SC go
// out through the clients, and Run delivers what waits for a retry.
func Example() {
	if err := sgd.LoadDictionary(); err != nil {
		panic(err)
	}
	node := &diameterNode{mme: &standInMME{}}
	sc := smsc.New(smsc.Config{Address: "12025550100", HSSRealm: realm},
		smsc.NewMemoryStore(), node, node, smsc.SystemClock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, stop := context.WithCancel(context.Background())
	running := make(chan struct{})
	go func() {
		sc.Run(ctx)
		close(running)
	}()

	// The MME brings Alice's short message to Bob.
	dcs, ud, _ := sms.EncodeText("hello")
	submit, _ := sms.Submit{
		MessageReference:   1,
		DestinationAddress: sms.InternationalNumber("+12025550122"),
		DataCodingScheme:   dcs,
		UserData:           ud,
	}.Marshal()
	ofr := overTheWire(sgd.MOForwardShortMessageRequest{
		Header:         sgd.Header{SessionID: mme + ";1;1", OriginHost: mme, OriginRealm: realm, DestinationRealm: realm},
		SCAddress:      "12025550100",
		UserIdentifier: sgd.UserIdentifier{UserName: "001010000000001", MSISDN: "12025550111"},
		SMRPUI:         submit,
	}.Message())

	// The Diameter server of the role hands the OFR to the SC and sends
	// back its answer.
	received, err := sgd.ParseMOForwardShortMessageRequest(ofr)
	if err != nil {
		panic(err)
	}
	ofa := sc.MOForwardShortMessage(ctx, received).Message(ofr, sgd.Identity{OriginHost: scHost, OriginRealm: realm})
	answer, err := sgd.ParseMOForwardShortMessageAnswer(overTheWire(ofa))
	if err != nil {
		panic(err)
	}
	fmt.Println("MME: OFA", answer.Result)

	// When the role stops, it stops the server first, then the SC.
	stop()
	<-running
	for _, line := range node.mme.log {
		fmt.Println(line)
	}
	// Output:
	// MME: OFA Result-Code 2001
	// MME: SMS-DELIVER for IMSI 001010000000002 from +12025550111: "hello"
}
