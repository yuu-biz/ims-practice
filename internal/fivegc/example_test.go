package fivegc_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/fivegc"
	"github.com/yuu-biz/ims-practice/internal/n5"
)

// config is a stub with one subscriber, the IMS DNN and one P-CSCF.
var config = fivegc.Config{
	DNN:            "ims",
	PCSCFAddresses: []netip.Addr{netip.MustParseAddr("172.28.0.40")},
	Subscribers:    []fivegc.Subscriber{{IMSI: "001010000000001", Address: netip.MustParseAddr("172.28.0.110")}},
	Cell: fivegc.SystemInformation{
		TrackingAreaIdentity: fivegc.TrackingAreaIdentity{PLMN: fivegc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 1},
		NRCellIdentity:       0x000000101,
	},
}

// A role serves the stub on two ports: its API, for the UEs and for
// the procedures that the network initiates, on plain HTTP, as the EPC
// stub does, and the PCF's Npcf_PolicyAuthorization on the SBI port
// with cleartext HTTP/2, port 80 by default for an "http" apiRoot (TS
// 29.500 5.3). The P-CSCF is then given the apiRoot of the second, such
// as http://pcf.5gc.example. The example serves both on free ports of
// the loopback, where a role listens on ":8080" and ":80".
func Example() {
	stub := fivegc.New(config, fivegc.NewMemoryStore(), slog.New(slog.DiscardHandler))

	api := httptest.NewServer(stub.Handler())
	defer api.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	pcf := n5.NewServer(l.Addr().String(), stub.N5Handler())
	go pcf.Serve(l)
	defer pcf.Close()

	cell, err := fivegc.NewClient(api.URL, "001010000000001").SystemInformation(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("the UE camps on the cell %#x of TAC %d\n", cell.NRCellIdentity, cell.TrackingAreaIdentity.TAC)
	// Output: the UE camps on the cell 0x101 of TAC 1
}

// A UE opens its event stream, registers, and sets up its PDU session to
// the IMS DNN, asking for the P-CSCF addresses. Then it answers what the
// network sends, until its stream closes.
func Example_ue() {
	api := httptest.NewServer(fivegc.New(config, fivegc.NewMemoryStore(), slog.New(slog.DiscardHandler)).Handler())
	defer api.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := fivegc.NewClient(api.URL, "001010000000001")
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
	fmt.Printf("UE address %s, P-CSCFs %v\n", a.PDUAddress.IPv4Address, a.ExtendedProtocolConfigurationOptions.PCSCFAddresses())

	// The example ends the stream here; a UE keeps it open while it is
	// registered.
	cancel()
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
	// Output: UE address 172.28.0.110, P-CSCFs [172.28.0.40]
}
