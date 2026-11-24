// Package sgd builds and reads the Diameter messages of TS 29.338, which
// move a short message between the SMS central functions and the nodes
// that serve a UE:
//
//   - SGd: MO-Forward-Short-Message (OFR/OFA) from an MME, an SMSF or an
//     IP-SM-GW to the SC, and MT-Forward-Short-Message (TFR/TFA) from
//     the SC to one of them (TS 29.338 6);
//   - S6c: Send-Routing-Info-for-SM (SRR/SRA), with which the SC asks
//     the HSS which node serves a user, Report-SM-Delivery-Status
//     (RDR/RDA), with which the SC has the HSS keep its address in the
//     message waiting data of a user it could not reach, and
//     Alert-Service-Centre (ALR/ALA), with which the HSS tells the SC
//     that such a user is reachable again (TS 29.338 5).
//
// "SC" stands here for the SMS-SC together with the SMS-IWMSC and the
// SMS-GMSC, which TS 23.040 keeps apart and which this project runs as
// one role.
//
// Each request and answer is a Go struct whose fields are AVPs of its
// command format in TS 29.338 5.3.2 and 6.3.2, written to the wire in
// the order of that format. The short message itself crosses in the
// SM-RP-UI AVP, which holds a TPDU of TS 23.040 such as SMS-SUBMIT or
// SMS-DELIVER. That is the RP-User-Data of the RP layer, not a whole RP
// message: the addresses that an RP-DATA carries beside the TPDU travel
// in AVPs of their own (SC-Address, User-Identifier, User-Name).
//
// SIMPLIFIED: TS 29.338 5.3.2 and 6.3.2. Of the optional AVPs only those
// named in the structs of this package are written and read. Left out
// on SGd are DRMP and MPS-Priority (priority services),
// SMSMI-Correlation-ID and SM-Delivery-Outcome (SMS without MSISDN),
// Supported-Features (SGd defines no feature), EPS-Location-Information
// and NR-Cell-Global-Identity (the cell of the UE), OFR-Flags and
// SGSN-Number (SGSN), External-Identifier (machine-type communication),
// TFR-Flags, SM-Delivery-Timer, SM-Delivery-Start-Time,
// Maximum-Retransmission-Time, Requested-Retransmission-Time,
// SMS-GMSC-Address and the User-Identifier of the TFA (queueing and
// retransmission in the SC), and Absent-User-Diagnostic-SM. Left out on
// S6c are DRMP and MPS-Priority (priority services), SMSMI-Correlation-ID
// and User-Name in the request (a user without MSISDN), SM-RP-MTI and
// SM-RP-SMEA (the RP message type and the originating SME address of the
// message), the SRR-Flags bits other than
// Single-Attempt-Delivery (two serving nodes, delivery despite message
// waiting data), SM-Delivery-Not-Intended (a query that delivers
// nothing), Additional-Serving-Node and
// SMSF-Non-3GPP-Address (a second serving node, non-3GPP access), LMSI
// (the VLR), the User-Identifier of the SRA (the MSISDN that the HSS
// stores), and MWD-Status and the absent user diagnostics (what the HSS
// has stored about earlier deliveries that failed). The SC of package
// smsc, which uses this package, sends none of these AVPs and acts on
// none of them, and so does the HSS of package hss on its side of S6c.
//
// Three roles speak these interfaces with this package: the sc role as
// the SMS central functions on both of them, the hss role as the other
// end of S6c, and the ipsmgw role as a serving node of SGd.
//
// SIMPLIFIED: TS 29.338 4.6. S6c and SGd run over TCP here, as every
// Diameter connection of this project does, and not over the SCTP that
// the clause requires of these two interfaces. RFC 6733 2.1 lets the
// base protocol run over either, and TCP works on every machine that
// runs the containers of this project, which SCTP does not.
package sgd

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// ApplicationIDSGd is the application identifier of SGd, TS 29.338 4.8.
// SGd is a vendor specific application of 3GPP, whose IANA enterprise
// number is diameter.VendorID3GPP.
const ApplicationIDSGd uint32 = 16777313

// Command codes of SGd, TS 29.338 Table 6.3.2.2/1.
const (
	CommandMOForwardShortMessage uint32 = 8388645 // OFR/OFA
	CommandMTForwardShortMessage uint32 = 8388646 // TFR/TFA
)

// Experimental-Result-Code values, TS 29.338 7.3. They travel inside
// Experimental-Result with Vendor-Id 10415, never in Result-Code. TS
// 29.338 7.2 uses the base Result-Code values of RFC 6733 as they are,
// so a successful answer carries diameter.DiameterSuccess.
const (
	DiameterErrorUserUnknown          uint32 = 5001 // 7.3.2
	DiameterErrorAbsentUser           uint32 = 5550 // 7.3.3
	DiameterErrorUserBusyForMTSMS     uint32 = 5551 // 7.3.4
	DiameterErrorFacilityNotSupported uint32 = 5552 // 7.3.5
	DiameterErrorIllegalUser          uint32 = 5553 // 7.3.6
	DiameterErrorIllegalEquipment     uint32 = 5554 // 7.3.7
	DiameterErrorSMDeliveryFailure    uint32 = 5555 // 7.3.8
	DiameterErrorServiceNotSubscribed uint32 = 5556 // 7.3.9
	DiameterErrorServiceBarred        uint32 = 5557 // 7.3.10
	DiameterErrorMWDListFull          uint32 = 5558 // 7.3.11
)

// begin writes the AVPs every request starts with, up to and including
// Destination-Realm. Unlike Cx and Sh, the commands of TS 29.338 carry
// no Vendor-Specific-Application-Id: 5.3.2.2 and 6.3.2.2 forbid a
// sender to include it. NO_STATE_MAINTAINED is what 4.5 asks for: the
// sessions end implicitly, so that no node keeps session state.
//
// Destination-Host may be empty where the command format makes it
// optional: in an OFR, when the sender knows only the realm of the SC
// (6.3.1.1), and in an SRR, when it does not know which HSS holds the
// user (5.3.1.1.3). A TFR always names the serving node (6.3.1.2).
func begin(m *diam.Message, h diameter.Header) {
	h.AddSessionID(m)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(diameter.AuthSessionStateNoStateMaintained))
	h.AddOrigin(m)
	h.AddDestinationHost(m)
	h.AddDestinationRealm(m)
}

// newAnswer starts the answer to request: Session-Id, the result,
// Auth-Session-State and the origin of local, which is the order of
// every answer format of TS 29.338.
func newAnswer(request *diam.Message, local diameter.Identity, result diameter.Result) *diam.Message {
	m := diameter.NewAnswer(request)
	result.AddTo(m)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(diameter.AuthSessionStateNoStateMaintained))
	local.AddOrigin(m)
	return m
}

// AVP codes of SGd. All have Vendor-Id 10415.
const (
	avpMSISDN                           uint32 = 701  // TS 29.329 6.3.2
	avpMMENumberForMTSMS                uint32 = 1645 // TS 29.272 7.3.159
	avpUserIdentifier                   uint32 = 3102 // TS 29.336 6.4.2
	avpSCAddress                        uint32 = 3300 // TS 29.338 6.3.3.2
	avpSMRPUI                           uint32 = 3301 // TS 29.338 6.3.3.3
	avpSMDeliveryFailureCause           uint32 = 3303 // TS 29.338 6.3.3.5
	avpSMEnumeratedDeliveryFailureCause uint32 = 3304 // TS 29.338 6.3.3.6
	avpSMDiagnosticInfo                 uint32 = 3305 // TS 29.338 6.3.3.7
)

// The flags of the AVPs: most must set M and V; a few must set V and
// must not set M. Each use below names the table it follows.
const (
	mv = avp.Mbit | avp.Vbit
	v  = avp.Vbit
)

// MaxSMRPUI is the largest TPDU that SM-RP-UI holds, in octets (TS
// 29.338 6.3.3.3).
const MaxSMRPUI = 200

// ValidE164 reports whether digits can go into one of the AVPs that
// hold an E.164 number as a TBCD-string (SC-Address, MSISDN,
// MME-Number-for-MT-SMS and the like): one to 15 decimal digits, as an
// international number has at most 15, without "+".
func ValidE164(digits string) bool {
	if len(digits) == 0 || len(digits) > 15 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// encodeTBCD is diameter.EncodeTBCD as an AVP value. TS 29.338 6.3.3.2
// asks for SC-Address what TS 29.329 6.3.2 asks for MSISDN, with no
// octet for the nature of address or the numbering plan. The digits
// must be decimal, as ValidE164 checks.
func encodeTBCD(digits string) datatype.OctetString {
	return datatype.OctetString(diameter.EncodeTBCD(digits))
}

// decodeTBCD is the reverse of encodeTBCD. An AVP that is absent, and
// one that holds no octet, both give an empty string, which the parse
// functions below read as "not supplied".
func decodeTBCD(b []byte) (string, error) {
	if len(b) == 0 {
		return "", nil
	}
	return diameter.DecodeTBCD(b)
}

// UserIdentifier is the User-Identifier AVP, TS 29.336 6.4.2: the
// identities of one user. At least one must be set, and the MSISDN must
// pass ValidE164.
//
// SIMPLIFIED: TS 29.336 6.4.2. The External-Identifier and the LMSI are
// left out: they belong to machine-type communication and to the VLR,
// neither of which this project has.
type UserIdentifier struct {
	// UserName is the IMSI.
	UserName string
	// MSISDN is the E.164 number as digits only, without "+", such as
	// 12025550112. On the wire it is a TBCD-string (TS 29.329 6.3.2).
	MSISDN string
}

func (u UserIdentifier) avp() *diam.AVP {
	var children []*diam.AVP
	if u.UserName != "" {
		children = append(children, diam.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(u.UserName)))
	}
	if u.MSISDN != "" {
		children = append(children, diameter.VendorAVP(avpMSISDN, mv, encodeTBCD(u.MSISDN)))
	}
	// M and V: TS 29.336 Table 6.4.1/1, and TS 29.329 Table 6.3.1 for
	// the MSISDN.
	return diameter.VendorAVP(avpUserIdentifier, mv, &diam.GroupedAVP{AVP: children})
}

// userIdentifierAVP reads a User-Identifier AVP.
type userIdentifierAVP struct {
	UserName string `avp:"User-Name,omitempty"`
	MSISDN   []byte `avp:"MSISDN,omitempty"`
}

func (u userIdentifierAVP) userIdentifier() (UserIdentifier, error) {
	msisdn, err := decodeTBCD(u.MSISDN)
	if err != nil {
		return UserIdentifier{}, fmt.Errorf("MSISDN: %w", err)
	}
	if u.UserName == "" && msisdn == "" {
		return UserIdentifier{}, fmt.Errorf("it holds no identity")
	}
	return UserIdentifier{UserName: u.UserName, MSISDN: msisdn}, nil
}

// SMEnumeratedDeliveryFailureCause is the
// SM-Enumerated-Delivery-Failure-Cause AVP, TS 29.338 6.3.3.6. The first
// three values come from a serving node in a TFA, the others from the SC
// in an OFA (6.2.1.1, 6.2.2.1).
type SMEnumeratedDeliveryFailureCause uint32

const (
	MemoryCapacityExceeded SMEnumeratedDeliveryFailureCause = 0
	EquipmentProtocolError SMEnumeratedDeliveryFailureCause = 1
	EquipmentNotSMEquipped SMEnumeratedDeliveryFailureCause = 2
	UnknownServiceCentre   SMEnumeratedDeliveryFailureCause = 3
	SCCongestion           SMEnumeratedDeliveryFailureCause = 4
	InvalidSMEAddress      SMEnumeratedDeliveryFailureCause = 5
	UserNotSCUser          SMEnumeratedDeliveryFailureCause = 6
)

func (c SMEnumeratedDeliveryFailureCause) String() string {
	switch c {
	case MemoryCapacityExceeded:
		return "MEMORY_CAPACITY_EXCEEDED"
	case EquipmentProtocolError:
		return "EQUIPMENT_PROTOCOL_ERROR"
	case EquipmentNotSMEquipped:
		return "EQUIPMENT_NOT_SM-EQUIPPED"
	case UnknownServiceCentre:
		return "UNKNOWN_SERVICE_CENTRE"
	case SCCongestion:
		return "SC-CONGESTION"
	case InvalidSMEAddress:
		return "INVALID_SME-ADDRESS"
	case UserNotSCUser:
		return "USER_NOT_SC-USER"
	}
	return fmt.Sprintf("SM-Enumerated-Delivery-Failure-Cause %d", uint32(c))
}

// SMDeliveryFailureCause is the SM-Delivery-Failure-Cause AVP, TS 29.338
// 6.3.3.5. An answer whose result is DIAMETER_ERROR_SM_DELIVERY_FAILURE
// carries it (6.2.1.1, 6.2.2.1).
type SMDeliveryFailureCause struct {
	SMEnumeratedDeliveryFailureCause SMEnumeratedDeliveryFailureCause
	// SMDiagnosticInfo is free information that goes with the cause
	// (6.3.3.7), or nil.
	SMDiagnosticInfo []byte
}

func (c SMDeliveryFailureCause) avp() *diam.AVP {
	// M and V for all three: TS 29.338 Table 6.3.3.1/1.
	children := []*diam.AVP{
		diameter.VendorAVP(avpSMEnumeratedDeliveryFailureCause, mv, datatype.Enumerated(c.SMEnumeratedDeliveryFailureCause)),
	}
	if len(c.SMDiagnosticInfo) > 0 {
		children = append(children, diameter.VendorAVP(avpSMDiagnosticInfo, mv, datatype.OctetString(c.SMDiagnosticInfo)))
	}
	return diameter.VendorAVP(avpSMDeliveryFailureCause, mv, &diam.GroupedAVP{AVP: children})
}

// smDeliveryFailureCauseAVP reads an SM-Delivery-Failure-Cause AVP.
type smDeliveryFailureCauseAVP struct {
	Cause      int32  `avp:"SM-Enumerated-Delivery-Failure-Cause"`
	Diagnostic []byte `avp:"SM-Diagnostic-Info,omitempty"`
}

func (c *smDeliveryFailureCauseAVP) smDeliveryFailureCause() *SMDeliveryFailureCause {
	if c == nil {
		return nil
	}
	return &SMDeliveryFailureCause{
		SMEnumeratedDeliveryFailureCause: SMEnumeratedDeliveryFailureCause(c.Cause),
		SMDiagnosticInfo:                 c.Diagnostic,
	}
}
