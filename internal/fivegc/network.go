package fivegc

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/n5"
)

// This file is the AMF and the SMF of the stub: the procedures a UE
// starts, and the end of a PDU session. The PCF is in pcf.go and the
// voice QoS flow in qos.go; between the SMF and the PCF, N7 is a Go
// call.
//
// SIMPLIFIED: TS 29.512. The PCF and the SMF share the PDU session in
// the Store instead of exchanging Npcf_SMPolicyControl requests (Create
// at establishment, UpdateNotify with PCC rules, Update with rule
// reports, Delete at release; 4.2.2 to 4.2.5): the stub has one PCF and
// one SMF, and N7 would add messages a learner of the IMS does not
// need, as Gx does in the EPC stub.

// Errors of the UE API.
var (
	ErrUnknownSubscriber = errors.New("fivegc: no subscriber has this IMSI")
	// ErrInvalidMessage: the message breaks a rule of TS 24.501, or asks
	// for what the stub does not serve.
	ErrInvalidMessage = errors.New("fivegc: invalid message")
	ErrUnknownQoSFlow = errors.New("fivegc: the PDU session has no such QoS flow")
)

// The identifiers the SMF gives the QoS flow and the QoS rule of the
// default QoS rule. TS 23.501 5.7.1.3 lets a 3GPP access use a
// dynamically assigned QFI for every flow; QFI 0 and QRI 0 mean "none
// assigned" (TS 24.501 9.11.4.12, 9.11.4.13).
const (
	defaultQFI = 1
	defaultQRI = 1
	// defaultRulePrecedence is the highest precedence value, which TS
	// 23.501 5.7.1.4 asks for the default QoS rule that matches all
	// packets.
	defaultRulePrecedence = 255
)

// noProcedureTransactionIdentity is the PTI of a procedure the network
// starts on its own (TS 24.501 6.2.1).
const noProcedureTransactionIdentity uint8 = 0

// sessionAMBR is the Session-AMBR of every PDU session, in kbit/s.
//
// SIMPLIFIED: TS 23.501 5.7.2.7. The SMF takes the subscribed
// Session-AMBR from the UDM; the stub has no UDM, so every session gets
// the same value. The stub enforces no rate.
var sessionAMBR = SessionAMBR{SessionAMBRForDownlink: 1000, SessionAMBRForUplink: 1000}

// effects are what a change to a PDU session makes the network send:
// messages on the UE's event stream and notifications to AFs. They go
// out after the change is kept, so no I/O happens inside a Store
// update.
type effects struct {
	imsi string
	toUE []ueMessage
	toAF []afMessage
}

type ueMessage struct {
	name    MessageType
	message any
}

// afMessage is one Npcf_PolicyAuthorization_Notify request (TS 29.514
// 4.2.5): an event notification or a termination request.
type afMessage struct {
	appSessionID string
	notifURI     string
	notification *n5.EventsNotification
	termination  *n5.TerminationInfo
}

func (g *FiveGC) address(imsi string) (netip.Addr, error) {
	addr, ok := g.addresses[imsi]
	if !ok {
		return netip.Addr{}, ErrUnknownSubscriber
	}
	return addr, nil
}

// Register serves a REGISTRATION REQUEST for initial registration (TS
// 23.502 4.2.2.2.2): the UE becomes 5GMM-REGISTERED, and learns that
// IMS voice over PS sessions are supported (TS 23.501 5.16.3.2).
//
// SIMPLIFIED: TS 23.502 4.2.2.2.2. Authentication (5G-AKA with the AUSF
// and the UDM), NAS security and the UE context in the UDM are left
// out; the stub serves the IMSIs of its configuration. No 5G-GUTI is
// assigned, so the UE sends no REGISTRATION COMPLETE (TS 24.501
// 5.5.1.2.4). REGISTRATION REJECT is not sent: a request the stub
// cannot serve gets an error from the API.
func (g *FiveGC) Register(ctx context.Context, imsi string, req RegistrationRequest) (RegistrationAccept, error) {
	addr, err := g.address(imsi)
	if err != nil {
		return RegistrationAccept{}, err
	}
	if req.FiveGSRegistrationType != InitialRegistration {
		return RegistrationAccept{}, fmt.Errorf("%w: 5GS registration type %q, the stub serves %q", ErrInvalidMessage, req.FiveGSRegistrationType, InitialRegistration)
	}
	// An initial registration from a UE that is still registered, as
	// after a restart, deletes its 5GMM context first (TS 24.501
	// 5.5.1.2.8 f), and with it the PDU session.
	if _, ok := g.store.TakeUEContext(imsi); ok {
		g.releaseLocally(ctx, addr)
	}
	g.store.PutUEContext(UEContext{SUPI: imsi, State: Registered})
	g.log.Info("UE registered", "imsi", imsi)
	return RegistrationAccept{
		FiveGSRegistrationResult:    RegistrationResult{Value: Access3GPP},
		TAIList:                     []TrackingAreaIdentity{g.cfg.Cell.TrackingAreaIdentity},
		FiveGSNetworkFeatureSupport: NetworkFeatureSupport{IMSVoPS3GPP: true},
	}, nil
}

// Deregister serves a DEREGISTRATION REQUEST from the UE (TS 24.501
// 5.5.2.2): the AMF has the SMF release the PDU session locally, and
// the PCF asks the AFs to end their contexts. A UE that switches off
// gets no DEREGISTRATION ACCEPT, so the result is nil.
func (g *FiveGC) Deregister(ctx context.Context, imsi string, req DeregistrationRequest) (*DeregistrationAccept, error) {
	addr, err := g.address(imsi)
	if err != nil {
		return nil, err
	}
	if req.DeregistrationType.AccessType != Access3GPP {
		return nil, fmt.Errorf("%w: access type %q, the stub serves %q", ErrInvalidMessage, req.DeregistrationType.AccessType, Access3GPP)
	}
	if _, ok := g.store.TakeUEContext(imsi); !ok {
		return nil, ErrNotRegistered
	}
	g.log.Info("UE deregistered", "imsi", imsi, "switch_off", req.DeregistrationType.SwitchOff)
	g.releaseLocally(ctx, addr)
	if req.DeregistrationType.SwitchOff {
		return nil, nil
	}
	return &DeregistrationAccept{}, nil
}

// NetworkInitiatedDeregistration is the network deregistering the UE
// (TS 24.501 5.5.2.3; TS 23.502 4.2.2.3.3): a DEREGISTRATION REQUEST
// goes to the UE, the PDU session is released locally, and the PCF asks
// the AFs to end their contexts. A learner starts it over the API
// (http.go).
func (g *FiveGC) NetworkInitiatedDeregistration(ctx context.Context, imsi string) error {
	addr, err := g.address(imsi)
	if err != nil {
		return err
	}
	if _, ok := g.store.TakeUEContext(imsi); !ok {
		return ErrNotRegistered
	}
	g.log.Info("network deregisters the UE", "imsi", imsi)
	fx := effects{imsi: imsi}
	if s, ok := g.store.TakePDUSession(addr); ok {
		fx = g.endPDUSession(s)
	}
	fx.toUE = append(fx.toUE, ueMessage{TypeDeregistrationRequest, DeregistrationRequest{
		DeregistrationType: DeregistrationType{AccessType: Access3GPP}}})
	g.carryOut(ctx, fx)
	return nil
}

// DeregistrationAccepted serves the UE's DEREGISTRATION ACCEPT. The
// network forgot the UE when it sent its DEREGISTRATION REQUEST, so
// there is nothing to change.
//
// SIMPLIFIED: TS 24.501 5.5.2.3. The AMF runs no T3522 and does not
// send the request again.
func (g *FiveGC) DeregistrationAccepted(imsi string) error {
	_, err := g.address(imsi)
	return err
}

// EstablishPDUSession serves a PDU SESSION ESTABLISHMENT REQUEST for the
// IMS DNN (TS 23.502 4.3.2.2.1, TS 24.501 6.4.1.3): it sets up the PDU
// session with the QoS flow of its default QoS rule, and answers with
// the UE's address and, when the UE asks for them, the P-CSCF addresses
// (step 11).
//
// SIMPLIFIED: TS 24.501 6.4.1. The stub serves one PDU session per UE,
// of type IPv4, in SSC mode 1, to the DNN of its configuration, which
// the UE must name: there is no subscription to take a default DNN
// from. PDU SESSION ESTABLISHMENT REJECT is not sent: a request the stub
// cannot serve gets an error from the API.
func (g *FiveGC) EstablishPDUSession(ctx context.Context, imsi string, m ULNASTransport[PDUSessionEstablishmentRequest]) (DLNASTransport[PDUSessionEstablishmentAccept], error) {
	var none DLNASTransport[PDUSessionEstablishmentAccept]
	addr, err := g.address(imsi)
	if err != nil {
		return none, err
	}
	if _, ok := g.store.UEContext(imsi); !ok {
		return none, ErrNotRegistered
	}
	req := m.PayloadContainer
	switch {
	case m.PayloadContainerType != N1SMInformation:
		return none, fmt.Errorf("%w: payload container type %q, want %q", ErrInvalidMessage, m.PayloadContainerType, N1SMInformation)
	case req.PDUSessionID == 0 || m.PDUSessionID != req.PDUSessionID:
		return none, fmt.Errorf("%w: PDU session ID %d in UL NAS TRANSPORT and %d in the 5GSM message", ErrInvalidMessage, m.PDUSessionID, req.PDUSessionID)
	case m.RequestType != InitialRequest:
		return none, fmt.Errorf("%w: request type %q, want %q", ErrInvalidMessage, m.RequestType, InitialRequest)
	case !strings.EqualFold(m.DNN, g.cfg.DNN):
		// A DNN is equivalent to an APN (TS 23.501 5.9.6), and the EPC stub
		// compares APNs without regard to case.
		return none, fmt.Errorf("%w: DNN %q, the stub serves %q", ErrInvalidMessage, m.DNN, g.cfg.DNN)
	case req.SSCMode != "" && req.SSCMode != SSCMode1:
		return none, fmt.Errorf("%w: SSC mode %q, the stub serves %q", ErrInvalidMessage, req.SSCMode, SSCMode1)
	}
	accept := PDUSessionEstablishmentAccept{PDUSessionID: req.PDUSessionID, PTI: req.PTI, SelectedPDUSessionType: IPv4, SelectedSSCMode: SSCMode1}
	switch req.PDUSessionType {
	case "", IPv4:
	case IPv4v6:
		// TS 24.501 6.4.1.3: a network limited to IPv4 selects IPv4 and
		// says why with #50.
		accept.FiveGSMCause = PDUSessionTypeIPv4OnlyAllowed
	default:
		return none, fmt.Errorf("%w: PDU session type %q, the stub serves %q", ErrInvalidMessage, req.PDUSessionType, IPv4)
	}
	if old, ok := g.store.PDUSession(addr); ok {
		if old.PDUSessionID != req.PDUSessionID {
			return none, fmt.Errorf("%w: PDU session %d is set up already; the stub serves one PDU session per UE", ErrInvalidMessage, old.PDUSessionID)
		}
		// TS 24.501 6.4.1.7 c: an initial request for an existing PDU
		// session ID releases the old session locally first.
		g.releaseLocally(ctx, addr)
	}

	session := PDUSession{
		IMSI: imsi, PDUSessionID: req.PDUSessionID, Address: addr, DNN: g.cfg.DNN, State: PDUSessionActive,
		DefaultQoSFlow: QoSFlow{
			QFI: defaultQFI, FiveQI: FiveQIIMSSignalling,
			QoSRuleIdentifier: defaultQRI, QoSRulePrecedence: defaultRulePrecedence,
			PacketFilters: []PacketFilter{{Direction: Bidirectional, Identifier: 1, MatchAll: true}},
		},
	}
	g.store.PutPDUSession(session)
	g.log.Info("PDU session set up", "imsi", imsi, "pdu_session_id", req.PDUSessionID, "address", addr.String(), "dnn", g.cfg.DNN)

	accept.AuthorizedQoSRules = []QoSRule{session.DefaultQoSFlow.qosRule(true)}
	accept.AuthorizedQoSFlowDescriptions = []QoSFlowDescription{session.DefaultQoSFlow.qosFlowDescription()}
	accept.SessionAMBR = sessionAMBR
	accept.PDUAddress = PDUAddress{PDUSessionTypeValue: IPv4, IPv4Address: addr}
	accept.DNN = g.cfg.DNN
	accept.ExtendedProtocolConfigurationOptions = g.pcscfAddresses(req.ExtendedProtocolConfigurationOptions)
	return newDLNASTransport(req.PDUSessionID, accept), nil
}

// pcscfAddresses answers the extended PCO of the UE: the P-CSCF
// addresses of each IP version it asked for (TS 24.501 6.4.1.3), and
// the IM CN Subsystem Signaling Flag back when it set it, to say that
// the session is one for IMS signalling (TS 24.008 10.5.6.3).
func (g *FiveGC) pcscfAddresses(req ExtendedProtocolConfigurationOptions) ExtendedProtocolConfigurationOptions {
	var answer ExtendedProtocolConfigurationOptions
	if req.has(IMCNSubsystemSignalingFlag) {
		answer.Containers = append(answer.Containers, Container{ID: IMCNSubsystemSignalingFlag})
	}
	for _, a := range g.cfg.PCSCFAddresses {
		switch {
		case a.Is4() && req.has(PCSCFIPv4Address):
			answer.Containers = append(answer.Containers, Container{ID: PCSCFIPv4Address, Contents: a.String()})
		case a.Is6() && req.has(PCSCFIPv6Address):
			answer.Containers = append(answer.Containers, Container{ID: PCSCFIPv6Address, Contents: a.String()})
		}
	}
	return answer
}

// ReleasePDUSession serves a PDU SESSION RELEASE REQUEST (TS 24.501
// 6.4.3.3): the SMF accepts it by releasing the session, and answers
// with PDU SESSION RELEASE COMMAND with the PTI of the request (6.3.3.2).
// The PCF asks the AFs to end their contexts.
func (g *FiveGC) ReleasePDUSession(ctx context.Context, imsi string, m ULNASTransport[PDUSessionReleaseRequest]) (DLNASTransport[PDUSessionReleaseCommand], error) {
	var none DLNASTransport[PDUSessionReleaseCommand]
	addr, err := g.address(imsi)
	if err != nil {
		return none, err
	}
	req := m.PayloadContainer
	s, ok := g.store.PDUSession(addr)
	if !ok {
		return none, ErrNoPDUSession
	}
	if req.PDUSessionID != s.PDUSessionID {
		return none, fmt.Errorf("%w: PDU session %d, the UE has %d", ErrInvalidMessage, req.PDUSessionID, s.PDUSessionID)
	}
	if s, ok = g.store.TakePDUSession(addr); !ok {
		return none, ErrNoPDUSession
	}
	g.log.Info("UE released the PDU session", "imsi", imsi, "pdu_session_id", s.PDUSessionID)
	g.carryOut(ctx, g.endPDUSession(s))
	return newDLNASTransport(s.PDUSessionID, PDUSessionReleaseCommand{
		PDUSessionID: s.PDUSessionID, PTI: req.PTI, FiveGSMCause: RegularDeactivation}), nil
}

// PDUSessionReleaseCompleted serves PDU SESSION RELEASE COMPLETE (TS
// 24.501 6.3.3.3).
//
// SIMPLIFIED: TS 24.501 6.3.3.2. The network forgets the session when
// it sends PDU SESSION RELEASE COMMAND, instead of keeping it in PDU
// SESSION INACTIVE PENDING until this answer, and runs no T3592.
func (g *FiveGC) PDUSessionReleaseCompleted(imsi string, m ULNASTransport[PDUSessionReleaseComplete]) error {
	_, err := g.address(imsi)
	return err
}

// NetworkRequestedPDUSessionRelease is the network releasing a PDU
// session on its own (TS 24.501 6.3.3.2; TS 23.502 4.3.4.2 step 1e):
// PDU SESSION RELEASE COMMAND goes to the UE with no PTI, and the PCF
// asks the AFs to end their contexts with PDU_SESSION_TERMINATION (TS
// 29.514 4.2.5.3). A learner starts it over the API (http.go).
func (g *FiveGC) NetworkRequestedPDUSessionRelease(ctx context.Context, imsi string, pduSessionID uint8) error {
	addr, err := g.address(imsi)
	if err != nil {
		return err
	}
	if s, ok := g.store.PDUSession(addr); !ok || s.PDUSessionID != pduSessionID {
		return fmt.Errorf("%w: PDU session %d", ErrNoPDUSession, pduSessionID)
	}
	s, ok := g.store.TakePDUSession(addr)
	if !ok {
		return ErrNoPDUSession
	}
	g.log.Info("network released the PDU session", "imsi", imsi, "pdu_session_id", s.PDUSessionID)
	fx := g.endPDUSession(s)
	fx.toUE = append(fx.toUE, ueMessage{TypePDUSessionReleaseCommand, newDLNASTransport(s.PDUSessionID, PDUSessionReleaseCommand{
		PDUSessionID: s.PDUSessionID, PTI: noProcedureTransactionIdentity, FiveGSMCause: RegularDeactivation})})
	g.carryOut(ctx, fx)
	return nil
}

// releaseLocally ends the PDU session of addr, if there is one, without
// telling the UE, as a deregistration or a new registration does (TS
// 24.501 5.5.2.2.3, 5.5.2.3.1).
func (g *FiveGC) releaseLocally(ctx context.Context, addr netip.Addr) {
	if s, ok := g.store.TakePDUSession(addr); ok {
		g.log.Info("PDU session released locally", "imsi", s.IMSI, "pdu_session_id", s.PDUSessionID)
		g.carryOut(ctx, g.endPDUSession(s))
	}
}

// endPDUSession is the end of the SM policy association that the PCF
// hears of when a PDU session ends: it asks the AF of every context
// bound to the session to delete it, with the cause
// PDU_SESSION_TERMINATION (TS 29.514 4.2.5.3).
func (g *FiveGC) endPDUSession(s PDUSession) effects {
	return effects{imsi: s.IMSI, toAF: g.terminateAll(s.Address, s.AppSessions, n5.PDUSessionTermination)}
}

// terminate is the termination request for the context a (TS 29.514
// 4.2.5.3), posted to {notifUri}/terminate.
func terminate(a AppSession, cause n5.TerminationCause) afMessage {
	return afMessage{appSessionID: a.AppSessionID, notifURI: a.NotifURI,
		termination: &n5.TerminationInfo{TermCause: cause, ResUri: a.ResourceURI}}
}

// Status is what the API shows of a UE.
func (g *FiveGC) Status(imsi string) (Status, error) {
	addr, err := g.address(imsi)
	if err != nil {
		return Status{}, err
	}
	c, ok := g.store.UEContext(imsi)
	if !ok {
		return Status{}, ErrNotRegistered
	}
	st := Status{IMSI: imsi, RegistrationState: c.State}
	if s, ok := g.store.PDUSession(addr); ok {
		ps := &PDUSessionStatus{PDUSessionID: s.PDUSessionID, DNN: s.DNN, PDUAddress: s.Address, State: s.State,
			QoSFlows: []QoSFlow{s.DefaultQoSFlow}, AppSessions: s.AppSessions}
		if s.VoiceQoSFlow != nil {
			ps.QoSFlows = append(ps.QoSFlows, *s.VoiceQoSFlow)
		}
		if ps.AppSessions == nil {
			ps.AppSessions = []AppSession{}
		}
		st.PDUSession = ps
	}
	return st, nil
}

// carryOut sends what a change to a PDU session calls for.
func (g *FiveGC) carryOut(ctx context.Context, fx effects) {
	for _, m := range fx.toUE {
		err := g.streams.Send(fx.imsi, m.name, m.message)
		if err == nil {
			g.log.Info("sent to UE", "imsi", fx.imsi, "message", string(m.name))
			continue
		}
		g.log.Warn("could not send to UE", "imsi", fx.imsi, "message", string(m.name), "error", err.Error())
		// A UE without an event stream cannot take a new voice QoS flow,
		// which then fails as if the UE had rejected it.
		if c, ok := m.message.(DLNASTransport[PDUSessionModificationCommand]); ok && createsQoSFlow(c.PayloadContainer) {
			if failed, err := g.voiceQoSFlowFailed(fx.imsi, c.PDUSessionID); err == nil {
				fx.toAF = append(fx.toAF, failed.toAF...)
			}
		}
	}
	for _, m := range fx.toAF {
		g.sendToAF(ctx, m)
	}
}

// sendToAF posts one notification of the PCF to an AF (TS 29.514
// 4.2.5.2, 4.2.5.3). The AF answers 204; a failure is only logged, as
// the PCF has nothing to undo.
func (g *FiveGC) sendToAF(ctx context.Context, m afMessage) {
	var err error
	what := "notify"
	if m.termination != nil {
		what = "terminate"
		err = g.notifier.Terminate(ctx, m.notifURI, *m.termination)
	} else {
		err = g.notifier.Notify(ctx, m.notifURI, *m.notification)
	}
	if err != nil {
		g.log.Warn("the AF did not take the "+what, "app_session_id", m.appSessionID, "notif_uri", m.notifURI, "error", err.Error())
		return
	}
	g.log.Info("sent "+what+" to the AF", "app_session_id", m.appSessionID)
}

// createsQoSFlow reports whether the command adds a QoS flow to the UE.
func createsQoSFlow(c PDUSessionModificationCommand) bool {
	for _, d := range c.AuthorizedQoSFlowDescriptions {
		if d.OperationCode == CreateNewQoSFlowDescription {
			return true
		}
	}
	return false
}
