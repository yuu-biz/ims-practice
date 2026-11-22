package epc

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

// This file is the PCRF stub: the Rx server side of TS 29.214. It binds
// each Rx session to the PDN connection of the UE's address, turns the
// media of an AF session into PCC rules for the voice bearer, and tells
// the AF what happens to them.
//
// SIMPLIFIED: TS 29.214 4.4.2 and 5.3.16. Every AAR is taken to carry
// the whole service information, as the P-CSCF of this project derives
// it from the whole SDP each time; AVPs left out are not filled in from
// earlier AARs.

// errNotAuthorized: the PCRF refuses the service information.
var errNotAuthorized = errors.New("epc: requested service not authorized")

// HandleRx answers the Rx requests of AFs. It is the Diameter handler of
// the node.
func (e *EPC) HandleRx(request *diam.Message) *diam.Message {
	if request.Header.ApplicationID == diameter.ApplicationIDRx {
		switch request.Header.CommandCode {
		case rx.CommandAA:
			return e.aaRequest(request)
		case rx.CommandSessionTermination:
			return e.sessionTermination(request)
		}
	}
	return diameter.CommandUnsupportedAnswer(e.node.Identity(), request)
}

// aaRequest provisions or updates the service information of an Rx
// session (TS 29.214 4.4.1, 4.4.2, 4.4.5).
func (e *EPC) aaRequest(request *diam.Message) *diam.Message {
	answer := func(result diameter.Result) *diam.Message {
		return rx.AAAnswer{Result: result}.Message(request, e.node.Identity())
	}
	aar, err := rx.ParseAARequest(request)
	switch {
	case errors.Is(err, rx.ErrFilterRestrictions):
		return answer(diameter.Experimental(rx.FilterRestrictions))
	case errors.Is(err, rx.ErrInvalidServiceInformation):
		return answer(diameter.Experimental(rx.InvalidServiceInformation))
	case err != nil:
		e.log.Warn("unreadable AAR", "error", err.Error())
		return answer(diameter.Base(diameter.DiameterUnableToComply))
	}
	rules, err := pccRules(aar.MediaComponents)
	if err != nil {
		e.log.Info("refused service information", "session_id", aar.SessionID, "error", err.Error())
		return answer(diameter.Experimental(rx.RequestedServiceNotAuthorized))
	}

	// Session binding (TS 29.213 5.2): a new Rx session by the UE's
	// address, an existing one by its earlier binding.
	binding, bound := e.store.Binding(aar.SessionID)
	addr := aar.FramedIPAddress
	if bound {
		if binding.Aborted {
			return answer(diameter.Base(diameter.DiameterUnknownSessionID))
		}
		addr = binding.Address
	}
	var fx effects
	_, err = e.store.UpdatePDNConnection(addr, func(c *PDNConnection) error {
		fx = effects{imsi: c.IMSI}
		s := AFSession{SessionID: aar.SessionID, AFHost: aar.OriginHost, AFRealm: aar.OriginRealm, Signalling: isSignalling(aar.MediaComponents)}
		i := slices.IndexFunc(c.AFSessions, func(s AFSession) bool { return s.SessionID == aar.SessionID })
		if i >= 0 {
			s.SpecificActions = c.AFSessions[i].SpecificActions
		} else {
			c.AFSessions = append(c.AFSessions, s)
			i = len(c.AFSessions) - 1
		}
		for _, a := range aar.SpecificActions {
			if !s.subscribed(a) {
				s.SpecificActions = append(s.SpecificActions, a)
			}
		}
		s.Rules = rules
		c.AFSessions[i] = s
		if err := e.reconcileVoiceBearer(c, &fx); err != nil {
			return fmt.Errorf("%w: %v", errNotAuthorized, err)
		}
		// Rules added to a bearer that is already up have their resources
		// at once.
		if b := c.VoiceBearer; b != nil && b.State == BearerActive && len(s.Rules) > 0 && s.subscribed(rx.IndicationOfSuccessfulResourcesAllocation) {
			fx.toAF = append(fx.toAF, e.report(s, rx.IndicationOfSuccessfulResourcesAllocation))
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrNoPDNConnection):
		// TS 29.214 4.4.1: session binding failed. A UE that does not come
		// through the EPC gets this.
		e.log.Info("no IP-CAN session for the AAR", "session_id", aar.SessionID, "framed_ip_address", addr.String())
		return answer(diameter.Experimental(rx.IPCANSessionNotAvailable))
	case err != nil:
		e.log.Info("refused service information", "session_id", aar.SessionID, "error", err.Error())
		return answer(diameter.Experimental(rx.RequestedServiceNotAuthorized))
	}
	if !bound {
		e.store.PutBinding(aar.SessionID, Binding{Address: addr})
	}
	// TS 29.214 4.4.1: the AAA goes before, or in parallel with, the
	// provisioning of the PCC rules, which here is the bearer for the UE.
	e.node.AfterAnswer(request, func() { e.carryOut(context.Background(), fx) })
	return answer(diameter.Base(diameter.DiameterSuccess))
}

// sessionTermination ends an Rx session: the STA goes first, then the
// resources of the session are freed (TS 29.214 4.4.4).
func (e *EPC) sessionTermination(request *diam.Message) *diam.Message {
	answer := func(code uint32) *diam.Message {
		return rx.SessionTerminationAnswer{Result: diameter.Base(code)}.Message(request, e.node.Identity())
	}
	str, err := rx.ParseSessionTerminationRequest(request)
	if err != nil {
		e.log.Warn("unreadable STR", "error", err.Error())
		return answer(diameter.DiameterUnableToComply)
	}
	binding, ok := e.store.Binding(str.SessionID)
	if !ok {
		return answer(diameter.DiameterUnknownSessionID)
	}
	e.store.DeleteBinding(str.SessionID)
	if binding.Aborted {
		return answer(diameter.DiameterSuccess)
	}
	var fx effects
	_, err = e.store.UpdatePDNConnection(binding.Address, func(c *PDNConnection) error {
		fx = effects{imsi: c.IMSI}
		c.AFSessions = slices.DeleteFunc(c.AFSessions, func(s AFSession) bool { return s.SessionID == str.SessionID })
		return e.reconcileVoiceBearer(c, &fx)
	})
	if err != nil && !errors.Is(err, ErrNoPDNConnection) {
		e.log.Warn("could not free the resources of the Rx session", "session_id", str.SessionID, "error", err.Error())
	}
	e.node.AfterAnswer(request, func() { e.carryOut(context.Background(), fx) })
	return answer(diameter.DiameterSuccess)
}

// isSignalling reports whether the service information is that of the
// subscription to the signalling path: media component 0 only (TS
// 29.214 4.4.5).
func isSignalling(components []policy.MediaComponent) bool {
	return len(components) > 0 && !slices.ContainsFunc(components, func(c policy.MediaComponent) bool { return c.Number != 0 })
}

// pccRules makes one PCC rule of each media component that has flows.
// The AF signalling of component 0 needs none: its rules came with the
// default bearer (TS 29.214 4.4.5).
//
// SIMPLIFIED: TS 29.213 6.3 Table 6.3.1. Only audio is authorised, with
// QCI 1 for the voice bearer (IR.92 4.4.2); the stub has no bearer for
// other media, and refuses them.
func pccRules(components []policy.MediaComponent) ([]PCCRule, error) {
	var rules []PCCRule
	for _, c := range components {
		if c.Number == 0 || c.FlowStatus == policy.Removed {
			continue
		}
		if c.Type != policy.Audio {
			return nil, fmt.Errorf("%w: media component %d is not audio", errNotAuthorized, c.Number)
		}
		r := PCCRule{MediaComponentNumber: c.Number, BitRateUL: valueOf(c.MaxRequestedBandwidthUL), BitRateDL: valueOf(c.MaxRequestedBandwidthDL)}
		for _, s := range c.SubComponents {
			r.FlowNumbers = append(r.FlowNumbers, s.FlowNumber)
			r.Flows = append(r.Flows, s.Flows...)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func valueOf(p *uint32) uint32 {
	if p == nil {
		return 0
	}
	return *p
}

// toAF is the header of a request from the PCRF to the AF of s.
func (e *EPC) toAF(s AFSession) diameter.Header {
	id := e.node.Identity()
	return diameter.Header{SessionID: s.SessionID, OriginHost: id.OriginHost, OriginRealm: id.OriginRealm,
		DestinationHost: s.AFHost, DestinationRealm: s.AFRealm}
}

// report is the RAR that tells the AF of s about action on the flows of
// its rules (TS 29.214 4.4.6.2, 5.3.13).
func (e *EPC) report(s AFSession, action rx.SpecificAction) afMessage {
	return afMessage{host: s.AFHost, request: rx.ReAuthRequest{Header: e.toAF(s),
		SpecificActions: []rx.SpecificAction{action}, Flows: sessionFlows(s)}}
}

// abortSession is the ASR that tells the AF of s its bearer is gone (TS
// 29.214 4.4.6.1, 4.4.6.2).
func (e *EPC) abortSession(s AFSession) afMessage {
	return afMessage{host: s.AFHost, request: rx.AbortSessionRequest{Header: e.toAF(s), AbortCause: rx.BearerReleased}}
}

func (e *EPC) sendToAF(ctx context.Context, m afMessage) {
	req := m.request.Message()
	ans, err := e.afs.Request(ctx, m.host, req)
	if err != nil {
		e.log.Warn("no answer from the AF", "af", m.host, "session_id", rx.SessionID(req), "error", err.Error())
		return
	}
	if ans.Header.CommandCode == rx.CommandReAuth {
		raa, err := rx.ParseReAuthAnswer(ans)
		if err != nil || !raa.Result.Success() {
			e.log.Warn("the AF refused the RAR", "af", m.host, "session_id", rx.SessionID(req), "result", raa.Result.String())
		}
	}
}
