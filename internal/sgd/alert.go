package sgd

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// This file is the Alert Service Centre procedure of S6c (TS 29.338
// 5.2.2). When a short message could not be delivered because the user
// was absent or its memory was full, the SC address went into the
// message waiting data (MWD) of the HSS. Once the HSS learns that the
// user can take short messages again, it alerts each SC in that list,
// and the SC tries the messages it holds for the user again (TS 23.040
// 3.2.6, 3.2.7).

// CommandAlertServiceCentre is the command code of ALR and ALA, TS
// 29.338 Table 5.3.2.2/1.
const CommandAlertServiceCentre uint32 = 8388648

// AlertServiceCentreRequest is the ALR of TS 29.338 5.3.2.5, from the
// HSS to the SC (Table 5.2.2.1-1). The HSS names the SC by the Diameter
// identity it stored in the MWD, so Destination-Host is present
// (5.3.1.2).
//
// SIMPLIFIED: TS 29.338 5.3.2.5 and 6.2.3. Only the alert of the HSS is
// written and read. The optional AVPs SMSMI-Correlation-ID (SMS without
// MSISDN), Maximum-UE-Availability-Time and SMS-GMSC-Alert-Event with
// Serving-Node (UEs in a power saving mode, which an MME alerts
// directly on SGd) are left out: this project has no such UE.
type AlertServiceCentreRequest struct {
	diameter.Header
	// SCAddress is the E.164 number of the SC whose address is in the
	// MWD, as digits only.
	SCAddress string
	// UserIdentifier names the user that is reachable again: the
	// MSISDN-Alert of TS 23.040 3.2.7 in MSISDN.
	UserIdentifier UserIdentifier
}

// Message builds the ALR. SCAddress and the MSISDN, if set, must pass
// ValidE164.
func (r AlertServiceCentreRequest) Message() *diam.Message {
	m := diameter.NewRequest(ApplicationIDS6c, CommandAlertServiceCentre)
	begin(m, r.Header)
	// M and V: TS 29.338 Table 6.3.3.1/1.
	m.AddAVP(diameter.VendorAVP(avpSCAddress, mv, encodeTBCD(r.SCAddress)))
	m.AddAVP(r.UserIdentifier.avp())
	return m
}

// ParseAlertServiceCentreRequest reads an ALR. It fails if SC-Address or
// User-Identifier, which the command format requires, is missing or
// empty.
func ParseAlertServiceCentreRequest(m *diam.Message) (AlertServiceCentreRequest, error) {
	var raw struct {
		diameter.Header
		SCAddress      []byte            `avp:"SC-Address"`
		UserIdentifier userIdentifierAVP `avp:"User-Identifier"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return AlertServiceCentreRequest{}, fmt.Errorf("ALR: %w", err)
	}
	sc, err := decodeTBCD(raw.SCAddress)
	if err != nil {
		return AlertServiceCentreRequest{}, fmt.Errorf("ALR: SC-Address: %w", err)
	}
	if err := required("SC-Address", sc); err != nil {
		return AlertServiceCentreRequest{}, fmt.Errorf("ALR: %w", err)
	}
	user, err := raw.UserIdentifier.userIdentifier()
	if err != nil {
		return AlertServiceCentreRequest{}, fmt.Errorf("ALR: User-Identifier: %w", err)
	}
	return AlertServiceCentreRequest{Header: raw.Header, SCAddress: sc, UserIdentifier: user}, nil
}

// AlertServiceCentreAnswer is the ALA of TS 29.338 5.3.2.6 (Table
// 5.2.2.1-2). S6c defines no error for it, so Result is a base
// Result-Code.
type AlertServiceCentreAnswer struct {
	Result diameter.Result
}

// Message builds the ALA that answers request.
func (a AlertServiceCentreAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return newAnswer(request, local, a.Result)
}

func ParseAlertServiceCentreAnswer(m *diam.Message) (AlertServiceCentreAnswer, error) {
	var raw diameter.ResultAVPs
	if err := m.Unmarshal(&raw); err != nil {
		return AlertServiceCentreAnswer{}, fmt.Errorf("ALA: %w", err)
	}
	result, err := raw.Result()
	if err != nil {
		return AlertServiceCentreAnswer{}, fmt.Errorf("ALA: %w", err)
	}
	return AlertServiceCentreAnswer{Result: result}, nil
}
