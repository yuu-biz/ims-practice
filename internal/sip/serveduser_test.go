package sip

import (
	"reflect"
	"testing"
)

func TestPServedUser(t *testing.T) {
	tests := []struct {
		in   string
		want PServedUser
		out  string
	}{
		// The example of RFC 5502 6.
		{"<sip:user@example.com>; sescase=orig; regstate=reg",
			PServedUser{URI: "sip:user@example.com", SessionCase: SessionCaseOrig, RegState: RegStateRegistered},
			"<sip:user@example.com>;sescase=orig;regstate=reg"},
		{"<sip:user@example.com>;sescase=term;regstate=unreg",
			PServedUser{URI: "sip:user@example.com", SessionCase: SessionCaseTerm, RegState: RegStateUnregistered},
			"<sip:user@example.com>;sescase=term;regstate=unreg"},
		// The examples of RFC 8498 6.2.
		{"<sip:user@example.com>; orig-cdiv; regstate=reg",
			PServedUser{URI: "sip:user@example.com", SessionCase: SessionCaseOrigCDIV, RegState: RegStateRegistered},
			"<sip:user@example.com>;orig-cdiv;regstate=reg"},
		{"<sip:user@example.com>; orig-cdiv",
			PServedUser{URI: "sip:user@example.com", SessionCase: SessionCaseOrigCDIV},
			"<sip:user@example.com>;orig-cdiv"},
		{"<tel:+12025550101>;x=y",
			PServedUser{URI: "tel:+12025550101", Params: Params{{"x", "y"}}},
			"<tel:+12025550101>;x=y"},
	}
	for _, tt := range tests {
		got, err := ParsePServedUser(tt.in)
		if err != nil {
			t.Errorf("ParsePServedUser(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParsePServedUser(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if s := got.String(); s != tt.out {
			t.Errorf("String() = %q, want %q", s, tt.out)
		}
	}
	for _, in := range []string{"", "<sip:user@example.com>;sescase=both", "<sip:user@example.com>;regstate=maybe"} {
		if _, err := ParsePServedUser(in); err == nil {
			t.Errorf("ParsePServedUser(%q) succeeded", in)
		}
	}
}
