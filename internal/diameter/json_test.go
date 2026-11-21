package diameter

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// Codes from cx.xml (TS 29.229 6.1 and 6.3). internal/cx has its own,
// but it imports this package.
const (
	codeUserAuthorization       = 300
	codeMultimediaAuth          = 303
	avpVisitedNetworkIdentifier = 600
	avpPublicIdentity           = 601
	avpSIPAuthenticate          = 609
	avpSIPAuthDataItem          = 612
	avpUserAuthorizationType    = 623
	flagsMV                     = avp.Mbit | avp.Vbit
	registration                = 0 // User-Authorization-Type REGISTRATION
)

func userAuthorizationRequest() *diam.Message {
	m := diam.NewMessage(codeUserAuthorization, diam.RequestFlag|diam.ProxiableFlag, ApplicationIDCx, 1234, 5678, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("icscf;1;2"))
	m.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(ApplicationIDCx)),
	}})
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("001010000000001@ims.mnc001.mcc001.3gppnetwork.org"))
	m.NewAVP(avpPublicIdentity, flagsMV, VendorID3GPP, datatype.UTF8String("sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org"))
	m.NewAVP(avpVisitedNetworkIdentifier, flagsMV, VendorID3GPP, datatype.OctetString("ims.mnc001.mcc001.3gppnetwork.org"))
	m.NewAVP(avpUserAuthorizationType, flagsMV, VendorID3GPP, datatype.Enumerated(registration))
	return m
}

func userAuthorizationAnswer() *diam.Message {
	m := diam.NewMessage(codeUserAuthorization, diam.ProxiableFlag, ApplicationIDCx, 1234, 5678, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("icscf;1;2"))
	m.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
		diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(2001)),
	}})
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("hss.ims.mnc001.mcc001.3gppnetwork.org"))
	return m
}

// received is m as the peer reads it: decoded from the wire with the
// dictionary, so its AVPs hold the decoder's types.
func received(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	var b bytes.Buffer
	if _, err := m.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	r, err := diam.ReadMessage(&b, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEncodeJSON(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		message *diam.Message
		want    string
	}{
		{
			name:    "UAR",
			message: userAuthorizationRequest(),
			want: `{"command":"User-Authorization","code":300,"request":true,"application_id":16777216,"hop_by_hop":1234,"end_to_end":5678,"avps":[
				{"name":"Session-Id","code":263,"vendor":0,"value":"icscf;1;2"},
				{"name":"Vendor-Specific-Application-Id","code":260,"vendor":0,"avps":[
					{"name":"Vendor-Id","code":266,"vendor":0,"value":10415},
					{"name":"Auth-Application-Id","code":258,"vendor":0,"value":16777216}]},
				{"name":"User-Name","code":1,"vendor":0,"value":"001010000000001@ims.mnc001.mcc001.3gppnetwork.org"},
				{"name":"Public-Identity","code":601,"vendor":10415,"value":"sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org"},
				{"name":"Visited-Network-Identifier","code":600,"vendor":10415,"value":"ims.mnc001.mcc001.3gppnetwork.org"},
				{"name":"User-Authorization-Type","code":623,"vendor":10415,"value":0}]}`,
		},
		{
			name:    "UAA",
			message: userAuthorizationAnswer(),
			want: `{"command":"User-Authorization","code":300,"request":false,"application_id":16777216,"hop_by_hop":1234,"end_to_end":5678,"avps":[
				{"name":"Session-Id","code":263,"vendor":0,"value":"icscf;1;2"},
				{"name":"Experimental-Result","code":297,"vendor":0,"avps":[
					{"name":"Vendor-Id","code":266,"vendor":0,"value":10415},
					{"name":"Experimental-Result-Code","code":298,"vendor":0,"value":2001}]},
				{"name":"Origin-Host","code":264,"vendor":0,"value":"hss.ims.mnc001.mcc001.3gppnetwork.org"}]}`,
		},
	}
	for _, tt := range tests {
		for _, form := range []struct {
			name    string
			message *diam.Message
		}{{"built", tt.message}, {"received", received(t, tt.message)}} {
			t.Run(tt.name+"/"+form.name, func(t *testing.T) {
				got, err := EncodeJSON(form.message)
				if err != nil {
					t.Fatal(err)
				}
				var want bytes.Buffer
				if err := json.Compact(&want, []byte(tt.want)); err != nil {
					t.Fatal(err)
				}
				if string(got) != want.String() {
					t.Errorf("got  %s\nwant %s", got, want.String())
				}
			})
		}
	}
}

// The MSISDN of a User-Identity is a TBCD string on the wire, and its
// digits in the JSON (TS 29.329 6.3.2).
func TestEncodeJSONGivesTheMSISDNAsDigits(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	m := diam.NewMessage(306, diam.RequestFlag|diam.ProxiableFlag, ApplicationIDSh, 1, 2, dict.Default)
	m.NewAVP(700, flagsMV, VendorID3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(701, flagsMV, VendorID3GPP, datatype.OctetString(EncodeTBCD("12025550101"))),
	}})
	got, err := EncodeJSON(received(t, m))
	if err != nil {
		t.Fatal(err)
	}
	var decoded messageJSON
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	identity := decoded.AVPs[0]
	if identity.Name != "User-Identity" || len(identity.AVPs) != 1 || identity.AVPs[0].Name != "MSISDN" || identity.AVPs[0].Value != "12025550101" {
		t.Errorf("User-Identity = %+v, want the MSISDN 12025550101", identity)
	}
}

func TestEncodeJSONGivesOctetsThatAreNotTextAsBase64(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	m := diam.NewMessage(codeMultimediaAuth, diam.ProxiableFlag, ApplicationIDCx, 1, 2, dict.Default)
	m.NewAVP(avpSIPAuthDataItem, flagsMV, VendorID3GPP, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avpSIPAuthenticate, flagsMV, VendorID3GPP, datatype.OctetString([]byte{0x00, 0xff, 0x10})),
	}})
	got, err := EncodeJSON(received(t, m))
	if err != nil {
		t.Fatal(err)
	}
	var decoded messageJSON
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	item := decoded.AVPs[0].AVPs[0]
	if item.Name != "SIP-Authenticate" || item.Value != "AP8Q" {
		t.Errorf("SIP-Authenticate = %+v, want the base64 AP8Q", item)
	}
}

func TestSessionID(t *testing.T) {
	if got := SessionID(userAuthorizationRequest()); got != "icscf;1;2" {
		t.Errorf("SessionID(UAR) = %q", got)
	}
	cer := capabilitiesExchangeRequest(Identity{OriginHost: "a", OriginRealm: "b"}, []uint32{ApplicationIDCx}, net.IPv4(127, 0, 0, 1), MessageIDs{})
	if got := SessionID(cer); got != "" {
		t.Errorf("SessionID(CER) = %q, want empty", got)
	}
}
