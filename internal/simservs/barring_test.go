package simservs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// barringDocument has the barring services of GSMA IR.92 2.3.9 as an
// operator provisions them, plus ACR and an allow list.
const barringDocument = `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"
          xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <incoming-communication-barring active="true">
    <cp:ruleset>
      <cp:rule id="baic">
        <cp:conditions><rule-deactivated/></cp:conditions>
        <cp:actions><allow>false</allow></cp:actions>
      </cp:rule>
      <cp:rule id="bic-roam">
        <cp:conditions><roaming/></cp:conditions>
        <cp:actions><allow>false</allow></cp:actions>
      </cp:rule>
      <cp:rule id="acr">
        <cp:conditions><anonymous/></cp:conditions>
        <cp:actions><allow>false</allow></cp:actions>
      </cp:rule>
      <cp:rule id="family">
        <cp:conditions>
          <cp:identity><cp:one id="tel:+12025550160"/></cp:identity>
        </cp:conditions>
        <cp:actions><allow>true</allow></cp:actions>
      </cp:rule>
    </cp:ruleset>
  </incoming-communication-barring>
  <outgoing-communication-barring active="false" password-required="true">
    <cp:ruleset>
      <cp:rule id="baoc">
        <cp:conditions><rule-deactivated/></cp:conditions>
        <cp:actions><allow>false</allow></cp:actions>
      </cp:rule>
      <cp:rule id="boic">
        <cp:conditions><international/></cp:conditions>
        <cp:actions><allow>false</allow></cp:actions>
      </cp:rule>
      <cp:rule id="boic-exhc">
        <cp:conditions><international-exHC/><media>video</media></cp:conditions>
        <cp:actions><allow>false</allow></cp:actions>
      </cp:rule>
    </cp:ruleset>
  </outgoing-communication-barring>
</simservs>
`

func TestParseCommunicationBarring(t *testing.T) {
	s, err := ParseSimservs([]byte(barringDocument))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	icb := s.IncomingCommunicationBarring
	if icb == nil || !icb.IsActive() || icb.PasswordRequired != nil || len(icb.Ruleset.Rules) != 4 {
		t.Fatalf("ICB = %+v", icb)
	}
	if r := icb.Ruleset.Rules[2]; r.Conditions.Anonymous == nil || *r.Actions.Allow {
		t.Errorf("ACR rule = %+v", r)
	}
	if r := icb.Ruleset.Rules[3]; r.Conditions.Identity.One[0].ID != "tel:+12025550160" || !*r.Actions.Allow {
		t.Errorf("allow list = %+v", r)
	}
	ocb := s.OutgoingCommunicationBarring
	if ocb == nil || ocb.IsActive() || ocb.PasswordRequired == nil || !*ocb.PasswordRequired {
		t.Fatalf("OCB = %+v", ocb)
	}
	if r := ocb.Ruleset.Rules[2]; r.Conditions.InternationalExHC == nil || !reflect.DeepEqual(r.Conditions.Media, []string{"video"}) {
		t.Errorf("BOIC-exHC = %+v", r.Conditions)
	}
}

func TestCommunicationBarringRoundTrip(t *testing.T) {
	s, err := ParseSimservs([]byte(barringDocument))
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

func TestMarshalCommunicationBarring(t *testing.T) {
	s := Simservs{OutgoingCommunicationBarring: &OutgoingCommunicationBarring{
		CommunicationBarring: CommunicationBarring{Ruleset: &Ruleset{Rules: []Rule{{
			ID: "baoc", Actions: &Actions{Allow: new(false)},
		}}}},
	}}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">` +
		`<outgoing-communication-barring><cp:ruleset><cp:rule id="baoc"><cp:actions><allow>false</allow></cp:actions></cp:rule></cp:ruleset></outgoing-communication-barring></simservs>`
	if got := string(s.Marshal()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestValidateCommunicationBarring(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replace   [2]string
		condition ConflictCondition
	}{
		{"diversion condition", [2]string{"<roaming/>", "<busy/>"}, ConstraintFailure},
		{"diversion action", [2]string{"<allow>true</allow>", "<forward-to><target>tel:+12025550161</target></forward-to>"}, ConstraintFailure},
		// xs:ID is unique in the whole document, across rule sets.
		{"id used in both rule sets", [2]string{`"boic"`, `"bic-roam"`}, UniquenessFailure},
		{"unknown condition", [2]string{"<international/>", "<request-name>INVITE</request-name>"}, SchemaValidationError},
	} {
		doc := strings.Replace(barringDocument, tc.replace[0], tc.replace[1], 1)
		if doc == barringDocument {
			t.Fatalf("%s: nothing replaced", tc.name)
		}
		s, err := ParseSimservs([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var ce *ConflictError
		if err := s.Validate(); !errors.As(err, &ce) || ce.Condition != tc.condition {
			t.Errorf("%s: Validate = %v, want %s", tc.name, err, tc.condition)
		}
	}
}

func TestValidateRefusesAnAllowActionInDiversion(t *testing.T) {
	doc := strings.Replace(diversionDocument, "<forward-to><target/></forward-to>", "<allow>false</allow>", 1)
	s, err := ParseSimservs([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var ce *ConflictError
	if err := s.Validate(); !errors.As(err, &ce) || ce.Condition != ConstraintFailure {
		t.Errorf("Validate = %v", err)
	}
}
