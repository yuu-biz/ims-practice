package sip

import (
	"reflect"
	"testing"
)

func TestPrivacy(t *testing.T) {
	p := ParsePrivacy("id; Header ;critical")
	if !reflect.DeepEqual(p, Privacy{PrivacyID, PrivacyHeader, PrivacyCritical}) {
		t.Fatalf("ParsePrivacy = %q", []string(p))
	}
	if !p.Has(PrivacyID) || p.Has(PrivacyNone) {
		t.Errorf("Has: id %v, none %v; want true, false", p.Has(PrivacyID), p.Has(PrivacyNone))
	}
	if got := p.Header(); got != (HeaderField{Name: "Privacy", Value: "id;header;critical"}) {
		t.Errorf("Header() = %+v", got)
	}
	if got := ParsePrivacy(""); len(got) != 0 {
		t.Errorf("ParsePrivacy of an empty value = %q", []string(got))
	}
}
