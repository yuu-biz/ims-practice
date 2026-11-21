// Package sh builds and reads the Sh messages between an application
// server, such as the IP-SM-GW, and the HSS (TS 29.328 for the
// procedures, TS 29.329 for the Diameter encoding). It mirrors package
// cx: each request and answer is a Go struct whose fields are the AVPs
// of its command format in TS 29.329 6.1, written in that order.
//
// TS 29.329 5 applies the Diameter clarifications of TS 29.229 5 to Sh,
// and TS 29.329 6.2 makes the result codes of TS 29.229 valid on Sh,
// so this package reuses cx.Result and the Cx result codes.
package sh

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// Command codes, TS 29.329 Table 6.1.1.
//
// SIMPLIFIED: TS 29.328 6.1.3 and 6.1.4 (Sh-Subs-Notif and Sh-Notif,
// the SNR/SNA and PNR/PNA commands 308 and 309) are left out. The
// IP-SM-GW only registers itself and looks users up, and the MMTel AS
// reads its repository data for every initial request and keeps no
// copy, so nothing in this project subscribes to changes in the
// HSS. TS 29.364 8.1 recommends that an AS that reads the MMTel
// service data subscribes.
const (
	CommandUserData      uint32 = 306 // UDR/UDA
	CommandProfileUpdate uint32 = 307 // PUR/PUA
)

// Experimental-Result-Code values of TS 29.329 6.2 that the HSS stub
// answers. They travel inside Experimental-Result with Vendor-Id 10415.
// The Cx codes of TS 29.229 6.2, such as DIAMETER_ERROR_USER_UNKNOWN,
// also apply to Sh.
const (
	DiameterErrorOperationNotAllowed      uint32 = 5101 // 6.2.2.2
	DiameterErrorUserDataCannotBeRead     uint32 = 5102 // 6.2.2.3
	DiameterErrorUserDataCannotBeModified uint32 = 5103 // 6.2.2.4
	// DiameterErrorTransparentDataOutOfSync refuses an update of
	// repository data that is not based on the version the HSS has, or
	// that creates data that exists already.
	DiameterErrorTransparentDataOutOfSync uint32 = 5105 // 6.2.2.7
	// DiameterErrorTooMuchData is the code of TS 29.229 6.2.2.8, which
	// Sh uses for repository data larger than the HSS takes (TS 29.329
	// 6.2.2.6).
	DiameterErrorTooMuchData uint32 = 5008
)

// authSessionStateNoStateMaintained is required on Sh as on Cx: TS
// 29.329 5 applies TS 29.229 5 (RFC 6733 8.11).
const authSessionStateNoStateMaintained = 1

// DataReference is the Data-Reference AVP, TS 29.329 6.3.4: which part
// of the user data a request reads or updates. TS 29.328 Table 7.6.1
// lists what each one holds. Only the values the HSS stub serves are
// named.
type DataReference uint32

const (
	RepositoryData      DataReference = 0  // TS 29.328 7.6.1
	IMSPublicIdentity   DataReference = 10 // TS 29.328 7.6.2
	SCSCFName           DataReference = 12 // TS 29.328 7.6.4
	MSISDN              DataReference = 17 // TS 29.328 7.6.9
	SMSRegistrationInfo DataReference = 24 // TS 29.328 7.6.16
)

func (d DataReference) String() string {
	switch d {
	case RepositoryData:
		return "RepositoryData"
	case IMSPublicIdentity:
		return "IMSPublicIdentity"
	case SCSCFName:
		return "S-CSCFName"
	case MSISDN:
		return "MSISDN"
	case SMSRegistrationInfo:
		return "SMSRegistrationInfo"
	}
	return fmt.Sprintf("Data-Reference %d", uint32(d))
}

// IdentitySet is the Identity-Set AVP, TS 29.329 6.3.10: which public
// identities a read of IMSPublicIdentity returns (TS 29.328 7.6.2).
type IdentitySet uint32

const (
	AllIdentities        IdentitySet = 0
	RegisteredIdentities IdentitySet = 1
	ImplicitIdentities   IdentitySet = 2
	AliasIdentities      IdentitySet = 3
)

// Header is the routing part every Sh request starts with. It is the
// same as on Cx, so cx.NewHeader fills it in.
type Header = cx.Header

func newRequest(command uint32) *diam.Message {
	return diam.NewMessage(command, diam.RequestFlag|diam.ProxiableFlag, diameter.ApplicationIDSh, 0, 0, dict.Default)
}

// begin writes the AVPs every Sh request starts with, up to and
// including Destination-Realm.
func begin(m *diam.Message, h Header) {
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(h.SessionID))
	addVendorSpecificApplicationID(m)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(authSessionStateNoStateMaintained))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(h.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(h.OriginRealm))
	if h.DestinationHost != "" {
		m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(h.DestinationHost))
	}
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(h.DestinationRealm))
}

// addVendorSpecificApplicationID is kept even though RFC 6733 deprecated
// it, because the Sh command formats still require it (TS 29.329 6.1,
// NOTE).
func addVendorSpecificApplicationID(m *diam.Message) {
	m.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(diameter.VendorID3GPP)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(diameter.ApplicationIDSh)),
	}})
}

// NewAnswer starts the answer to request: Session-Id, the
// Vendor-Specific-Application-Id, the result, Auth-Session-State and the
// origin of local, which is the order of every Sh answer format.
func NewAnswer(request *diam.Message, local diameter.Identity, result cx.Result) *diam.Message {
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
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(authSessionStateNoStateMaintained))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(local.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(local.OriginRealm))
	return m
}

// requestHeader reads the routing AVPs every Sh request starts with.
type requestHeader struct {
	SessionID        string `avp:"Session-Id"`
	OriginHost       string `avp:"Origin-Host"`
	OriginRealm      string `avp:"Origin-Realm"`
	DestinationHost  string `avp:"Destination-Host,omitempty"`
	DestinationRealm string `avp:"Destination-Realm"`
}

func (h requestHeader) header() Header {
	return Header{
		SessionID:        h.SessionID,
		OriginHost:       h.OriginHost,
		OriginRealm:      h.OriginRealm,
		DestinationHost:  h.DestinationHost,
		DestinationRealm: h.DestinationRealm,
	}
}

// answerResult reads the result of an Sh answer from Result-Code or
// Experimental-Result.
type answerResult struct {
	ResultCode         *uint32 `avp:"Result-Code,omitempty"`
	ExperimentalResult *struct {
		Code uint32 `avp:"Experimental-Result-Code"`
	} `avp:"Experimental-Result,omitempty"`
}

func (a answerResult) result() (cx.Result, error) {
	switch {
	case a.ExperimentalResult != nil:
		return cx.Experimental(a.ExperimentalResult.Code), nil
	case a.ResultCode != nil:
		return cx.Base(*a.ResultCode), nil
	}
	return cx.Result{}, fmt.Errorf("answer has neither Result-Code nor Experimental-Result")
}

// AVP codes, TS 29.329 Table 6.3.1. All have Vendor-Id 10415. Server-Name
// and Public-Identity are the Cx AVPs of TS 29.229.
const (
	avpPublicIdentity    uint32 = 601
	avpServerName        uint32 = 602
	avpUserIdentity      uint32 = 700
	avpMSISDN            uint32 = 701
	avpUserData          uint32 = 702
	avpDataReference     uint32 = 703
	avpServiceIndication uint32 = 704
	avpIdentitySet       uint32 = 708
)

// The flags of TS 29.329 Table 6.3.1: most must set M and V; Identity-Set
// must set V and must not set M.
const (
	mv = avp.Mbit | avp.Vbit
	v  = avp.Vbit
)

func vendorAVP(code uint32, flags uint8, data datatype.Type) *diam.AVP {
	return diam.NewAVP(code, flags, diameter.VendorID3GPP, data)
}

// UserIdentity is the User-Identity AVP, TS 29.329 6.3.1: the user a
// request is about, named by exactly one of a public identity or an
// MSISDN (TS 29.328 7.1).
//
// SIMPLIFIED: TS 29.328 7.1.3. The External-Identifier, the third way
// to name a user, is left out: it is for machine-type communication,
// which this project does not serve.
type UserIdentity struct {
	PublicIdentity string
	// MSISDN is the E.164 number as digits only, without "+", such as
	// 12025550101. On the wire it is a TBCD string (TS 29.329 6.3.2).
	MSISDN string
}

func (u UserIdentity) avp() *diam.AVP {
	var children []*diam.AVP
	if u.PublicIdentity != "" {
		children = append(children, vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(u.PublicIdentity)))
	}
	if u.MSISDN != "" {
		children = append(children, vendorAVP(avpMSISDN, mv, datatype.OctetString(diameter.EncodeTBCD(u.MSISDN))))
	}
	return vendorAVP(avpUserIdentity, mv, &diam.GroupedAVP{AVP: children})
}

// userIdentityAVP reads a User-Identity AVP.
type userIdentityAVP struct {
	PublicIdentity string `avp:"Public-Identity,omitempty"`
	MSISDN         []byte `avp:"MSISDN,omitempty"`
}

func (u userIdentityAVP) userIdentity() (UserIdentity, error) {
	id := UserIdentity{PublicIdentity: u.PublicIdentity}
	if len(u.MSISDN) > 0 {
		digits, err := diameter.DecodeTBCD(u.MSISDN)
		if err != nil {
			return UserIdentity{}, fmt.Errorf("MSISDN: %w", err)
		}
		id.MSISDN = digits
	}
	return id, nil
}

// ValidMSISDN reports whether s can go into an MSISDN AVP: one to 15
// decimal digits, as an international E.164 number has at most 15.
func ValidMSISDN(s string) bool {
	if len(s) == 0 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
