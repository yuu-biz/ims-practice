// Package sh builds and reads the Sh messages between an application
// server, such as the IP-SM-GW, and the HSS (TS 29.328 for the
// procedures, TS 29.329 for the Diameter encoding). It mirrors package
// cx: each request and answer is a Go struct whose fields are the AVPs
// of its command format in TS 29.329 6.1, written in that order.
//
// The result of an answer and the routing AVPs of a request are those
// of package diameter, which the base protocol defines. TS 29.329 5
// applies the Diameter clarifications of TS 29.229 5 to Sh, and TS
// 29.329 6.2 makes the result codes of TS 29.229 valid on Sh, so an
// answer may also carry a Cx code of package cx.
package sh

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

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
	IMSI                DataReference = 32 // TS 29.328 7.6.24
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
	case IMSI:
		return "IMSI"
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

// begin writes the AVPs every Sh request starts with, up to and
// including Destination-Realm. NO_STATE_MAINTAINED is required on Sh as
// on Cx: TS 29.329 5 applies TS 29.229 5.
func begin(m *diam.Message, h diameter.Header) {
	h.AddSessionID(m)
	diameter.AddVendorSpecificApplicationID(m, diameter.ApplicationIDSh)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(diameter.AuthSessionStateNoStateMaintained))
	h.AddOrigin(m)
	h.AddDestinationHost(m)
	h.AddDestinationRealm(m)
}

// NewAnswer starts the answer to request: Session-Id, the
// Vendor-Specific-Application-Id, the result, Auth-Session-State and the
// origin of local, which is the order of every Sh answer format.
func NewAnswer(request *diam.Message, local diameter.Identity, result diameter.Result) *diam.Message {
	m := diameter.NewAnswer(request)
	diameter.AddVendorSpecificApplicationID(m, diameter.ApplicationIDSh)
	result.AddTo(m)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(diameter.AuthSessionStateNoStateMaintained))
	local.AddOrigin(m)
	return m
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
		children = append(children, diameter.VendorAVP(avpPublicIdentity, mv, datatype.UTF8String(u.PublicIdentity)))
	}
	if u.MSISDN != "" {
		children = append(children, diameter.VendorAVP(avpMSISDN, mv, datatype.OctetString(diameter.EncodeTBCD(u.MSISDN))))
	}
	return diameter.VendorAVP(avpUserIdentity, mv, &diam.GroupedAVP{AVP: children})
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
