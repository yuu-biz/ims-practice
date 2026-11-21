package dashboard

import (
	"errors"
	"fmt"
	"mime"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// smsContentType is the body of a MESSAGE of SMS over IP (TS 24.341).
const smsContentType = "application/vnd.3gpp.sms"

// ShortMessage is the body of a MESSAGE of SMS over IP, decoded, so that
// the learner reads the RP and TP fields and the text next to the SIP
// message without opening Wireshark. HEP carries the body as raw octets,
// which a browser cannot show as text.
type ShortMessage struct {
	// Hex is the body octet by octet, as the guide reads it.
	Hex string `json:"hex"`
	// Fields are those of the RP message, then those of the TPDU inside
	// it, in the order of their octets.
	Fields []SMSField `json:"fields"`
	Text   string     `json:"text,omitempty"`
	// Error says why the decoding stopped; the fields before it stay.
	Error string `json:"error,omitempty"`
}

// SMSField is one field, named as TS 24.011 (RP-) or TS 23.040 (TP-)
// names it.
type SMSField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// rpMessageTypes names the values of RP-MTI (TS 24.011 8.2.2).
var rpMessageTypes = map[sms.RPMTI]string{
	0: "RP-DATA (MS to network)",
	1: "RP-DATA (network to MS)",
	2: "RP-ACK (MS to network)",
	3: "RP-ACK (network to MS)",
	4: "RP-ERROR (MS to network)",
	5: "RP-ERROR (network to MS)",
	6: "RP-SMMA (MS to network)",
}

// shortMessageOf decodes the body of m when it is a short message, and
// returns nil otherwise.
func shortMessageOf(m *sip.Message) *ShortMessage {
	media, _, err := mime.ParseMediaType(m.Get("Content-Type"))
	if err != nil || media != smsContentType {
		return nil
	}
	return decodeShortMessage(m.Body)
}

func decodeShortMessage(body []byte) *ShortMessage {
	d := &ShortMessage{Hex: fmt.Sprintf("% x", body), Fields: []SMSField{}}
	rp, err := sms.ParseRP(body)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	d.addRP(rp)
	if _, ok := rp.(sms.RPSMMA); ok {
		return d
	}
	tpdu, err := sms.ParseTPDU(rp)
	switch {
	case errors.Is(err, sms.ErrNoUserData):
		return d
	case err != nil:
		d.Error = err.Error()
		return d
	}
	d.addTP(tpdu)
	return d
}

func (d *ShortMessage) add(name string, value any) {
	d.Fields = append(d.Fields, SMSField{Name: name, Value: fmt.Sprint(value)})
}

func (d *ShortMessage) addRP(rp sms.RPMessage) {
	d.add("RP-MTI", fmt.Sprintf("%d, %s", rp.MTI(), rpMessageTypes[rp.MTI()]))
	switch m := rp.(type) {
	case sms.RPDataMSToNetwork:
		d.add("RP-MR", m.MessageReference)
		d.add("RP-DA", m.DestinationAddress)
	case sms.RPDataNetworkToMS:
		d.add("RP-MR", m.MessageReference)
		d.add("RP-OA", m.OriginatorAddress)
	case sms.RPAck:
		d.add("RP-MR", m.MessageReference)
	case sms.RPError:
		d.add("RP-MR", m.MessageReference)
		d.add("RP-Cause", m.Cause)
	case sms.RPSMMA:
		d.add("RP-MR", m.MessageReference)
	}
}

func (d *ShortMessage) addTP(tpdu sms.TPDU) {
	switch m := tpdu.(type) {
	case sms.Submit:
		d.add("TP-MTI", "SMS-SUBMIT")
		d.add("TP-SRR", m.StatusReportRequest)
		d.add("TP-MR", m.MessageReference)
		d.add("TP-DA", m.DestinationAddress)
		d.addText(m.DataCodingScheme, m.UserData)
	case sms.Deliver:
		d.add("TP-MTI", "SMS-DELIVER")
		d.add("TP-SRI", m.StatusReportIndication)
		d.add("TP-OA", m.OriginatingAddress)
		d.add("TP-SCTS", timeStamp(m.ServiceCentreTimeStamp))
		d.addText(m.DataCodingScheme, m.UserData)
	case sms.StatusReport:
		d.add("TP-MTI", "SMS-STATUS-REPORT")
		d.add("TP-MR", m.MessageReference)
		d.add("TP-RA", m.RecipientAddress)
		d.add("TP-SCTS", timeStamp(m.ServiceCentreTimeStamp))
		d.add("TP-DT", timeStamp(m.DischargeTime))
		d.add("TP-ST", fmt.Sprintf("0x%02x", uint8(m.Status)))
	case sms.SubmitReport:
		d.add("TP-MTI", "SMS-SUBMIT-REPORT")
		d.addFailureCause(m.FailureCause)
		d.add("TP-SCTS", timeStamp(m.ServiceCentreTimeStamp))
	case sms.DeliverReport:
		d.add("TP-MTI", "SMS-DELIVER-REPORT")
		d.addFailureCause(m.FailureCause)
	}
}

// addFailureCause adds TP-FCS, which only the form for RP-ERROR has
// (TS 23.040 9.2.2.1a, 9.2.2.2a).
func (d *ShortMessage) addFailureCause(fcs sms.FailureCause) {
	if fcs != 0 {
		d.add("TP-FCS", fmt.Sprintf("0x%02x", uint8(fcs)))
	}
}

func (d *ShortMessage) addText(dcs sms.DataCodingScheme, ud sms.UserData) {
	alphabet := map[sms.Alphabet]string{sms.AlphabetGSM7: "GSM 7 bit", sms.Alphabet8Bit: "8 bit data", sms.AlphabetUCS2: "UCS2"}[dcs.Alphabet()]
	d.add("TP-DCS", fmt.Sprintf("0x%02x, %s", uint8(dcs), alphabet))
	unit := "octets"
	if dcs.Alphabet() == sms.AlphabetGSM7 {
		unit = "septets"
	}
	d.add("TP-UDL", fmt.Sprintf("%d %s", ud.Length, unit))
	text, err := sms.DecodeText(dcs, ud)
	if err != nil {
		d.Error = err.Error()
		return
	}
	d.Text = text
}

// timeStamp writes a TP-SCTS or TP-DT with the time zone it carries.
func timeStamp(t time.Time) string {
	return t.Format("2006-01-02 15:04:05 -07:00")
}
