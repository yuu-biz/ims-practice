package fivegc_test

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/fivegc"
	"github.com/yuu-biz/ims-practice/internal/n5"
)

// A role serves the stub on two ports: its API, for the UEs and for
// the procedures that the network initiates, on plain HTTP, as the EPC
// stub does, and the PCF's
// Npcf_PolicyAuthorization on the SBI port with cleartext HTTP/2, port
// 80 by default for an "http" apiRoot (TS 29.500 5.3). The P-CSCF is
// then given the apiRoot of the second, such as http://pcf.5gc.example.
func Example() {
	stub := fivegc.New(fivegc.Config{
		DNN:            "ims",
		PCSCFAddresses: []netip.Addr{netip.MustParseAddr("172.28.0.40")},
		Subscribers:    []fivegc.Subscriber{{IMSI: "001010000000001", Address: netip.MustParseAddr("172.28.0.110")}},
		Cell: fivegc.SystemInformation{
			TrackingAreaIdentity: fivegc.TrackingAreaIdentity{PLMN: fivegc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 1},
			NRCellIdentity:       0x000000101,
		},
	}, fivegc.NewMemoryStore(), slog.Default())

	go func() { log.Print(http.ListenAndServe(":8080", stub.Handler())) }()
	go func() { log.Print(n5.NewServer(":80", stub.N5Handler()).ListenAndServe()) }()
}

// A UE opens its event stream, registers, and sets up its PDU session to
// the IMS DNN, asking for the P-CSCF addresses. Then it answers what the
// network sends.
func Example_ue() {
	ctx := context.Background()
	c := fivegc.NewClient("http://5gc.example:8080", "001010000000001")
	events, err := c.Events(ctx)
	if err != nil {
		log.Fatal(err)
	}
	reg, err := c.Register(ctx, fivegc.RegistrationRequest{FiveGSRegistrationType: fivegc.InitialRegistration})
	if err != nil || !reg.FiveGSNetworkFeatureSupport.IMSVoPS3GPP {
		log.Fatal("no IMS voice over PS session: ", err)
	}
	req := fivegc.NewULNASTransport(1, fivegc.PDUSessionEstablishmentRequest{
		PDUSessionID: 1, PTI: 1, PDUSessionType: fivegc.IPv4v6, SSCMode: fivegc.SSCMode1,
		ExtendedProtocolConfigurationOptions: fivegc.PCSCFAddressRequest(),
	})
	req.RequestType, req.DNN = fivegc.InitialRequest, "ims"
	accept, err := c.EstablishPDUSession(ctx, req)
	if err != nil {
		log.Fatal(err)
	}
	a := accept.PayloadContainer
	log.Printf("UE address %s, P-CSCFs %v", a.PDUAddress.IPv4Address, a.ExtendedProtocolConfigurationOptions.PCSCFAddresses())

	for m := range events {
		switch msg := m.Message.(type) {
		case *fivegc.DLNASTransport[fivegc.PDUSessionModificationCommand]:
			// The voice QoS flow came or went; match its QoS rule to the
			// calls, then complete.
			c.CompletePDUSessionModification(ctx, fivegc.NewULNASTransport(msg.PDUSessionID,
				fivegc.PDUSessionModificationComplete{PDUSessionID: msg.PDUSessionID, PTI: msg.PayloadContainer.PTI}))
		case *fivegc.DLNASTransport[fivegc.PDUSessionReleaseCommand]:
			c.CompletePDUSessionRelease(ctx, fivegc.NewULNASTransport(msg.PDUSessionID,
				fivegc.PDUSessionReleaseComplete{PDUSessionID: msg.PDUSessionID, PTI: msg.PayloadContainer.PTI}))
		case *fivegc.DeregistrationRequest:
			c.AcceptDeregistration(ctx)
		}
	}
}
