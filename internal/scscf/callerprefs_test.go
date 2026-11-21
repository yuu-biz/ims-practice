package scscf

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// smsAcceptContact is what the IP-SM-GW puts in its MESSAGEs (TS 24.341
// 5.3.3.4.2 to 5.3.3.4.4).
const smsAcceptContact = "*;+g.3gpp.smsip;require;explicit"

const mmtelICSI = `+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`

func TestCallerPreferences(t *testing.T) {
	tests := []struct {
		name    string
		accept  []string // Accept-Contact values
		reject  []string // Reject-Contact values
		contact string   // the registered Contact parameters
		want    bool
	}{
		{"the SMS tag that the contact registered", []string{smsAcceptContact}, nil, ";+g.3gpp.smsip", true},
		{"the SMS tag among other parameters", []string{smsAcceptContact}, nil, ";audio;" + mmtelICSI + ";+g.3gpp.smsip;q=0.5", true},
		{"the SMS tag that the contact did not register", []string{smsAcceptContact}, nil, ";" + mmtelICSI, false},
		// RFC 3841 7.2.3: no feature parameters at all is immune.
		{"a contact without feature parameters", []string{smsAcceptContact}, nil, "", true},
		{"a contact with other parameters only", []string{smsAcceptContact}, nil, ";q=0.5;reg-id=1", true},
		{"a contact that says FALSE", []string{smsAcceptContact}, nil, `;+g.3gpp.smsip="FALSE"`, false},
		{"TRUE written out", []string{smsAcceptContact}, nil, `;+g.3gpp.smsip="TRUE"`, true},
		// Without explicit, a contact that did not register the tag is not
		// dropped (Figure 1: the score is unchanged).
		{"required, not explicit, tag absent", []string{"*;+g.3gpp.smsip;require"}, nil, ";audio", true},
		{"required, not explicit, value differs", []string{`*;mobility="fixed";require`}, nil, `;mobility="mobile"`, false},
		// Without require, a preference only orders the contacts.
		{"explicit, not required", []string{"*;+g.3gpp.smsip;explicit"}, nil, ";audio", true},
		{"one of a tag-value-list", []string{`*;methods="MESSAGE";require;explicit`}, nil, `;methods="INVITE,MESSAGE"`, true},
		{"none of a tag-value-list", []string{`*;methods="SUBSCRIBE";require;explicit`}, nil, `;methods="INVITE,MESSAGE"`, false},
		{"tokens compare without case", []string{`*;methods="message";require;explicit`}, nil, `;methods="MESSAGE"`, true},
		{"strings compare with case", []string{`*;description="<PC>";require;explicit`}, nil, `;description="<pc>"`, false},
		{"the same tag in its two encodings", []string{"*;+sip.audio;require;explicit"}, nil, ";audio", true},
		{"two values, both met", []string{smsAcceptContact, "*;audio;require;explicit"}, nil, ";audio;+g.3gpp.smsip", true},
		{"two values, one not met", []string{smsAcceptContact, "*;video;require;explicit"}, nil, ";audio;+g.3gpp.smsip", false},
		{"Reject-Contact that matches", nil, []string{"*;+g.3gpp.smsip"}, ";+g.3gpp.smsip", false},
		// 7.2.4: a Reject-Contact predicate with a tag the contact did not
		// register is discarded.
		{"Reject-Contact for a tag not registered", nil, []string{"*;+g.3gpp.smsip"}, ";audio", true},
		{"Reject-Contact that does not match", nil, []string{"*;audio"}, `;audio="FALSE"`, true},
		{"no preferences", nil, nil, ";audio", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &sip.Message{Method: sip.MESSAGE, RequestURI: impu}
			for _, v := range tt.accept {
				req.Add("Accept-Contact", v)
			}
			for _, v := range tt.reject {
				req.Add("Reject-Contact", v)
			}
			if got := meetsCallerPreferences(req, sip.ParseParams(tt.contact)); got != tt.want {
				t.Errorf("meetsCallerPreferences = %v, want %v", got, tt.want)
			}
		})
	}
}

// RFC 3841 9.2: the compact form, and two values in one header field.
func TestCallerPreferencesInOneCompactHeader(t *testing.T) {
	req, err := sip.Parse([]byte("MESSAGE " + impu + " SIP/2.0\r\n" +
		"a: *;audio;require;explicit, " + smsAcceptContact + "\r\n" +
		"Content-Length: 0\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if meetsCallerPreferences(req, sip.ParseParams(";audio")) {
		t.Error("a contact without +g.3gpp.smsip meets the second value")
	}
	if !meetsCallerPreferences(req, sip.ParseParams(";audio;+g.3gpp.smsip")) {
		t.Error("a contact with both tags does not meet the preferences")
	}
}
