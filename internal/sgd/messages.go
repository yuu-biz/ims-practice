package sgd

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// MOForwardShortMessageRequest is the OFR of TS 29.338 6.3.2.3. The node
// that serves the sender (an MME, an SMSF or an IP-SM-GW) forwards a
// short message from the UE to the SC with it (6.2.1). Its AVPs are the
// parts of the RP-DATA that the UE sent (Table 6.2.1.1/1).
type MOForwardShortMessageRequest struct {
	Header
	// SCAddress is SM-RP-DA: the E.164 number of the SC that the UE put
	// in RP-Destination Address, as digits only, such as 12025550100.
	SCAddress string
	// UserIdentifier is SM-RP-OA: the sender, by IMSI and by MSISDN as
	// far as the serving node knows them.
	UserIdentifier UserIdentifier
	// SMRPUI is SM-RP-UI: the TPDU, an SMS-SUBMIT, of at most MaxSMRPUI
	// octets.
	SMRPUI []byte
}

// Message builds the OFR. SCAddress must pass ValidE164.
func (r MOForwardShortMessageRequest) Message() *diam.Message {
	m := newRequest(ApplicationIDSGd, CommandMOForwardShortMessage)
	r.begin(m)
	// M and V: TS 29.338 Table 6.3.3.1/1.
	m.AddAVP(vendorAVP(avpSCAddress, mv, encodeTBCD(r.SCAddress)))
	m.AddAVP(r.UserIdentifier.avp())
	m.AddAVP(vendorAVP(avpSMRPUI, mv, datatype.OctetString(r.SMRPUI)))
	return m
}

// ParseMOForwardShortMessageRequest reads an OFR. It fails if one of the
// AVPs that the command format requires is missing or empty; RFC 6733
// 7.1.5 gives DIAMETER_MISSING_AVP as the result for that.
func ParseMOForwardShortMessageRequest(m *diam.Message) (MOForwardShortMessageRequest, error) {
	var raw struct {
		requestHeader
		SCAddress      []byte            `avp:"SC-Address"`
		UserIdentifier userIdentifierAVP `avp:"User-Identifier"`
		SMRPUI         []byte            `avp:"SM-RP-UI"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return MOForwardShortMessageRequest{}, fmt.Errorf("OFR: %w", err)
	}
	sc, err := decodeTBCD(raw.SCAddress)
	if err != nil {
		return MOForwardShortMessageRequest{}, fmt.Errorf("OFR: SC-Address: %w", err)
	}
	user, err := raw.UserIdentifier.userIdentifier()
	if err != nil {
		return MOForwardShortMessageRequest{}, fmt.Errorf("OFR: User-Identifier: %w", err)
	}
	if err := required("SC-Address", sc, "SM-RP-UI", string(raw.SMRPUI)); err != nil {
		return MOForwardShortMessageRequest{}, fmt.Errorf("OFR: %w", err)
	}
	return MOForwardShortMessageRequest{Header: raw.header(), SCAddress: sc, UserIdentifier: user, SMRPUI: raw.SMRPUI}, nil
}

// MOForwardShortMessageAnswer is the OFA of TS 29.338 6.3.2.4 (Table
// 6.2.1.1/2). The serving node turns it into the RP-ACK or RP-ERROR for
// the UE.
type MOForwardShortMessageAnswer struct {
	// Result is DIAMETER_SUCCESS when the SC took the short message.
	// Otherwise it is DIAMETER_ERROR_FACILITY_NOT_SUPPORTED or
	// DIAMETER_ERROR_SM_DELIVERY_FAILURE (6.2.1.3).
	Result Result
	// SMDeliveryFailureCause goes with
	// DIAMETER_ERROR_SM_DELIVERY_FAILURE, and is nil otherwise.
	SMDeliveryFailureCause *SMDeliveryFailureCause
	// SMRPUI is the TPDU of the acknowledgement, an SMS-SUBMIT-REPORT,
	// or nil when the SC sends none.
	SMRPUI []byte
}

// Message builds the OFA that answers request.
func (a MOForwardShortMessageAnswer) Message(request *diam.Message, local Identity) *diam.Message {
	return forwardShortMessageAnswer(request, local, a.Result, a.SMDeliveryFailureCause, a.SMRPUI)
}

func ParseMOForwardShortMessageAnswer(m *diam.Message) (MOForwardShortMessageAnswer, error) {
	result, cause, tpdu, err := parseForwardShortMessageAnswer(m)
	if err != nil {
		return MOForwardShortMessageAnswer{}, fmt.Errorf("OFA: %w", err)
	}
	return MOForwardShortMessageAnswer{Result: result, SMDeliveryFailureCause: cause, SMRPUI: tpdu}, nil
}

// MTForwardShortMessageRequest is the TFR of TS 29.338 6.3.2.5. The SC
// forwards a short message for a UE to the node that serves it (an MME,
// an SMSF or an IP-SM-GW) with it (6.2.2). Its AVPs are the parts of the
// RP-DATA that the serving node will send to the UE (Table 6.2.2.1/1).
// DestinationHost names the serving node, as the SRA gave it (6.3.1.2).
type MTForwardShortMessageRequest struct {
	Header
	// UserName is SM-RP-DA: the IMSI of the recipient. Towards an
	// IP-SM-GW it is the MT Correlation ID that the IP-SM-GW gave in
	// place of the IMSI in the SRA (TS 23.204 6.4 steps 3c and 4).
	UserName string
	// SCAddress is SM-RP-OA: the E.164 number of the SC, as digits only.
	SCAddress string
	// SMRPUI is SM-RP-UI: the TPDU, an SMS-DELIVER or an
	// SMS-STATUS-REPORT, of at most MaxSMRPUI octets.
	SMRPUI []byte
	// MMENumberForMTSMS is the E.164 number of the MME, as digits only.
	// It is present when the request goes to an MME, and empty
	// otherwise.
	MMENumberForMTSMS string
}

// Message builds the TFR. SCAddress, and MMENumberForMTSMS if set, must
// pass ValidE164.
func (r MTForwardShortMessageRequest) Message() *diam.Message {
	m := newRequest(ApplicationIDSGd, CommandMTForwardShortMessage)
	r.begin(m)
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(r.UserName))
	m.AddAVP(vendorAVP(avpSCAddress, mv, encodeTBCD(r.SCAddress)))
	m.AddAVP(vendorAVP(avpSMRPUI, mv, datatype.OctetString(r.SMRPUI)))
	if r.MMENumberForMTSMS != "" {
		// V without M: TS 29.272 Table 7.3.1/1, which TS 29.338 Table
		// 6.3.3.1/2 leaves as it is for SGd.
		m.AddAVP(vendorAVP(avpMMENumberForMTSMS, v, encodeTBCD(r.MMENumberForMTSMS)))
	}
	return m
}

// ParseMTForwardShortMessageRequest reads a TFR. Like
// ParseMOForwardShortMessageRequest it fails if a required AVP is
// missing or empty.
func ParseMTForwardShortMessageRequest(m *diam.Message) (MTForwardShortMessageRequest, error) {
	var raw struct {
		requestHeader
		UserName          string `avp:"User-Name"`
		SCAddress         []byte `avp:"SC-Address"`
		SMRPUI            []byte `avp:"SM-RP-UI"`
		MMENumberForMTSMS []byte `avp:"MME-Number-for-MT-SMS,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return MTForwardShortMessageRequest{}, fmt.Errorf("TFR: %w", err)
	}
	sc, err := decodeTBCD(raw.SCAddress)
	if err != nil {
		return MTForwardShortMessageRequest{}, fmt.Errorf("TFR: SC-Address: %w", err)
	}
	mme, err := decodeTBCD(raw.MMENumberForMTSMS)
	if err != nil {
		return MTForwardShortMessageRequest{}, fmt.Errorf("TFR: MME-Number-for-MT-SMS: %w", err)
	}
	if err := required("User-Name", raw.UserName, "SC-Address", sc, "SM-RP-UI", string(raw.SMRPUI)); err != nil {
		return MTForwardShortMessageRequest{}, fmt.Errorf("TFR: %w", err)
	}
	return MTForwardShortMessageRequest{
		Header:            raw.header(),
		UserName:          raw.UserName,
		SCAddress:         sc,
		SMRPUI:            raw.SMRPUI,
		MMENumberForMTSMS: mme,
	}, nil
}

// MTForwardShortMessageAnswer is the TFA of TS 29.338 6.3.2.6 (Table
// 6.2.2.1/2). The serving node builds it from the RP-ACK or RP-ERROR of
// the UE, or from its own failure to reach the UE.
type MTForwardShortMessageAnswer struct {
	// Result is DIAMETER_SUCCESS when the UE took the short message.
	// 6.2.2.2 lists the errors: DIAMETER_ERROR_USER_UNKNOWN,
	// DIAMETER_ERROR_ABSENT_USER, DIAMETER_ERROR_USER_BUSY_FOR_MT_SMS,
	// DIAMETER_ERROR_ILLEGAL_USER, DIAMETER_ERROR_ILLEGAL_EQUIPMENT,
	// DIAMETER_ERROR_FACILITY_NOT_SUPPORTED and
	// DIAMETER_ERROR_SM_DELIVERY_FAILURE.
	Result Result
	// SMDeliveryFailureCause goes with
	// DIAMETER_ERROR_SM_DELIVERY_FAILURE, and is nil otherwise.
	SMDeliveryFailureCause *SMDeliveryFailureCause
	// SMRPUI is the TPDU of the acknowledgement, an SMS-DELIVER-REPORT,
	// or nil when the UE sent none.
	SMRPUI []byte
}

// Message builds the TFA that answers request.
func (a MTForwardShortMessageAnswer) Message(request *diam.Message, local Identity) *diam.Message {
	return forwardShortMessageAnswer(request, local, a.Result, a.SMDeliveryFailureCause, a.SMRPUI)
}

func ParseMTForwardShortMessageAnswer(m *diam.Message) (MTForwardShortMessageAnswer, error) {
	result, cause, tpdu, err := parseForwardShortMessageAnswer(m)
	if err != nil {
		return MTForwardShortMessageAnswer{}, fmt.Errorf("TFA: %w", err)
	}
	return MTForwardShortMessageAnswer{Result: result, SMDeliveryFailureCause: cause, SMRPUI: tpdu}, nil
}

// forwardShortMessageAnswer builds an OFA or a TFA. Of the AVPs this
// project sends, the two answers carry the same ones in the same order
// (6.3.2.4, 6.3.2.6).
func forwardShortMessageAnswer(request *diam.Message, local Identity, result Result, cause *SMDeliveryFailureCause, tpdu []byte) *diam.Message {
	m := newAnswer(request, local, result)
	if cause != nil {
		m.AddAVP(cause.avp())
	}
	if len(tpdu) > 0 {
		m.AddAVP(vendorAVP(avpSMRPUI, mv, datatype.OctetString(tpdu)))
	}
	return m
}

func parseForwardShortMessageAnswer(m *diam.Message) (Result, *SMDeliveryFailureCause, []byte, error) {
	var raw struct {
		answerResult
		SMDeliveryFailureCause *smDeliveryFailureCauseAVP `avp:"SM-Delivery-Failure-Cause,omitempty"`
		SMRPUI                 []byte                     `avp:"SM-RP-UI,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return Result{}, nil, nil, err
	}
	result, err := raw.result()
	if err != nil {
		return Result{}, nil, nil, err
	}
	return result, raw.SMDeliveryFailureCause.smDeliveryFailureCause(), raw.SMRPUI, nil
}

// required takes pairs of an AVP name and its value as read, and names
// the first AVP whose value is empty. go-diameter leaves the field of an
// AVP that is not in the message at its zero value, so an empty value is
// how a missing AVP shows.
func required(namesAndValues ...string) error {
	for i := 0; i < len(namesAndValues); i += 2 {
		if namesAndValues[i+1] == "" {
			return fmt.Errorf("%s is missing or empty", namesAndValues[i])
		}
	}
	return nil
}
