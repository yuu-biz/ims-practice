package simservs

import (
	"reflect"
	"strings"
	"testing"
)

func TestUserDocumentShowsTheUserParts(t *testing.T) {
	mmtel := mustDocument(t, mmtelDocument)
	user, err := UserDocument(mmtel)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSimservs(user.Bytes())
	if err != nil {
		t.Fatalf("%v\n%s", err, user.Bytes())
	}
	m, _ := ParseMMTelServices([]byte(mmtelDocument))
	want := m.Simservs()
	want.XMLName = got.XMLName
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UserDocument:\n%s", user.Bytes())
	}
	if strings.Contains(string(user.Bytes()), "operator-") {
		t.Errorf("operator parts are visible:\n%s", user.Bytes())
	}
	if _, err := UserDocument(mustDocument(t, storedDocument)); conflictOf(err) != SchemaValidationError {
		t.Errorf("UserDocument of a simservs document: %v", err)
	}
}

func TestUserDocumentCarriesTheDeclarationsOfTheCompleteElement(t *testing.T) {
	mmtel := mustDocument(t, `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap">
  <complete-communication-diversion xmlns:p="urn:ietf:params:xml:ns:common-policy">
    <communication-diversion><p:ruleset><p:rule id="cfu"/></p:ruleset></communication-diversion>
    <operator-communication-diversion authorized="true"/>
  </complete-communication-diversion>
</MMTelServices>`)
	user, err := UserDocument(mmtel)
	if err != nil {
		t.Fatal(err)
	}
	want := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap">
  <communication-diversion xmlns:p="urn:ietf:params:xml:ns:common-policy"><p:ruleset><p:rule id="cfu"/></p:ruleset></communication-diversion>
</simservs>`
	if got := string(user.RootElement()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestXCAPChangeThroughMMTelServices runs what the XCAP server of the
// MMTel AS does for a PUT: show the user parts, change one rule, check
// the result, and write it back into the document for the HSS.
func TestXCAPChangeThroughMMTelServices(t *testing.T) {
	stored := mustDocument(t, mmtelDocument)
	before, err := UserDocument(stored)
	if err != nil {
		t.Fatal(err)
	}
	after := before.Clone()
	sel := mustSelector(t, `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"]`, cpQuery)
	rule := MarshalElement(Rule{ID: "cfu", Actions: &Actions{ForwardTo: &ForwardTo{Target: "tel:+12025550153"}}})
	if _, err := after.PutElement(sel, rule); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSimservsDocument(after); err != nil {
		t.Fatal(err)
	}
	if err := CheckOwnerChange(before, after); err != nil {
		t.Fatal(err)
	}
	next := stored.Clone()
	if err := SetUserDocument(next, after); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMMTelServicesDocument(next); err != nil {
		t.Fatal(err)
	}

	m, err := ParseMMTelServices(next.RootElement())
	if err != nil {
		t.Fatal(err)
	}
	cfu := m.CompleteCommunicationDiversion.CommunicationDiversion.Ruleset.Rules[0]
	if cfu.Conditions != nil || cfu.Actions.ForwardTo.Target != "tel:+12025550153" {
		t.Errorf("CFU after the change = %+v", cfu)
	}
	out := string(next.RootElement())
	// The parts the user cannot see, and the services this package does
	// not model, are kept.
	for _, keep := range []string{
		"<total-number-of-diversions-for-each-communication>5</total-number-of-diversions-for-each-communication>",
		`<operator-customized-alerting-tone authorized="false"/>`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %s", keep)
		}
	}
	if strings.HasPrefix(out, "<?xml") {
		t.Error("RootElement has an XML declaration")
	}
	if !strings.Contains(string(stored.RootElement()), "<rule-deactivated/>") || strings.Count(out, "<rule-deactivated/>") != 3 {
		t.Errorf("the stored document changed, or the change did not land:\n%s", out)
	}
}

func TestSetUserDocumentRefusals(t *testing.T) {
	stored := mustDocument(t, mmtelDocument)
	user, _ := UserDocument(stored)
	text := string(user.Bytes())

	// TIR is not authorized: the unchanged part passes, a change does
	// not (TS 24.623 5.3.2.4).
	if err := SetUserDocument(stored.Clone(), user); err != nil {
		t.Errorf("unchanged document: %v", err)
	}
	changed := strings.Replace(text, `<terminating-identity-presentation-restriction active="false"/>`,
		`<terminating-identity-presentation-restriction active="true"/>`, 1)
	if changed == text {
		t.Fatal("nothing replaced")
	}
	next := stored.Clone()
	if err := SetUserDocument(next, mustDocument(t, changed)); conflictOf(err) != ConstraintFailure {
		t.Errorf("change to a service that is not authorized: %v", err)
	}
	if string(next.Bytes()) != string(stored.Bytes()) {
		t.Error("a refused change reached the document")
	}

	// A user part with other prefixes is the same content.
	prefixed := strings.Replace(strings.ReplaceAll(text, "cp:", "policy:"), "xmlns:cp=", "xmlns:policy=", 1)
	if err := SetUserDocument(stored.Clone(), mustDocument(t, prefixed)); err != nil {
		t.Errorf("same content with other prefixes: %v", err)
	}

	other := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><x-service/></simservs>`
	if err := SetUserDocument(stored.Clone(), mustDocument(t, other)); conflictOf(err) != ConstraintFailure {
		t.Errorf("a service that is not provisioned: %v", err)
	}
	if err := SetUserDocument(stored.Clone(), stored); conflictOf(err) != SchemaValidationError {
		t.Errorf("not a simservs document: %v", err)
	}
}

func TestSetUserDocumentDeclaresThePrefixesItNeeds(t *testing.T) {
	// The cp prefix of the user document is declared on its root, which
	// does not go into MMTelServices; the part has to bring it along.
	stored := mustDocument(t, `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap">
  <complete-communication-diversion>
    <communication-diversion/>
    <operator-communication-diversion authorized="true"/>
  </complete-communication-diversion>
</MMTelServices>`)
	user := mustDocument(t, `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <communication-diversion><cp:ruleset><cp:rule id="cfb"><cp:conditions><busy/></cp:conditions></cp:rule></cp:ruleset></communication-diversion>
</simservs>`)
	if err := SetUserDocument(stored, user); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.RootElement()), `<communication-diversion xmlns:cp="urn:ietf:params:xml:ns:common-policy"><cp:ruleset>`) {
		t.Errorf("document:\n%s", stored.RootElement())
	}
	if _, err := ParseDocument(stored.Bytes()); err != nil {
		t.Errorf("not well formed: %v", err)
	}
}
