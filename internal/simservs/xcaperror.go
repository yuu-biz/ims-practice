package simservs

import (
	"encoding/xml"
	"fmt"
)

// The detailed conflict report that an XCAP server puts in the body of
// a 409 (Conflict) response (RFC 4825 11).
const (
	NamespaceXCAPError = "urn:ietf:params:xml:ns:xcap-error"
	ErrorMIMEType      = "application/xcap-error+xml"
)

// ConflictCondition is one of the error elements of RFC 4825 11.1.
type ConflictCondition string

const (
	NotWellFormed         ConflictCondition = "not-well-formed"
	NotXMLFrag            ConflictCondition = "not-xml-frag"
	NoParent              ConflictCondition = "no-parent"
	SchemaValidationError ConflictCondition = "schema-validation-error"
	NotXMLAttValue        ConflictCondition = "not-xml-att-value"
	CannotInsert          ConflictCondition = "cannot-insert"
	CannotDelete          ConflictCondition = "cannot-delete"
	UniquenessFailure     ConflictCondition = "uniqueness-failure"
	ConstraintFailure     ConflictCondition = "constraint-failure"
	NotUTF8               ConflictCondition = "not-utf-8"
)

// ConflictError is an error that an XCAP server answers with 409
// (Conflict) and the report of Report in the body. TS 24.623 also
// answers 409 when a user changes what the authorization policy of
// 6.2 or the operator does not allow (5.3.2.4); those carry
// ConstraintFailure.
type ConflictError struct {
	Condition ConflictCondition
	// Phrase is text for a human user (the phrase attribute).
	Phrase string
	// Field is set for UniquenessFailure: the node selector, relative to
	// the document, of the value that is not unique (the field attribute
	// of <exists>).
	Field string
}

func (e *ConflictError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Condition, e.Phrase, e.Field)
	}
	return fmt.Sprintf("%s: %s", e.Condition, e.Phrase)
}

func conflict(c ConflictCondition, format string, args ...any) *ConflictError {
	return &ConflictError{Condition: c, Phrase: fmt.Sprintf(format, args...)}
}

// Report returns the <xcap-error> document for the body of the 409
// response, of type ErrorMIMEType (RFC 4825 11.2).
func (e *ConflictError) Report() []byte {
	type exists struct {
		Field string `xml:"field,attr"`
	}
	type condition struct {
		XMLName xml.Name
		Phrase  string  `xml:"phrase,attr,omitempty"`
		Exists  *exists `xml:"urn:ietf:params:xml:ns:xcap-error exists"`
	}
	type report struct {
		XMLName   xml.Name `xml:"urn:ietf:params:xml:ns:xcap-error xcap-error"`
		Condition condition
	}
	r := report{Condition: condition{
		XMLName: xml.Name{Space: NamespaceXCAPError, Local: string(e.Condition)},
		Phrase:  e.Phrase,
	}}
	if e.Condition == UniquenessFailure && e.Field != "" {
		r.Condition.Exists = &exists{Field: e.Field}
	}
	b, err := xml.Marshal(r)
	if err != nil {
		panic(err) // the structs above always marshal
	}
	p, err := parseXML(b, documentScope())
	if err != nil {
		panic(err)
	}
	canonicalize(p.root)
	return append([]byte(xml.Header), p.root.serialize()...)
}
