package jex

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// node is a data node of the document (TS 32.161 6): the name of a
// name/value pair and its value. The root node has no name, and its
// value is the whole document. A pair whose value is a JSON array is
// as many nodes as the array has items, all with the name of the pair
// (6.3.3).
type node struct {
	name  string
	value any
}

// children are the element nodes below n: one per name/value pair of
// an object, or per item of an array value. A JSON object has no order,
// so the children are sorted by name, which keeps the answers the same
// from one call to the next.
func (n node) children() []node {
	object, ok := n.value.(map[string]any)
	if !ok {
		return nil
	}
	var out []node
	for _, name := range slices.Sorted(maps.Keys(object)) {
		if items, ok := object[name].([]any); ok {
			for _, item := range items {
				out = append(out, node{name, item})
			}
			continue
		}
		out = append(out, node{name, object[name]})
	}
	return out
}

// selectNodes evaluates the path: from the root node when it is
// absolute, else from the context node, each step keeps the children
// that its name test and its predicate select.
func (p locationPath) selectNodes(root, context node) []node {
	nodes := []node{context}
	if p.absolute {
		nodes = []node{root}
	}
	for _, s := range p.steps {
		var next []node
		for _, n := range nodes {
			candidates := []node{n}
			if s.name != "." {
				candidates = n.children()
			}
			for _, c := range candidates {
				if s.name != "." && s.name != "*" && c.name != s.name {
					continue
				}
				if s.predicate == nil || s.predicate.eval(root, c) {
					next = append(next, c)
				}
			}
		}
		nodes = next
	}
	return nodes
}

func (e orExpr) eval(root, context node) bool {
	for _, t := range e.terms {
		if t.eval(root, context) {
			return true
		}
	}
	return false
}

func (e andExpr) eval(root, context node) bool {
	for _, o := range e.operands {
		if !o.eval(root, context) {
			return false
		}
	}
	return true
}

func (g group) eval(root, context node) bool { return g.expr.eval(root, context) }

func (n not) eval(root, context node) bool { return !n.expr.eval(root, context) }

func (t pathTest) eval(root, context node) bool {
	return len(t.path.selectNodes(root, context)) > 0
}

// single is the scalar value of the one node that the path selects. A
// comparison is false when the path selects no node, several, or one
// whose value is an object or an array (TS 32.161 C.2).
func single(p locationPath, root, context node) (any, bool) {
	nodes := p.selectNodes(root, context)
	if len(nodes) != 1 {
		return nil, false
	}
	switch nodes[0].value.(type) {
	case map[string]any, []any:
		return nil, false
	}
	return nodes[0].value, true
}

func (c contains) eval(root, context node) bool {
	v, ok := single(c.path, root, context)
	s, isString := v.(string)
	return ok && isString && strings.Contains(s, c.text)
}

// eval compares the value of the path with the literal. Values of
// different types are never equal, nor unequal: the comparison is
// false (7.5).
func (c comparison) eval(root, context node) bool {
	v, ok := single(c.path, root, context)
	if !ok {
		return false
	}
	switch c.value.kind {
	case stringLiteral:
		s, ok := v.(string)
		return ok && c.holds(strings.Compare(s, c.value.text))
	case numberLiteral:
		f, ok := toFloat(v)
		return ok && c.holds(compareFloats(f, c.value.number))
	case trueLiteral, falseLiteral:
		b, ok := v.(bool)
		return ok && c.holds(boolDiff(b, c.value.kind == trueLiteral))
	case nullLiteral:
		return v == nil && c.holds(0)
	}
	return false
}

// holds says whether the operator holds for a value that compares as
// diff with the literal: below zero when smaller, zero when equal.
// Only = and != reach here for a string, a boolean or null.
func (c comparison) holds(diff int) bool {
	switch c.op {
	case "=":
		return diff == 0
	case "!=":
		return diff != 0
	case "<":
		return diff < 0
	case ">":
		return diff > 0
	case "<=":
		return diff <= 0
	case ">=":
		return diff >= 0
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func boolDiff(a, b bool) int {
	if a == b {
		return 0
	}
	return 1
}
