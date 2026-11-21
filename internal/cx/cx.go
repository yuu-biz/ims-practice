// Package cx builds and reads the Cx messages between the I-CSCF, the
// S-CSCF and the HSS (TS 29.228 for the procedures, TS 29.229 for the
// Diameter encoding). Each request and answer is a Go struct whose
// fields are the AVPs of its command format in TS 29.229 6.1, and is
// written to the wire in that order.
package cx

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// Command codes, TS 29.229 Table 6.1.1.
const (
	CommandUserAuthorization       uint32 = 300 // UAR/UAA
	CommandServerAssignment        uint32 = 301 // SAR/SAA
	CommandLocationInfo            uint32 = 302 // LIR/LIA
	CommandMultimediaAuth          uint32 = 303 // MAR/MAA
	CommandRegistrationTermination uint32 = 304 // RTR/RTA
)

// Experimental-Result-Code values, TS 29.229 6.2. They travel inside
// Experimental-Result with Vendor-Id 10415, never in Result-Code, which
// is how 2001 here differs from DIAMETER_SUCCESS (RFC 6733 7.1.2).
const (
	DiameterFirstRegistration              uint32 = 2001 // 6.2.1.1
	DiameterSubsequentRegistration         uint32 = 2002 // 6.2.1.2
	DiameterUnregisteredService            uint32 = 2003 // 6.2.1.3
	DiameterSuccessServerNameNotStored     uint32 = 2004 // 6.2.1.4
	DiameterErrorUserUnknown               uint32 = 5001 // 6.2.2.1
	DiameterErrorIdentitiesDontMatch       uint32 = 5002 // 6.2.2.2
	DiameterErrorIdentityNotRegistered     uint32 = 5003 // 6.2.2.3
	DiameterErrorRoamingNotAllowed         uint32 = 5004 // 6.2.2.4
	DiameterErrorIdentityAlreadyRegistered uint32 = 5005 // 6.2.2.5
	DiameterErrorAuthSchemeNotSupported    uint32 = 5006 // 6.2.2.6
	DiameterErrorInAssignmentType          uint32 = 5007 // 6.2.2.7
)

// Base Result-Code values that TS 29.228 uses, RFC 6733 7.1.
const (
	DiameterSuccess                 uint32 = 2001 // 7.1.2
	DiameterAuthorizationRejected   uint32 = 5003 // 7.1.5
	DiameterAVPOccursTooManyTimes   uint32 = 5009 // 7.1.5
	DiameterUnableToComply          uint32 = 5012 // 7.1.5
	authSessionStateNoStateMaintain        = 1    // RFC 6733 8.11, required by TS 29.229 5.3
)

// UserAuthorizationType is the User-Authorization-Type AVP, TS 29.229
// 6.3.24.
type UserAuthorizationType uint32

const (
	Registration                UserAuthorizationType = 0
	DeRegistration              UserAuthorizationType = 1
	RegistrationAndCapabilities UserAuthorizationType = 2
)

// ServerAssignmentType is the Server-Assignment-Type AVP, TS 29.229
// 6.3.15. Only the values this project sends are named.
type ServerAssignmentType uint32

const (
	NoAssignment          ServerAssignmentType = 0
	AssignRegistration    ServerAssignmentType = 1
	ReRegistration        ServerAssignmentType = 2
	UnregisteredUser      ServerAssignmentType = 3
	TimeoutDeregistration ServerAssignmentType = 4
	UserDeregistration    ServerAssignmentType = 5
	// AdministrativeDeregistration is a deregistration that the S-CSCF
	// decided on itself (TS 24.229 5.4.1.5).
	AdministrativeDeregistration ServerAssignmentType = 8
	AuthenticationFailure        ServerAssignmentType = 9
	AuthenticationTimeout        ServerAssignmentType = 10
)

func (t ServerAssignmentType) String() string {
	switch t {
	case NoAssignment:
		return "NO_ASSIGNMENT"
	case AssignRegistration:
		return "REGISTRATION"
	case ReRegistration:
		return "RE_REGISTRATION"
	case UnregisteredUser:
		return "UNREGISTERED_USER"
	case TimeoutDeregistration:
		return "TIMEOUT_DEREGISTRATION"
	case UserDeregistration:
		return "USER_DEREGISTRATION"
	case AdministrativeDeregistration:
		return "ADMINISTRATIVE_DEREGISTRATION"
	case AuthenticationFailure:
		return "AUTHENTICATION_FAILURE"
	case AuthenticationTimeout:
		return "AUTHENTICATION_TIMEOUT"
	}
	return fmt.Sprintf("Server-Assignment-Type %d", uint32(t))
}

// User-Data-Already-Available, TS 29.229 6.3.26.
const (
	userDataNotAvailable     = 0
	userDataAlreadyAvailable = 1
)

// Authentication schemes in SIP-Authentication-Scheme, TS 29.229 6.3.9.
const (
	SchemeSIPDigest = "SIP Digest"
	// SchemeDigestAKAv1MD5 is IMS-AKA, whichever Digest AKA version the
	// S-CSCF and the UE use (TS 29.229 6.3.9 NOTE 1).
	SchemeDigestAKAv1MD5 = "Digest-AKAv1-MD5"
	// SchemeUnknown lets the HSS pick the scheme it has stored for the
	// user (TS 24.229 5.4.1.1, TS 29.228 6.3.1).
	SchemeUnknown = "Unknown"
)

// Result is the outcome of an answer: a base Result-Code, or an
// Experimental-Result-Code when Experimental is true.
type Result struct {
	Code         uint32
	Experimental bool
}

func (r Result) String() string {
	if r.Experimental {
		return fmt.Sprintf("Experimental-Result-Code %d", r.Code)
	}
	return fmt.Sprintf("Result-Code %d", r.Code)
}

// Success reports a 2xxx code of either kind.
func (r Result) Success() bool { return r.Code >= 2000 && r.Code < 3000 }

// Base returns a base protocol Result-Code.
func Base(code uint32) Result { return Result{Code: code} }

// Experimental returns a Cx Experimental-Result-Code.
func Experimental(code uint32) Result { return Result{Code: code, Experimental: true} }

// Header is the routing part every Cx request starts with (TS 29.229
// 6.1). DestinationHost is empty when the HSS is not yet known, which
// TS 29.229 5.5 allows.
type Header struct {
	SessionID        string
	OriginHost       string
	OriginRealm      string
	DestinationHost  string
	DestinationRealm string
}

// NewHeader fills in the identity of node and a new Session-Id.
func NewHeader(node *diameter.Node, destinationHost, destinationRealm string) Header {
	id := node.Identity()
	return Header{
		SessionID:        node.NewSessionID(),
		OriginHost:       id.OriginHost,
		OriginRealm:      id.OriginRealm,
		DestinationHost:  destinationHost,
		DestinationRealm: destinationRealm,
	}
}

func newRequest(command uint32) *diam.Message {
	return diam.NewMessage(command, diam.RequestFlag|diam.ProxiableFlag, diameter.ApplicationIDCx, 0, 0, dict.Default)
}

// begin writes the AVPs before the Destination ones: Session-Id,
// Vendor-Specific-Application-Id, Auth-Session-State, Origin-Host and
// Origin-Realm.
func (h Header) begin(m *diam.Message) {
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(h.SessionID))
	addVendorSpecificApplicationID(m)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(authSessionStateNoStateMaintain))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(h.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(h.OriginRealm))
}

func (h Header) destinationHost(m *diam.Message) {
	if h.DestinationHost != "" {
		m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(h.DestinationHost))
	}
}

func (h Header) destinationRealm(m *diam.Message) {
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(h.DestinationRealm))
}

// addVendorSpecificApplicationID is kept even though RFC 6733 deprecated
// it, because the Cx command formats still require it (TS 29.229 6.1,
// NOTE).
func addVendorSpecificApplicationID(m *diam.Message) {
	m.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(diameter.VendorID3GPP)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(diameter.ApplicationIDCx)),
	}})
}

// NewAnswer starts the answer to request: Session-Id, the
// Vendor-Specific-Application-Id, the result, Auth-Session-State and the
// origin of local, which is the order of every Cx answer format.
func NewAnswer(request *diam.Message, local diameter.Identity, result Result) *diam.Message {
	m := diam.NewMessage(request.Header.CommandCode, request.Header.CommandFlags&diam.ProxiableFlag,
		request.Header.ApplicationID, request.Header.HopByHopID, request.Header.EndToEndID, dict.Default)
	if sid, err := request.FindAVP(avp.SessionID, 0); err == nil {
		m.AddAVP(sid)
	}
	addVendorSpecificApplicationID(m)
	if result.Experimental {
		m.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(diameter.VendorID3GPP)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(result.Code)),
		}})
	} else {
		m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(result.Code))
	}
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(authSessionStateNoStateMaintain))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(local.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(local.OriginRealm))
	return m
}

// answerResult reads the result of a Cx answer from Result-Code or
// Experimental-Result.
type answerResult struct {
	ResultCode         *uint32 `avp:"Result-Code,omitempty"`
	ExperimentalResult *struct {
		Code uint32 `avp:"Experimental-Result-Code"`
	} `avp:"Experimental-Result,omitempty"`
}

func (a answerResult) result() (Result, error) {
	switch {
	case a.ExperimentalResult != nil:
		return Experimental(a.ExperimentalResult.Code), nil
	case a.ResultCode != nil:
		return Base(*a.ResultCode), nil
	}
	return Result{}, fmt.Errorf("answer has neither Result-Code nor Experimental-Result")
}

// AVP codes, TS 29.229 Table 6.3.0.1. The Vendor-Id is 10415 for all
// of them except the Digest ones, which are IETF AVPs (TS 29.229
// 6.3.37 to 6.3.41).
const (
	avpVisitedNetworkIdentifier uint32 = 600
	avpPublicIdentity           uint32 = 601
	avpServerName               uint32 = 602
	avpServerCapabilities       uint32 = 603
	avpMandatoryCapability      uint32 = 604
	avpOptionalCapability       uint32 = 605
	avpUserData                 uint32 = 606
	avpSIPNumberAuthItems       uint32 = 607
	avpSIPAuthenticationScheme  uint32 = 608
	avpSIPAuthenticate          uint32 = 609
	avpSIPAuthorization         uint32 = 610
	avpSIPAuthDataItem          uint32 = 612
	avpSIPItemNumber            uint32 = 613
	avpServerAssignmentType     uint32 = 614
	avpDeregistrationReason     uint32 = 615
	avpReasonCode               uint32 = 616
	avpReasonInfo               uint32 = 617
	avpUserAuthorizationType    uint32 = 623
	avpUserDataAlreadyAvailable uint32 = 624
	avpConfidentialityKey       uint32 = 625
	avpIntegrityKey             uint32 = 626
	avpSIPDigestAuthenticate    uint32 = 635
	avpDigestRealm              uint32 = 104
	avpDigestQoP                uint32 = 110
	avpDigestAlgorithm          uint32 = 111
	avpDigestHA1                uint32 = 121
)

// The flags of the Cx AVPs, TS 29.229 Table 6.3.0.1: most must set M
// and V; a few must set V and must not set M.
const (
	mv = avp.Mbit | avp.Vbit
	v  = avp.Vbit
)

func vendorAVP(code uint32, flags uint8, data datatype.Type) *diam.AVP {
	return diam.NewAVP(code, flags, diameter.VendorID3GPP, data)
}
