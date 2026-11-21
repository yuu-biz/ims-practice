package simservs

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// The tree below keeps an XML document as it was written: the prefixes,
// the namespace declarations, the comments and the whitespace. An XCAP
// server has to keep all of them, because a client that PUTs an
// element gets back the same element (RFC 4825 6.3, 8.2.3), and the
// server must not change the namespace declarations of an inserted
// element (RFC 4825 8.2.3). encoding/xml keeps none of them, so the
// typed values of this package are a view that is read from and written
// to this tree.

// namespaceXML is bound to the prefix xml in every document (Namespaces
// in XML 1.0, section 3).
const namespaceXML = "http://www.w3.org/XML/1998/namespace"

type node interface {
	write(b *strings.Builder)
	clone() node
}

// element is an XML element with its name expanded (Name.Space is the
// namespace URI) and the prefix it was written with.
type element struct {
	prefix   string
	name     xml.Name
	nsDecls  []nsDecl
	attrs    []attribute
	children []node
}

// nsDecl is an xmlns or xmlns:prefix attribute. An empty prefix
// declares the default namespace.
type nsDecl struct {
	prefix string
	uri    string
}

type attribute struct {
	prefix string
	name   xml.Name
	value  string
}

type charData string
type comment string
type procInst struct{ target, inst string }
type directive string

func (e *element) clone() node {
	c := &element{
		prefix:  e.prefix,
		name:    e.name,
		nsDecls: slices.Clone(e.nsDecls),
		attrs:   slices.Clone(e.attrs),
	}
	for _, child := range e.children {
		c.children = append(c.children, child.clone())
	}
	return c
}

func (c charData) clone() node  { return c }
func (c comment) clone() node   { return c }
func (p procInst) clone() node  { return p }
func (d directive) clone() node { return d }

func xmlName(space, local string) xml.Name { return xml.Name{Space: space, Local: local} }

func qualified(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + ":" + local
}

func (e *element) write(b *strings.Builder) {
	b.WriteByte('<')
	b.WriteString(qualified(e.prefix, e.name.Local))
	for _, d := range e.nsDecls {
		b.WriteByte(' ')
		if d.prefix == "" {
			b.WriteString("xmlns")
		} else {
			b.WriteString("xmlns:" + d.prefix)
		}
		b.WriteString(`="`)
		escapeAttrValue(b, d.uri)
		b.WriteByte('"')
	}
	for _, a := range e.attrs {
		b.WriteByte(' ')
		b.WriteString(qualified(a.prefix, a.name.Local))
		b.WriteString(`="`)
		escapeAttrValue(b, a.value)
		b.WriteByte('"')
	}
	if len(e.children) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	for _, child := range e.children {
		child.write(b)
	}
	b.WriteString("</")
	b.WriteString(qualified(e.prefix, e.name.Local))
	b.WriteByte('>')
}

func (c charData) write(b *strings.Builder) { escapeText(b, string(c)) }
func (c comment) write(b *strings.Builder)  { b.WriteString("<!--" + string(c) + "-->") }
func (p procInst) write(b *strings.Builder) {
	b.WriteString("<?" + p.target)
	if p.inst != "" {
		b.WriteString(" " + p.inst)
	}
	b.WriteString("?>")
}
func (d directive) write(b *strings.Builder) { b.WriteString("<!" + string(d) + ">") }

// escapeText escapes character data. Unlike xml.EscapeText it leaves
// line feeds and tabs as they are, so that the whitespace a client sent
// comes back as it was (RFC 4825 6.3).
func escapeText(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
}

// escapeAttrValue escapes an attribute value. Whitespace characters are
// written as references, because a parser would otherwise normalise them
// to spaces (XML 1.0, section 3.3.3).
func escapeAttrValue(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '"':
			b.WriteString("&quot;")
		case '\t':
			b.WriteString("&#x9;")
		case '\n':
			b.WriteString("&#xA;")
		case '\r':
			b.WriteString("&#xD;")
		default:
			b.WriteRune(r)
		}
	}
}

// errNotUTF8 marks input that is not in UTF-8, which RFC 4825 8.2.2
// reports with its own error element.
var errNotUTF8 = errors.New("not UTF-8")

// scope holds the namespace bindings in force at one point of a
// document, from prefix ("" for the default namespace) to URI.
type scope map[string]string

func (s scope) with(decls []nsDecl) scope {
	if len(decls) == 0 {
		return s
	}
	next := make(scope, len(s)+len(decls))
	for p, u := range s {
		next[p] = u
	}
	for _, d := range decls {
		next[d.prefix] = d.uri
	}
	return next
}

func documentScope() scope { return scope{"xml": namespaceXML} }

// parsed is the result of reading XML text: the nodes before the root
// element (comments, processing instructions, a document type
// declaration) and the root element.
type parsed struct {
	prolog []node
	root   *element
}

// parseXML reads XML text that holds exactly one element, with the
// namespace bindings in outer already in force. A whole document is read
// with documentScope; an XML fragment body that a client PUTs is read
// with the bindings in scope at its parent (RFC 4825 8.2.3, XML Fragment
// Interchange). Whitespace around the element is dropped; the XML
// declaration is dropped, because the writer always writes its own.
func parseXML(data []byte, outer scope) (parsed, error) {
	if !utf8.Valid(data) {
		return parsed{}, errNotUTF8
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	d.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return nil, fmt.Errorf("encoding %q: %w", charset, errNotUTF8)
	}

	var (
		result parsed
		stack  []*element
		scopes = []scope{outer}
	)
	add := func(n node) {
		if len(stack) == 0 {
			result.prolog = append(result.prolog, n)
			return
		}
		top := stack[len(stack)-1]
		top.children = append(top.children, n)
	}
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, errNotUTF8) {
				return parsed{}, errNotUTF8
			}
			return parsed{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 && result.root != nil {
				return parsed{}, errors.New("more than one root element")
			}
			e, err := newElement(t, scopes[len(scopes)-1])
			if err != nil {
				return parsed{}, err
			}
			if len(stack) == 0 {
				result.root = e
			} else {
				add(e)
			}
			stack = append(stack, e)
			scopes = append(scopes, scopes[len(scopes)-1].with(e.nsDecls))
		case xml.EndElement:
			if len(stack) == 0 {
				return parsed{}, fmt.Errorf("unexpected end tag </%s>", qualified(t.Name.Space, t.Name.Local))
			}
			top := stack[len(stack)-1]
			if t.Name.Space != top.prefix || t.Name.Local != top.name.Local {
				return parsed{}, fmt.Errorf("end tag </%s> does not match <%s>",
					qualified(t.Name.Space, t.Name.Local), qualified(top.prefix, top.name.Local))
			}
			stack = stack[:len(stack)-1]
			scopes = scopes[:len(scopes)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if len(bytes.TrimSpace(t)) != 0 {
					return parsed{}, errors.New("text outside the root element")
				}
				continue
			}
			add(charData(t))
		case xml.Comment:
			if len(stack) == 0 && result.root != nil {
				continue
			}
			add(comment(t))
		case xml.ProcInst:
			if t.Target == "xml" {
				continue
			}
			if len(stack) == 0 && result.root != nil {
				continue
			}
			add(procInst{target: t.Target, inst: string(t.Inst)})
		case xml.Directive:
			if result.root != nil {
				return parsed{}, errors.New("declaration after the root element")
			}
			add(directive(t))
		}
	}
	if len(stack) != 0 {
		return parsed{}, fmt.Errorf("element <%s> is not closed", qualified(stack[0].prefix, stack[0].name.Local))
	}
	if result.root == nil {
		return parsed{}, errors.New("no element")
	}
	return result, nil
}

// newElement turns a raw start tag into an element, resolving its
// prefixes with the bindings in s and the declarations on the tag
// itself (Namespaces in XML 1.0, sections 5 and 6).
func newElement(t xml.StartElement, s scope) (*element, error) {
	e := &element{prefix: t.Name.Space, name: xml.Name{Local: t.Name.Local}}
	var plain []xml.Attr
	for _, a := range t.Attr {
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			e.nsDecls = append(e.nsDecls, nsDecl{prefix: "", uri: a.Value})
		case a.Name.Space == "xmlns":
			if a.Value == "" {
				return nil, fmt.Errorf("prefix %q is bound to an empty namespace name", a.Name.Local)
			}
			e.nsDecls = append(e.nsDecls, nsDecl{prefix: a.Name.Local, uri: a.Value})
		default:
			plain = append(plain, a)
		}
	}
	inner := s.with(e.nsDecls)
	uri, ok := inner[e.prefix]
	if !ok && e.prefix != "" {
		return nil, fmt.Errorf("prefix %q of <%s> is not declared", e.prefix, qualified(e.prefix, e.name.Local))
	}
	e.name.Space = uri
	for _, a := range plain {
		attr := attribute{prefix: a.Name.Space, name: xml.Name{Local: a.Name.Local}, value: a.Value}
		if attr.prefix != "" {
			// An unprefixed attribute is in no namespace; the default
			// namespace does not apply to attributes.
			u, ok := inner[attr.prefix]
			if !ok {
				return nil, fmt.Errorf("prefix %q of attribute %s is not declared", attr.prefix, qualified(attr.prefix, attr.name.Local))
			}
			attr.name.Space = u
		}
		for _, other := range e.attrs {
			if other.name == attr.name {
				return nil, fmt.Errorf("attribute %s appears twice on <%s>", attr.name.Local, e.name.Local)
			}
		}
		e.attrs = append(e.attrs, attr)
	}
	return e, nil
}

// childElements returns the element children of e in document order.
func (e *element) childElements() []*element {
	var out []*element
	for _, c := range e.children {
		if el, ok := c.(*element); ok {
			out = append(out, el)
		}
	}
	return out
}

func (e *element) attr(name xml.Name) (string, bool) {
	for _, a := range e.attrs {
		if a.name == name {
			return a.value, true
		}
	}
	return "", false
}

// serialize writes e alone, without an XML declaration.
func (e *element) serialize() []byte {
	var b strings.Builder
	e.write(&b)
	return []byte(b.String())
}

// preferredPrefixes are the prefixes the specifications use in their
// examples: the simservs namespace is the default namespace of the
// document (TS 24.623 6.2), the common policy namespaces carry cp and
// ocp (TS 24.604 4.9.1.0), and the conflict report uses its namespace
// as the default (RFC 4825 11.1).
var preferredPrefixes = map[string]string{
	NamespaceSimservs:        "",
	NamespaceCommonPolicy:    "cp",
	NamespaceOMACommonPolicy: "ocp",
	NamespaceXCAPError:       "",
}

// canonicalize rewrites the prefixes of a tree that encoding/xml wrote.
// encoding/xml repeats xmlns="..." on every element and never writes a
// prefix, which is correct XML but hard to read in a capture. The
// result declares every namespace once, on the root, with the prefixes
// above.
func canonicalize(root *element) {
	// The default namespace can only be declared on the root when the
	// root itself is in a namespace.
	defaultAllowed := root.name.Space != ""
	prefixes := map[string]string{}
	attributePrefixes := map[string]string{}
	defaultTaken := false
	var order []string
	prefixFor := func(uri string, forAttribute bool) string {
		if uri == namespaceXML {
			return "xml"
		}
		p, ok := prefixes[uri]
		if !ok {
			p, ok = preferredPrefixes[uri]
			if !ok || (p == "" && (!defaultAllowed || defaultTaken)) {
				p = fmt.Sprintf("ns%d", len(order)+1)
			}
			prefixes[uri] = p
			order = append(order, uri)
			if p == "" {
				defaultTaken = true
			}
		}
		if p == "" && forAttribute {
			// The default namespace does not apply to attributes, so an
			// attribute in that namespace needs a prefix of its own.
			p = "ss"
			attributePrefixes[uri] = p
		}
		return p
	}
	var noNamespace []*element
	var walk func(e *element)
	walk = func(e *element) {
		e.nsDecls = nil
		e.prefix = ""
		if e.name.Space == "" {
			noNamespace = append(noNamespace, e)
		} else {
			e.prefix = prefixFor(e.name.Space, false)
		}
		for i, a := range e.attrs {
			e.attrs[i].prefix = ""
			if a.name.Space != "" {
				e.attrs[i].prefix = prefixFor(a.name.Space, true)
			}
		}
		for _, c := range e.childElements() {
			walk(c)
		}
	}
	walk(root)
	var decls []nsDecl
	defaultUsed := false
	for _, uri := range order {
		p := prefixes[uri]
		decls = append(decls, nsDecl{prefix: p, uri: uri})
		if p == "" {
			defaultUsed = true
		}
		if ap, ok := attributePrefixes[uri]; ok {
			decls = append(decls, nsDecl{prefix: ap, uri: uri})
		}
	}
	slices.SortStableFunc(decls, func(a, b nsDecl) int { return strings.Compare(a.prefix, b.prefix) })
	root.nsDecls = decls
	if defaultUsed {
		// An element in no namespace below the default namespace has to
		// undeclare it.
		for _, e := range noNamespace {
			e.nsDecls = []nsDecl{{prefix: "", uri: ""}}
		}
	}
}
