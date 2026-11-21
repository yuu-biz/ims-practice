package hss

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

// The HSS stores repository data without reading it (TS 29.328 7.6.1).
// What it starts with is the operator's to provision, as part of the
// subscription, and these two functions build the data that the MMTel
// AS reads.

// MMTelServicesData is the operator's first MMTelServices document of a
// subscription (TS 29.364 7.2), as repository data under the
// MMTEL-Services Service-Indication. The document must pass
// simservs.ValidateMMTelServicesDocument, so that the AS and the XCAP
// server can read it.
func MMTelServicesData(document []byte) (sh.TransparentData, error) {
	d, err := simservs.ParseDocument(document)
	if err == nil {
		err = simservs.ValidateMMTelServicesDocument(d)
	}
	if err != nil {
		return sh.TransparentData{}, fmt.Errorf("mmtel_services: %w", err)
	}
	return sh.TransparentData{ServiceIndication: simservs.ServiceIndication, ServiceData: &sh.ServiceData{XML: d.RootElement()}}, nil
}

// UtCredentialData is the Ut credential of the user of privateID for
// the XCAP server, as repository data: H(A1) of the password in realm,
// never the password itself (xcap.Credential).
func UtCredentialData(privateID, realm, password string) sh.TransparentData {
	return sh.TransparentData{
		ServiceIndication: xcap.CredentialServiceIndication,
		ServiceData:       &sh.ServiceData{XML: xcap.NewCredential(privateID, realm, password).Marshal()},
	}
}

// CATData is the customized alerting tone of a subscription, as the
// repository data that the MMTel AS reads (mmtel.CATServiceIndication).
func CATData(t mmtel.CustomizedAlertingTone) (sh.TransparentData, error) {
	if err := t.Validate(); err != nil {
		return sh.TransparentData{}, fmt.Errorf("customized_alerting_tone: %w", err)
	}
	return sh.TransparentData{ServiceIndication: mmtel.CATServiceIndication, ServiceData: &sh.ServiceData{XML: t.Marshal()}}, nil
}

// RepositoryData is the repository data that a new subscription starts
// with: its own MMTelServices document, or else the configured one,
// the Ut credential if it has a Ut password, and its CAT if the
// operator provisioned one.
func (c Config) RepositoryData(privateID, mmtelServices, utPassword string, cat *mmtel.CustomizedAlertingTone) ([]sh.TransparentData, error) {
	var data []sh.TransparentData
	switch {
	case mmtelServices != "":
		d, err := MMTelServicesData([]byte(mmtelServices))
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	case c.MMTelServices != nil:
		data = append(data, *c.MMTelServices)
	}
	if utPassword != "" {
		data = append(data, UtCredentialData(privateID, c.Realm, utPassword))
	}
	if cat != nil {
		d, err := CATData(*cat)
		if err != nil {
			return nil, err
		}
		data = append(data, d)
	}
	return data, nil
}
