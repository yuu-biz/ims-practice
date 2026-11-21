package ue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/epc"
)

// IMSAPN is the access point name of the IMS PDN (IR.92 4.3, TS 23.003
// 9.1).
const IMSAPN = "ims"

// attachPTI is the procedure transaction identity of the PDN
// CONNECTIVITY REQUEST in the attach: the UE has no other ESM procedure
// running then (TS 24.301 6.4.1.2).
const attachPTI = 1

// EPCAccess is the access network of the EPC stub (role epc): the
// UE talks to it over the HTTP and JSON API whose messages are named
// after the ESM and EMM messages of TS 24.301.
//
// SIMPLIFIED: TS 24.301 and TS 36.331. There is no NAS, no RRC and no
// radio: the messages go over HTTP to the stub, and the UE's IP packets
// go straight onto the Docker network.
type EPCAccess struct {
	client *epc.Client
	log    *slog.Logger
	events chan AccessEvent

	mu sync.Mutex
	// stop ends the event stream; nil before the attach.
	stop context.CancelFunc
	// voice is the EPS bearer identity of the dedicated bearer for voice,
	// or 0 while there is none.
	voice uint8
	// detaching tells that the UE has asked to detach, so the end of the
	// event stream is no loss of the PDN connection.
	detaching bool
}

// NewEPCAccess is the access network of the stub whose UE API is at
// baseURL, such as http://172.28.0.80:8080, for the UE with imsi.
func NewEPCAccess(baseURL, imsi string, log *slog.Logger) *EPCAccess {
	return &EPCAccess{client: epc.NewClient(baseURL, imsi), log: log, events: make(chan AccessEvent, 16)}
}

// IMSIOf is the IMSI in a private user identity derived from it, such
// as 001010000000001 in 001010000000001@ims.mnc001.mcc001.3gppnetwork.org
// (TS 23.003 13.3).
func IMSIOf(privateIdentity string) (string, error) {
	imsi, _, ok := strings.Cut(privateIdentity, "@")
	if !ok || len(imsi) < 6 || len(imsi) > 15 || strings.Trim(imsi, "0123456789") != "" {
		return "", fmt.Errorf("%q is not a private user identity derived from an IMSI", privateIdentity)
	}
	return imsi, nil
}

// Attach is the attach of TS 23.401 5.3.2.1 with the PDN connection to
// the IMS APN: the UE asks for the P-CSCF addresses in the PCO (TS
// 24.229 L.2.2.1, IR.92 4.5) and gets its address and its default bearer
// for SIP (IR.92 4.4.1). The event stream opens first, so that the
// network can reach the UE as soon as it is attached.
func (a *EPCAccess) Attach(ctx context.Context) (Attachment, error) {
	si, err := a.client.SystemInformation(ctx)
	if err != nil {
		return Attachment{}, fmt.Errorf("system information: %w", err)
	}
	streamCtx, stop := context.WithCancel(context.Background())
	stream, err := a.client.Events(streamCtx)
	if err != nil {
		stop()
		return Attachment{}, fmt.Errorf("event stream: %w", err)
	}
	accept, err := a.client.Attach(ctx, epc.AttachRequest{ESMMessageContainer: epc.PDNConnectivityRequest{
		ProcedureTransactionIdentity: attachPTI,
		RequestType:                  epc.InitialRequest,
		PDNType:                      epc.IPv4,
		AccessPointName:              IMSAPN,
		ProtocolConfigurationOptions: epc.PCSCFIPv4AddressRequest(),
	}})
	if err != nil {
		stop()
		return Attachment{}, fmt.Errorf("ATTACH REQUEST: %w", err)
	}
	bearer := accept.ESMMessageContainer
	if err := a.client.AttachComplete(ctx, epc.AttachComplete{ESMMessageContainer: epc.ActivateDefaultEPSBearerContextAccept{
		EPSBearerIdentity: bearer.EPSBearerIdentity, ProcedureTransactionIdentity: bearer.ProcedureTransactionIdentity,
	}}); err != nil {
		stop()
		return Attachment{}, fmt.Errorf("ATTACH COMPLETE: %w", err)
	}
	a.mu.Lock()
	a.stop = stop
	a.mu.Unlock()
	go a.follow(stream)

	att := Attachment{
		Address:           bearer.PDNAddress.IPv4Address,
		PCSCFs:            bearer.ProtocolConfigurationOptions.PCSCFIPv4Addresses(),
		AccessNetworkInfo: AccessNetworkInfo(si),
	}
	a.log.Info("attached", "apn", bearer.AccessPointName, "pdn_address", att.Address.String(),
		"default_bearer", bearer.EPSBearerIdentity, "qci", int(bearer.EPSQoS.QCI), "pcscf_addresses", fmt.Sprint(att.PCSCFs))
	if len(att.PCSCFs) == 0 {
		return att, errors.New("the PCO of the attach has no P-CSCF address")
	}
	return att, nil
}

// AccessNetworkInfo is the P-Access-Network-Info of a UE on E-UTRAN in
// the cell si: the MCC, the MNC, the TAC in four hexadecimal digits and
// the ECI in seven (TS 24.229 7.2A.4.3 item 12).
//
// SIMPLIFIED: TS 24.229 7.2A.4.3. The stub tells nothing of the duplex
// mode of its cell, so the access type is always 3GPP-E-UTRAN-FDD.
func AccessNetworkInfo(si epc.SystemInformation) string {
	tai := si.TrackingAreaIdentity
	return fmt.Sprintf("3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=%s%s%04X%07X", tai.PLMN.MCC, tai.PLMN.MNC, tai.TAC, si.CellIdentity)
}

func (a *EPCAccess) Events() <-chan AccessEvent { return a.events }

// follow answers what the network sends the UE, and tells the UE what
// it means for its calls.
func (a *EPCAccess) follow(stream <-chan epc.NetworkMessage) {
	defer close(a.events)
	ctx := context.Background()
	for m := range stream {
		switch msg := m.Message.(type) {
		case *epc.ActivateDedicatedEPSBearerContextRequest:
			a.dedicatedBearer(ctx, msg)
		case *epc.DeactivateEPSBearerContextRequest:
			a.deactivated(ctx, msg)
		case *epc.DetachRequest:
			// TS 24.301 5.5.2.3.2: the UE answers with DETACH ACCEPT. The
			// stub sends "re-attach not required" without an EMM cause,
			// after which 5.5.2.3.4 item b has the UE attach again only
			// when T3402 expires, 12 minutes by default (Table 10.2.1).
			if err := a.client.AcceptDetach(ctx); err != nil {
				a.log.Warn("DETACH ACCEPT not sent", "error", err.Error())
			}
			a.log.Info("detached by the network", "detach_type", string(msg.DetachType))
			a.events <- Detached
			a.mu.Lock()
			a.stop()
			a.mu.Unlock()
			return
		}
	}
	a.mu.Lock()
	detaching := a.detaching
	a.mu.Unlock()
	if !detaching {
		a.log.Warn("the event stream of the access network ended")
		a.events <- Detached
	}
}

// dedicatedBearer accepts a dedicated bearer that the network activates
// (TS 24.301 6.4.2.3). The one of QCI 1 carries voice (IR.92 4.4.2).
//
// SIMPLIFIED: TS 24.229 L.2.2.5.1B. The UE takes the voice bearer as the
// resources of every call it has, instead of matching the packet
// filters of the TFT against the media of each call: IR.92 4.4.2 has
// one voice bearer carry the voice of all calls, and the stub does not
// send MODIFY EPS BEARER CONTEXT REQUEST when the flows change.
func (a *EPCAccess) dedicatedBearer(ctx context.Context, m *epc.ActivateDedicatedEPSBearerContextRequest) {
	err := a.client.AcceptDedicatedBearer(ctx, epc.ActivateDedicatedEPSBearerContextAccept{
		EPSBearerIdentity: m.EPSBearerIdentity, ProcedureTransactionIdentity: m.ProcedureTransactionIdentity,
	})
	if err != nil {
		a.log.Warn("ACTIVATE DEDICATED EPS BEARER CONTEXT ACCEPT not sent", "ebi", m.EPSBearerIdentity, "error", err.Error())
		return
	}
	a.log.Info("dedicated bearer active", "ebi", m.EPSBearerIdentity, "linked_ebi", m.LinkedEPSBearerIdentity,
		"qci", int(m.EPSQoS.QCI), "gbr_ul_kbps", m.EPSQoS.GuaranteedBitRateForUplink, "gbr_dl_kbps", m.EPSQoS.GuaranteedBitRateForDownlink,
		"packet_filters", len(m.TFT.PacketFilters))
	if m.EPSQoS.QCI != epc.QCIConversationalVoice {
		return
	}
	a.mu.Lock()
	a.voice = m.EPSBearerIdentity
	a.mu.Unlock()
	a.events <- MediaResourcesReserved
}

// deactivated accepts the release of a bearer (TS 24.301 6.4.4.3).
func (a *EPCAccess) deactivated(ctx context.Context, m *epc.DeactivateEPSBearerContextRequest) {
	err := a.client.AcceptDeactivation(ctx, epc.DeactivateEPSBearerContextAccept{
		EPSBearerIdentity: m.EPSBearerIdentity, ProcedureTransactionIdentity: m.ProcedureTransactionIdentity,
	})
	if err != nil {
		a.log.Warn("DEACTIVATE EPS BEARER CONTEXT ACCEPT not sent", "ebi", m.EPSBearerIdentity, "error", err.Error())
	}
	a.log.Info("bearer deactivated", "ebi", m.EPSBearerIdentity, "esm_cause", m.ESMCause.String())
	a.mu.Lock()
	voice := a.voice == m.EPSBearerIdentity
	if voice {
		a.voice = 0
	}
	a.mu.Unlock()
	if voice {
		a.events <- MediaResourcesReleased
	}
}

// Detach is the detach of TS 23.401 5.3.8.2.1, as the UE switches off.
func (a *EPCAccess) Detach(ctx context.Context) error {
	a.mu.Lock()
	stop := a.stop
	a.detaching = true
	a.mu.Unlock()
	if stop == nil {
		return nil
	}
	err := a.client.Detach(ctx)
	stop()
	if err == nil {
		a.log.Info("detached")
	}
	return err
}
