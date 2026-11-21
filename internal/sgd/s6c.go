package sgd

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// This file is S6c, the interface between the SMS central functions and
// the HSS (TS 29.338 5). Before the SC delivers a short message it asks
// the HSS which node serves the recipient; the answer names the node by
// its Diameter identity, to which the SC then sends the TFR (5.2.1.2).

// ApplicationIDS6c is the application identifier of S6c, TS 29.338 4.8.
const ApplicationIDS6c uint32 = 16777312

// CommandSendRoutingInfoForSM is the command code of SRR and SRA, TS
// 29.338 Table 5.3.2.2/1.
const CommandSendRoutingInfoForSM uint32 = 8388647

// AVP codes of S6c. All have Vendor-Id 10415.
const (
	avpSupportedFeatures uint32 = 628  // TS 29.229 6.3.29
	avpFeatureListID     uint32 = 629  // TS 29.229 6.3.30
	avpFeatureList       uint32 = 630  // TS 29.229 6.3.31
	avpServingNode       uint32 = 2401 // TS 29.338 5.3.3.6
	avpMMEName           uint32 = 2402 // TS 29.173 6.4.4
	avpMMERealm          uint32 = 2408 // TS 29.173 6.4.12
	avpIPSMGWNumber      uint32 = 3100 // TS 29.336 6.4.14
	avpIPSMGWName        uint32 = 3101 // TS 29.336 6.4.15
	avpIPSMGWRealm       uint32 = 3112 // TS 29.336 6.4.18
	avpSMSF3GPPNumber    uint32 = 3338 // TS 29.338 5.3.3.29
	avpSMSF3GPPName      uint32 = 3340 // TS 29.338 5.3.3.31
	avpSMSF3GPPRealm     uint32 = 3342 // TS 29.338 5.3.3.33
	avpSMSF3GPPAddress   uint32 = 3344 // TS 29.338 5.3.3.35
)

// FeatureList is the Feature-List AVP of the feature list that S6c
// defines, the one with Feature-List-ID 1 (TS 29.338 5.3.3.12 and
// 5.3.3.13, TS 29.229 6.3.31). Each bit is one feature that the sender
// supports.
type FeatureList uint32

// SMSFSupport is feature bit 0 of TS 29.338 Table 5.3.3.13/1. Only if
// the sender of an SRR sets it does the HSS name an SMSF in
// SMSF-3GPP-Address.
const SMSFSupport FeatureList = 1 << 0

// featureListID1 is the Feature-List-ID of the only feature list of
// S6c (TS 29.338 5.3.3.12).
const featureListID1 = 1

// avp wraps the features in a Supported-Features AVP (TS 29.229 6.3.29),
// or returns nil when there is none to announce.
func (f FeatureList) avp() *diam.AVP {
	if f == 0 {
		return nil
	}
	// V without M. TS 29.229 7.2.1 lets a request leave M off when the
	// features only widen the answer the sender accepts, which is all
	// SMSF-Support does, and has an answer never set it. Feature-List-ID
	// and Feature-List never set it (Table 6.3.0.1).
	return vendorAVP(avpSupportedFeatures, v, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
		vendorAVP(avpFeatureListID, v, datatype.Unsigned32(featureListID1)),
		vendorAVP(avpFeatureList, v, datatype.Unsigned32(f)),
	}})
}

// supportedFeaturesAVP reads one Supported-Features AVP.
type supportedFeaturesAVP struct {
	VendorID      uint32 `avp:"Vendor-Id"`
	FeatureListID uint32 `avp:"Feature-List-ID"`
	FeatureList   uint32 `avp:"Feature-List"`
}

// featureList picks the feature list of S6c out of the Supported-Features
// AVPs of a message. Lists of other vendors or with other identifiers
// are not for this application.
func featureList(avps []supportedFeaturesAVP) FeatureList {
	var features FeatureList
	for _, a := range avps {
		if a.VendorID == VendorID3GPP && a.FeatureListID == featureListID1 {
			features |= FeatureList(a.FeatureList)
		}
	}
	return features
}

// SendRoutingInfoForSMRequest is the SRR of TS 29.338 5.3.2.3. The SC
// asks the HSS which node serves a user (5.2.1, Table 5.2.1.1-1). An
// IP-SM-GW asks the same way before it delivers outside the IMS (TS
// 23.204 6.4 step 3b).
type SendRoutingInfoForSMRequest struct {
	Header
	// MSISDN is the E.164 number of the recipient, as digits only.
	MSISDN string
	// SupportedFeatures are the features of S6c that the sender
	// supports. Zero writes no Supported-Features AVP.
	SupportedFeatures FeatureList
	// SCAddress is the E.164 number of the SC, as digits only.
	SCAddress string
}

// Message builds the SRR. MSISDN and SCAddress must pass ValidE164.
func (r SendRoutingInfoForSMRequest) Message() *diam.Message {
	m := newRequest(ApplicationIDS6c, CommandSendRoutingInfoForSM)
	r.begin(m)
	// M and V: TS 29.329 Table 6.3.1.
	m.AddAVP(vendorAVP(avpMSISDN, mv, encodeTBCD(r.MSISDN)))
	if features := r.SupportedFeatures.avp(); features != nil {
		m.AddAVP(features)
	}
	// M and V: TS 29.338 Table 6.3.3.1/1.
	m.AddAVP(vendorAVP(avpSCAddress, mv, encodeTBCD(r.SCAddress)))
	return m
}

// ParseSendRoutingInfoForSMRequest reads an SRR. The command format
// requires none of the AVPs after the routing ones, so a field is empty
// when its AVP is missing, and the HSS decides what that means.
func ParseSendRoutingInfoForSMRequest(m *diam.Message) (SendRoutingInfoForSMRequest, error) {
	var raw struct {
		requestHeader
		MSISDN            []byte                 `avp:"MSISDN,omitempty"`
		SupportedFeatures []supportedFeaturesAVP `avp:"Supported-Features,omitempty"`
		SCAddress         []byte                 `avp:"SC-Address,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return SendRoutingInfoForSMRequest{}, fmt.Errorf("SRR: %w", err)
	}
	msisdn, err := decodeTBCD(raw.MSISDN)
	if err != nil {
		return SendRoutingInfoForSMRequest{}, fmt.Errorf("SRR: MSISDN: %w", err)
	}
	sc, err := decodeTBCD(raw.SCAddress)
	if err != nil {
		return SendRoutingInfoForSMRequest{}, fmt.Errorf("SRR: SC-Address: %w", err)
	}
	return SendRoutingInfoForSMRequest{
		Header:            raw.header(),
		MSISDN:            msisdn,
		SupportedFeatures: featureList(raw.SupportedFeatures),
		SCAddress:         sc,
	}, nil
}

// ServingNode is the Serving-Node AVP, TS 29.338 5.3.3.6: one node that
// serves the user for mobile terminated SMS. It names either an MME or
// an IP-SM-GW.
//
// SIMPLIFIED: TS 29.338 5.3.3.6. The SGSN and MSC of the combinations a,
// b, d and e, and the IP-SM-GW-SBI-Support-Indicator of h and i, are
// left out: this project has no SGSN and no MSC, and reaches the
// IP-SM-GW over Diameter only.
type ServingNode struct {
	// MMEName and MMERealm are the Diameter identity and realm of the
	// MME registered for MT SMS, and MMENumberForMTSMS its E.164 number,
	// as digits only (combination c).
	MMEName           string
	MMERealm          string
	MMENumberForMTSMS string
	// IPSMGWNumber is the E.164 number of the IP-SM-GW, as digits only,
	// and IPSMGWName and IPSMGWRealm its Diameter identity and realm
	// (combinations f and g).
	IPSMGWNumber string
	IPSMGWName   string
	IPSMGWRealm  string
}

func (n ServingNode) avp() *diam.AVP {
	var children []*diam.AVP
	// The order of the AVP format in 5.3.3.6.
	if n.MMEName != "" {
		// M and V: TS 29.173 Table 6.4.1/1.
		children = append(children, vendorAVP(avpMMEName, mv, datatype.DiameterIdentity(n.MMEName)))
	}
	if n.MMERealm != "" {
		// TS 29.338 Table 5.3.3.1/2 has S6c set M on MME-Realm and on
		// MME-Number-for-MT-SMS, which their own specifications leave
		// off.
		children = append(children, vendorAVP(avpMMERealm, mv, datatype.DiameterIdentity(n.MMERealm)))
	}
	if n.MMENumberForMTSMS != "" {
		children = append(children, vendorAVP(avpMMENumberForMTSMS, mv, encodeTBCD(n.MMENumberForMTSMS)))
	}
	// M and V for the three of the IP-SM-GW: TS 29.336 Table 6.4.1/1.
	if n.IPSMGWNumber != "" {
		children = append(children, vendorAVP(avpIPSMGWNumber, mv, encodeTBCD(n.IPSMGWNumber)))
	}
	if n.IPSMGWName != "" {
		children = append(children, vendorAVP(avpIPSMGWName, mv, datatype.DiameterIdentity(n.IPSMGWName)))
	}
	if n.IPSMGWRealm != "" {
		children = append(children, vendorAVP(avpIPSMGWRealm, mv, datatype.DiameterIdentity(n.IPSMGWRealm)))
	}
	// M and V: TS 29.173 Table 6.4.1/1.
	return vendorAVP(avpServingNode, mv, &diam.GroupedAVP{AVP: children})
}

// servingNodeAVP reads a Serving-Node AVP.
type servingNodeAVP struct {
	MMEName           string `avp:"MME-Name,omitempty"`
	MMERealm          string `avp:"MME-Realm,omitempty"`
	MMENumberForMTSMS []byte `avp:"MME-Number-for-MT-SMS,omitempty"`
	IPSMGWNumber      []byte `avp:"IP-SM-GW-Number,omitempty"`
	IPSMGWName        string `avp:"IP-SM-GW-Name,omitempty"`
	IPSMGWRealm       string `avp:"IP-SM-GW-Realm,omitempty"`
}

func (n *servingNodeAVP) servingNode() (*ServingNode, error) {
	if n == nil {
		return nil, nil
	}
	mme, err := decodeTBCD(n.MMENumberForMTSMS)
	if err != nil {
		return nil, fmt.Errorf("MME-Number-for-MT-SMS: %w", err)
	}
	ipsmgw, err := decodeTBCD(n.IPSMGWNumber)
	if err != nil {
		return nil, fmt.Errorf("IP-SM-GW-Number: %w", err)
	}
	return &ServingNode{
		MMEName:           n.MMEName,
		MMERealm:          n.MMERealm,
		MMENumberForMTSMS: mme,
		IPSMGWNumber:      ipsmgw,
		IPSMGWName:        n.IPSMGWName,
		IPSMGWRealm:       n.IPSMGWRealm,
	}, nil
}

// SMSF3GPPAddress is the SMSF-3GPP-Address AVP, TS 29.338 5.3.3.35: the
// SMSF that serves the user for SMS over NAS on 3GPP access.
//
// SIMPLIFIED: TS 29.338 5.3.3.35 and 5.3.3.36. The
// SMSF-3GPP-SBI-Support-Indicator and the whole SMSF-Non-3GPP-Address
// are left out: this project reaches the SMSF over Diameter only and has
// no non-3GPP access.
type SMSF3GPPAddress struct {
	// SMSF3GPPNumber is the E.164 number of the SMSF, as digits only.
	SMSF3GPPNumber string
	// SMSF3GPPName and SMSF3GPPRealm are its Diameter identity and
	// realm.
	SMSF3GPPName  string
	SMSF3GPPRealm string
}

func (a SMSF3GPPAddress) avp() *diam.AVP {
	var children []*diam.AVP
	// V without M for all four: TS 29.338 Table 5.3.3.1/1.
	if a.SMSF3GPPNumber != "" {
		children = append(children, vendorAVP(avpSMSF3GPPNumber, v, encodeTBCD(a.SMSF3GPPNumber)))
	}
	if a.SMSF3GPPName != "" {
		children = append(children, vendorAVP(avpSMSF3GPPName, v, datatype.DiameterIdentity(a.SMSF3GPPName)))
	}
	if a.SMSF3GPPRealm != "" {
		children = append(children, vendorAVP(avpSMSF3GPPRealm, v, datatype.DiameterIdentity(a.SMSF3GPPRealm)))
	}
	return vendorAVP(avpSMSF3GPPAddress, v, &diam.GroupedAVP{AVP: children})
}

// smsf3GPPAddressAVP reads an SMSF-3GPP-Address AVP.
type smsf3GPPAddressAVP struct {
	Number []byte `avp:"SMSF-3GPP-Number,omitempty"`
	Name   string `avp:"SMSF-3GPP-Name,omitempty"`
	Realm  string `avp:"SMSF-3GPP-Realm,omitempty"`
}

func (a *smsf3GPPAddressAVP) smsf3GPPAddress() (*SMSF3GPPAddress, error) {
	if a == nil {
		return nil, nil
	}
	number, err := decodeTBCD(a.Number)
	if err != nil {
		return nil, fmt.Errorf("SMSF-3GPP-Number: %w", err)
	}
	return &SMSF3GPPAddress{SMSF3GPPNumber: number, SMSF3GPPName: a.Name, SMSF3GPPRealm: a.Realm}, nil
}

// SendRoutingInfoForSMAnswer is the SRA of TS 29.338 5.3.2.4 (Table
// 5.2.1.1-2).
type SendRoutingInfoForSMAnswer struct {
	// Result is DIAMETER_SUCCESS when the HSS names a serving node.
	// 5.2.1.3 lists the errors: DIAMETER_ERROR_USER_UNKNOWN,
	// DIAMETER_ERROR_SERVICE_NOT_SUBSCRIBED,
	// DIAMETER_ERROR_SERVICE_BARRED and, when no node is registered for
	// MT SMS, DIAMETER_ERROR_ABSENT_USER.
	Result Result
	// UserName is the IMSI of the user, which a successful answer always
	// carries. An IP-SM-GW that answers in place of the HSS puts an MT
	// Correlation ID here instead (TS 23.204 6.4 step 3c).
	UserName string
	// SupportedFeatures are the features of S6c that the sender of the
	// answer supports. Zero writes no Supported-Features AVP.
	SupportedFeatures FeatureList
	// ServingNode is the MME or the IP-SM-GW that serves the user, or
	// nil.
	ServingNode *ServingNode
	// SMSF3GPPAddress is the SMSF that serves the user, or nil. The HSS
	// sends it only to a requester that announced SMSFSupport (Table
	// 5.2.1.1-2, NOTE).
	SMSF3GPPAddress *SMSF3GPPAddress
}

// Message builds the SRA that answers request. The numbers in
// ServingNode and SMSF3GPPAddress must pass ValidE164.
func (a SendRoutingInfoForSMAnswer) Message(request *diam.Message, local Identity) *diam.Message {
	m := newAnswer(request, local, a.Result)
	if a.UserName != "" {
		m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(a.UserName))
	}
	if features := a.SupportedFeatures.avp(); features != nil {
		m.AddAVP(features)
	}
	if a.ServingNode != nil {
		m.AddAVP(a.ServingNode.avp())
	}
	if a.SMSF3GPPAddress != nil {
		m.AddAVP(a.SMSF3GPPAddress.avp())
	}
	return m
}

func ParseSendRoutingInfoForSMAnswer(m *diam.Message) (SendRoutingInfoForSMAnswer, error) {
	var raw struct {
		answerResult
		UserName          string                 `avp:"User-Name,omitempty"`
		SupportedFeatures []supportedFeaturesAVP `avp:"Supported-Features,omitempty"`
		ServingNode       *servingNodeAVP        `avp:"Serving-Node,omitempty"`
		SMSF3GPPAddress   *smsf3GPPAddressAVP    `avp:"SMSF-3GPP-Address,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return SendRoutingInfoForSMAnswer{}, fmt.Errorf("SRA: %w", err)
	}
	result, err := raw.result()
	if err != nil {
		return SendRoutingInfoForSMAnswer{}, fmt.Errorf("SRA: %w", err)
	}
	node, err := raw.ServingNode.servingNode()
	if err != nil {
		return SendRoutingInfoForSMAnswer{}, fmt.Errorf("SRA: Serving-Node: %w", err)
	}
	smsf, err := raw.SMSF3GPPAddress.smsf3GPPAddress()
	if err != nil {
		return SendRoutingInfoForSMAnswer{}, fmt.Errorf("SRA: SMSF-3GPP-Address: %w", err)
	}
	return SendRoutingInfoForSMAnswer{
		Result:            result,
		UserName:          raw.UserName,
		SupportedFeatures: featureList(raw.SupportedFeatures),
		ServingNode:       node,
		SMSF3GPPAddress:   smsf,
	}, nil
}
