// Package rx builds and reads the Rx messages between an AF, the
// P-CSCF, and the PCRF (TS 29.214). Like packages cx and sh, each
// request and answer is a Go struct whose fields are the AVPs of its
// command format in TS 29.214 5.6, written in that order. The service
// information is the protocol-neutral one of package policy; this
// package turns it into Media-Component-Description AVPs and back.
//
// Rx is in go-diameter's default dictionary, so the names of its
// commands and AVPs are known without a dictionary of this project.
//
// The result of an answer, the routing AVPs of a request and the 3GPP
// Vendor-Id are those of package diameter: the base protocol defines
// them, not Rx.
package rx

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// Command codes, TS 29.214 5.6. Rx reuses the commands of the base
// protocol and of NASREQ under its own Application-ID (5.6.0).
const (
	CommandAA                 uint32 = 265 // AAR/AAA
	CommandReAuth             uint32 = 258 // RAR/RAA
	CommandSessionTermination uint32 = 275 // STR/STA
	CommandAbortSession       uint32 = 274 // ASR/ASA
)

// Experimental-Result-Code values of TS 29.214 5.5.1. They travel inside
// Experimental-Result with Vendor-Id 10415.
const (
	InvalidServiceInformation     uint32 = 5061
	FilterRestrictions            uint32 = 5062
	RequestedServiceNotAuthorized uint32 = 5063
	DuplicatedAFSession           uint32 = 5064
	IPCANSessionNotAvailable      uint32 = 5065
)

// SpecificAction is the Specific-Action AVP (TS 29.214 5.3.13): in an
// AAR, an event the AF wants to hear about; in a RAR, the event that
// happened. Only the values this project uses are named.
type SpecificAction uint32

const (
	IndicationOfLossOfBearer                  SpecificAction = 2
	IndicationOfReleaseOfBearer               SpecificAction = 4
	IndicationOfSuccessfulResourcesAllocation SpecificAction = 8
	IndicationOfFailedResourcesAllocation     SpecificAction = 9
)

func (a SpecificAction) String() string {
	switch a {
	case IndicationOfLossOfBearer:
		return "INDICATION_OF_LOSS_OF_BEARER"
	case IndicationOfReleaseOfBearer:
		return "INDICATION_OF_RELEASE_OF_BEARER"
	case IndicationOfSuccessfulResourcesAllocation:
		return "INDICATION_OF_SUCCESSFUL_RESOURCES_ALLOCATION"
	case IndicationOfFailedResourcesAllocation:
		return "INDICATION_OF_FAILED_RESOURCES_ALLOCATION"
	}
	return fmt.Sprintf("Specific-Action %d", uint32(a))
}

// ServiceInfoStatus is the Service-Info-Status AVP (TS 29.214 5.3.25).
// Without it the PCRF takes the service information as final.
type ServiceInfoStatus uint32

const (
	FinalServiceInformation       ServiceInfoStatus = 0
	PreliminaryServiceInformation ServiceInfoStatus = 1
)

// RxRequestType is the Rx-Request-Type AVP (TS 29.214 5.3.31).
type RxRequestType uint32

const (
	InitialRequest RxRequestType = 0
	UpdateRequest  RxRequestType = 1
)

// AbortCause is the Abort-Cause AVP (TS 29.214 5.3.1).
type AbortCause uint32

const (
	// BearerReleased: the bearer was released, or every resource of the
	// AF session failed.
	BearerReleased              AbortCause = 0
	InsufficientServerResources AbortCause = 1
	InsufficientBearerResources AbortCause = 2
)

// TerminationCause is the Termination-Cause AVP of an STR (RFC 6733
// 8.15; the values are in the IANA registry of Termination-Cause AVP
// values).
type TerminationCause uint32

const (
	// DiameterLogout: the AF ends the session itself, as when a call
	// ends.
	DiameterLogout TerminationCause = 1
	// DiameterAdministrative: the session ends because the PCRF aborted
	// it with an ASR.
	DiameterAdministrative TerminationCause = 4
)

// AVP codes, TS 29.214 Table 5.3.0.1. All have Vendor-Id 10415.
const (
	avpAbortCause                uint32 = 500
	avpAFApplicationIdentifier   uint32 = 504
	avpCodecData                 uint32 = 524
	avpFlowDescription           uint32 = 507
	avpFlowNumber                uint32 = 509
	avpFlows                     uint32 = 510
	avpFlowStatus                uint32 = 511
	avpFlowUsage                 uint32 = 512
	avpSpecificAction            uint32 = 513
	avpMaxRequestedBandwidthDL   uint32 = 515
	avpMaxRequestedBandwidthUL   uint32 = 516
	avpMediaComponentDescription uint32 = 517
	avpMediaComponentNumber      uint32 = 518
	avpMediaSubComponent         uint32 = 519
	avpMediaType                 uint32 = 520
	avpRRBandwidth               uint32 = 521
	avpRSBandwidth               uint32 = 522
	avpServiceInfoStatus         uint32 = 527
	avpRxRequestType             uint32 = 533
)

// Framed-IP-Address and Framed-IPv6-Prefix come from NASREQ (TS 29.214
// Table 5.4.0.1); they have no vendor and must set M (RFC 7155
// 4.4.10.5.1, 4.4.10.5.6).
const (
	avpFramedIPAddress  uint32 = 8
	avpFramedIPv6Prefix uint32 = 97
)

// The flags of TS 29.214 Table 5.3.0.1: most Rx AVPs must set M and V;
// Rx-Request-Type must set V and must not set M.
const (
	mv = avp.Mbit | avp.Vbit
	v  = avp.Vbit
)

// Errors of the parse functions that a PCRF answers with an Rx result
// code (TS 29.214 5.5.1).
var (
	// ErrFilterRestrictions: a Flow-Description breaks the rules of TS
	// 29.214 5.3.8.
	ErrFilterRestrictions = errors.New("rx: Flow-Description outside the restrictions of TS 29.214 5.3.8")
	// ErrInvalidServiceInformation: a Media-Component-Description cannot
	// be read.
	ErrInvalidServiceInformation = errors.New("rx: invalid service information")
)

func authApplicationID(m *diam.Message) {
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(diameter.ApplicationIDRx))
}

// newAnswer starts the answer to request with Session-Id, the
// Auth-Application-Id when the answer format has it (only the AAA), the
// origin of local, and the result.
func newAnswer(request *diam.Message, local diameter.Identity, result diameter.Result, withApplicationID bool) *diam.Message {
	m := diameter.NewAnswer(request)
	if withApplicationID {
		authApplicationID(m)
	}
	local.AddOrigin(m)
	result.AddTo(m)
	return m
}

// readResult reads the result of an answer from Result-Code or
// Experimental-Result.
func readResult(m *diam.Message) (diameter.Result, error) {
	var raw diameter.ResultAVPs
	if err := m.Unmarshal(&raw); err != nil {
		return diameter.Result{}, err
	}
	return raw.Result()
}

// SessionID returns the Session-Id of an Rx message, or "" without one.
func SessionID(m *diam.Message) string {
	a, err := m.FindAVP(avp.SessionID, 0)
	if err != nil {
		return ""
	}
	s, _ := a.Data.(datatype.UTF8String)
	return string(s)
}

// ueAddressAVP names the UE by its full address, as TS 29.214 4.4.1
// asks: an IPv4 address in Framed-IP-Address, its four octets (RFC 7155
// 4.4.10.5.1), and an IPv6 address in Framed-IPv6-Prefix as a prefix of
// 128 bits. Framed-IPv6-Prefix keeps the layout of the RADIUS attribute
// after its type and length (RFC 7155 4.4.10.5.6, RFC 3162 2.3): a
// reserved octet of zero, the prefix length in bits, and the prefix.
func ueAddressAVP(a netip.Addr) *diam.AVP {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return diam.NewAVP(avpFramedIPAddress, avp.Mbit, 0, datatype.OctetString(b[:]))
	}
	b := a.As16()
	return diam.NewAVP(avpFramedIPv6Prefix, avp.Mbit, 0, datatype.OctetString(append([]byte{0, 128}, b[:]...)))
}

func parseFramedIPAddress(b []byte) (netip.Addr, error) {
	a, ok := netip.AddrFromSlice(b)
	if !ok || !a.Is4() {
		return netip.Addr{}, fmt.Errorf("Framed-IP-Address % x is not an IPv4 address", b)
	}
	return a, nil
}

// parseFramedIPv6Prefix reads the full IPv6 address that an AF puts in
// Framed-IPv6-Prefix (TS 29.214 Table 5.4.0.1): a prefix of 128 bits.
func parseFramedIPv6Prefix(b []byte) (netip.Addr, error) {
	if len(b) != 2+net.IPv6len || b[1] != 128 {
		return netip.Addr{}, fmt.Errorf("Framed-IPv6-Prefix % x is not a full IPv6 address", b)
	}
	return netip.AddrFrom16([16]byte(b[2:])), nil
}
