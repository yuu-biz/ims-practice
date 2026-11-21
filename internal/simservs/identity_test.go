package simservs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const identityDocument = `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap">
  <originating-identity-presentation/>
  <originating-identity-presentation-restriction active="true">
    <default-behaviour>presentation-not-restricted</default-behaviour>
  </originating-identity-presentation-restriction>
  <terminating-identity-presentation active="false"/>
  <terminating-identity-presentation-restriction>
    <default-behaviour/>
  </terminating-identity-presentation-restriction>
  <communication-waiting active="true"/>
</simservs>
`

func TestParseIdentityServicesAndCommunicationWaiting(t *testing.T) {
	s, err := ParseSimservs([]byte(identityDocument))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if s.OriginatingIdentityPresentation == nil || !s.OriginatingIdentityPresentation.IsActive() {
		t.Errorf("OIP = %+v", s.OriginatingIdentityPresentation)
	}
	oir := s.OriginatingIdentityPresentationRestriction
	if oir == nil || !oir.IsActive() || oir.DefaultBehaviourOrDefault() != PresentationNotRestricted {
		t.Errorf("OIR = %+v", oir)
	}
	if s.TerminatingIdentityPresentation == nil || s.TerminatingIdentityPresentation.IsActive() {
		t.Errorf("TIP = %+v", s.TerminatingIdentityPresentation)
	}
	// An empty <default-behaviour/> takes the schema default.
	tir := s.TerminatingIdentityPresentationRestriction
	if tir == nil || !tir.IsActive() || tir.DefaultBehaviourOrDefault() != PresentationRestricted {
		t.Errorf("TIR = %+v", tir)
	}
	if s.CommunicationWaiting == nil || !s.CommunicationWaiting.IsActive() {
		t.Errorf("CW = %+v", s.CommunicationWaiting)
	}
}

func TestIdentityServicesRoundTrip(t *testing.T) {
	s, err := ParseSimservs([]byte(identityDocument))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseSimservs(s.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, s) {
		t.Errorf("parse, build, parse changed the value\n%s", s.Marshal())
	}
}

func TestMarshalIdentityServices(t *testing.T) {
	restricted := PresentationRestricted
	s := Simservs{
		OriginatingIdentityPresentationRestriction: &OriginatingIdentityPresentationRestriction{
			SimservType: SimservType{Active: new(true)}, DefaultBehaviour: &restricted},
		CommunicationWaiting: &CommunicationWaiting{SimservType: SimservType{Active: new(false)}},
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap">` +
		`<originating-identity-presentation-restriction active="true"><default-behaviour>presentation-restricted</default-behaviour></originating-identity-presentation-restriction>` +
		`<communication-waiting active="false"/></simservs>`
	if got := string(s.Marshal()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestValidateDefaultBehaviour(t *testing.T) {
	doc := strings.Replace(identityDocument, ">presentation-not-restricted<", ">sometimes<", 1)
	s, err := ParseSimservs([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var ce *ConflictError
	if err := s.Validate(); !errors.As(err, &ce) || ce.Condition != SchemaValidationError {
		t.Errorf("Validate = %v", err)
	}
}
