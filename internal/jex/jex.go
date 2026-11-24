// Package jex reads and evaluates the Jex expressions of TS 32.161
// (Release 19, V19.0.0) that the 3GPP management services use to pick
// what a request or a subscription is about: the Jex conditions profile
// of 7.6, whose grammar is A.4. An expression is evaluated against a
// JSON document, such as an alarm notification or an alarm list, by
// the rules of clause 6 that map JSON to the XPath data model.
//
// The notificationFilter of an NtfSubscriptionControl is such an
// expression: the schema JexConditions of TS28623_ComDefs.yaml 19.6.0
// (TS 28.623 V20.3.0). ParseCondition reads it and Condition.Matches
// evaluates it against a notification.
//
// What of the grammar is evaluated:
//
//	location paths, absolute (/a/b) or relative (a/b), with *, . and a predicate per step
//	comparisons = != with a string, a number, true, false or null
//	comparisons < > <= >= with a number
//	and, or, not(...) and parentheses
//	contains(path,"text")
//
// White space is allowed only as the single space on each side of "and"
// and "or" (7.2.4), and a string cannot hold a quotation mark (A.4).
// A comparison or contains() is true only when its path selects
// exactly one data node with a scalar value of the same type as the
// other side (7.5, C.2); a path alone is true when it selects any node.
//
// An expression that does not follow the grammar is a *SyntaxError. TS
// 32.158 6.6.5.2 answers a GET with such a query parameter with 400,
// VALIDATION_ERROR and QUERY_PARAM_VALUES_INVALID, and 6.6.5.3.2 a PUT
// with such an attribute value with 400, VALIDATION_ERROR and
// NEW_ATTRIBUTE_VALUE_INVALID. One that follows the grammar but asks
// for more than the package evaluates is an *UnsupportedError, which
// 6.6.5.2 answers with 500, SERVER_LIMITATION and
// QUERY_PARAMS_TOO_COMPLEX. TS 32.161 7.2.3 leaves it to the processor
// how it reports either.
//
// An expression longer than 4096 bytes, or nested deeper than 64
// levels of parentheses, not() and predicates, is an *UnsupportedError
// too: the parser descends once per level, and the limits keep a client
// from making it use the stack without bound.
//
// SIMPLIFIED: TS 32.161 V19.0.0 7.5 and A.4. A predicate that is a
// position, such as [0], which selects an element of a JSON array
// (AttributeElementSelector), is an *UnsupportedError. The documents
// here are notifications and alarm lists, whose records are a JSON
// object keyed by alarm id, so no expression needs one.
package jex

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SyntaxError reports an expression that does not follow the grammar
// of TS 32.161 A.4.
type SyntaxError struct {
	Expr string
	// Offset is the byte in Expr where the reading stopped.
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("jex: %s at offset %d of %q", e.Msg, e.Offset, e.Expr)
}

// UnsupportedError reports an expression that follows the grammar but
// that the package does not evaluate.
type UnsupportedError struct {
	Expr   string
	Offset int
	// What names the part of the expression that is not evaluated.
	What string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("jex: %s at offset %d of %q is not supported", e.What, e.Offset, e.Expr)
}

// Condition is a Jex conditions expression (TS 32.161 7.6), such as
// perceivedSeverity="CRITICAL" and contains(href,"ManagedElement=scscf").
type Condition struct {
	text string
	expr orExpr
}

// ParseCondition reads a Jex conditions expression.
func ParseCondition(s string) (*Condition, error) {
	p := &parser{text: s}
	e, err := p.parseConditions()
	if err != nil {
		return nil, err
	}
	return &Condition{text: s, expr: e}, nil
}

func (c *Condition) String() string { return c.text }

// Matches evaluates the condition against document, such as the body of
// a notification (Document). The root node of the document is the
// context node, so that a relative path such as perceivedSeverity and
// an absolute one such as /perceivedSeverity name the same parameter
// (TS 32.161 Annex D, notification filtering).
func (c *Condition) Matches(document map[string]any) bool {
	root := node{value: document}
	return c.expr.eval(root, root)
}

// Document turns v, such as a notification of package mns, into the
// JSON document that expressions are evaluated against: v is encoded
// as JSON and decoded again, keeping numbers as json.Number. v must
// encode as a JSON object (TS 32.161 5.1).
func Document(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("jex: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return nil, fmt.Errorf("jex: %T is not a JSON object", v)
	}
	return doc, nil
}
