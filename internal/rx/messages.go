package rx

import (
	"fmt"
	"net/netip"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

// AARequest is the AAR of TS 29.214 5.6.1. The AF opens an Rx session
// with it, or updates the service information of one (4.4.1, 4.4.2,
// 4.4.5).
type AARequest struct {
	diameter.Header
	// AFApplicationIdentifier is the ICSI of an IMS session (TS 29.214
	// A.1). Empty leaves it out.
	AFApplicationIdentifier string
	MediaComponents         []policy.MediaComponent
	// ServiceInfoStatus is nil when not supplied, which the PCRF takes as
	// final (TS 29.214 5.3.25).
	ServiceInfoStatus *ServiceInfoStatus
	SpecificActions   []SpecificAction
	// FramedIPAddress is the UE's address, by which the PCRF binds the Rx
	// session to an IP-CAN session (TS 29.214 4.4.1). The zero Addr leaves
	// it out.
	FramedIPAddress netip.Addr
	RxRequestType   *RxRequestType
}

func (r AARequest) Message() *diam.Message {
	m := diameter.NewRequest(diameter.ApplicationIDRx, CommandAA)
	r.Header.AddSessionID(m)
	authApplicationID(m)
	r.Header.AddOrigin(m)
	r.Header.AddDestinationRealm(m)
	r.Header.AddDestinationHost(m)
	if r.AFApplicationIdentifier != "" {
		m.AddAVP(diameter.VendorAVP(avpAFApplicationIdentifier, mv, datatype.OctetString(r.AFApplicationIdentifier)))
	}
	for _, c := range r.MediaComponents {
		m.AddAVP(mediaComponentDescription(c))
	}
	if r.ServiceInfoStatus != nil {
		m.AddAVP(diameter.VendorAVP(avpServiceInfoStatus, mv, datatype.Enumerated(*r.ServiceInfoStatus)))
	}
	for _, a := range r.SpecificActions {
		m.AddAVP(diameter.VendorAVP(avpSpecificAction, mv, datatype.Enumerated(a)))
	}
	if r.FramedIPAddress.IsValid() {
		m.AddAVP(framedIPAddress(r.FramedIPAddress))
	}
	if r.RxRequestType != nil {
		m.AddAVP(diameter.VendorAVP(avpRxRequestType, v, datatype.Enumerated(*r.RxRequestType)))
	}
	return m
}

// ParseAARequest reads an AAR. Errors wrap ErrFilterRestrictions or
// ErrInvalidServiceInformation when the service information is at
// fault.
func ParseAARequest(m *diam.Message) (AARequest, error) {
	var raw struct {
		diameter.Header
		AFApplicationIdentifier []byte  `avp:"AF-Application-Identifier,omitempty"`
		ServiceInfoStatus       *int32  `avp:"Service-Info-Status,omitempty"`
		SpecificActions         []int32 `avp:"Specific-Action,omitempty"`
		FramedIPAddress         []byte  `avp:"Framed-IP-Address,omitempty"`
		RxRequestType           *int32  `avp:"Rx-Request-Type,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return AARequest{}, fmt.Errorf("AAR: %w", err)
	}
	r := AARequest{Header: raw.Header, AFApplicationIdentifier: string(raw.AFApplicationIdentifier)}
	for _, a := range m.AVP {
		if a.Code != avpMediaComponentDescription || a.VendorID != diameter.VendorID3GPP {
			continue
		}
		c, err := parseMediaComponentDescription(a)
		if err != nil {
			return AARequest{}, fmt.Errorf("AAR: %w", err)
		}
		r.MediaComponents = append(r.MediaComponents, c)
	}
	if raw.ServiceInfoStatus != nil {
		s := ServiceInfoStatus(*raw.ServiceInfoStatus)
		r.ServiceInfoStatus = &s
	}
	for _, a := range raw.SpecificActions {
		r.SpecificActions = append(r.SpecificActions, SpecificAction(a))
	}
	if raw.FramedIPAddress != nil {
		addr, err := parseFramedIPAddress(raw.FramedIPAddress)
		if err != nil {
			return AARequest{}, fmt.Errorf("AAR: %w", err)
		}
		r.FramedIPAddress = addr
	}
	if raw.RxRequestType != nil {
		t := RxRequestType(*raw.RxRequestType)
		r.RxRequestType = &t
	}
	return r, nil
}

// AAAnswer is the AAA of TS 29.214 5.6.2.
type AAAnswer struct {
	Result diameter.Result
}

func (a AAAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return newAnswer(request, local, a.Result, true)
}

func ParseAAAnswer(m *diam.Message) (AAAnswer, error) {
	result, err := readResult(m)
	if err != nil {
		return AAAnswer{}, fmt.Errorf("AAA: %w", err)
	}
	return AAAnswer{Result: result}, nil
}

// ReAuthRequest is the RAR of TS 29.214 5.6.3. The PCRF reports with it
// an event the AF subscribed to (4.4.6). Destination-Host is required.
type ReAuthRequest struct {
	diameter.Header
	SpecificActions []SpecificAction
	// Flows names the IP flows the event is about.
	Flows      []Flows
	AbortCause *AbortCause
}

func (r ReAuthRequest) Message() *diam.Message {
	m := diameter.NewRequest(diameter.ApplicationIDRx, CommandReAuth)
	r.Header.AddSessionID(m)
	r.Header.AddOrigin(m)
	r.Header.AddDestinationRealm(m)
	r.Header.AddDestinationHost(m)
	authApplicationID(m)
	for _, a := range r.SpecificActions {
		m.AddAVP(diameter.VendorAVP(avpSpecificAction, mv, datatype.Enumerated(a)))
	}
	for _, f := range r.Flows {
		m.AddAVP(f.avp())
	}
	if r.AbortCause != nil {
		m.AddAVP(diameter.VendorAVP(avpAbortCause, mv, datatype.Enumerated(*r.AbortCause)))
	}
	return m
}

func ParseReAuthRequest(m *diam.Message) (ReAuthRequest, error) {
	var raw struct {
		diameter.Header
		SpecificActions []int32 `avp:"Specific-Action"`
		AbortCause      *int32  `avp:"Abort-Cause,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ReAuthRequest{}, fmt.Errorf("RAR: %w", err)
	}
	r := ReAuthRequest{Header: raw.Header}
	for _, a := range raw.SpecificActions {
		r.SpecificActions = append(r.SpecificActions, SpecificAction(a))
	}
	for _, a := range m.AVP {
		if a.Code != avpFlows || a.VendorID != diameter.VendorID3GPP {
			continue
		}
		f, err := parseFlows(a)
		if err != nil {
			return ReAuthRequest{}, fmt.Errorf("RAR: %w", err)
		}
		r.Flows = append(r.Flows, f)
	}
	if raw.AbortCause != nil {
		c := AbortCause(*raw.AbortCause)
		r.AbortCause = &c
	}
	return r, nil
}

// ReAuthAnswer is the RAA of TS 29.214 5.6.4.
type ReAuthAnswer struct {
	Result diameter.Result
}

func (a ReAuthAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return newAnswer(request, local, a.Result, false)
}

func ParseReAuthAnswer(m *diam.Message) (ReAuthAnswer, error) {
	result, err := readResult(m)
	if err != nil {
		return ReAuthAnswer{}, fmt.Errorf("RAA: %w", err)
	}
	return ReAuthAnswer{Result: result}, nil
}

// SessionTerminationRequest is the STR of TS 29.214 5.6.5. The AF ends
// an Rx session with it (4.4.4).
type SessionTerminationRequest struct {
	diameter.Header
	TerminationCause TerminationCause
}

func (r SessionTerminationRequest) Message() *diam.Message {
	m := diameter.NewRequest(diameter.ApplicationIDRx, CommandSessionTermination)
	r.Header.AddSessionID(m)
	r.Header.AddOrigin(m)
	r.Header.AddDestinationRealm(m)
	authApplicationID(m)
	m.NewAVP(avp.TerminationCause, avp.Mbit, 0, datatype.Enumerated(r.TerminationCause))
	r.Header.AddDestinationHost(m)
	return m
}

func ParseSessionTerminationRequest(m *diam.Message) (SessionTerminationRequest, error) {
	var raw struct {
		diameter.Header
		TerminationCause int32 `avp:"Termination-Cause"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return SessionTerminationRequest{}, fmt.Errorf("STR: %w", err)
	}
	return SessionTerminationRequest{Header: raw.Header, TerminationCause: TerminationCause(raw.TerminationCause)}, nil
}

// SessionTerminationAnswer is the STA of TS 29.214 5.6.6. Its result is
// a Result-Code; the format has no Experimental-Result.
type SessionTerminationAnswer struct {
	Result diameter.Result
}

func (a SessionTerminationAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return newAnswer(request, local, a.Result, false)
}

func ParseSessionTerminationAnswer(m *diam.Message) (SessionTerminationAnswer, error) {
	result, err := readResult(m)
	if err != nil {
		return SessionTerminationAnswer{}, fmt.Errorf("STA: %w", err)
	}
	return SessionTerminationAnswer{Result: result}, nil
}

// AbortSessionRequest is the ASR of TS 29.214 5.6.7. The PCRF tells the
// AF with it that the bearer of an Rx session is gone (4.4.6.1,
// 4.4.6.2). Destination-Host is required.
type AbortSessionRequest struct {
	diameter.Header
	AbortCause AbortCause
}

func (r AbortSessionRequest) Message() *diam.Message {
	m := diameter.NewRequest(diameter.ApplicationIDRx, CommandAbortSession)
	r.Header.AddSessionID(m)
	r.Header.AddOrigin(m)
	r.Header.AddDestinationRealm(m)
	r.Header.AddDestinationHost(m)
	authApplicationID(m)
	m.AddAVP(diameter.VendorAVP(avpAbortCause, mv, datatype.Enumerated(r.AbortCause)))
	return m
}

func ParseAbortSessionRequest(m *diam.Message) (AbortSessionRequest, error) {
	var raw struct {
		diameter.Header
		AbortCause int32 `avp:"Abort-Cause"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return AbortSessionRequest{}, fmt.Errorf("ASR: %w", err)
	}
	return AbortSessionRequest{Header: raw.Header, AbortCause: AbortCause(raw.AbortCause)}, nil
}

// AbortSessionAnswer is the ASA of TS 29.214 5.6.8. Its result is a
// Result-Code (RFC 6733 8.5.2).
type AbortSessionAnswer struct {
	Result diameter.Result
}

func (a AbortSessionAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return newAnswer(request, local, a.Result, false)
}

func ParseAbortSessionAnswer(m *diam.Message) (AbortSessionAnswer, error) {
	result, err := readResult(m)
	if err != nil {
		return AbortSessionAnswer{}, fmt.Errorf("ASA: %w", err)
	}
	return AbortSessionAnswer{Result: result}, nil
}
