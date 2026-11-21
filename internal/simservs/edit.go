package simservs

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"unicode/utf8"
)

// PutElement creates or replaces the element that sel selects with the
// XML fragment body in fragment (RFC 4825 7.4, 8.2). created tells
// whether the element is new, which the server answers with 201 instead
// of 200. On an error d is unchanged; a failure of the request itself
// is a *ConflictError (409).
//
// PutElement only places the element. The server then validates the
// result against the application usage (ValidateSimservsDocument,
// CheckOwnerChange) before it keeps it (RFC 4825 8.2.5).
func (d *Document) PutElement(sel *NodeSelector, fragment []byte) (created bool, err error) {
	if sel.namespaces {
		return false, ErrNamespaceSelector
	}
	if sel.attribute != nil {
		return false, conflict(NotXMLFrag, "the selector selects an attribute")
	}
	parentSteps, target := sel.steps[:len(sel.steps)-1], sel.steps[len(sel.steps)-1]

	var (
		parent *element
		outer  scope
	)
	if len(parentSteps) > 0 {
		loc, ok := d.selectSteps(parentSteps)
		if !ok {
			return false, conflict(NoParent, "the parent of the element does not exist")
		}
		parent, outer = loc.el, loc.outer.with(loc.el.nsDecls)
	} else {
		outer = documentScope()
	}

	// The fragment is read in the scope of its parent, so that it can
	// use the prefixes declared there (RFC 4825 8.2.3).
	if !utf8.Valid(fragment) {
		return false, conflict(NotUTF8, "the body is not UTF-8")
	}
	p, err := parseXML(fragment, outer)
	if err != nil {
		if errors.Is(err, errNotUTF8) {
			return false, conflict(NotUTF8, "the body is not UTF-8")
		}
		return false, conflict(NotXMLFrag, "%v", err)
	}
	if len(p.prolog) != 0 {
		return false, conflict(NotXMLFrag, "the body holds more than one element")
	}
	newEl := p.root

	if parent == nil {
		// The parent is the document, which holds only the root.
		if target.apply([]*element{d.root}) != 0 {
			return false, conflict(CannotInsert, "a document has a single root element")
		}
		old := d.root
		d.root = newEl
		if loc, ok := d.selectSteps(sel.steps); !ok || loc.el != newEl {
			d.root = old
			return false, conflict(CannotInsert, "the selector would not select the new root")
		}
		return false, nil
	}

	saved := slices.Clone(parent.children)
	children := parent.childElements()
	if i := target.apply(children); i >= 0 {
		// Replacement (RFC 4825 8.2.4).
		parent.children[childIndex(parent, children[i])] = newEl
	} else {
		at, ok := insertionPoint(parent, target, newEl)
		if !ok {
			return false, conflict(CannotInsert, "there is no place for the element that the selector would select")
		}
		parent.children = slices.Insert(parent.children, at, node(newEl))
		created = true
	}
	// A GET of the request URI has to return what was put (RFC 4825
	// 8.2.3, 8.2.4).
	if loc, ok := d.selectSteps(sel.steps); !ok || loc.el != newEl {
		parent.children = saved
		return false, conflict(CannotInsert, "after the PUT the selector would not select the new element")
	}
	return created, nil
}

// insertionPoint finds where a new child goes (RFC 4825 8.2.3). The
// rules keep elements of the same name together: without a position the
// element goes right after the last sibling of its name ("earliest
// last"), or at the very end when there is none; with a position n it
// goes right after the (n-1)th sibling of its name, or of any name for
// a wildcard ("earliest nth").
func insertionPoint(parent *element, target step, newEl *element) (int, bool) {
	siblings := parent.childElements()
	var same []*element
	for _, s := range siblings {
		if target.any || s.name == newEl.name {
			same = append(same, s)
		}
	}
	after := func(e *element) int { return childIndex(parent, e) + 1 }
	end := len(parent.children)

	switch {
	case target.position == 0 && (target.any || len(same) == 0):
		return end, true
	case target.position == 0:
		return after(same[len(same)-1]), true
	case target.position == 1 && len(same) == 0:
		return end, true
	case target.position == 1:
		return childIndex(parent, same[0]), true
	case target.position-1 > len(same):
		return 0, false
	default:
		return after(same[target.position-2]), true
	}
}

// PutAttribute creates or replaces the attribute that sel selects (RFC
// 4825 7.7, 8.2). value is the body of the request: an AttValue with
// its quotes, such as "false".
func (d *Document) PutAttribute(sel *NodeSelector, value []byte) (created bool, err error) {
	if sel.namespaces {
		return false, ErrNamespaceSelector
	}
	if sel.attribute == nil {
		return false, conflict(NotXMLAttValue, "the selector selects an element")
	}
	loc, ok := d.selectSteps(sel.steps)
	if !ok {
		return false, conflict(NoParent, "the element of the attribute does not exist")
	}
	if !utf8.Valid(value) {
		return false, conflict(NotUTF8, "the body is not UTF-8")
	}
	v, err := parseAttValue(string(value))
	if err != nil {
		return false, conflict(NotXMLAttValue, "%v", err)
	}

	e := loc.el
	savedAttrs, savedDecls := slices.Clone(e.attrs), slices.Clone(e.nsDecls)
	name := *sel.attribute
	i := slices.IndexFunc(e.attrs, func(a attribute) bool { return a.name == name })
	if i >= 0 {
		e.attrs[i].value = v
	} else {
		created = true
		a := attribute{name: name, value: v}
		if name.Space != "" {
			a.prefix = d.prefixFor(loc, name.Space, sel)
		}
		e.attrs = append(e.attrs, a)
	}
	if after, ok := d.selectSteps(sel.steps); !ok || after.el != e {
		e.attrs, e.nsDecls = savedAttrs, savedDecls
		return false, conflict(CannotInsert, "after the PUT the selector would not select the attribute")
	}
	return created, nil
}

// prefixFor finds a prefix bound to uri at the element, declaring the
// prefix of the query component on the element when none is.
func (d *Document) prefixFor(loc located, uri string, sel *NodeSelector) string {
	inScope := loc.outer.with(loc.el.nsDecls)
	for _, p := range slices.Sorted(maps.Keys(inScope)) {
		if inScope[p] == uri && p != "" {
			return p
		}
	}
	for _, p := range slices.Sorted(maps.Keys(sel.bindings)) {
		if _, taken := inScope[p]; sel.bindings[p] == uri && !taken {
			loc.el.nsDecls = append(loc.el.nsDecls, nsDecl{prefix: p, uri: uri})
			return p
		}
	}
	p := "ns1"
	for i := 2; inScope[p] != ""; i++ {
		p = fmt.Sprintf("ns%d", i)
	}
	loc.el.nsDecls = append(loc.el.nsDecls, nsDecl{prefix: p, uri: uri})
	return p
}

// Delete removes the element or attribute that sel selects (RFC 4825
// 7.5, 7.8, 8.4). The whitespace around a removed element stays. A
// selector that selects nothing is ErrNoMatch (404).
func (d *Document) Delete(sel *NodeSelector) error {
	if sel.namespaces {
		return ErrNamespaceSelector
	}
	loc, ok := d.selectSteps(sel.steps)
	if !ok {
		return ErrNoMatch
	}
	if sel.attribute != nil {
		e := loc.el
		i := slices.IndexFunc(e.attrs, func(a attribute) bool { return a.name == *sel.attribute })
		if i < 0 {
			return ErrNoMatch
		}
		saved := slices.Clone(e.attrs)
		e.attrs = slices.Delete(e.attrs, i, i+1)
		if _, _, err := d.Get(sel); err == nil {
			e.attrs = saved
			return conflict(CannotDelete, "after the DELETE the selector would still select an attribute")
		}
		return nil
	}
	if loc.parent == nil {
		return conflict(CannotDelete, "the root element cannot be deleted; delete the document instead")
	}
	saved := slices.Clone(loc.parent.children)
	loc.parent.children = slices.Delete(loc.parent.children, loc.index, loc.index+1)
	// DELETE must be idempotent: the selector may not now pick another
	// element (RFC 4825 8.4).
	if _, ok := d.selectSteps(sel.steps); ok {
		loc.parent.children = saved
		return conflict(CannotDelete, "after the DELETE the selector would select another element")
	}
	return nil
}
