package simservs

import (
	"bytes"
	"encoding/xml"
)

// ValidateSimservsDocument checks a simservs document after a change
// (RFC 4825 8.2.5): that it is a simservs document at all, then
// Simservs.Validate. The error is a *ConflictError.
func ValidateSimservsDocument(d *Document) error {
	if d.root.name != xmlName(NamespaceSimservs, "simservs") {
		return conflict(SchemaValidationError, "the root element is not <simservs>")
	}
	// The schema puts <extensions> once, after the services (TS 24.623
	// 6.3); the typed view does not keep the order, so this is checked
	// here.
	seenExtensions := false
	for _, c := range d.root.childElements() {
		if seenExtensions {
			return conflict(SchemaValidationError, "<%s> comes after <extensions>", c.name.Local)
		}
		seenExtensions = c.name == xmlName(NamespaceSimservs, "extensions")
	}
	s, err := ParseSimservs(d.Bytes())
	if err != nil {
		return conflict(SchemaValidationError, "%v", err)
	}
	return s.Validate()
}

// modelledServices are the children of <simservs> that this package
// understands. A user may change them, within the limits below.
var modelledServices = map[string]bool{
	"originating-identity-presentation":             true,
	"originating-identity-presentation-restriction": true,
	"terminating-identity-presentation":             true,
	"terminating-identity-presentation-restriction": true,
	"communication-diversion":                       true,
	"communication-waiting":                         true,
	"incoming-communication-barring":                true,
	"outgoing-communication-barring":                true,
}

// CheckOwnerChange applies the authorization policy of TS 24.623 6.2 to
// a change that the owner of the document made: before is the stored
// document and after the result of the request. The owner may change
// the content of the services but may not add or remove a service, nor
// add or remove an attribute of a service element. Children of
// <simservs> that this package does not model are treated as read-only
// (TS 24.623 6.2 d and NOTE 2) and must stay as they were. The error is
// a *ConflictError, which the server answers with 409.
//
// SIMPLIFIED: TS 24.623 5.2.3.4 and 6.2 e. There are no service
// capability fragments (the *-serv-cap elements), so a change is not
// checked against what the operator provisioned beyond the services and
// conditions Validate accepts.
func CheckOwnerChange(before, after *Document) error {
	if before.root.name != after.root.name {
		return conflict(ConstraintFailure, "the root element cannot be replaced by another element")
	}
	old, next := before.root.childElements(), after.root.childElements()
	used := make([]bool, len(next))
	for _, o := range old {
		j := -1
		for k, n := range next {
			if !used[k] && n.name == o.name {
				j = k
				break
			}
		}
		if j < 0 {
			return conflict(ConstraintFailure, "<%s> cannot be removed from <simservs>", o.name.Local)
		}
		used[j] = true
		n := next[j]
		if !sameAttributeNames(o, n) {
			return conflict(ConstraintFailure, "attributes of <%s> cannot be added or removed", o.name.Local)
		}
		if !isModelledService(o.name) && !bytes.Equal(o.serialize(), n.serialize()) {
			return conflict(ConstraintFailure, "<%s> is read-only", o.name.Local)
		}
	}
	for k, n := range next {
		if !used[k] {
			return conflict(ConstraintFailure, "<%s> cannot be added to <simservs>", n.name.Local)
		}
	}
	return nil
}

func isModelledService(n xml.Name) bool {
	return n.Space == NamespaceSimservs && modelledServices[n.Local]
}

func sameAttributeNames(a, b *element) bool {
	if len(a.attrs) != len(b.attrs) {
		return false
	}
	for _, x := range a.attrs {
		if _, ok := b.attr(x.name); !ok {
			return false
		}
	}
	return true
}
