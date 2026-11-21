package simservs

import (
	"reflect"
	"testing"
)

// roundTrip marshals v, reads it back and checks that nothing changed.
func roundTrip[T any](t *testing.T, v T) []byte {
	t.Helper()
	b := MarshalElement(v)
	var got T
	if err := unmarshal(b, &got); err != nil {
		t.Fatalf("read back %s: %v", b, err)
	}
	if !reflect.DeepEqual(got, v) {
		t.Errorf("round trip changed the value\n got %#v\nwant %#v\nxml %s", got, v, b)
	}
	return b
}

func TestRulesetRoundTrip(t *testing.T) {
	rs := Ruleset{Rules: []Rule{
		{ID: "allow-family", Conditions: &Conditions{Identity: &Identity{
			One:  []One{{ID: "tel:+12025550111"}, {ID: "sip:+12025550112@ims.mnc001.mcc001.3gppnetwork.org;user=phone"}},
			Many: []Many{{Domain: "ims.mnc001.mcc001.3gppnetwork.org", Except: []Except{{ID: "sip:spam@ims.mnc001.mcc001.3gppnetwork.org"}}}},
		}}},
		{ID: "empty", Conditions: &Conditions{}, Actions: &Actions{}},
		{ID: "unconditional"},
	}}
	fixName(&rs)
	b := roundTrip(t, rs)
	want := `<cp:ruleset xmlns:cp="urn:ietf:params:xml:ns:common-policy">` +
		`<cp:rule id="allow-family"><cp:conditions><cp:identity>` +
		`<cp:one id="tel:+12025550111"/><cp:one id="sip:+12025550112@ims.mnc001.mcc001.3gppnetwork.org;user=phone"/>` +
		`<cp:many domain="ims.mnc001.mcc001.3gppnetwork.org"><cp:except id="sip:spam@ims.mnc001.mcc001.3gppnetwork.org"/></cp:many>` +
		`</cp:identity></cp:conditions></cp:rule>` +
		`<cp:rule id="empty"><cp:conditions/><cp:actions/></cp:rule>` +
		`<cp:rule id="unconditional"/></cp:ruleset>`
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
}

// fixName fills the XMLName fields that Unmarshal fills, so that a value
// built by hand compares equal to the value read back.
func fixName(rs *Ruleset) {
	rs.XMLName = xmlName(NamespaceCommonPolicy, "ruleset")
	for i := range rs.Rules {
		rs.Rules[i].XMLName = xmlName(NamespaceCommonPolicy, "rule")
	}
}

func TestParseSimservsNamesUnknownChildren(t *testing.T) {
	s, err := ParseSimservs([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:x="urn:example:vendor">
  <communication-diversion-serv-cap active="true"/>
  <extensions><x:ringback-tone/></extensions>
</simservs>`))
	if err != nil {
		t.Fatal(err)
	}
	want := []UnknownElement{
		{Name: xmlName(NamespaceSimservs, "communication-diversion-serv-cap")},
		{Name: xmlName(NamespaceSimservs, "extensions")},
	}
	if !reflect.DeepEqual(s.Unknown, want) {
		t.Errorf("Unknown = %v", s.Unknown)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestParseSimservsRefusesOtherDocuments(t *testing.T) {
	for _, in := range []string{
		`<simservs/>`, // no namespace
		`<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><cp:ruleset/></simservs>`,
		`<resource-lists xmlns="urn:ietf:params:xml:ns:resource-lists"/>`,
	} {
		if _, err := ParseSimservs([]byte(in)); err == nil {
			t.Errorf("%s: parsed", in)
		}
	}
}

func TestSimservTypeIsActive(t *testing.T) {
	// An absent active attribute means active (TS 24.623 6.1).
	if !(SimservType{}).IsActive() || !(SimservType{Active: new(true)}).IsActive() || (SimservType{Active: new(false)}).IsActive() {
		t.Error("IsActive")
	}
}
