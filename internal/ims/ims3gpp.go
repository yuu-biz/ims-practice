package ims

import (
	"encoding/xml"
	"errors"
	"fmt"
)

// IMS3GPPContentType is the media type of the 3GPP IM CN subsystem XML
// body (TS 24.229 7.6.4).
const IMS3GPPContentType = "application/3gpp-ims+xml"

// ims3GPP is the root element of that body (TS 24.229 7.6.2, Table
// 7.6.1). Only the service-info child is modelled: the S-CSCF sends it
// in a third-party REGISTER, and the application server reads it. The
// alternative-service child is for emergency and restoration answers,
// which this project does not send.
type ims3GPP struct {
	XMLName     xml.Name `xml:"ims-3gpp"`
	Version     string   `xml:"version,attr"`
	ServiceInfo *string  `xml:"service-info"`
}

// ServiceInfoBody is the body of a third-party REGISTER that hands the
// application server the ServiceInfo of its filter criterion (TS 24.229
// 5.4.1.7A): the text inside <service-info>, inside <ims-3gpp> with
// version 1.
func ServiceInfoBody(serviceInfo string) []byte {
	b, err := xml.Marshal(ims3GPP{Version: "1", ServiceInfo: &serviceInfo})
	if err != nil {
		panic(err) // the struct above always marshals
	}
	return append([]byte(xml.Header), b...)
}

// ParseServiceInfo reads the service-info of a 3GPP IM CN subsystem XML
// body, as an application server does with the body of a third-party
// REGISTER.
func ParseServiceInfo(body []byte) (string, error) {
	var doc ims3GPP
	if err := xml.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("ims-3gpp: %w", err)
	}
	if doc.ServiceInfo == nil {
		return "", errors.New("ims-3gpp: no service-info")
	}
	return *doc.ServiceInfo, nil
}
