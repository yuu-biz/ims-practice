package simservs

import (
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// MIME types of the parts of a document (RFC 4825 15.2).
const (
	ElementMIMEType   = "application/xcap-el+xml"
	AttributeMIMEType = "application/xcap-att+xml"
	NamespaceMIMEType = "application/xcap-ns+xml"
)

// Errors of node selection, with the HTTP status that RFC 4825 8 gives
// them. Changes that fail return a *ConflictError (409) instead.
var (
	// ErrNoMatch: the selector selects no node, or more than one
	// element, which makes it invalid (RFC 4825 6.3). GET and DELETE
	// answer 404 (RFC 4825 8.3, 8.4).
	ErrNoMatch = errors.New("the node selector selects no single node")
	// ErrUndefinedPrefix: a prefix in the selector has no xmlns()
	// binding in the query component; the server answers 400 (RFC 4825
	// 8).
	ErrUndefinedPrefix = errors.New("prefix not bound in the query component")
	// ErrUnsupportedSelector: a step is not one of the forms of RFC 4825
	// 6.3, so it is an extension selector this server does not
	// understand; the server answers 404 (RFC 4825 8).
	ErrUnsupportedSelector = errors.New("extension selector not understood")
	// ErrNamespaceSelector: PUT and DELETE cannot target the namespace
	// bindings; the server answers 405 with Allow: GET (RFC 4825 8.2,
	// 8.4).
	ErrNamespaceSelector = errors.New("namespace bindings can only be read")
)

// NodeSelector is a parsed XCAP node selector (RFC 4825 6.3): element
// steps from the root, then optionally an attribute or the namespace
// bindings of the last element.
type NodeSelector struct {
	steps      []step
	attribute  *xml.Name
	namespaces bool
	// bindings are the prefixes of the query component (RFC 4825 6.4).
	bindings map[string]string
	text     string
}

// step is one location step: a name or *, then predicates.
type step struct {
	any  bool
	name xml.Name
	// position is the [n] predicate, 0 when absent.
	position int
	attrTest *attrTest
}

type attrTest struct {
	name  xml.Name
	value string
}

func (s NodeSelector) String() string { return s.text }

// TargetsAttribute tells whether the selector ends in an attribute
// selector, so that the body is an attribute value of
// AttributeMIMEType.
func (s NodeSelector) TargetsAttribute() bool { return s.attribute != nil }

// TargetsNamespaces tells whether the selector ends in namespace::*.
func (s NodeSelector) TargetsNamespaces() bool { return s.namespaces }

// ParseNodeSelector reads the node selector of an XCAP URI, the part
// after "~~/", with the query component that binds its prefixes. Both
// must already be percent-decoded. Unprefixed element names are in
// defaultNamespace, the default document namespace of the application
// usage (NamespaceSimservs for simservs, RFC 4825 6.3); unprefixed
// attribute names are in no namespace.
func ParseNodeSelector(selector, query, defaultNamespace string) (*NodeSelector, error) {
	bindings, err := parseXmlnsQuery(query)
	if err != nil {
		return nil, err
	}
	sel := &NodeSelector{bindings: bindings, text: selector}
	segments, err := splitSteps(selector)
	if err != nil {
		return nil, err
	}
	for i, seg := range segments {
		last := i == len(segments)-1
		switch {
		case last && i > 0 && seg == "namespace::*":
			sel.namespaces = true
		case last && i > 0 && strings.HasPrefix(seg, "@"):
			name, err := sel.expand(seg[1:], "")
			if err != nil {
				return nil, err
			}
			sel.attribute = &name
		default:
			st, err := sel.parseStep(seg, defaultNamespace)
			if err != nil {
				return nil, err
			}
			sel.steps = append(sel.steps, st)
		}
	}
	return sel, nil
}

// splitSteps splits a selector at the slashes that are not inside a
// quoted attribute value.
func splitSteps(s string) ([]string, error) {
	var (
		out   []string
		quote rune
		start int
	)
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '/':
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("%w: unterminated attribute value in %q", ErrUnsupportedSelector, s)
	}
	out = append(out, s[start:])
	for _, seg := range out {
		if seg == "" {
			return nil, fmt.Errorf("%w: empty step in %q", ErrUnsupportedSelector, s)
		}
	}
	return out, nil
}

// parseStep reads by-name, by-pos, by-attr and by-pos-attr (RFC 4825
// 6.3).
func (s *NodeSelector) parseStep(seg, defaultNamespace string) (step, error) {
	unsupported := fmt.Errorf("%w: %q", ErrUnsupportedSelector, seg)
	var st step
	nameEnd := strings.IndexByte(seg, '[')
	if nameEnd < 0 {
		nameEnd = len(seg)
	}
	if qname := seg[:nameEnd]; qname == "*" {
		st.any = true
	} else {
		if !isQName(qname) {
			return step{}, unsupported
		}
		name, err := s.expand(qname, defaultNamespace)
		if err != nil {
			return step{}, err
		}
		st.name = name
	}
	rest := seg[nameEnd:]
	if strings.HasPrefix(rest, "[") && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9' {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return step{}, unsupported
		}
		n, err := strconv.Atoi(rest[1:end])
		if err != nil || n < 1 {
			return step{}, unsupported
		}
		st.position = n
		rest = rest[end+1:]
	}
	if strings.HasPrefix(rest, "[@") && strings.HasSuffix(rest, "]") {
		test, err := s.parseAttrTest(rest[2 : len(rest)-1])
		if err != nil {
			return step{}, err
		}
		st.attrTest = &test
		rest = ""
	}
	if rest != "" {
		return step{}, unsupported
	}
	return st, nil
}

// parseAttrTest reads att-name "=" AttValue.
func (s *NodeSelector) parseAttrTest(t string) (attrTest, error) {
	eq := strings.IndexByte(t, '=')
	if eq < 0 {
		return attrTest{}, fmt.Errorf("%w: %q", ErrUnsupportedSelector, t)
	}
	qname := t[:eq]
	if !isQName(qname) {
		return attrTest{}, fmt.Errorf("%w: %q", ErrUnsupportedSelector, t)
	}
	name, err := s.expand(qname, "")
	if err != nil {
		return attrTest{}, err
	}
	value, err := parseAttValue(t[eq+1:])
	if err != nil {
		return attrTest{}, fmt.Errorf("%w: %v", ErrUnsupportedSelector, err)
	}
	return attrTest{name: name, value: value}, nil
}

// expand turns a QName into an expanded name with the bindings of the
// query component. An unprefixed name takes unprefixedNamespace.
func (s *NodeSelector) expand(qname, unprefixedNamespace string) (xml.Name, error) {
	prefix, local, found := strings.Cut(qname, ":")
	if !found {
		return xml.Name{Space: unprefixedNamespace, Local: qname}, nil
	}
	uri, ok := s.bindings[prefix]
	if !ok {
		return xml.Name{}, fmt.Errorf("%w: %q", ErrUndefinedPrefix, prefix)
	}
	return xml.Name{Space: uri, Local: local}, nil
}

func isQName(s string) bool {
	prefix, local, found := strings.Cut(s, ":")
	if !found {
		return isNCName(s)
	}
	return isNCName(prefix) && isNCName(local)
}

// parseAttValue reads an AttValue of XML 1.0: a quoted string that may
// hold character and entity references. The XML decoder does the work,
// so the rules are exactly those of an attribute in a document.
func parseAttValue(v string) (string, error) {
	if len(v) < 2 || (v[0] != '"' && v[0] != '\'') || v[len(v)-1] != v[0] {
		return "", fmt.Errorf("%q is not a quoted attribute value", v)
	}
	inner := v[1 : len(v)-1]
	if strings.ContainsRune(inner, rune(v[0])) || strings.ContainsRune(inner, '<') {
		return "", fmt.Errorf("%q is not an attribute value", v)
	}
	var probe struct {
		V string `xml:"v,attr"`
	}
	if err := xml.Unmarshal([]byte("<p v="+v+"/>"), &probe); err != nil {
		return "", fmt.Errorf("%q is not an attribute value: %v", v, err)
	}
	return probe.V, nil
}

// quoteAttValue writes value as an AttValue, the body of a GET of an
// attribute (RFC 4825 7.9).
func quoteAttValue(value string) []byte {
	var b strings.Builder
	b.WriteByte('"')
	escapeAttrValue(&b, value)
	b.WriteByte('"')
	return []byte(b.String())
}

// parseXmlnsQuery reads the xmlns() pointer parts of the query
// component (RFC 4825 6.4, XPointer xmlns() scheme). Parts of other
// schemes are ignored, as RFC 4825 6.4 requires. A circumflex escapes a
// parenthesis or a circumflex inside a part (XPointer Framework 3.1).
func parseXmlnsQuery(q string) (map[string]string, error) {
	// The prefix xml is bound in every document (Namespaces in XML 1.0,
	// section 3).
	bindings := map[string]string{"xml": namespaceXML}
	for q = strings.TrimSpace(q); q != ""; q = strings.TrimSpace(q) {
		open := strings.IndexByte(q, '(')
		if open <= 0 {
			return nil, fmt.Errorf("%w: query %q is not an XPointer", ErrUnsupportedSelector, q)
		}
		scheme := q[:open]
		var data strings.Builder
		depth, end := 1, -1
		for i := open + 1; i < len(q) && end < 0; i++ {
			switch c := q[i]; c {
			case '^':
				if i+1 < len(q) {
					i++
					data.WriteByte(q[i])
				}
			case '(':
				depth++
				data.WriteByte(c)
			case ')':
				depth--
				if depth == 0 {
					end = i
				} else {
					data.WriteByte(c)
				}
			default:
				data.WriteByte(c)
			}
		}
		if end < 0 {
			return nil, fmt.Errorf("%w: unbalanced parentheses in %q", ErrUnsupportedSelector, q)
		}
		if scheme == "xmlns" {
			prefix, uri, ok := strings.Cut(data.String(), "=")
			prefix, uri = strings.TrimSpace(prefix), strings.TrimSpace(uri)
			if !ok || !isNCName(prefix) || uri == "" {
				return nil, fmt.Errorf("%w: xmlns(%s)", ErrUnsupportedSelector, data.String())
			}
			bindings[prefix] = uri
		}
		q = q[end+1:]
	}
	return bindings, nil
}

// located is an element found by a selector, with the scope in force at
// its parent and its place among the parent's children. parent is nil
// for the root element.
type located struct {
	el     *element
	parent *element
	index  int
	// outer is the scope at the parent, that is, without the
	// declarations on el.
	outer scope
}

// matchesName tells whether e passes the name test of st.
func (st step) matchesName(e *element) bool { return st.any || e.name == st.name }

// apply evaluates one step among candidates in document order (RFC 4825
// 6.3): the name test, then the predicates in order. It returns the
// index of the one element left, or -1.
func (st step) apply(candidates []*element) int {
	var list []int
	for i, e := range candidates {
		if st.matchesName(e) {
			list = append(list, i)
		}
	}
	if st.position > 0 {
		if st.position > len(list) {
			return -1
		}
		list = list[st.position-1 : st.position]
	}
	if st.attrTest != nil {
		var kept []int
		for _, i := range list {
			if v, ok := candidates[i].attr(st.attrTest.name); ok && v == st.attrTest.value {
				kept = append(kept, i)
			}
		}
		list = kept
	}
	if len(list) != 1 {
		return -1
	}
	return list[0]
}

// selectSteps evaluates steps from the document, each step choosing a
// single element in the context of the one before.
func (d *Document) selectSteps(steps []step) (located, bool) {
	if len(steps) == 0 {
		return located{}, false
	}
	if steps[0].apply([]*element{d.root}) != 0 {
		return located{}, false
	}
	loc := located{el: d.root, outer: documentScope()}
	for _, st := range steps[1:] {
		inner := loc.outer.with(loc.el.nsDecls)
		children := loc.el.childElements()
		i := st.apply(children)
		if i < 0 {
			return located{}, false
		}
		loc = located{el: children[i], parent: loc.el, index: childIndex(loc.el, children[i]), outer: inner}
	}
	return loc, true
}

// childIndex returns the place of child among all the children of
// parent, text and comments included.
func childIndex(parent, child *element) int {
	for i, c := range parent.children {
		if c == node(child) {
			return i
		}
	}
	return -1
}

// Get returns the node that sel selects, with its MIME type (RFC 4825
// 8.3): an element as an XML fragment body without the namespace
// declarations of its ancestors, an attribute as an AttValue, or the
// namespace bindings in scope as an element of the same name that
// declares them (RFC 4825 10).
func (d *Document) Get(sel *NodeSelector) (body []byte, contentType string, err error) {
	loc, ok := d.selectSteps(sel.steps)
	if !ok {
		return nil, "", ErrNoMatch
	}
	switch {
	case sel.attribute != nil:
		v, ok := loc.el.attr(*sel.attribute)
		if !ok {
			return nil, "", ErrNoMatch
		}
		return quoteAttValue(v), AttributeMIMEType, nil
	case sel.namespaces:
		inScope := loc.outer.with(loc.el.nsDecls)
		e := &element{prefix: loc.el.prefix, name: loc.el.name}
		for _, p := range slices.Sorted(maps.Keys(inScope)) {
			if p != "xml" && inScope[p] != "" {
				e.nsDecls = append(e.nsDecls, nsDecl{prefix: p, uri: inScope[p]})
			}
		}
		return e.serialize(), NamespaceMIMEType, nil
	default:
		return loc.el.serialize(), ElementMIMEType, nil
	}
}
