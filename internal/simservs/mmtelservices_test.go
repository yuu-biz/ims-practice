package simservs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// mmtelDocument is the service data an operator provisions in the HSS
// for one subscriber: every service of GSMA IR.92 2.3 for the network,
// the rules deactivated (IR.92 2.3.2), OIR in the temporary mode.
const mmtelDocument = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <complete-originating-identity-presentation>
    <originating-identity-presentation active="true"/>
    <operator-originating-identity-presentation authorized="true"/>
  </complete-originating-identity-presentation>
  <complete-originating-identity-restriction>
    <originating-identity-presentation-restriction active="true">
      <default-behaviour>presentation-not-restricted</default-behaviour>
    </originating-identity-presentation-restriction>
    <operator-originating-identity-presentation-restriction authorized="true">
      <mode>temporary</mode>
      <restriction>only-identity</restriction>
    </operator-originating-identity-presentation-restriction>
  </complete-originating-identity-restriction>
  <complete-terminating-identity-presentation>
    <terminating-identity-presentation/>
    <operator-terminating-identity-presentation authorized="true">
      <restriction-override>override-not-active</restriction-override>
    </operator-terminating-identity-presentation>
  </complete-terminating-identity-presentation>
  <complete-terminating-identity-restriction>
    <terminating-identity-presentation-restriction active="false"/>
    <operator-terminating-identity-presentation-restriction authorized="false">
      <mode>permanent</mode>
    </operator-terminating-identity-presentation-restriction>
  </complete-terminating-identity-restriction>
  <complete-communication-diversion>
    <communication-diversion>
      <cp:ruleset>
        <cp:rule id="cfu">
          <cp:conditions><rule-deactivated/></cp:conditions>
          <cp:actions><forward-to><target/></forward-to></cp:actions>
        </cp:rule>
        <cp:rule id="cfnr">
          <cp:conditions><no-answer/><rule-deactivated/></cp:conditions>
          <cp:actions><forward-to><target/></forward-to></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </communication-diversion>
    <operator-communication-diversion authorized="true">
      <communication-retention-on-invocation>clear-communication-on-invocation-of-diversion</communication-retention-on-invocation>
      <retention-when-diverting-rejected-at-diverted-to-user>no-action-at-diverting-user</retention-when-diverting-rejected-at-diverted-to-user>
      <total-number-of-diversions-for-each-communication>5</total-number-of-diversions-for-each-communication>
      <cdiv-indication-timer>0</cdiv-indication-timer>
      <communication-forwarding-on-no-reply-timer>20</communication-forwarding-on-no-reply-timer>
    </operator-communication-diversion>
  </complete-communication-diversion>
  <complete-communication-waiting>
    <communication-waiting active="true"/>
    <operator-communication-waiting authorized="true">
      <calling-user-receives-notification-his-call-is-waiting>false</calling-user-receives-notification-his-call-is-waiting>
    </operator-communication-waiting>
  </complete-communication-waiting>
  <complete-communication-hold>
    <operator-communication-hold authorized="true"/>
  </complete-communication-hold>
  <complete-communication-barring>
    <incoming-communication-barring>
      <cp:ruleset>
        <cp:rule id="baic">
          <cp:conditions><rule-deactivated/></cp:conditions>
          <cp:actions><allow>false</allow></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </incoming-communication-barring>
    <outgoing-communication-barring>
      <cp:ruleset>
        <cp:rule id="boic">
          <cp:conditions><international/><rule-deactivated/></cp:conditions>
          <cp:actions><allow>false</allow></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </outgoing-communication-barring>
    <operator-incoming-communication-barring authorized="true"/>
    <operator-outgoing-communication-barring authorized="true"/>
  </complete-communication-barring>
  <complete-customized-alerting-tone>
    <operator-customized-alerting-tone authorized="false"/>
  </complete-customized-alerting-tone>
</MMTelServices>`

func TestParseMMTelServices(t *testing.T) {
	m, err := ParseMMTelServices([]byte(mmtelDocument))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c := m.CompleteOriginatingIdentityRestriction; c == nil ||
		c.OperatorOriginatingIdentityPresentationRestriction.Mode != ModeTemporary ||
		c.OperatorOriginatingIdentityPresentationRestriction.Restriction != RestrictOnlyIdentity ||
		c.OriginatingIdentityPresentationRestriction.DefaultBehaviourOrDefault() != PresentationNotRestricted {
		t.Errorf("OIR = %+v", c)
	}
	if c := m.CompleteTerminatingIdentityRestriction; c == nil || c.OperatorTerminatingIdentityPresentationRestriction.Authorized {
		t.Errorf("TIR = %+v", c)
	}
	cdiv := m.CompleteCommunicationDiversion
	if cdiv == nil || !cdiv.OperatorCommunicationDiversion.Authorized ||
		cdiv.OperatorCommunicationDiversion.TotalNumberOfDiversionsForEachCommunication != 5 ||
		len(cdiv.CommunicationDiversion.Ruleset.Rules) != 2 {
		t.Fatalf("CDIV = %+v", cdiv)
	}
	// Without a user NoReplyTimer the operator default applies (TS
	// 24.604 4.8.1).
	if got := cdiv.NoReplyTimer(); got != 20 {
		t.Errorf("NoReplyTimer() = %d", got)
	}
	cdiv.CommunicationDiversion.NoReplyTimer = new(40)
	if got := cdiv.NoReplyTimer(); got != 40 {
		t.Errorf("NoReplyTimer() with a user value = %d", got)
	}
	if m.CompleteCommunicationHold == nil || !m.CompleteCommunicationHold.OperatorCommunicationHold.Authorized {
		t.Errorf("HOLD = %+v", m.CompleteCommunicationHold)
	}
	cb := m.CompleteCommunicationBarring
	if cb == nil || cb.OutgoingCommunicationBarring.Ruleset.Rules[0].ID != "boic" || !cb.OperatorOutgoingCommunicationBarring.Authorized {
		t.Errorf("CB = %+v", cb)
	}
	if len(m.Unknown) != 1 || m.Unknown[0].Name.Local != "complete-customized-alerting-tone" {
		t.Errorf("Unknown = %v", m.Unknown)
	}
}

func TestMMTelServicesRoundTrip(t *testing.T) {
	m, err := ParseMMTelServices([]byte(mmtelDocument))
	if err != nil {
		t.Fatal(err)
	}
	out := m.Marshal()
	if strings.HasPrefix(string(out), "<?xml") {
		t.Error("the element inside ServiceData has an XML declaration")
	}
	again, err := ParseMMTelServices(out)
	if err != nil {
		t.Fatal(err)
	}
	// Marshal leaves out the services that are not modelled.
	m.Unknown = nil
	if !reflect.DeepEqual(again, m) {
		t.Errorf("parse, build, parse changed the value\n%s", out)
	}
}

func TestMMTelServicesSimservs(t *testing.T) {
	m, err := ParseMMTelServices([]byte(mmtelDocument))
	if err != nil {
		t.Fatal(err)
	}
	s := m.Simservs()
	if s.OriginatingIdentityPresentation == nil || s.OriginatingIdentityPresentationRestriction == nil ||
		s.TerminatingIdentityPresentation == nil || s.TerminatingIdentityPresentationRestriction == nil ||
		s.CommunicationDiversion == nil || s.CommunicationWaiting == nil ||
		s.IncomingCommunicationBarring == nil || s.OutgoingCommunicationBarring == nil {
		t.Fatalf("Simservs() = %+v", s)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">` +
		`<originating-identity-presentation active="true"/>`
	if got := string(s.Marshal()); !strings.HasPrefix(got, want) {
		t.Errorf("Marshal() = %s", got)
	}
}

func TestValidateMMTelServices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace [2]string
	}{
		{"user part", [2]string{`id="boic"`, `id="baic"`}},
		{"retention", [2]string{">clear-communication-on-invocation-of-diversion<", ">clear<"}},
		{"indication timer", [2]string{"<cdiv-indication-timer>0<", "<cdiv-indication-timer>61<"}},
		{"no reply timer", [2]string{"reply-timer>20<", "reply-timer>181<"}},
		{"mode", [2]string{"<mode>permanent<", "<mode>forever<"}},
	} {
		doc := strings.Replace(mmtelDocument, tc.replace[0], tc.replace[1], 1)
		if doc == mmtelDocument {
			t.Fatalf("%s: nothing replaced", tc.name)
		}
		m, err := ParseMMTelServices([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var ce *ConflictError
		if err := m.Validate(); !errors.As(err, &ce) {
			t.Errorf("%s: Validate = %v", tc.name, err)
		}
	}
}
