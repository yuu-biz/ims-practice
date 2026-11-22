package epc

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

// This file is the MME and the PDN GW of the stub: the procedures a UE
// starts, and the bearers that the PCRF's PCC rules bring about. The
// PCRF is in pcrf.go; between the two, Gx is a Go call.
//
// SIMPLIFIED: TS 29.212. The PCRF and the PDN GW share the PDN
// connection in the Store instead of exchanging CCR, CCA and RAR over
// Gx: the stub is one node that plays both, so the two have its Store
// in common and no network between them.

// Errors of the UE API.
var (
	ErrUnknownSubscriber = errors.New("epc: no subscriber has this IMSI")
	// ErrInvalidMessage: the message breaks a rule of TS 24.301, or asks
	// for what the stub does not serve.
	ErrInvalidMessage = errors.New("epc: invalid message")
	ErrUnknownBearer  = errors.New("epc: the UE has no such EPS bearer")
)

// defaultBearerIdentity is the first EPS bearer identity of the range
// 5 to 15 (TS 24.301 9.3.2), which the default bearer takes.
const defaultBearerIdentity uint8 = 5

// noProcedureTransactionIdentity is the PTI of a procedure the network
// starts on its own, such as a dedicated bearer for the PCRF (TS 24.301
// 6.4.2.2): no PTI is assigned.
const noProcedureTransactionIdentity uint8 = 0

// maxPacketFilters is the most packet filters one TFT holds (TS 24.008
// 10.5.6.12).
const maxPacketFilters = 16

// effects are what a change to a PDN connection makes the network send:
// messages on the UE's event stream and Rx requests to AFs. They go out
// after the change is kept, so no I/O happens inside a Store update.
type effects struct {
	imsi string
	toUE []ueMessage
	toAF []afMessage
}

type ueMessage struct {
	name    MessageType
	message any
}

type afMessage struct {
	host    string // the AF's Origin-Host
	request interface{ Message() *diam.Message }
}

func (e *EPC) address(imsi string) (netip.Addr, error) {
	addr, ok := e.addresses[imsi]
	if !ok {
		return netip.Addr{}, ErrUnknownSubscriber
	}
	return addr, nil
}

// Attach serves an ATTACH REQUEST with its PDN CONNECTIVITY REQUEST for
// the IMS APN (TS 23.401 5.3.2.1): it sets up the PDN connection with
// its default bearer, and answers with the UE's address and, when the
// UE asks for them, the P-CSCF addresses (TS 24.229 L.2.2.1).
//
// SIMPLIFIED: TS 23.401 5.3.2.1. Authentication, NAS security and the
// HSS (S6a) are left out; the stub serves the IMSIs of its
// configuration. ATTACH REJECT is not sent: a request the stub cannot
// serve gets an error from the API.
func (e *EPC) Attach(ctx context.Context, imsi string, req AttachRequest) (AttachAccept, error) {
	addr, err := e.address(imsi)
	if err != nil {
		return AttachAccept{}, err
	}
	pdn := req.ESMMessageContainer
	switch {
	case pdn.RequestType != InitialRequest:
		return AttachAccept{}, fmt.Errorf("%w: request type %q, want %q", ErrInvalidMessage, pdn.RequestType, InitialRequest)
	case pdn.PDNType != IPv4:
		return AttachAccept{}, fmt.Errorf("%w: PDN type %q, the stub serves %q only", ErrInvalidMessage, pdn.PDNType, IPv4)
	case !strings.EqualFold(pdn.AccessPointName, e.cfg.APN):
		// TS 23.003 9.1: the case of the letters of an APN is not significant.
		return AttachAccept{}, fmt.Errorf("%w: access point name %q, the stub serves %q", ErrInvalidMessage, pdn.AccessPointName, e.cfg.APN)
	}
	// An ATTACH REQUEST from a UE that is still attached, as after a
	// restart, deletes its old EPS bearer contexts first (TS 24.301
	// 5.5.1.2.7 f).
	if old, ok := e.store.TakePDNConnection(addr); ok {
		e.carryOut(ctx, e.endPDNConnection(old))
	}
	defaultBearer := Bearer{EPSBearerIdentity: defaultBearerIdentity, State: BearerActivePending, QoS: EPSQoS{QCI: QCIIMSSignalling}}
	e.store.PutPDNConnection(PDNConnection{IMSI: imsi, Address: addr, APN: e.cfg.APN, DefaultBearer: defaultBearer})
	e.log.Info("PDN connection set up", "imsi", imsi, "address", addr.String(), "apn", e.cfg.APN)

	accept := ActivateDefaultEPSBearerContextRequest{
		EPSBearerIdentity:            defaultBearer.EPSBearerIdentity,
		ProcedureTransactionIdentity: pdn.ProcedureTransactionIdentity,
		EPSQoS:                       defaultBearer.QoS,
		AccessPointName:              e.cfg.APN,
		PDNAddress:                   PDNAddress{PDNTypeValue: IPv4, IPv4Address: addr},
	}
	if pdn.ProtocolConfigurationOptions.asksForPCSCF() {
		for _, a := range e.cfg.PCSCFAddresses {
			accept.ProtocolConfigurationOptions.Containers = append(accept.ProtocolConfigurationOptions.Containers,
				Container{ID: PCSCFIPv4Address, Contents: a.String()})
		}
	}
	return AttachAccept{TAIList: []TrackingAreaIdentity{e.cfg.Cell.TrackingAreaIdentity}, ESMMessageContainer: accept}, nil
}

// AttachComplete serves ATTACH COMPLETE, whose ACTIVATE DEFAULT EPS
// BEARER CONTEXT ACCEPT makes the default bearer active (TS 24.301
// 6.4.1.3).
func (e *EPC) AttachComplete(imsi string, m AttachComplete) error {
	addr, err := e.address(imsi)
	if err != nil {
		return err
	}
	ebi := m.ESMMessageContainer.EPSBearerIdentity
	_, err = e.store.UpdatePDNConnection(addr, func(c *PDNConnection) error {
		if ebi != c.DefaultBearer.EPSBearerIdentity {
			return fmt.Errorf("%w: EPS bearer identity %d is not the default bearer %d", ErrUnknownBearer, ebi, c.DefaultBearer.EPSBearerIdentity)
		}
		c.DefaultBearer.State = BearerActive
		return nil
	})
	return err
}

// Detach serves a DETACH REQUEST from the UE (TS 23.401 5.3.8.2.1): the
// PDN connection ends, and the PCRF aborts its Rx sessions.
func (e *EPC) Detach(ctx context.Context, imsi string, req DetachRequest) (DetachAccept, error) {
	addr, err := e.address(imsi)
	if err != nil {
		return DetachAccept{}, err
	}
	if req.DetachType != EPSDetach {
		return DetachAccept{}, fmt.Errorf("%w: detach type %q, want %q", ErrInvalidMessage, req.DetachType, EPSDetach)
	}
	c, ok := e.store.TakePDNConnection(addr)
	if !ok {
		return DetachAccept{}, ErrNoPDNConnection
	}
	e.log.Info("UE detached", "imsi", imsi)
	e.carryOut(ctx, e.endPDNConnection(c))
	return DetachAccept{}, nil
}

// NetworkInitiatedDetach is the network detaching the UE (TS 24.301
// 5.5.2.3; TS 23.401 5.3.8.3, the MME initiated detach): a DETACH
// REQUEST goes to the UE, the PDN connection ends, and the PCRF aborts
// its Rx sessions. A learner starts it over the API (http.go).
func (e *EPC) NetworkInitiatedDetach(ctx context.Context, imsi string) error {
	addr, err := e.address(imsi)
	if err != nil {
		return err
	}
	c, ok := e.store.TakePDNConnection(addr)
	if !ok {
		return ErrNoPDNConnection
	}
	e.log.Info("network detaches the UE", "imsi", imsi)
	fx := e.endPDNConnection(c)
	fx.toUE = append(fx.toUE, ueMessage{TypeDetachRequest, DetachRequest{DetachType: ReattachNotRequired}})
	e.carryOut(ctx, fx)
	return nil
}

// DetachAccepted serves the UE's DETACH ACCEPT. The network forgot the
// UE when it sent its DETACH REQUEST, so there is nothing to change.
func (e *EPC) DetachAccepted(imsi string) error {
	_, err := e.address(imsi)
	return err
}

// endPDNConnection is the IP-CAN session termination the PCRF hears of
// when a PDN connection ends: an ASR on every Rx session bound to it
// (TS 29.214 4.4.6.1). The bindings stay, marked aborted, so the STR
// that the AF then sends ends its session with success.
func (e *EPC) endPDNConnection(c PDNConnection) effects {
	fx := effects{imsi: c.IMSI}
	for _, s := range c.AFSessions {
		e.store.PutBinding(s.SessionID, Binding{Address: c.Address, Aborted: true})
		fx.toAF = append(fx.toAF, e.abortSession(s))
	}
	return fx
}

// DedicatedBearerAccepted serves ACTIVATE DEDICATED EPS BEARER CONTEXT
// ACCEPT (TS 24.301 6.4.2.3). The bearer becomes active, and the PCRF
// tells each AF that asked that its resources are there (TS 29.214
// 5.3.13).
func (e *EPC) DedicatedBearerAccepted(ctx context.Context, imsi string, m ActivateDedicatedEPSBearerContextAccept) error {
	addr, err := e.address(imsi)
	if err != nil {
		return err
	}
	var fx effects
	_, err = e.store.UpdatePDNConnection(addr, func(c *PDNConnection) error {
		b := c.VoiceBearer
		if b == nil || b.EPSBearerIdentity != m.EPSBearerIdentity {
			return fmt.Errorf("%w: %d", ErrUnknownBearer, m.EPSBearerIdentity)
		}
		fx = effects{imsi: imsi}
		if b.State != BearerActivePending {
			return nil // a repeated ACCEPT
		}
		b.State = BearerActive
		for _, s := range c.AFSessions {
			if len(s.Rules) > 0 && s.subscribed(rx.IndicationOfSuccessfulResourcesAllocation) {
				fx.toAF = append(fx.toAF, e.report(s, rx.IndicationOfSuccessfulResourcesAllocation))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.carryOut(ctx, fx)
	return nil
}

// DedicatedBearerRejected serves ACTIVATE DEDICATED EPS BEARER CONTEXT
// REJECT (TS 24.301 6.4.2.4): the bearer is gone, and so are the PCC
// rules it was for.
func (e *EPC) DedicatedBearerRejected(ctx context.Context, imsi string, m ActivateDedicatedEPSBearerContextReject) error {
	if _, err := e.address(imsi); err != nil {
		return err
	}
	e.log.Info("UE rejected the dedicated bearer", "imsi", imsi, "ebi", m.EPSBearerIdentity, "esm_cause", m.ESMCause.String())
	fx, err := e.voiceBearerFailed(imsi, m.EPSBearerIdentity)
	if err != nil {
		return err
	}
	e.carryOut(ctx, fx)
	return nil
}

// voiceBearerFailed removes a voice bearer whose activation failed. The
// PCRF tells each AF that asked that its resources could not be set up,
// naming the flows (TS 29.214 4.4.1), and drops the session's rules, as
// the PCEF would report them inactive.
func (e *EPC) voiceBearerFailed(imsi string, ebi uint8) (effects, error) {
	addr, err := e.address(imsi)
	if err != nil {
		return effects{}, err
	}
	fx := effects{imsi: imsi}
	_, err = e.store.UpdatePDNConnection(addr, func(c *PDNConnection) error {
		if c.VoiceBearer == nil || c.VoiceBearer.EPSBearerIdentity != ebi {
			return fmt.Errorf("%w: %d", ErrUnknownBearer, ebi)
		}
		c.VoiceBearer = nil
		for i, s := range c.AFSessions {
			if len(s.Rules) > 0 && s.subscribed(rx.IndicationOfFailedResourcesAllocation) {
				fx.toAF = append(fx.toAF, e.report(s, rx.IndicationOfFailedResourcesAllocation))
			}
			c.AFSessions[i].Rules = nil
		}
		return nil
	})
	return fx, err
}

// BearerDeactivated serves DEACTIVATE EPS BEARER CONTEXT ACCEPT (TS
// 24.301 6.4.4.3).
//
// SIMPLIFIED: TS 24.301 6.4.4.2. The network forgets a bearer when it
// sends DEACTIVATE EPS BEARER CONTEXT REQUEST, instead of keeping it in
// BEARER CONTEXT INACTIVE PENDING until this answer, and runs no T3495.
func (e *EPC) BearerDeactivated(imsi string, m DeactivateEPSBearerContextAccept) error {
	_, err := e.address(imsi)
	return err
}

// DeactivateEPSBearerContext is the network deactivating the voice
// bearer of a UE (TS 24.301 6.4.4.2), as when the radio can no longer
// carry it (TS 23.401 5.4.4.1). A learner starts it over the API
// (http.go). The bearer carried every flow of the AF
// sessions on it, so the PCRF aborts each session that asked to hear of
// a released bearer (TS 29.214 4.4.6.2: an ASR when all of a session's
// flows are gone), and the UE hears DEACTIVATE EPS BEARER CONTEXT
// REQUEST.
func (e *EPC) DeactivateEPSBearerContext(ctx context.Context, imsi string, ebi uint8) error {
	addr, err := e.address(imsi)
	if err != nil {
		return err
	}
	if ebi == defaultBearerIdentity {
		return fmt.Errorf("%w: the default bearer goes with the PDN connection; detach the UE instead", ErrInvalidMessage)
	}
	var fx effects
	_, err = e.store.UpdatePDNConnection(addr, func(c *PDNConnection) error {
		if c.VoiceBearer == nil || c.VoiceBearer.EPSBearerIdentity != ebi {
			return fmt.Errorf("%w: %d", ErrUnknownBearer, ebi)
		}
		c.VoiceBearer = nil
		fx = effects{imsi: imsi}
		// #26 because the bearer is lost to a lack of radio resources,
		// not to the end of a call.
		fx.toUE = append(fx.toUE, ueMessage{TypeDeactivateEPSBearerContextRequest, DeactivateEPSBearerContextRequest{
			EPSBearerIdentity: ebi, ProcedureTransactionIdentity: noProcedureTransactionIdentity, ESMCause: InsufficientResources}})
		for i, s := range c.AFSessions {
			if len(s.Rules) > 0 && s.subscribed(rx.IndicationOfReleaseOfBearer) {
				fx.toAF = append(fx.toAF, e.abortSession(s))
			}
			c.AFSessions[i].Rules = nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.log.Info("network released the bearer", "imsi", imsi, "ebi", ebi)
	e.carryOut(ctx, fx)
	return nil
}

// Status is what the API shows of a UE.
func (e *EPC) Status(imsi string) (Status, error) {
	addr, err := e.address(imsi)
	if err != nil {
		return Status{}, err
	}
	c, ok := e.store.PDNConnection(addr)
	if !ok {
		return Status{}, ErrNoPDNConnection
	}
	s := Status{IMSI: c.IMSI, PDNAddress: c.Address, APN: c.APN, RxSessions: []string{}}
	for _, b := range []*Bearer{&c.DefaultBearer, c.VoiceBearer} {
		if b != nil {
			s.Bearers = append(s.Bearers, BearerStatus{EPSBearerIdentity: b.EPSBearerIdentity, State: b.State, EPSQoS: b.QoS, TFT: b.TFT})
		}
	}
	for _, a := range c.AFSessions {
		s.RxSessions = append(s.RxSessions, a.SessionID)
	}
	return s, nil
}

// reconcileVoiceBearer makes the voice bearer carry the PCC rules of all
// the AF sessions of c: it activates the bearer for the first rules and
// deactivates it when the last ones go (TS 23.401 5.4.1, 5.4.4.1).
//
// SIMPLIFIED: TS 24.301 6.4.3. When the rules change while the bearer
// exists, the stub updates its own record of the TFT and the QoS but
// sends no MODIFY EPS BEARER CONTEXT REQUEST: it enforces neither, and
// a UE only needs to know that the bearer is there.
func (e *EPC) reconcileVoiceBearer(c *PDNConnection, fx *effects) error {
	var rules []PCCRule
	for _, s := range c.AFSessions {
		rules = append(rules, s.Rules...)
	}
	if len(rules) == 0 {
		if b := c.VoiceBearer; b != nil {
			fx.toUE = append(fx.toUE, ueMessage{TypeDeactivateEPSBearerContextRequest, DeactivateEPSBearerContextRequest{
				EPSBearerIdentity: b.EPSBearerIdentity, ProcedureTransactionIdentity: noProcedureTransactionIdentity, ESMCause: RegularDeactivation}})
			c.VoiceBearer = nil
		}
		return nil
	}
	tft, err := trafficFlowTemplate(rules)
	if err != nil {
		return err
	}
	qos := voiceQoS(rules)
	if b := c.VoiceBearer; b != nil {
		b.QoS, b.TFT = qos, &tft
		return nil
	}
	// The voice bearer is the only dedicated one, so it takes the identity
	// after the default bearer's.
	b := &Bearer{EPSBearerIdentity: c.DefaultBearer.EPSBearerIdentity + 1, State: BearerActivePending, QoS: qos, TFT: &tft}
	c.VoiceBearer = b
	fx.toUE = append(fx.toUE, ueMessage{TypeActivateDedicatedEPSBearerContextRequest, ActivateDedicatedEPSBearerContextRequest{
		EPSBearerIdentity:            b.EPSBearerIdentity,
		ProcedureTransactionIdentity: noProcedureTransactionIdentity,
		LinkedEPSBearerIdentity:      c.DefaultBearer.EPSBearerIdentity,
		EPSQoS:                       qos,
		TFT:                          tft,
	}})
	return nil
}

// voiceQoS is the EPS QoS of the voice bearer: QCI 1, with the bit
// rates of its rules added up.
//
// SIMPLIFIED: TS 29.213 6.3 Table 6.3.1. The guaranteed bit rate is the
// maximum one, both taken from the requested bandwidth, where the table
// lets operator policy and codec algorithms decide; RTCP has no share
// of its own. The stub enforces neither rate.
func voiceQoS(rules []PCCRule) EPSQoS {
	var ul, dl uint32
	for _, r := range rules {
		ul += r.BitRateUL
		dl += r.BitRateDL
	}
	kbps := func(bps uint32) uint32 { return (bps + 999) / 1000 }
	return EPSQoS{
		QCI:                          QCIConversationalVoice,
		MaximumBitRateForUplink:      kbps(ul),
		MaximumBitRateForDownlink:    kbps(dl),
		GuaranteedBitRateForUplink:   kbps(ul),
		GuaranteedBitRateForDownlink: kbps(dl),
	}
}

// trafficFlowTemplate turns the flows of the rules into the packet
// filters of a new TFT, seen from the UE: the remote end is the
// destination of an uplink flow and the source of a downlink one.
func trafficFlowTemplate(rules []PCCRule) (TrafficFlowTemplate, error) {
	tft := TrafficFlowTemplate{OperationCode: CreateNewTFT}
	for _, r := range rules {
		for _, f := range r.Flows {
			pf := PacketFilter{ProtocolIdentifier: f.Protocol}
			if f.Direction == policy.Uplink {
				pf.Direction, pf.IPv4RemoteAddress, pf.SingleRemotePort = UplinkOnly, f.Destination, f.DestinationPort
			} else {
				pf.Direction, pf.IPv4RemoteAddress, pf.SingleLocalPort = DownlinkOnly, f.Source, f.DestinationPort
			}
			tft.PacketFilters = append(tft.PacketFilters, pf)
		}
	}
	if len(tft.PacketFilters) > maxPacketFilters {
		return TrafficFlowTemplate{}, fmt.Errorf("%d packet filters, a TFT holds %d", len(tft.PacketFilters), maxPacketFilters)
	}
	for i := range tft.PacketFilters {
		tft.PacketFilters[i].Identifier = uint8(i + 1)
		tft.PacketFilters[i].EvaluationPrecedence = uint8(i)
	}
	return tft, nil
}

// carryOut sends what a change to a PDN connection calls for. A UE
// without an event stream cannot take a new dedicated bearer, which
// then fails as if the UE had rejected it.
func (e *EPC) carryOut(ctx context.Context, fx effects) {
	for _, m := range fx.toUE {
		err := e.streams.send(fx.imsi, m.name, m.message)
		if err == nil {
			e.log.Info("sent to UE", "imsi", fx.imsi, "message", string(m.name))
			continue
		}
		e.log.Warn("could not send to UE", "imsi", fx.imsi, "message", string(m.name), "error", err.Error())
		if req, ok := m.message.(ActivateDedicatedEPSBearerContextRequest); ok {
			failed, err := e.voiceBearerFailed(fx.imsi, req.EPSBearerIdentity)
			if err == nil {
				fx.toAF = append(fx.toAF, failed.toAF...)
			}
		}
	}
	for _, m := range fx.toAF {
		e.sendToAF(ctx, m)
	}
}

// sessionFlows names the flows of a session's rules in a Flows AVP.
func sessionFlows(s AFSession) []rx.Flows {
	var flows []rx.Flows
	for _, r := range s.Rules {
		flows = append(flows, rx.Flows{MediaComponentNumber: r.MediaComponentNumber, FlowNumbers: slices.Clone(r.FlowNumbers)})
	}
	return flows
}
