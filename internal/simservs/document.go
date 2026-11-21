package simservs

import (
	"encoding/xml"
	"errors"
	"strings"
)

// Document is an XML document kept as it was written, as an XCAP server
// stores it: prefixes, namespace declarations, comments and whitespace
// stay as the client sent them (RFC 4825 6.3). Get, PutElement,
// PutAttribute and Delete read and change one node through an XCAP node
// selector.
//
// A Document is not safe for concurrent use. An XCAP server reads the
// stored document for each request, changes a Clone, validates it and
// writes it back, so no document is shared between requests.
type Document struct {
	prolog []node
	root   *element
}

// ParseDocument reads a whole document, as in a PUT without a node
// selector or as stored in the HSS. The error is a *ConflictError with
// NotWellFormed or NotUTF8 (RFC 4825 8.2.2).
func ParseDocument(data []byte) (*Document, error) {
	p, err := parseXML(data, documentScope())
	if err != nil {
		if errors.Is(err, errNotUTF8) {
			return nil, conflict(NotUTF8, "the document is not UTF-8")
		}
		return nil, conflict(NotWellFormed, "%v", err)
	}
	return &Document{prolog: p.prolog, root: p.root}, nil
}

// Bytes writes the document with an XML declaration.
func (d *Document) Bytes() []byte {
	var b strings.Builder
	b.WriteString(xml.Header)
	for _, n := range d.prolog {
		n.write(&b)
		b.WriteByte('\n')
	}
	d.root.write(&b)
	return []byte(b.String())
}

// Clone returns a deep copy, to be changed without touching d.
func (d *Document) Clone() *Document {
	c := &Document{root: d.root.clone().(*element)}
	for _, n := range d.prolog {
		c.prolog = append(c.prolog, n.clone())
	}
	return c
}

// RootName returns the expanded name of the root element.
func (d *Document) RootName() xml.Name { return d.root.name }
