package simservs

import (
	"bytes"
	"slices"
	"strings"
)

// The XCAP server of the MMTel AS keeps no document of its own: it reads
// MMTelServices from the HSS, shows the user parts as simservs.xml, and
// writes a change back into the user parts (TS 29.364 5), so that the
// settings live in one place only. The two
// functions below do that on Documents, so that elements the typed view
// does not model, and the services it does not model, survive.

// UserDocument builds the simservs document of the user from an
// MMTelServices document: the user part of every complete-* element,
// in document order. The parts keep their content, and the namespace
// declarations they relied on are carried along.
func UserDocument(mmtel *Document) (*Document, error) {
	root := mmtel.root
	if root.name != xmlName(NamespaceSimservs, "MMTelServices") {
		return nil, conflict(SchemaValidationError, "the root element is not <MMTelServices>")
	}
	user := &element{
		prefix:  root.prefix,
		name:    xmlName(NamespaceSimservs, "simservs"),
		nsDecls: append([]nsDecl(nil), root.nsDecls...),
	}
	for _, complete := range root.childElements() {
		for _, part := range userParts(complete) {
			c := part.clone().(*element)
			// The part was in the scope of its complete-* element too.
			c.nsDecls = withMissingDecls(c.nsDecls, complete.nsDecls)
			user.children = append(user.children, charData("\n  "), c)
		}
	}
	if len(user.children) > 0 {
		user.children = append(user.children, charData("\n"))
	}
	return &Document{root: user}, nil
}

// userParts returns the user parts of a complete-* element: the
// children that are not operator parts (TS 29.364 7.1). HOLD, CAT and
// the other services without user data have none.
func userParts(complete *element) []*element {
	if complete.name.Space != NamespaceSimservs || !strings.HasPrefix(complete.name.Local, "complete-") {
		return nil
	}
	var out []*element
	for _, c := range complete.childElements() {
		if !strings.HasPrefix(c.name.Local, "operator-") {
			out = append(out, c)
		}
	}
	return out
}

// withMissingDecls adds to own the declarations of outer whose prefix
// own does not declare.
func withMissingDecls(own, outer []nsDecl) []nsDecl {
	out := append([]nsDecl(nil), own...)
	for _, d := range outer {
		declared := false
		for _, o := range own {
			if o.prefix == d.prefix {
				declared = true
			}
		}
		if !declared {
			out = append(out, d)
		}
	}
	return out
}

// SetUserDocument writes the services of a simservs document into the
// user parts of an MMTelServices document. The caller has checked user
// with ValidateSimservsDocument and CheckOwnerChange; services absent
// from user stay as they are. A service that mmtel does not hold, or a
// change to a service that the operator has not authorized, is a
// *ConflictError (TS 24.623 5.3.2.4). On an error mmtel is unchanged.
func SetUserDocument(mmtel, user *Document) error {
	if user.root.name != xmlName(NamespaceSimservs, "simservs") {
		return conflict(SchemaValidationError, "the root element is not <simservs>")
	}
	if _, err := UserDocument(mmtel); err != nil {
		return err
	}
	type change struct {
		complete *element
		index    int
		part     *element
	}
	var changes []change
	userScope := documentScope().with(user.root.nsDecls)
	mmtelScope := documentScope().with(mmtel.root.nsDecls)
	for _, part := range user.root.childElements() {
		complete, current := findUserPart(mmtel.root, part)
		if complete == nil {
			return conflict(ConstraintFailure, "<%s> is not provisioned for this user", part.name.Local)
		}
		completeScope := mmtelScope.with(complete.nsDecls)
		if sameContent(part, current) {
			continue
		}
		if !authorized(complete) {
			return conflict(ConstraintFailure, "<%s> is not authorized for this user", part.name.Local)
		}
		c := part.clone().(*element)
		c.nsDecls = withMissingDecls(c.nsDecls, declsMissingFrom(userScope, completeScope))
		changes = append(changes, change{complete: complete, index: childIndex(complete, current), part: c})
	}
	for _, ch := range changes {
		ch.complete.children[ch.index] = ch.part
	}
	return nil
}

// findUserPart finds the complete-* element whose user part has the
// name of part.
func findUserPart(root, part *element) (complete, current *element) {
	for _, c := range root.childElements() {
		for _, p := range userParts(c) {
			if p.name == part.name {
				return c, p
			}
		}
	}
	return nil, nil
}

// authorized reads the authorized attribute of the operator parts of a
// complete-* element (TS 29.364 7.2.3.2.1 and the clauses after it).
// Barring has two operator parts; a change needs both.
func authorized(complete *element) bool {
	found := false
	for _, c := range complete.childElements() {
		if strings.HasPrefix(c.name.Local, "operator-") {
			found = true
			if v, _ := c.attr(xmlName("", "authorized")); v != "true" && v != "1" {
				return false
			}
		}
	}
	return found
}

// declsMissingFrom lists the bindings of from that to lacks or binds to
// another namespace.
func declsMissingFrom(from, to scope) []nsDecl {
	var out []nsDecl
	for p, u := range from {
		if p != "xml" && to[p] != u {
			out = append(out, nsDecl{prefix: p, uri: u})
		}
	}
	// A fixed order keeps the written document the same from run to run.
	slices.SortFunc(out, func(a, b nsDecl) int { return strings.Compare(a.prefix, b.prefix) })
	return out
}

// sameContent compares two elements by their expanded names and
// content, whatever prefixes they were written with: canonicalize works
// from the expanded names and rewrites the prefixes the same way.
func sameContent(a, b *element) bool {
	ca, cb := a.clone().(*element), b.clone().(*element)
	canonicalize(ca)
	canonicalize(cb)
	return bytes.Equal(ca.serialize(), cb.serialize())
}

// ValidateMMTelServicesDocument checks an MMTelServices document as
// MMTelServices.Validate does. The error is a *ConflictError.
func ValidateMMTelServicesDocument(d *Document) error {
	if d.root.name != xmlName(NamespaceSimservs, "MMTelServices") {
		return conflict(SchemaValidationError, "the root element is not <MMTelServices>")
	}
	m, err := ParseMMTelServices(d.Bytes())
	if err != nil {
		return conflict(SchemaValidationError, "%v", err)
	}
	return m.Validate()
}

// RootElement writes the root element alone, without the XML
// declaration: the form that goes into the ServiceData of Sh Repository
// Data, inside the Sh-Data document.
func (d *Document) RootElement() []byte { return d.root.serialize() }
