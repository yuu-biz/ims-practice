package ims

import "testing"

// TestParseTelephoneNumber covers both forms that carry a number, the
// parameters of the number, and the URIs that carry no number. The
// conditions of the roles are not here: the function takes a local
// number, a GRUU and a user part that is no number at all, and each
// role rejects what it cannot use.
func TestParseTelephoneNumber(t *testing.T) {
	tests := []struct {
		uri string
		// number is empty when the URI carries no telephone number.
		number string
		params string
		// sip is the SIP URI without its user part, empty for a tel URI.
		sip string
	}{
		{uri: "tel:+12025550101", number: "+12025550101"},
		{uri: "TEL:+12025550101", number: "+12025550101"},
		{uri: "tel:+1-202-555-0101", number: "+1-202-555-0101"},
		{uri: "tel:5550101;phone-context=+1-202", number: "5550101", params: ";phone-context=+1-202"},
		{uri: "tel:+12025550101;npdi;rn=+12025550100", number: "+12025550101", params: ";npdi;rn=+12025550100"},
		{
			uri:    "sip:+12025550101@" + domain + ";user=phone",
			number: "+12025550101",
			sip:    "sip:" + domain + ";user=phone",
		},
		{
			uri:    "sips:+12025550101@" + domain + ":5061;user=PHONE",
			number: "+12025550101",
			sip:    "sips:" + domain + ":5061;user=PHONE",
		},
		{
			uri:    "sip:+12025550101;npdi@" + domain + ";user=phone;gr=urn:uuid:f81d4fae",
			number: "+12025550101",
			params: ";npdi",
			sip:    "sip:" + domain + ";user=phone;gr=urn:uuid:f81d4fae",
		},
		{
			uri:    "sip:5550101;phone-context=" + domain + "@" + domain + ";user=phone",
			number: "5550101",
			params: ";phone-context=" + domain,
			sip:    "sip:" + domain + ";user=phone",
		},
		// user=phone says the user part is a number, so the user part of
		// a name comes back as it is; a role that needs a global number
		// rejects it.
		{uri: "sip:alice@" + domain + ";user=phone", number: "alice", sip: "sip:" + domain + ";user=phone"},
		{uri: "sip:+12025550101@" + domain},
		{uri: "sip:+12025550101@" + domain + ";user=ip"},
		{uri: "sip:" + domain + ";user=phone"},
		{uri: "sip:@" + domain + ";user=phone"},
		{uri: "tel:"},
		{uri: "tel:;phone-context=+1-202"},
		{uri: "urn:service:sos"},
		{uri: "+12025550101"},
		{uri: ""},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got, ok := ParseTelephoneNumber(tt.uri)
			if ok != (tt.number != "") {
				t.Fatalf("ParseTelephoneNumber(%q) = %+v, %v", tt.uri, got, ok)
			}
			if !ok {
				return
			}
			if got.Number != tt.number || got.Params.String() != tt.params {
				t.Errorf("number = %q%q, want %q%q", got.Number, got.Params.String(), tt.number, tt.params)
			}
			switch {
			case tt.sip == "":
				if got.SIP != nil {
					t.Errorf("SIP = %q, want none for a tel URI", got.SIP.String())
				}
			case got.SIP == nil:
				t.Errorf("SIP = none, want %q", tt.sip)
			case got.SIP.String() != tt.sip:
				t.Errorf("SIP = %q, want %q", got.SIP.String(), tt.sip)
			}
		})
	}
}

func TestWithoutVisualSeparators(t *testing.T) {
	for in, want := range map[string]string{
		"+1-202-555-0101": "+12025550101",
		"+1(202)555.0101": "+12025550101",
		"+12025550101":    "+12025550101",
		"5550101":         "5550101",
		"-.()":            "",
	} {
		if got := WithoutVisualSeparators(in); got != want {
			t.Errorf("WithoutVisualSeparators(%q) = %q, want %q", in, got, want)
		}
	}
}
