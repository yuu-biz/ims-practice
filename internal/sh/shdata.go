package sh

import (
	"encoding/xml"
	"fmt"
)

// ShData is the Sh-Data document that the User-Data AVP carries, from
// the XML schema of TS 29.328 Annex D (ShDataType_Rel20.xsd, V20.0.0).
// The schema has no target namespace, so neither does the document.
//
// SIMPLIFIED: TS 29.328 Annex D. Only the elements of the data
// references the HSS stub serves are here: PublicIdentifiers with its
// IMSPublicIdentity and MSISDN elements, RepositoryData, and from
// Sh-IMS-Data the SCSCFName and SMSRegistrationInfo. The rest of the
// schema is left out until a role needs it.
type ShData struct {
	PublicIdentifiers *PublicIdentifiers `xml:"PublicIdentifiers,omitempty"`
	RepositoryData    []TransparentData  `xml:"RepositoryData,omitempty"`
	ShIMSData         *ShIMSData         `xml:"Sh-IMS-Data,omitempty"`
}

// Empty reports whether d holds no data at all, the case in which a UDA
// has no User-Data (TS 29.328 6.1.1.1 step 5).
func (d ShData) Empty() bool {
	return d.PublicIdentifiers == nil && len(d.RepositoryData) == 0 && d.ShIMSData == nil
}

// TransparentData is the tTransparentData type of Annex D: the
// repository data of TS 29.328 7.6.1, which application servers keep in
// the HSS for a user under a Service Indication (7.4). The HSS stores
// it without reading it, so several servers of one service can share
// it.
type TransparentData struct {
	ServiceIndication string `xml:"ServiceIndication"`
	// SequenceNumber tells the versions of the data apart (TS 29.328
	// 6.1.2.1): 0 for new data, and NextSequenceNumber for each change.
	SequenceNumber uint16 `xml:"SequenceNumber"`
	// ServiceData nil means none. In a PUR that removes the data.
	ServiceData *ServiceData `xml:"ServiceData,omitempty"`
}

// ServiceData is the tServiceData type of Annex D: any one XML element,
// such as the MMTelServices document of TS 29.364. XML holds the
// element as it is, so that it goes through the HSS byte for byte,
// namespaces and all.
type ServiceData struct {
	XML []byte `xml:",innerxml"`
}

// NextSequenceNumber is the Sequence Number that the change after n
// goes with (TS 29.328 6.1.2.1): n+1, and 1 after 65535, since 0 is only
// for new data.
func NextSequenceNumber(n uint16) uint16 {
	if n == 65535 {
		return 1
	}
	return n + 1
}

// PublicIdentifiers is the tPublicIdentity type of Annex D: the answer
// to IMSPublicIdentity (TS 29.328 7.6.2) and to MSISDN (7.6.9).
type PublicIdentifiers struct {
	IMSPublicIdentities []string `xml:"IMSPublicIdentity"`
	// MSISDNs are digits only, as in the MSISDN AVP but ASCII (Annex D,
	// tMSISDN).
	MSISDNs []string `xml:"MSISDN"`
}

// ShIMSData is the tShIMSData type of Annex D.
type ShIMSData struct {
	// SCSCFName is the S-CSCF assigned to the user (TS 29.328 7.6.4).
	SCSCFName string `xml:"SCSCFName,omitempty"`
	// SMSRegistrationInfo was added to the schema in a later release,
	// so it sits inside three Extension elements: tShIMSData,
	// tShIMSDataExtension and tShIMSDataExtension2 each end with one,
	// and the third holds it.
	SMSRegistrationInfo *SMSRegistrationInfoData `xml:"Extension>Extension>Extension>SMSRegistrationInfo,omitempty"`
}

// SMSRegistrationInfoData is the tSMSRegistrationInfo type of Annex D,
// the data of TS 29.328 7.6.16: the IP-SM-GW that delivers short
// messages to the user over IMS.
//
// SIMPLIFIED: TS 29.328 7.6.16. The SCAddress and the indication of SBI
// support are left out. The HSS of this project hands no Service Centre
// address to the IP-SM-GW, which reaches the SC over Diameter only.
type SMSRegistrationInfoData struct {
	// IPSMGWNumber is the E.164 number of the IP-SM-GW as digits only
	// (Annex D, tIP-SM-GW-Number; TS 23.008 defines the number). An
	// empty one in a PUR removes the registration (TS 29.328 6.1.2.1
	// step 4b).
	IPSMGWNumber string `xml:"IP-SM-GW-Number"`
	// S6cSupport says whether the IP-SM-GW can be reached over S6c. The
	// HSS keeps the IP-SM-GW's Diameter identity, and so can name it as
	// the serving node of the user, only when both support S6c (TS
	// 29.328 6.1.2.1 step 4b). Nil writes no element, which 7.6.16 has
	// an IP-SM-GW without S6c do. Annex D puts the element three
	// Extension elements deep, as it was added to tSMSRegistrationInfo
	// after the SCAddress and the SBI support indication.
	S6cSupport *S6cSupport `xml:"Extension>Extension>Extension>IP-SM-GW-S6cSupportIndication,omitempty"`
}

// SupportsS6c reports whether the IP-SM-GW announced that S6c reaches
// it. A registration without the indication does not.
func (d *SMSRegistrationInfoData) SupportsS6c() bool {
	return d != nil && d.S6cSupport != nil && *d.S6cSupport == S6cSupported
}

// S6cSupport is the tIpSmGwS6cSupInd type of TS 29.328 Annex D.
type S6cSupport int

const (
	S6cNotSupported S6cSupport = 0
	S6cSupported    S6cSupport = 1
)

// shDataDocument names the root element. encoding/xml treats the
// fields of an embedded struct as its own.
type shDataDocument struct {
	XMLName xml.Name `xml:"Sh-Data"`
	ShData
}

// Marshal writes d as the XML document that User-Data holds.
func (d ShData) Marshal() []byte {
	b, err := xml.Marshal(shDataDocument{ShData: d})
	if err != nil {
		panic(err) // the structs above always marshal
	}
	return append([]byte(xml.Header), b...)
}

// ParseShData reads the XML document of a User-Data AVP.
func ParseShData(data []byte) (ShData, error) {
	var doc shDataDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return ShData{}, fmt.Errorf("User-Data: %w", err)
	}
	return doc.ShData, nil
}
