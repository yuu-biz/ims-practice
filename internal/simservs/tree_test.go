package simservs

import (
	"errors"
	"strings"
	"testing"
)

func TestParseXMLKeepsTheDocumentAsWritten(t *testing.T) {
	// Prefixes, declarations, comments and whitespace come back as they
	// were; only the XML declaration is written anew.
	in := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <!-- provisioned by the operator -->
  <communication-diversion active="false">
    <cp:ruleset>
      <cp:rule id="cfu"><cp:conditions><rule-deactivated/></cp:conditions></cp:rule>
    </cp:ruleset>
  </communication-diversion>
</simservs>`
	p, err := parseXML([]byte(`<?xml version="1.0" encoding="UTF-8"?>`+"\n"+in), documentScope())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(p.root.serialize()); got != in {
		t.Errorf("serialize:\n%s\nwant:\n%s", got, in)
	}
	rule := p.root.childElements()[0].childElements()[0].childElements()[0]
	if rule.name != xmlName(NamespaceCommonPolicy, "rule") || rule.prefix != "cp" {
		t.Errorf("rule = %+v", rule)
	}
	if id, _ := rule.attr(xmlName("", "id")); id != "cfu" {
		t.Errorf("id = %q", id)
	}
}

func TestParseXMLRefusesBadDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, in string
	}{
		{"undeclared element prefix", `<cp:ruleset xmlns="urn:x"/>`},
		{"undeclared attribute prefix", `<a xmlns="urn:x" b:c="1"/>`},
		{"mismatched end tag", `<a><b></a></b>`},
		{"unclosed", `<a><b/>`},
		{"two roots", `<a/><b/>`},
		{"text outside the root", `<a/>text`},
		{"duplicate attribute", `<a xmlns:p="urn:x" xmlns:q="urn:x" p:b="1" q:b="2"/>`},
		{"empty prefixed namespace", `<p:a xmlns:p=""/>`},
		{"nothing", ``},
	} {
		if _, err := parseXML([]byte(tc.in), documentScope()); err == nil {
			t.Errorf("%s: parsed", tc.name)
		}
	}
}

func TestParseXMLRefusesOtherEncodings(t *testing.T) {
	// RFC 4825 8.2.2 reports these with <not-utf-8>.
	for _, in := range []string{
		`<?xml version="1.0" encoding="ISO-8859-1"?><a/>`,
		"<a>\xff</a>",
	} {
		if _, err := parseXML([]byte(in), documentScope()); !errors.Is(err, errNotUTF8) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

func TestParseXMLResolvesAFragmentInItsParentsScope(t *testing.T) {
	// A rule that a client PUTs uses the prefix cp declared on the root
	// of the stored document (RFC 4825 8.2.3).
	outer := documentScope().with([]nsDecl{{"", NamespaceSimservs}, {"cp", NamespaceCommonPolicy}})
	p, err := parseXML([]byte(`<cp:rule id="cfb"><cp:conditions><busy/></cp:conditions></cp:rule>`), outer)
	if err != nil {
		t.Fatal(err)
	}
	busy := p.root.childElements()[0].childElements()[0]
	if busy.name != xmlName(NamespaceSimservs, "busy") {
		t.Errorf("busy = %v", busy.name)
	}
}

func TestWriteEscapes(t *testing.T) {
	e := &element{name: xmlName("", "a"), attrs: []attribute{{name: xmlName("", "v"), value: "x\"<&\n"}},
		children: []node{charData("1 < 2 & \n 3 > 2")}}
	want := `<a v="x&quot;&lt;&amp;&#xA;">1 &lt; 2 &amp; ` + "\n" + ` 3 &gt; 2</a>`
	if got := string(e.serialize()); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	p, err := parseXML([]byte(want), documentScope())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := p.root.attr(xmlName("", "v")); v != "x\"<&\n" {
		t.Errorf("attribute read back as %q", v)
	}
}

func TestCanonicalize(t *testing.T) {
	// This is how encoding/xml writes namespaced elements.
	in := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-diversion xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><ruleset xmlns="urn:ietf:params:xml:ns:common-policy"><rule xmlns="urn:ietf:params:xml:ns:common-policy" id="r"><x xmlns="urn:other" xmlns:o="urn:other" o:a="1"></x><plain xmlns=""></plain></rule></ruleset></communication-diversion></simservs>`
	p, err := parseXML([]byte(in), documentScope())
	if err != nil {
		t.Fatal(err)
	}
	canonicalize(p.root)
	want := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy" xmlns:ns3="urn:other"><communication-diversion><cp:ruleset><cp:rule id="r"><ns3:x ns3:a="1"/><plain xmlns=""/></cp:rule></cp:ruleset></communication-diversion></simservs>`
	if got := string(p.root.serialize()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestConflictReport(t *testing.T) {
	e := &ConflictError{Condition: UniquenessFailure, Phrase: `rule id "cfu" is already used`,
		Field: "simservs/communication-diversion/cp:ruleset/cp:rule%5B2%5D/@id" + selectorQuery}
	got := string(e.Report())
	want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<xcap-error xmlns="urn:ietf:params:xml:ns:xcap-error"><uniqueness-failure phrase="rule id &quot;cfu&quot; is already used"><exists field="simservs/communication-diversion/cp:ruleset/cp:rule%5B2%5D/@id?xmlns(cp=urn:ietf:params:xml:ns:common-policy)"/></uniqueness-failure></xcap-error>`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(e.Error(), "uniqueness-failure") {
		t.Errorf("Error() = %s", e.Error())
	}
	plain := (&ConflictError{Condition: NoParent}).Report()
	if !strings.Contains(string(plain), "<no-parent/>") {
		t.Errorf("no-parent report = %s", plain)
	}
}

func TestIsNCName(t *testing.T) {
	for s, want := range map[string]bool{
		"cfu": true, "rule66": true, "_a.b-c": true, "call-forwarding-unconditional": true,
		"": false, "1rule": false, "-a": false, "a:b": false, "a b": false,
	} {
		if got := isNCName(s); got != want {
			t.Errorf("isNCName(%q) = %v", s, got)
		}
	}
}
