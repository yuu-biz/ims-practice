package simservs

import (
	"errors"
	"strings"
	"testing"
)

func conflictOf(err error) ConflictCondition {
	var ce *ConflictError
	if errors.As(err, &ce) {
		return ce.Condition
	}
	return ""
}

func TestPutElementReplacesARule(t *testing.T) {
	// The UE activates CFU: it replaces the provisioned rule without
	// <rule-deactivated/> and with a target (GSMA IR.92 2.3.2). The body
	// uses the prefix cp that the stored document declares.
	d := mustDocument(t, storedDocument)
	sel := mustSelector(t, `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"]`, cpQuery)
	body := `<cp:rule id="cfu"><cp:conditions/><cp:actions><forward-to><target>tel:+12025550153</target></forward-to></cp:actions></cp:rule>`
	created, err := d.PutElement(sel, []byte(body))
	if err != nil || created {
		t.Fatalf("PutElement = %v, %v", created, err)
	}
	if got, _, _ := d.Get(sel); string(got) != body {
		t.Errorf("GET after PUT = %s", got)
	}
	want := strings.Replace(storedDocument, `<cp:rule id="cfu">
        <cp:conditions><rule-deactivated/></cp:conditions>
        <cp:actions><forward-to><target/></forward-to></cp:actions>
      </cp:rule>`, body, 1)
	if got := string(d.Bytes()) + "\n"; got != want {
		t.Errorf("document after PUT:\n%s", got)
	}
	if err := ValidateSimservsDocument(d); err != nil {
		t.Errorf("ValidateSimservsDocument: %v", err)
	}
}

func TestPutElementCreatesARuleAfterTheLastRule(t *testing.T) {
	d := mustDocument(t, storedDocument)
	sel := mustSelector(t, `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfnl"]`, cpQuery)
	body := `<cp:rule id="cfnl"><cp:conditions><not-registered/></cp:conditions></cp:rule>`
	created, err := d.PutElement(sel, []byte(body))
	if err != nil || !created {
		t.Fatalf("PutElement = %v, %v", created, err)
	}
	if !strings.Contains(string(d.Bytes()), "</cp:rule>"+body+"\n    </cp:ruleset>") {
		t.Errorf("not inserted right after the last rule:\n%s", d.Bytes())
	}
}

// insertDocument checks the placement rules of RFC 4825 8.2.3.
const insertDocument = `<list xmlns="urn:example:list">
 <item n="1"/>
 <item n="2"/>
 <!-- notes -->
 <note n="1"/>
</list>`

func TestPutElementPlacesNewElements(t *testing.T) {
	for _, tc := range []struct {
		name, selector, body, want string
	}{
		{"earliest last", `list/item[@n="3"]`, `<item n="3"/>`,
			"<list xmlns=\"urn:example:list\">\n <item n=\"1\"/>\n <item n=\"2\"/><item n=\"3\"/>\n <!-- notes -->\n <note n=\"1\"/>\n</list>"},
		{"earliest last with a position", `list/item[3][@n="3"]`, `<item n="3"/>`,
			"<list xmlns=\"urn:example:list\">\n <item n=\"1\"/>\n <item n=\"2\"/><item n=\"3\"/>\n <!-- notes -->\n <note n=\"1\"/>\n</list>"},
		{"a new name goes at the end", `list/extra`, `<extra/>`,
			"<list xmlns=\"urn:example:list\">\n <item n=\"1\"/>\n <item n=\"2\"/>\n <!-- notes -->\n <note n=\"1\"/>\n<extra/></list>"},
		{"first of its name", `list/note[1][@n="0"]`, `<note n="0"/>`,
			"<list xmlns=\"urn:example:list\">\n <item n=\"1\"/>\n <item n=\"2\"/>\n <!-- notes -->\n <note n=\"0\"/><note n=\"1\"/>\n</list>"},
		{"nth of any name", `list/*[2][@n="9"]`, `<note n="9"/>`,
			"<list xmlns=\"urn:example:list\">\n <item n=\"1\"/><note n=\"9\"/>\n <item n=\"2\"/>\n <!-- notes -->\n <note n=\"1\"/>\n</list>"},
		{"first of any name", `list/*[1][@n="0"]`, `<item n="0"/>`,
			"<list xmlns=\"urn:example:list\">\n <item n=\"0\"/><item n=\"1\"/>\n <item n=\"2\"/>\n <!-- notes -->\n <note n=\"1\"/>\n</list>"},
	} {
		d := mustDocument(t, insertDocument)
		sel, err := ParseNodeSelector(tc.selector, "", "urn:example:list")
		if err != nil {
			t.Fatal(err)
		}
		created, err := d.PutElement(sel, []byte(tc.body))
		if err != nil || !created {
			t.Errorf("%s: PutElement = %v, %v", tc.name, created, err)
			continue
		}
		if got := string(d.root.serialize()); got != tc.want {
			t.Errorf("%s:\n%s\nwant\n%s", tc.name, got, tc.want)
		}
	}
}

func TestPutElementReplacesTheRoot(t *testing.T) {
	d := mustDocument(t, storedDocument)
	body := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-waiting/></simservs>`
	created, err := d.PutElement(mustSelector(t, "simservs", ""), []byte(body))
	if err != nil || created {
		t.Fatalf("PutElement = %v, %v", created, err)
	}
	if string(d.root.serialize()) != body {
		t.Errorf("root = %s", d.root.serialize())
	}
}

func TestPutElementFailuresLeaveTheDocument(t *testing.T) {
	rule := `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"]`
	for _, tc := range []struct {
		name, selector, body string
		want                 ConflictCondition
	}{
		{"no parent", `simservs/communication-barring/cp:ruleset/cp:rule[@id="x"]`, `<cp:rule id="x"/>`, NoParent},
		{"not XML", rule, `<cp:rule id="cfu">`, NotXMLFrag},
		{"two elements", rule, `<cp:rule id="cfu"/><cp:rule id="cfu"/>`, NotXMLFrag},
		{"a comment besides the element", rule, `<!-- x --><cp:rule id="cfu"/>`, NotXMLFrag},
		{"undeclared prefix", rule, `<p:rule id="cfu"/>`, NotXMLFrag},
		{"not UTF-8", rule, "<cp:rule id=\"\xff\"/>", NotUTF8},
		// The new rule would not be selected by its old id.
		{"replacement changes the id", rule, `<cp:rule id="other"/>`, CannotInsert},
		{"creation with another name", `simservs/communication-diversion/cp:ruleset/cp:rule[@id="new"]`, `<cp:other id="new"/>`, CannotInsert},
		{"position too far", `simservs/communication-diversion/cp:ruleset/cp:rule[4]`, `<cp:rule id="x"/>`, CannotInsert},
		{"a second root", "other", `<other/>`, CannotInsert},
		{"an attribute selector", "simservs/communication-diversion/@active", `<x/>`, NotXMLFrag},
	} {
		d := mustDocument(t, storedDocument)
		before := string(d.Bytes())
		_, err := d.PutElement(mustSelector(t, tc.selector, cpQuery), []byte(tc.body))
		if got := conflictOf(err); got != tc.want {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
		if string(d.Bytes()) != before {
			t.Errorf("%s: the document changed", tc.name)
		}
	}
	d := mustDocument(t, storedDocument)
	if _, err := d.PutElement(mustSelector(t, "simservs/namespace::*", ""), []byte(`<x/>`)); !errors.Is(err, ErrNamespaceSelector) {
		t.Errorf("namespace selector: %v", err)
	}
}

func TestPutAttribute(t *testing.T) {
	d := mustDocument(t, storedDocument)
	sel := mustSelector(t, "simservs/communication-waiting/@active", "")
	created, err := d.PutAttribute(sel, []byte(`"true"`))
	if err != nil || created {
		t.Fatalf("PutAttribute = %v, %v", created, err)
	}
	if got, ct, _ := d.Get(sel); string(got) != `"true"` || ct != AttributeMIMEType {
		t.Errorf("GET = %s %s", got, ct)
	}

	// A new attribute of another namespace takes a prefix in scope.
	sel = mustSelector(t, "simservs/extensions/v:ringback/@v:volume", "xmlns(v=urn:example:vendor)")
	if created, err := d.PutAttribute(sel, []byte(`'loud'`)); err != nil || !created {
		t.Fatalf("PutAttribute = %v, %v", created, err)
	}
	if !strings.Contains(string(d.Bytes()), `x:tone="classic" x:volume="loud"`) {
		t.Errorf("document:\n%s", d.Bytes())
	}

	// The prefix of the query is declared when none is in scope.
	sel = mustSelector(t, "simservs/communication-waiting/@q:note", "xmlns(q=urn:example:q)")
	if _, err := d.PutAttribute(sel, []byte(`"a&amp;b"`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Bytes()), `<communication-waiting xmlns:q="urn:example:q" active="true" q:note="a&amp;b"/>`) {
		t.Errorf("document:\n%s", d.Bytes())
	}
}

func TestPutAttributeFailures(t *testing.T) {
	for _, tc := range []struct {
		name, selector, body string
		want                 ConflictCondition
	}{
		{"unquoted", "simservs/communication-waiting/@active", `true`, NotXMLAttValue},
		{"stray quote", "simservs/communication-waiting/@active", `"a"b"`, NotXMLAttValue},
		{"no element", "simservs/originating-identity-presentation/@active", `"true"`, NoParent},
		// The rule would no longer be selected by its id (RFC 4825 7.7).
		{"replaces the value it is selected by", `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfb"]/@id`, `"cfx"`, CannotInsert},
		{"an element selector", "simservs/communication-waiting", `"x"`, NotXMLAttValue},
	} {
		d := mustDocument(t, storedDocument)
		before := string(d.Bytes())
		_, err := d.PutAttribute(mustSelector(t, tc.selector, cpQuery), []byte(tc.body))
		if got := conflictOf(err); got != tc.want {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
		if string(d.Bytes()) != before {
			t.Errorf("%s: the document changed", tc.name)
		}
	}
}

func TestDelete(t *testing.T) {
	d := mustDocument(t, storedDocument)
	sel := mustSelector(t, `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"]`, cpQuery)
	if err := d.Delete(sel); err != nil {
		t.Fatal(err)
	}
	// The whitespace around the element stays (RFC 4825 8.4).
	if !strings.Contains(string(d.Bytes()), "<cp:ruleset>\n      \n      <cp:rule id=\"cfb\">") {
		t.Errorf("document:\n%s", d.Bytes())
	}
	if err := d.Delete(sel); !errors.Is(err, ErrNoMatch) {
		t.Errorf("second DELETE: %v", err)
	}
	if err := d.Delete(mustSelector(t, "simservs/communication-diversion/@active", "")); err != nil {
		t.Errorf("DELETE of an attribute: %v", err)
	}
	if strings.Contains(string(d.Bytes()), `<communication-diversion active`) {
		t.Errorf("attribute still there")
	}
}

func TestDeleteFailures(t *testing.T) {
	for _, tc := range []struct {
		name, selector string
		want           ConflictCondition
	}{
		// After the DELETE, rule[1] would select the other rule.
		{"not idempotent", `simservs/communication-diversion/cp:ruleset/cp:rule[1]`, CannotDelete},
		{"the root", "simservs", CannotDelete},
	} {
		d := mustDocument(t, storedDocument)
		before := string(d.Bytes())
		if got := conflictOf(d.Delete(mustSelector(t, tc.selector, cpQuery))); got != tc.want {
			t.Errorf("%s: %s", tc.name, got)
		}
		if string(d.Bytes()) != before {
			t.Errorf("%s: the document changed", tc.name)
		}
	}
	d := mustDocument(t, storedDocument)
	if err := d.Delete(mustSelector(t, "simservs/communication-waiting/@other", "")); !errors.Is(err, ErrNoMatch) {
		t.Errorf("missing attribute: %v", err)
	}
	if err := d.Delete(mustSelector(t, "simservs/namespace::*", "")); !errors.Is(err, ErrNamespaceSelector) {
		t.Errorf("namespace selector: %v", err)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	d := mustDocument(t, storedDocument)
	c := d.Clone()
	if err := c.Delete(mustSelector(t, "simservs/communication-waiting", "")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Bytes()), "communication-waiting") {
		t.Error("the change reached the original")
	}
}

func TestValidateSimservsDocumentAfterAPut(t *testing.T) {
	d := mustDocument(t, storedDocument)
	sel := mustSelector(t, `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"]/cp:conditions`, cpQuery)
	if _, err := d.PutElement(sel, []byte(`<cp:conditions><roaming/></cp:conditions>`)); err != nil {
		t.Fatal(err)
	}
	if got := conflictOf(ValidateSimservsDocument(d)); got != ConstraintFailure {
		t.Errorf("ValidateSimservsDocument = %s", got)
	}
	other := mustDocument(t, `<resource-lists xmlns="urn:ietf:params:xml:ns:resource-lists"/>`)
	if got := conflictOf(ValidateSimservsDocument(other)); got != SchemaValidationError {
		t.Errorf("other document = %s", got)
	}
}

func TestCheckOwnerChange(t *testing.T) {
	before := mustDocument(t, storedDocument)
	for _, tc := range []struct {
		name    string
		replace [2]string
		ok      bool
	}{
		{"change a rule", [2]string{"tel:+12025550150", "tel:+12025550154"}, true},
		{"change an attribute", [2]string{`<communication-waiting active="false"/>`, `<communication-waiting active="true"/>`}, true},
		{"add a second copy of a service", [2]string{"<simservs xmlns=\"http://uri.etsi.org/ngn/params/xml/simservs/xcap\" xmlns:cp=\"urn:ietf:params:xml:ns:common-policy\">",
			"<simservs xmlns=\"http://uri.etsi.org/ngn/params/xml/simservs/xcap\" xmlns:cp=\"urn:ietf:params:xml:ns:common-policy\"><communication-waiting active=\"false\"/>"}, false},
		{"remove a service", [2]string{`<communication-waiting active="false"/>`, ``}, false},
		{"add a service", [2]string{`<communication-waiting active="false"/>`, `<communication-waiting active="false"/><originating-identity-presentation/>`}, false},
		{"remove an attribute", [2]string{`<communication-waiting active="false"/>`, `<communication-waiting/>`}, false},
		{"add an attribute", [2]string{`<communication-diversion active="true">`, `<communication-diversion active="true" x="1">`}, false},
		{"change a read-only element", [2]string{`x:tone="classic"`, `x:tone="jazz"`}, false},
	} {
		doc := strings.Replace(storedDocument, tc.replace[0], tc.replace[1], 1)
		if doc == storedDocument {
			t.Fatalf("%s: nothing replaced", tc.name)
		}
		err := CheckOwnerChange(before, mustDocument(t, doc))
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && conflictOf(err) != ConstraintFailure {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}
