package sgd

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// This file is the Report SM Delivery Status procedure of S6c (TS
// 29.338 5.2.3). After a delivery failed because the user was absent or
// its memory was full, the SMS-GMSC asks the HSS to put the SC address
// into the message waiting data (MWD), so that the HSS alerts the SC
// once the user is back (TS 23.040 3.2.6, 8.1.1).

// CommandReportSMDeliveryStatus is the command code of RDR and RDA, TS
// 29.338 Table 5.3.2.2/1.
const CommandReportSMDeliveryStatus uint32 = 8388649

// AVP codes of the delivery outcome, TS 29.338 Table 5.3.3.1/1. All
// have Vendor-Id 10415.
const (
	avpSMDeliveryOutcome         uint32 = 3316 // 5.3.3.14
	avpMMESMDeliveryOutcome      uint32 = 3317 // 5.3.3.15
	avpIPSMGWSMDeliveryOutcome   uint32 = 3320 // 5.3.3.18
	avpSMDeliveryCause           uint32 = 3321 // 5.3.3.19
	avpAbsentUserDiagnosticSM    uint32 = 3322 // 5.3.3.20
	avpSMSF3GPPSMDeliveryOutcome uint32 = 3336 // 5.3.3.27
)

// SMDeliveryCause is the SM-Delivery-Cause AVP, TS 29.338 5.3.3.19: why
// the HSS is to change the MWD.
type SMDeliveryCause uint32

const (
	SMDeliveryCauseUEMemoryCapacityExceeded SMDeliveryCause = 0
	SMDeliveryCauseAbsentUser               SMDeliveryCause = 1
	SMDeliveryCauseSuccessfulTransfer       SMDeliveryCause = 2
)

func (c SMDeliveryCause) String() string {
	switch c {
	case SMDeliveryCauseUEMemoryCapacityExceeded:
		return "UE_MEMORY_CAPACITY_EXCEEDED"
	case SMDeliveryCauseAbsentUser:
		return "ABSENT_USER"
	case SMDeliveryCauseSuccessfulTransfer:
		return "SUCCESSFUL_TRANSFER"
	}
	return fmt.Sprintf("SM-Delivery-Cause %d", uint32(c))
}

// NodeSMDeliveryOutcome is the content that MME-SM-Delivery-Outcome,
// IP-SM-GW-SM-Delivery-Outcome and SMSF-3GPP-SM-Delivery-Outcome share
// (TS 29.338 5.3.3.15, 5.3.3.18, 5.3.3.27): the outcome of the delivery
// through one kind of node.
type NodeSMDeliveryOutcome struct {
	SMDeliveryCause SMDeliveryCause
	// AbsentUserDiagnosticSM is the reason for the absence, one of the
	// values of TS 23.040 Table 1a (5.3.3.20), or nil when the node gave
	// none. Zero is a reason of its own, "no paging response via the
	// MSC", hence the pointer.
	AbsentUserDiagnosticSM *uint32
}

func (o NodeSMDeliveryOutcome) avp(code uint32, flags uint8) *diam.AVP {
	// M and V for both: TS 29.338 Table 5.3.3.1/1.
	children := []*diam.AVP{diameter.VendorAVP(avpSMDeliveryCause, mv, datatype.Enumerated(o.SMDeliveryCause))}
	if o.AbsentUserDiagnosticSM != nil {
		children = append(children, diameter.VendorAVP(avpAbsentUserDiagnosticSM, mv, datatype.Unsigned32(*o.AbsentUserDiagnosticSM)))
	}
	return diameter.VendorAVP(code, flags, &diam.GroupedAVP{AVP: children})
}

// nodeSMDeliveryOutcomeAVP reads one of the node outcomes.
type nodeSMDeliveryOutcomeAVP struct {
	Cause      *int32  `avp:"SM-Delivery-Cause,omitempty"`
	Diagnostic *uint32 `avp:"Absent-User-Diagnostic-SM,omitempty"`
}

func (o *nodeSMDeliveryOutcomeAVP) nodeSMDeliveryOutcome() (*NodeSMDeliveryOutcome, error) {
	if o == nil {
		return nil, nil
	}
	// The AVP format makes SM-Delivery-Cause optional, but an outcome
	// without its cause tells the HSS nothing to record.
	if o.Cause == nil {
		return nil, fmt.Errorf("SM-Delivery-Cause is missing")
	}
	return &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCause(*o.Cause), AbsentUserDiagnosticSM: o.Diagnostic}, nil
}

// SMDeliveryOutcome is the SM-Delivery-Outcome AVP, TS 29.338 5.3.3.14:
// one outcome for each kind of node that the delivery went through. At
// least one is set (Table 5.2.3.1-1).
//
// SIMPLIFIED: TS 29.338 5.3.3.14. The outcomes through an MSC, an SGSN
// and an SMSF for non-3GPP access are left out: this project has none
// of these nodes.
type SMDeliveryOutcome struct {
	MMESMDeliveryOutcome      *NodeSMDeliveryOutcome
	IPSMGWSMDeliveryOutcome   *NodeSMDeliveryOutcome
	SMSF3GPPSMDeliveryOutcome *NodeSMDeliveryOutcome
}

func (o SMDeliveryOutcome) avp() *diam.AVP {
	var children []*diam.AVP
	// The order of the AVP format in 5.3.3.14.
	if o.MMESMDeliveryOutcome != nil {
		children = append(children, o.MMESMDeliveryOutcome.avp(avpMMESMDeliveryOutcome, mv))
	}
	if o.IPSMGWSMDeliveryOutcome != nil {
		children = append(children, o.IPSMGWSMDeliveryOutcome.avp(avpIPSMGWSMDeliveryOutcome, mv))
	}
	if o.SMSF3GPPSMDeliveryOutcome != nil {
		// V without M: Table 5.3.3.1/1.
		children = append(children, o.SMSF3GPPSMDeliveryOutcome.avp(avpSMSF3GPPSMDeliveryOutcome, v))
	}
	return diameter.VendorAVP(avpSMDeliveryOutcome, mv, &diam.GroupedAVP{AVP: children})
}

// smDeliveryOutcomeAVP reads an SM-Delivery-Outcome AVP.
type smDeliveryOutcomeAVP struct {
	MME      *nodeSMDeliveryOutcomeAVP `avp:"MME-SM-Delivery-Outcome,omitempty"`
	IPSMGW   *nodeSMDeliveryOutcomeAVP `avp:"IP-SM-GW-SM-Delivery-Outcome,omitempty"`
	SMSF3GPP *nodeSMDeliveryOutcomeAVP `avp:"SMSF-3GPP-SM-Delivery-Outcome,omitempty"`
}

func (o *smDeliveryOutcomeAVP) smDeliveryOutcome() (SMDeliveryOutcome, error) {
	if o == nil {
		return SMDeliveryOutcome{}, fmt.Errorf("it is missing")
	}
	var out SMDeliveryOutcome
	var err error
	for _, n := range []struct {
		name string
		raw  *nodeSMDeliveryOutcomeAVP
		dst  **NodeSMDeliveryOutcome
	}{
		{"MME-SM-Delivery-Outcome", o.MME, &out.MMESMDeliveryOutcome},
		{"IP-SM-GW-SM-Delivery-Outcome", o.IPSMGW, &out.IPSMGWSMDeliveryOutcome},
		{"SMSF-3GPP-SM-Delivery-Outcome", o.SMSF3GPP, &out.SMSF3GPPSMDeliveryOutcome},
	} {
		if *n.dst, err = n.raw.nodeSMDeliveryOutcome(); err != nil {
			return SMDeliveryOutcome{}, fmt.Errorf("%s: %w", n.name, err)
		}
	}
	if out == (SMDeliveryOutcome{}) {
		return SMDeliveryOutcome{}, fmt.Errorf("it holds no outcome")
	}
	return out, nil
}

// RDRFlags is the RDR-Flags AVP, TS 29.338 5.3.3.21: a bit mask that
// tells the HSS how to treat the message waiting data.
type RDRFlags uint32

// RDRSingleAttemptDelivery is bit 0 of Table 5.3.3.21/1, the meaning
// that SRRSingleAttemptDelivery has in SRR-Flags.
const RDRSingleAttemptDelivery RDRFlags = 1 << 0

// ReportSMDeliveryStatusRequest is the RDR of TS 29.338 5.3.2.7, from
// the SMS-GMSC or the IP-SM-GW to the HSS (Table 5.2.3.1-1).
//
// SIMPLIFIED: TS 29.338 5.3.2.7. Supported-Features,
// SMSMI-Correlation-ID and the serving nodes through which the delivery
// failed are left out: S6c defines no feature for RDR, this project has
// no SMS without MSISDN, and the HSS of this project keeps one serving
// node of each kind.
type ReportSMDeliveryStatusRequest struct {
	diameter.Header
	// UserIdentifier names the recipient, by MSISDN.
	UserIdentifier UserIdentifier
	// SCAddress is the E.164 number of the SC that holds the short
	// message, as digits only.
	SCAddress         string
	SMDeliveryOutcome SMDeliveryOutcome
	// RDRFlags are the bits of 5.3.3.21. Zero writes no RDR-Flags AVP.
	RDRFlags RDRFlags
}

// Message builds the RDR. SCAddress and the MSISDN, if set, must pass
// ValidE164.
func (r ReportSMDeliveryStatusRequest) Message() *diam.Message {
	m := diameter.NewRequest(ApplicationIDS6c, CommandReportSMDeliveryStatus)
	begin(m, r.Header)
	m.AddAVP(r.UserIdentifier.avp())
	// M and V: TS 29.338 Table 6.3.3.1/1.
	m.AddAVP(diameter.VendorAVP(avpSCAddress, mv, encodeTBCD(r.SCAddress)))
	m.AddAVP(r.SMDeliveryOutcome.avp())
	if r.RDRFlags != 0 {
		// V without M: TS 29.338 Table 5.3.3.1/1.
		m.AddAVP(diameter.VendorAVP(avpRDRFlags, v, datatype.Unsigned32(r.RDRFlags)))
	}
	return m
}

// ParseReportSMDeliveryStatusRequest reads an RDR. It fails if
// User-Identifier, SC-Address or SM-Delivery-Outcome, which the command
// format requires, is missing or empty.
func ParseReportSMDeliveryStatusRequest(m *diam.Message) (ReportSMDeliveryStatusRequest, error) {
	var raw struct {
		diameter.Header
		UserIdentifier    userIdentifierAVP     `avp:"User-Identifier"`
		SCAddress         []byte                `avp:"SC-Address"`
		SMDeliveryOutcome *smDeliveryOutcomeAVP `avp:"SM-Delivery-Outcome,omitempty"`
		RDRFlags          uint32                `avp:"RDR-Flags,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ReportSMDeliveryStatusRequest{}, fmt.Errorf("RDR: %w", err)
	}
	user, err := raw.UserIdentifier.userIdentifier()
	if err != nil {
		return ReportSMDeliveryStatusRequest{}, fmt.Errorf("RDR: User-Identifier: %w", err)
	}
	sc, err := decodeTBCD(raw.SCAddress)
	if err != nil {
		return ReportSMDeliveryStatusRequest{}, fmt.Errorf("RDR: SC-Address: %w", err)
	}
	if err := required("SC-Address", sc); err != nil {
		return ReportSMDeliveryStatusRequest{}, fmt.Errorf("RDR: %w", err)
	}
	outcome, err := raw.SMDeliveryOutcome.smDeliveryOutcome()
	if err != nil {
		return ReportSMDeliveryStatusRequest{}, fmt.Errorf("RDR: SM-Delivery-Outcome: %w", err)
	}
	return ReportSMDeliveryStatusRequest{
		Header:            raw.Header,
		UserIdentifier:    user,
		SCAddress:         sc,
		SMDeliveryOutcome: outcome,
		RDRFlags:          RDRFlags(raw.RDRFlags),
	}, nil
}

// ReportSMDeliveryStatusAnswer is the RDA of TS 29.338 5.3.2.8 (Table
// 5.2.3.1-2).
//
// SIMPLIFIED: TS 29.338 5.3.2.8. Supported-Features and the serving
// nodes of the alert (Serving-Node, Additional-Serving-Node and the SMSF
// addresses, which tell the SMS-GMSC to try again at once through a
// node it did not know of, 5.2.3.2) are left out: the HSS of this
// project alerts the SC with ALR instead.
type ReportSMDeliveryStatusAnswer struct {
	// Result is DIAMETER_SUCCESS when the HSS updated the MWD. 5.2.3.4
	// lists the errors: DIAMETER_ERROR_USER_UNKNOWN and
	// DIAMETER_ERROR_MWD_LIST_FULL.
	Result diameter.Result
	// UserIdentifier holds the MSISDN-Alert of the user when it differs
	// from the MSISDN of the request, and is nil otherwise.
	UserIdentifier *UserIdentifier
}

// Message builds the RDA that answers request.
func (a ReportSMDeliveryStatusAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	m := newAnswer(request, local, a.Result)
	if a.UserIdentifier != nil {
		m.AddAVP(a.UserIdentifier.avp())
	}
	return m
}

func ParseReportSMDeliveryStatusAnswer(m *diam.Message) (ReportSMDeliveryStatusAnswer, error) {
	var raw struct {
		diameter.ResultAVPs
		UserIdentifier *userIdentifierAVP `avp:"User-Identifier,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ReportSMDeliveryStatusAnswer{}, fmt.Errorf("RDA: %w", err)
	}
	result, err := raw.Result()
	if err != nil {
		return ReportSMDeliveryStatusAnswer{}, fmt.Errorf("RDA: %w", err)
	}
	a := ReportSMDeliveryStatusAnswer{Result: result}
	if raw.UserIdentifier != nil {
		user, err := raw.UserIdentifier.userIdentifier()
		if err != nil {
			return ReportSMDeliveryStatusAnswer{}, fmt.Errorf("RDA: User-Identifier: %w", err)
		}
		a.UserIdentifier = &user
	}
	return a, nil
}
