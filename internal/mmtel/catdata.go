package mmtel

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// CATServiceIndication is the Service-Indication of the Sh Repository
// Data that holds a user's customized alerting tone (TS 24.182).
//
// The user's settings of CAT have no standard form: TS 24.182 leaves the
// user configuration out (4.5.1, 4.8), and TS 29.364 7.2.22 gives CAT
// an operator part in MMTelServices but no user data. TS 29.364 7.2.0
// has proprietary service data use a Service-Indication of its own, so
// CAT has one here, as the Ut credential has (xcap.Credential). The HSS
// stores it without reading it (TS 29.328 7.6.1), the operator
// provisions it (TS 24.182 4.5.2), and the AS reads it.
const CATServiceIndication = "Customized-Alerting-Tone"

// CustomizedAlertingTone is the CAT of a user, in this project's own
// format:
//
//	<customized-alerting-tone active="true">
//	  <tone>cat1</tone>
//	</customized-alerting-tone>
type CustomizedAlertingTone struct {
	XMLName xml.Name `xml:"customized-alerting-tone" json:"-"`
	// Active tells whether the callers of the user hear the tone.
	Active bool `xml:"active,attr" json:"active"`
	// Tone names the announcement that the MRF plays, one that the
	// MRFPs have provisioned (RFC 4240 3.3), such as cat1.
	Tone string `xml:"tone" json:"tone"`
}

// Marshal writes the CAT as the ServiceData of repository data.
func (t CustomizedAlertingTone) Marshal() []byte {
	b, _ := xml.Marshal(t)
	return b
}

// Validate checks that the tone is a name that an MRFC can ask an MRFP
// for: RFC 4240 3.3 has announcement-id = 1*(ALPHA / DIGIT).
func (t CustomizedAlertingTone) Validate() error {
	if t.Tone == "" || strings.IndexFunc(t.Tone, func(c rune) bool {
		return !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9')
	}) >= 0 {
		return fmt.Errorf("mmtel: the tone %q is not letters and digits", t.Tone)
	}
	return nil
}

// ParseCustomizedAlertingTone reads the ServiceData of CAT.
func ParseCustomizedAlertingTone(data []byte) (CustomizedAlertingTone, error) {
	var t CustomizedAlertingTone
	if err := xml.Unmarshal(data, &t); err != nil {
		return CustomizedAlertingTone{}, fmt.Errorf("mmtel: CAT data: %w", err)
	}
	return t, t.Validate()
}
