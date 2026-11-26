package sgd

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// This file is the one command of this package that no specification
// defines: Ready-For-SM, with which the IP-SM-GW tells the HSS that a
// user can take short messages again, because the UE is reachable again
// or has memory again (TS 23.204 6.5b step 2d and 6.6 step 3). The HSS
// then alerts the service centres of the message waiting data (TS
// 23.040 3.2.6 items 2c and 2f).
//
// A network carries these two notices in MAP-READY-FOR-SM, which TS
// 29.002 12.4.1 has the IP-SM-GW send to the HLR. TS 23.204 5.2.3 sets
// S6c beside J, the MAP interface between the two, but S6c has no
// command for them (TS 29.338 Table 5.3.2.2/1), and neither has Sh.

// CommandReadyForSM is the command code of Ready-For-SM-Request and
// Ready-For-SM-Answer: 16777214, the first of the two codes that RFC
// 6733 11.2.1 keeps for experimental commands. No standard node knows
// it. Wireshark marks it as an unknown command, and an HSS without it
// answers DIAMETER_COMMAND_UNSUPPORTED (RFC 6733 7.1.3).
const CommandReadyForSM uint32 = 16777214

// avpAlertReason is the code of the Alert-Reason AVP, TS 29.272 7.3.83,
// with Vendor-Id 10415.
const avpAlertReason uint32 = 1434

// AlertReason is the Alert-Reason AVP of TS 29.272 7.3.83. An MME or an
// SGSN puts it in the NOR with which it tells the HSS the same (TS
// 29.272 5.2.5.1), and the HSS treats that NOR as the
// MAP-READY-FOR-SM whose Alert Reason it carries (5.2.5.1.3, TS 29.002
// 7.6.8.8).
type AlertReason uint32

const (
	// AlertReasonUEPresent is the UE that is reachable again: ms-Present
	// of MAP.
	AlertReasonUEPresent AlertReason = 0
	// AlertReasonUEMemoryAvailable is the UE that has memory for a short
	// message again: memoryAvailable of MAP.
	AlertReasonUEMemoryAvailable AlertReason = 1
)

func (r AlertReason) String() string {
	switch r {
	case AlertReasonUEPresent:
		return "UE_PRESENT"
	case AlertReasonUEMemoryAvailable:
		return "UE_MEMORY_AVAILABLE"
	}
	return fmt.Sprintf("Alert-Reason %d", uint32(r))
}

// valid reports whether r is one of the two values of 7.3.83.
func (r AlertReason) valid() bool {
	return r == AlertReasonUEPresent || r == AlertReasonUEMemoryAvailable
}

// ReadyForSMRequest is the Ready-For-SM-Request, from the IP-SM-GW to
// the HSS. Its format, in the notation of RFC 6733 3.2:
//
//	< Ready-For-SM-Request > ::= < Diameter Header: 16777214, REQ, PXY, 16777312 >
//	                             < Session-Id >
//	                             { Auth-Session-State }
//	                             { Origin-Host }
//	                             { Origin-Realm }
//	                             [ Destination-Host ]
//	                             { Destination-Realm }
//	                             { User-Identifier }
//	                             { IP-SM-GW-Number }
//	                             { Alert-Reason }
//
// SIMPLIFIED: TS 23.204 6.5b step 2d and 6.6 step 3, TS 29.002 12.4.1.
// The two notices that the UE can take short messages again go from the
// IP-SM-GW to the HSS in this command of this project's own, on S6c, in
// place of MAP-READY-FOR-SM, which this project does not implement. Its
// experimental command code keeps it from passing for a command of TS
// 29.338. It names the user by MSISDN in User-Identifier, as an RDR
// does, where MAP names the user by IMSI, and carries the alert reason
// in Alert-Reason, as a NOR does. IP-SM-GW-Number names the IP-SM-GW
// that sends it, which MAP leaves to the address of the sender: every
// instance of the IP-SM-GW has that number, while each has a Diameter
// identity of its own. The Additional Alert Reason Indicator, which
// tells an HLR that the notice comes from the IMS (TS 29.002 7.6.8.10A),
// is left out, since only an IP-SM-GW sends this command, and so is the
// Maximum UE Availability Time, which no UE of this project needs.
type ReadyForSMRequest struct {
	diameter.Header
	// UserIdentifier names the user by MSISDN. A User-Name alone is not
	// enough: the HSS keeps the message waiting data under the MSISDN,
	// as every RDR names the user by it.
	UserIdentifier UserIdentifier
	// IPSMGWNumber is the E.164 number of the IP-SM-GW that sends the
	// request, as digits only (TS 29.336 6.4.14). The HSS acts only when
	// it is the number of the IP-SM-GW that the user is registered to.
	IPSMGWNumber string
	AlertReason  AlertReason
}

// Message builds the request. The MSISDN and IPSMGWNumber must pass
// ValidE164.
func (r ReadyForSMRequest) Message() *diam.Message {
	m := diameter.NewRequest(ApplicationIDS6c, CommandReadyForSM)
	begin(m, r.Header)
	m.AddAVP(r.UserIdentifier.avp())
	// M and V for both: TS 29.336 Table 6.4.1/1 and TS 29.272 Table
	// 7.3.1/1.
	m.AddAVP(diameter.VendorAVP(avpIPSMGWNumber, mv, encodeTBCD(r.IPSMGWNumber)))
	m.AddAVP(diameter.VendorAVP(avpAlertReason, mv, datatype.Enumerated(r.AlertReason)))
	return m
}

// ParseReadyForSMRequest reads a Ready-For-SM-Request. It fails if
// User-Identifier holds no MSISDN, if IP-SM-GW-Number or Alert-Reason is
// missing, or if Alert-Reason has neither value of TS 29.272 7.3.83.
func ParseReadyForSMRequest(m *diam.Message) (ReadyForSMRequest, error) {
	var raw struct {
		diameter.Header
		UserIdentifier userIdentifierAVP `avp:"User-Identifier"`
		IPSMGWNumber   []byte            `avp:"IP-SM-GW-Number"`
		AlertReason    *int32            `avp:"Alert-Reason,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: %w", err)
	}
	user, err := raw.UserIdentifier.userIdentifier()
	if err != nil {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: User-Identifier: %w", err)
	}
	if user.MSISDN == "" {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: User-Identifier holds no MSISDN")
	}
	number, err := decodeTBCD(raw.IPSMGWNumber)
	if err != nil {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: IP-SM-GW-Number: %w", err)
	}
	if err := required("IP-SM-GW-Number", number); err != nil {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: %w", err)
	}
	if raw.AlertReason == nil {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: Alert-Reason is missing")
	}
	reason := AlertReason(*raw.AlertReason)
	if !reason.valid() {
		return ReadyForSMRequest{}, fmt.Errorf("Ready-For-SM: %v is not a value of TS 29.272 7.3.83", reason)
	}
	return ReadyForSMRequest{Header: raw.Header, UserIdentifier: user, IPSMGWNumber: number, AlertReason: reason}, nil
}

// ReadyForSMAnswer is the Ready-For-SM-Answer:
//
//	< Ready-For-SM-Answer > ::= < Diameter Header: 16777214, PXY, 16777312 >
//	                            < Session-Id >
//	                            [ Result-Code ]
//	                            [ Experimental-Result ]
//	                            { Auth-Session-State }
//	                            { Origin-Host }
//	                            { Origin-Realm }
type ReadyForSMAnswer struct {
	// Result is DIAMETER_SUCCESS when the HSS took the notice, whether
	// or not it alerted anyone, and DIAMETER_ERROR_USER_UNKNOWN when no
	// user has the MSISDN, as for an RDR (TS 29.338 5.2.3.4).
	Result diameter.Result
}

// Message builds the answer to request.
func (a ReadyForSMAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return newAnswer(request, local, a.Result)
}

func ParseReadyForSMAnswer(m *diam.Message) (ReadyForSMAnswer, error) {
	var raw diameter.ResultAVPs
	if err := m.Unmarshal(&raw); err != nil {
		return ReadyForSMAnswer{}, fmt.Errorf("Ready-For-SM answer: %w", err)
	}
	result, err := raw.Result()
	if err != nil {
		return ReadyForSMAnswer{}, fmt.Errorf("Ready-For-SM answer: %w", err)
	}
	return ReadyForSMAnswer{Result: result}, nil
}
