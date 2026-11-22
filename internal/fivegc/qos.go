package fivegc

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/n5"
)

// This file is the voice QoS flow: how the SMF binds the PCC rules of
// the PCF to it, tells the UE with PDU SESSION MODIFICATION COMMAND (TS
// 23.502 4.3.3.2), and reports to the PCF what became of it.

// The identifiers of the voice QoS flow and its QoS rule: the next ones
// after those of the default QoS rule, as the voice flow is the only
// other one.
const (
	voiceQFI = defaultQFI + 1
	voiceQRI = defaultQRI + 1
	// voiceRulePrecedence puts the voice rule before the default one,
	// which matches all packets (TS 23.501 5.7.1.9).
	voiceRulePrecedence = 1
)

// maxPacketFiltersPerRule is the most packet filters one QoS rule holds:
// its number of packet filters is four bits (TS 24.501 9.11.4.13).
const maxPacketFiltersPerRule = 15

// reconcileVoiceQoSFlow makes the voice QoS flow carry the PCC rules of
// all the contexts of s: the SMF binds every rule of 5QI 1 to the one
// flow (TS 29.513 6.4), creates the flow for the first rules, and
// deletes it when the last ones go. Either way the UE hears PDU SESSION
// MODIFICATION COMMAND, and the session waits for its answer.
//
// SIMPLIFIED: TS 24.501 6.3.2.2. When the rules change while the flow
// exists, the stub updates its own record of the QoS rule and the QoS
// but sends no PDU SESSION MODIFICATION COMMAND: it enforces neither,
// and a UE only needs to know that the flow is there. Nor does it wait
// for one command to be answered before sending the next, or run T3591.
func (g *FiveGC) reconcileVoiceQoSFlow(s *PDUSession, fx *effects) error {
	var rules []PCCRule
	for _, a := range s.AppSessions {
		rules = append(rules, a.Rules...)
	}
	if len(rules) == 0 {
		if f := s.VoiceQoSFlow; f != nil {
			fx.toUE = append(fx.toUE, ueMessage{TypePDUSessionModificationCommand, newDLNASTransport(s.PDUSessionID, deleteQoSFlow(s.PDUSessionID, *f))})
			s.VoiceQoSFlow = nil
			s.State = PDUSessionModificationPending
		}
		return nil
	}
	flow, err := voiceQoSFlow(rules)
	if err != nil {
		return err
	}
	if s.VoiceQoSFlow != nil {
		s.VoiceQoSFlow = &flow
		return nil
	}
	s.VoiceQoSFlow = &flow
	s.State = PDUSessionModificationPending
	// TS 24.501 6.3.2.2 a): a new GBR QoS flow comes with its QoS flow
	// description.
	fx.toUE = append(fx.toUE, ueMessage{TypePDUSessionModificationCommand, newDLNASTransport(s.PDUSessionID, PDUSessionModificationCommand{
		PDUSessionID:                  s.PDUSessionID,
		PTI:                           noProcedureTransactionIdentity,
		AuthorizedQoSRules:            []QoSRule{flow.qosRule(false)},
		AuthorizedQoSFlowDescriptions: []QoSFlowDescription{flow.qosFlowDescription()},
	})})
	return nil
}

// voiceQoSFlow is the voice QoS flow for rules: 5QI 1, with the bit
// rates of the rules added up, and one packet filter for each of their
// flows.
//
// SIMPLIFIED: TS 23.501 5.7.1.2. The QoS profile that would go to the
// NG-RAN, with its ARP, is not kept: there is no RAN to admit the flow.
// The stub enforces neither rate.
func voiceQoSFlow(rules []PCCRule) (QoSFlow, error) {
	f := QoSFlow{QFI: voiceQFI, FiveQI: FiveQIConversationalVoice, QoSRuleIdentifier: voiceQRI, QoSRulePrecedence: voiceRulePrecedence}
	var gUL, gDL, mUL, mDL uint64
	for _, r := range rules {
		gUL, gDL = gUL+r.GuaranteedBitRateUL, gDL+r.GuaranteedBitRateDL
		mUL, mDL = mUL+r.MaxBitRateUL, mDL+r.MaxBitRateDL
		for _, fd := range r.Flows {
			f.PacketFilters = append(f.PacketFilters, packetFilter(fd))
		}
	}
	if len(f.PacketFilters) > maxPacketFiltersPerRule {
		return QoSFlow{}, fmt.Errorf("%d packet filters, a QoS rule holds %d", len(f.PacketFilters), maxPacketFiltersPerRule)
	}
	for i := range f.PacketFilters {
		if f.PacketFilters[i] == (PacketFilter{Direction: f.PacketFilters[i].Direction}) {
			// TS 24.501 6.4.1.3: only the default QoS rule may match all
			// packets.
			return QoSFlow{}, fmt.Errorf("flow %d matches every packet", i+1)
		}
		f.PacketFilters[i].Identifier = uint8(i + 1)
	}
	f.GFBRUplink, f.GFBRDownlink, f.MFBRUplink, f.MFBRDownlink = kbps(gUL), kbps(gDL), kbps(mUL), kbps(mDL)
	return f, nil
}

// kbps rounds a bit rate up to the kbit/s that the QoS flow
// descriptions carry (TS 24.501 9.11.4.12, in units of 1 Kbps).
func kbps(bps uint64) uint32 { return uint32((bps + 999) / 1000) }

// deleteQoSFlow is the command that removes the flow f from the UE: its
// QoS rule and its QoS flow description, each with only its identifier
// (TS 24.501 9.11.4.12, 9.11.4.13).
func deleteQoSFlow(pduSessionID uint8, f QoSFlow) PDUSessionModificationCommand {
	return PDUSessionModificationCommand{
		PDUSessionID:                  pduSessionID,
		PTI:                           noProcedureTransactionIdentity,
		AuthorizedQoSRules:            []QoSRule{{QoSRuleIdentifier: f.QoSRuleIdentifier, RuleOperationCode: DeleteExistingQoSRule}},
		AuthorizedQoSFlowDescriptions: []QoSFlowDescription{{QFI: f.QFI, OperationCode: DeleteExistingQoSFlowDescription}},
	}
}

// ModificationCompleted serves PDU SESSION MODIFICATION COMPLETE (TS
// 24.501 6.3.2.3). When it answers the creation of the voice QoS flow,
// the SMF tells the PCF that its rules are enforced (TS 23.502 4.3.3.2
// step 13), and the PCF tells each AF that asked that its resources are
// there (TS 29.514 4.2.5.8).
func (g *FiveGC) ModificationCompleted(ctx context.Context, imsi string, m ULNASTransport[PDUSessionModificationComplete]) error {
	addr, err := g.address(imsi)
	if err != nil {
		return err
	}
	var fx effects
	_, err = g.store.UpdatePDUSession(addr, func(s *PDUSession) error {
		if m.PayloadContainer.PDUSessionID != s.PDUSessionID {
			return fmt.Errorf("%w: PDU session %d, the UE has %d", ErrInvalidMessage, m.PayloadContainer.PDUSessionID, s.PDUSessionID)
		}
		fx = effects{imsi: imsi}
		if s.State != PDUSessionModificationPending {
			return nil // a repeated COMPLETE
		}
		s.State = PDUSessionActive
		if s.VoiceQoSFlow == nil {
			return nil
		}
		for _, a := range s.AppSessions {
			if len(a.Rules) > 0 && a.subscribed(n5.SuccessfulResourcesAllocation) {
				fx.toAF = append(fx.toAF, succeeded(a))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	g.carryOut(ctx, fx)
	return nil
}

// ModificationRejected serves PDU SESSION MODIFICATION COMMAND REJECT
// (TS 24.501 6.3.2.4): the voice QoS flow could not be set up.
func (g *FiveGC) ModificationRejected(ctx context.Context, imsi string, m ULNASTransport[PDUSessionModificationCommandReject]) error {
	if _, err := g.address(imsi); err != nil {
		return err
	}
	g.log.Info("UE rejected the PDU session modification", "imsi", imsi, "5gsm_cause", m.PayloadContainer.FiveGSMCause.String())
	fx, err := g.voiceQoSFlowFailed(imsi, m.PayloadContainer.PDUSessionID)
	if err != nil {
		return err
	}
	g.carryOut(ctx, fx)
	return nil
}

// voiceQoSFlowFailed removes a voice QoS flow whose creation failed. The
// SMF reports its PCC rules inactive (TS 29.512 4.2.4.7), and the PCF
// tells each AF that asked that its resources could not be allocated
// (TS 29.514 4.2.5.8). As all the resources of those contexts failed,
// the PCF then asks their AFs to delete them with ALL_SDF_DEACTIVATION
// (TS 29.514 4.2.5.3).
func (g *FiveGC) voiceQoSFlowFailed(imsi string, pduSessionID uint8) (effects, error) {
	addr, err := g.address(imsi)
	if err != nil {
		return effects{}, err
	}
	var ended []AppSession
	_, err = g.store.UpdatePDUSession(addr, func(s *PDUSession) error {
		if pduSessionID != s.PDUSessionID {
			return fmt.Errorf("%w: PDU session %d, the UE has %d", ErrInvalidMessage, pduSessionID, s.PDUSessionID)
		}
		s.State = PDUSessionActive
		if s.VoiceQoSFlow == nil {
			return nil // the command was the one that deleted the flow
		}
		s.VoiceQoSFlow = nil
		ended = takeMediaContexts(s)
		return nil
	})
	if err != nil {
		return effects{}, err
	}
	fx := effects{imsi: imsi}
	for _, a := range ended {
		if a.subscribed(n5.FailedResourcesAllocation) {
			fx.toAF = append(fx.toAF, failed(a))
		}
	}
	fx.toAF = append(fx.toAF, g.terminateAll(addr, ended, n5.AllSDFDeactivation)...)
	return fx, nil
}

// takeMediaContexts removes from s the contexts whose media the voice
// QoS flow carried, and returns them. The context of the signalling
// path, whose flows are on the default QoS flow, stays.
func takeMediaContexts(s *PDUSession) []AppSession {
	var media, rest []AppSession
	for _, a := range s.AppSessions {
		if len(a.Rules) > 0 {
			media = append(media, a)
		} else {
			rest = append(rest, a)
		}
	}
	s.AppSessions = rest
	return media
}

// terminateAll asks the AFs of the contexts to delete them (TS 29.514
// 4.2.5.3). The bindings stay, marked terminated, so the Delete that
// each AF then sends ends its context with success.
func (g *FiveGC) terminateAll(addr netip.Addr, contexts []AppSession, cause n5.TerminationCause) []afMessage {
	var out []afMessage
	for _, a := range contexts {
		g.store.PutBinding(a.AppSessionID, Binding{Address: addr, Terminated: true})
		out = append(out, terminate(a, cause))
	}
	return out
}

// ReleaseQoSFlow is the network releasing the voice QoS flow of a UE,
// as when the NG-RAN can no longer carry it (TS 23.502 4.3.3.2 step
// 1e). A learner starts it over the API (http.go). The UE hears PDU
// SESSION MODIFICATION COMMAND deleting the flow's QoS rule and QoS
// flow description, the network-requested PDU session modification
// (TS 24.501 6.3.2.2).
//
// The flow carried every flow of the contexts on it, so the SMF reports
// their PCC rules inactive (TS 29.512 4.2.4.7), and the PCF asks each
// AF to delete its context with ALL_SDF_DEACTIVATION: when all the
// service data flows of a context are gone, TS 29.514 4.2.5.5 sends the
// termination request of 4.2.5.3 instead of an event. This is where Rx
// would send INDICATION_OF_RELEASE_OF_BEARER and an ASR (TS 29.214
// 4.4.6.2); N5 has no event of that name, and FAILED_RESOURCES_ALLOCATION
// naming the lost flows (4.2.5.5) is sent only when some flows of a
// context remain, which the one voice QoS flow never leaves.
func (g *FiveGC) ReleaseQoSFlow(ctx context.Context, imsi string, pduSessionID, qfi uint8) error {
	addr, err := g.address(imsi)
	if err != nil {
		return err
	}
	if qfi == defaultQFI {
		return fmt.Errorf("%w: the QoS flow of the default QoS rule lasts as long as the PDU session; release the PDU session instead", ErrInvalidMessage)
	}
	var ended []AppSession
	fx := effects{imsi: imsi}
	_, err = g.store.UpdatePDUSession(addr, func(s *PDUSession) error {
		if s.PDUSessionID != pduSessionID {
			return fmt.Errorf("%w: PDU session %d", ErrNoPDUSession, pduSessionID)
		}
		f := s.VoiceQoSFlow
		if f == nil || f.QFI != qfi {
			return fmt.Errorf("%w: QFI %d", ErrUnknownQoSFlow, qfi)
		}
		fx.toUE = append(fx.toUE, ueMessage{TypePDUSessionModificationCommand, newDLNASTransport(s.PDUSessionID, deleteQoSFlow(s.PDUSessionID, *f))})
		s.VoiceQoSFlow = nil
		s.State = PDUSessionModificationPending
		ended = takeMediaContexts(s)
		return nil
	})
	if err != nil {
		return err
	}
	g.log.Info("network released the QoS flow", "imsi", imsi, "pdu_session_id", pduSessionID, "qfi", qfi)
	fx.toAF = g.terminateAll(addr, ended, n5.AllSDFDeactivation)
	g.carryOut(ctx, fx)
	return nil
}
