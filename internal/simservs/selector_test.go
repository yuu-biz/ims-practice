package simservs

import (
	"errors"
	"testing"
)

const cpQuery = "xmlns(cp=urn:ietf:params:xml:ns:common-policy)"

// storedDocument is a simservs document as an XCAP server stores it,
// with the prefixes, the whitespace and a vendor extension.
const storedDocument = `<?xml version="1.0" encoding="UTF-8"?>
<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <communication-diversion active="true">
    <NoReplyTimer>20</NoReplyTimer>
    <cp:ruleset>
      <cp:rule id="cfu">
        <cp:conditions><rule-deactivated/></cp:conditions>
        <cp:actions><forward-to><target/></forward-to></cp:actions>
      </cp:rule>
      <cp:rule id="cfb">
        <cp:conditions><busy/></cp:conditions>
        <cp:actions><forward-to><target>tel:+12025550150</target></forward-to></cp:actions>
      </cp:rule>
    </cp:ruleset>
  </communication-diversion>
  <communication-waiting active="false"/>
  <extensions><x:ringback xmlns:x="urn:example:vendor" x:tone="classic"/></extensions>
</simservs>
`

func mustDocument(t *testing.T, s string) *Document {
	t.Helper()
	d, err := ParseDocument([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustSelector(t *testing.T, selector, query string) *NodeSelector {
	t.Helper()
	sel, err := ParseNodeSelector(selector, query, NamespaceSimservs)
	if err != nil {
		t.Fatalf("ParseNodeSelector(%q, %q): %v", selector, query, err)
	}
	return sel
}

func TestDocumentBytesKeepsTheDocument(t *testing.T) {
	d := mustDocument(t, storedDocument)
	if got := string(d.Bytes()); got != storedDocument[:len(storedDocument)-1] {
		t.Errorf("Bytes:\n%s", got)
	}
	if d.RootName() != xmlName(NamespaceSimservs, "simservs") {
		t.Errorf("RootName = %v", d.RootName())
	}
}

func TestParseDocumentErrors(t *testing.T) {
	var ce *ConflictError
	if _, err := ParseDocument([]byte(`<simservs>`)); !errors.As(err, &ce) || ce.Condition != NotWellFormed {
		t.Errorf("not well formed: %v", err)
	}
	if _, err := ParseDocument([]byte("<simservs>\xfe</simservs>")); !errors.As(err, &ce) || ce.Condition != NotUTF8 {
		t.Errorf("not UTF-8: %v", err)
	}
}

func TestGet(t *testing.T) {
	d := mustDocument(t, storedDocument)
	for _, tc := range []struct {
		name, selector, query string
		body, contentType     string
	}{
		{
			name:     "an element by name",
			selector: "simservs/communication-diversion/NoReplyTimer",
			body:     `<NoReplyTimer>20</NoReplyTimer>`, contentType: ElementMIMEType,
		},
		{
			// The declarations of the ancestors are not added (RFC 4825
			// 8.3).
			name:     "a rule by its id",
			selector: `simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfb"]`, query: cpQuery,
			body: `<cp:rule id="cfb">
        <cp:conditions><busy/></cp:conditions>
        <cp:actions><forward-to><target>tel:+12025550150</target></forward-to></cp:actions>
      </cp:rule>`, contentType: ElementMIMEType,
		},
		{
			name:     "a rule by position, with single quotes",
			selector: `simservs/communication-diversion/cp:ruleset/cp:rule[1][@id='cfu']/cp:conditions`, query: cpQuery,
			body: `<cp:conditions><rule-deactivated/></cp:conditions>`, contentType: ElementMIMEType,
		},
		{
			name:     "any element by position",
			selector: `simservs/*[2]`,
			body:     `<communication-waiting active="false"/>`, contentType: ElementMIMEType,
		},
		{
			name:     "the prefix in the query need not be the one in the document",
			selector: `simservs/communication-diversion/policy:ruleset/policy:rule[2]/policy:actions/forward-to/target`,
			query:    "xmlns(policy=urn:ietf:params:xml:ns:common-policy)",
			body:     `<target>tel:+12025550150</target>`, contentType: ElementMIMEType,
		},
		{
			name:     "an element of another namespace",
			selector: `simservs/extensions/v:ringback`, query: "xmlns(v=urn:example:vendor) other(ignored)",
			body: `<x:ringback xmlns:x="urn:example:vendor" x:tone="classic"/>`, contentType: ElementMIMEType,
		},
		{
			name:     "an attribute",
			selector: `simservs/communication-diversion/@active`,
			body:     `"true"`, contentType: AttributeMIMEType,
		},
		{
			name:     "a namespaced attribute",
			selector: `simservs/extensions/v:ringback/@v:tone`, query: "xmlns(v=urn:example:vendor)",
			body: `"classic"`, contentType: AttributeMIMEType,
		},
		{
			name:     "the namespace bindings in scope",
			selector: `simservs/communication-diversion/cp:ruleset/namespace::*`, query: cpQuery,
			body:        `<cp:ruleset xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy"/>`,
			contentType: NamespaceMIMEType,
		},
		{
			name:     "a service element",
			selector: `simservs/communication-waiting`,
			body:     `<communication-waiting active="false"/>`, contentType: ElementMIMEType,
		},
	} {
		sel := mustSelector(t, tc.selector, tc.query)
		body, ct, err := d.Get(sel)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(body) != tc.body || ct != tc.contentType {
			t.Errorf("%s: got %s (%s)\nwant %s (%s)", tc.name, body, ct, tc.body, tc.contentType)
		}
	}
}

func TestGetNoMatch(t *testing.T) {
	d := mustDocument(t, storedDocument)
	for _, tc := range []struct{ selector, query string }{
		{"simservs/originating-identity-presentation", ""},
		{"simservs/communication-diversion/cp:ruleset/cp:rule", cpQuery}, // two rules: invalid
		{`simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfnr"]`, cpQuery},
		{`simservs/communication-diversion/cp:ruleset/cp:rule[3]`, cpQuery},
		{`simservs/communication-diversion/cp:ruleset/cp:rule[2][@id="cfu"]`, cpQuery}, // position first
		{"simservs/communication-diversion/@password-required", ""},
		{"simservs/communication-diversion/ruleset", ""}, // ruleset is not in the default namespace
		{"other/communication-diversion", ""},
	} {
		if _, _, err := d.Get(mustSelector(t, tc.selector, tc.query)); !errors.Is(err, ErrNoMatch) {
			t.Errorf("%s: err = %v", tc.selector, err)
		}
	}
}

func TestParseNodeSelectorErrors(t *testing.T) {
	for _, tc := range []struct {
		selector, query string
		want            error
	}{
		{"simservs/cp:ruleset", "", ErrUndefinedPrefix},
		{"simservs/x/@cp:id", "", ErrUndefinedPrefix},
		{"simservs/a[last()]", "", ErrUnsupportedSelector},
		{"simservs/a[@id=cfu]", "", ErrUnsupportedSelector},
		{`simservs/a[@id="cfu]`, "", ErrUnsupportedSelector},
		{"simservs//a", "", ErrUnsupportedSelector},
		{"simservs/a[0]", "", ErrUnsupportedSelector},
		{"simservs/a", "xmlns(cp=urn:x", ErrUnsupportedSelector},
		{"simservs/a", "nonsense", ErrUnsupportedSelector},
	} {
		if _, err := ParseNodeSelector(tc.selector, tc.query, NamespaceSimservs); !errors.Is(err, tc.want) {
			t.Errorf("%s ? %s: err = %v, want %v", tc.selector, tc.query, err, tc.want)
		}
	}
}

func TestParseNodeSelectorReadsQuotedSlashesAndReferences(t *testing.T) {
	d := mustDocument(t, `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><x id="a/b&amp;c"/></simservs>`)
	for _, selector := range []string{`simservs/x[@id="a/b&amp;c"]`, `simservs/x[@id='a/b&#38;c']`} {
		sel := mustSelector(t, selector, "")
		if body, _, err := d.Get(sel); err != nil || string(body) != `<x id="a/b&amp;c"/>` {
			t.Errorf("%s: %s, %v", selector, body, err)
		}
	}
	sel := mustSelector(t, "simservs/x/@id", "")
	if !sel.TargetsAttribute() || sel.TargetsNamespaces() || sel.String() != "simservs/x/@id" {
		t.Errorf("selector = %+v", sel)
	}
}

func TestParseXmlnsQueryEscapes(t *testing.T) {
	b, err := parseXmlnsQuery("xmlns(a=urn:x:^(1^)) xmlns( b = urn:y )")
	if err != nil {
		t.Fatal(err)
	}
	if b["a"] != "urn:x:(1)" || b["b"] != "urn:y" {
		t.Errorf("bindings = %v", b)
	}
}
