package simservs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// diversionDocument is the kind of document an operator provisions: one
// rule per diversion service, CFU still deactivated, the others active.
const diversionDocument = `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"
          xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <communication-diversion active="true">
    <NoReplyTimer>20</NoReplyTimer>
    <cp:ruleset>
      <cp:rule id="call-diversion-unconditional">
        <cp:conditions>
          <rule-deactivated/>
        </cp:conditions>
        <cp:actions>
          <forward-to><target/></forward-to>
        </cp:actions>
      </cp:rule>
      <cp:rule id="call-diversion-busy">
        <cp:conditions>
          <busy/>
        </cp:conditions>
        <cp:actions>
          <forward-to>
            <target>tel:+12025550150</target>
            <notify-caller>false</notify-caller>
          </forward-to>
        </cp:actions>
      </cp:rule>
      <cp:rule id="call-diversion-no-reply">
        <cp:conditions>
          <no-answer/>
          <media>audio</media>
        </cp:conditions>
        <cp:actions>
          <forward-to>
            <target>sip:+12025550151@ims.mnc001.mcc001.3gppnetwork.org;user=phone</target>
            <reveal-identity-to-caller>false</reveal-identity-to-caller>
            <reveal-served-user-identity-to-caller>not-reveal-GRUU</reveal-served-user-identity-to-caller>
            <notify-served-user>true</notify-served-user>
            <notify-served-user-on-outbound-call>true</notify-served-user-on-outbound-call>
            <reveal-identity-to-target>false</reveal-identity-to-target>
          </forward-to>
        </cp:actions>
      </cp:rule>
      <cp:rule id="call-diversion-not-reachable">
        <cp:conditions><not-reachable/></cp:conditions>
        <cp:actions><forward-to><target>tel:+12025550152</target></forward-to></cp:actions>
      </cp:rule>
      <cp:rule id="call-diversion-not-logged-in">
        <cp:conditions><not-registered/></cp:conditions>
        <cp:actions><forward-to><target>tel:+12025550152</target></forward-to></cp:actions>
      </cp:rule>
    </cp:ruleset>
  </communication-diversion>
</simservs>
`

func TestParseCommunicationDiversion(t *testing.T) {
	s, err := ParseSimservs([]byte(diversionDocument))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cd := s.CommunicationDiversion
	if cd == nil || !cd.IsActive() || cd.NoReplyTimer == nil || *cd.NoReplyTimer != 20 {
		t.Fatalf("communication-diversion = %+v", cd)
	}
	rules := cd.Ruleset.Rules
	if len(rules) != 5 {
		t.Fatalf("%d rules", len(rules))
	}
	if rules[0].Conditions.RuleDeactivated == nil || rules[0].Actions.ForwardTo.Target != "" {
		t.Errorf("CFU = %+v", rules[0])
	}
	cfb := rules[1].Actions.ForwardTo
	if cfb.Target != "tel:+12025550150" || cfb.NotifyCallerOrDefault() || cfb.RevealIdentityToCallerOrDefault() != RevealTrue {
		t.Errorf("CFB forward-to = %+v", cfb)
	}
	cfnr := rules[2]
	if cfnr.Conditions.NoAnswer == nil || !reflect.DeepEqual(cfnr.Conditions.Media, []string{"audio"}) {
		t.Errorf("CFNR conditions = %+v", cfnr.Conditions)
	}
	f := cfnr.Actions.ForwardTo
	if f.RevealIdentityToCallerOrDefault() != RevealFalse ||
		f.RevealServedUserIdentityToCallerOrDefault() != RevealNotRevealGRUU ||
		!f.NotifyServedUserOrDefault() || !f.NotifyServedUserOnOutboundCallOrDefault() ||
		f.RevealIdentityToTargetOrDefault() != RevealFalse || !f.NotifyCallerOrDefault() {
		t.Errorf("CFNR forward-to = %+v", f)
	}
	if rules[3].Conditions.NotReachable == nil || rules[4].Conditions.NotRegistered == nil {
		t.Errorf("CFNRc, CFNL = %+v, %+v", rules[3].Conditions, rules[4].Conditions)
	}
}

func TestCommunicationDiversionRoundTrip(t *testing.T) {
	s, err := ParseSimservs([]byte(diversionDocument))
	if err != nil {
		t.Fatal(err)
	}
	out := s.Marshal()
	again, err := ParseSimservs(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !reflect.DeepEqual(again, s) {
		t.Errorf("parse, build, parse changed the value\n%s", out)
	}
}

func TestMarshalCommunicationDiversion(t *testing.T) {
	s := Simservs{CommunicationDiversion: &CommunicationDiversion{
		NoReplyTimer: new(30),
		Ruleset: &Ruleset{Rules: []Rule{{
			ID:         "cfu",
			Conditions: &Conditions{},
			Actions:    &Actions{ForwardTo: &ForwardTo{Target: "tel:+12025550153", NotifyCaller: new(true)}},
		}}},
	}}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">` +
		`<communication-diversion><NoReplyTimer>30</NoReplyTimer><cp:ruleset><cp:rule id="cfu"><cp:conditions/>` +
		`<cp:actions><forward-to><target>tel:+12025550153</target><notify-caller>true</notify-caller></forward-to></cp:actions>` +
		`</cp:rule></cp:ruleset></communication-diversion></simservs>`
	if got := string(s.Marshal()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMarshalElementOfARule(t *testing.T) {
	// The fragment an XCAP client PUTs to change one rule carries its
	// own declarations (RFC 4825 7.4).
	r := Rule{ID: "cfb", Conditions: &Conditions{Busy: &Empty{}},
		Actions: &Actions{ForwardTo: &ForwardTo{Target: "tel:+12025550150"}}}
	want := `<cp:rule xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy" id="cfb">` +
		`<cp:conditions><busy/></cp:conditions><cp:actions><forward-to><target>tel:+12025550150</target></forward-to></cp:actions></cp:rule>`
	if got := string(MarshalElement(r)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestValidateCommunicationDiversion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replace   [2]string
		condition ConflictCondition
	}{
		{"timer below range", [2]string{"<NoReplyTimer>20<", "<NoReplyTimer>4<"}, SchemaValidationError},
		{"timer above range", [2]string{"<NoReplyTimer>20<", "<NoReplyTimer>181<"}, SchemaValidationError},
		{"duplicate rule id", [2]string{`"call-diversion-busy"`, `"call-diversion-unconditional"`}, UniquenessFailure},
		{"rule id not an NCName", [2]string{`"call-diversion-busy"`, `"1busy"`}, SchemaValidationError},
		{"target not a URI", [2]string{"tel:+12025550150", "12025550150"}, SchemaValidationError},
		{"bad reveal option", [2]string{">not-reveal-GRUU<", ">sometimes<"}, SchemaValidationError},
		{"barring condition", [2]string{"<busy/>", "<roaming/>"}, ConstraintFailure},
		{"unsupported condition", [2]string{"<busy/>", "<presence-status>meeting</presence-status>"}, ConstraintFailure},
		{"unknown simservs condition", [2]string{"<busy/>", "<busy-ish/>"}, SchemaValidationError},
		{"unknown simservs action", [2]string{"<forward-to><target/></forward-to>", "<forward/>"}, SchemaValidationError},
		{"empty identity", [2]string{"<busy/>", "<cp:identity/>"}, SchemaValidationError},
	} {
		doc := strings.Replace(diversionDocument, tc.replace[0], tc.replace[1], 1)
		if doc == diversionDocument {
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

func TestValidateAllowsConditionsOfOtherNamespaces(t *testing.T) {
	// The schema lets other namespaces in (RFC 4745 13); such a
	// condition never matches (RFC 4745 7).
	doc := strings.Replace(diversionDocument, "<busy/>", `<busy/><x:weather xmlns:x="urn:example:vendor">rain</x:weather>`, 1)
	s, err := ParseSimservs([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	// The same evaluation tells the two documents apart: the rule of
	// <busy/> diverts on busy, until a condition it cannot know joins
	// it.
	busy := func(cd *CommunicationDiversion) string {
		if r := EvaluateDiversion(cd, OnBusy, CallInfo{}); r.Rule != nil {
			return r.Rule.ID
		}
		return ""
	}
	if got := busy(mustSimservs(t, diversionDocument).CommunicationDiversion); got != "call-diversion-busy" {
		t.Fatalf("on busy without the foreign condition, rule %q matched, want call-diversion-busy", got)
	}
	if got := busy(s.CommunicationDiversion); got == "call-diversion-busy" {
		t.Errorf("on busy with a condition of another namespace, rule %q matched, want it not to", got)
	}
}

func TestUniquenessFailureNamesTheField(t *testing.T) {
	doc := strings.Replace(diversionDocument, `"call-diversion-busy"`, `"call-diversion-unconditional"`, 1)
	s, _ := ParseSimservs([]byte(doc))
	var ce *ConflictError
	if !errors.As(s.Validate(), &ce) {
		t.Fatal("no conflict")
	}
	want := "simservs/communication-diversion/cp:ruleset/cp:rule%5B2%5D/@id?xmlns(cp=urn:ietf:params:xml:ns:common-policy)"
	if ce.Field != want {
		t.Errorf("Field = %s", ce.Field)
	}
}
