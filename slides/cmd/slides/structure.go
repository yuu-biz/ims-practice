package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// structureOf lists what the languages of a visual deck share, one line
// for each node, group, event, scene and step: the ids, the ends of the
// events, the names of the signals and of their fields, and the complete
// state of each step. The words around them are translated; these are
// not, so that a scene, a step and a signal mean the same in both
// languages, and an address such as #select/uaa opens the same state.
func structureOf(d *deck.Deck) []string {
	var out []string
	if d.Model != nil {
		for _, n := range d.Model.Nodes {
			out = append(out, fmt.Sprintf("node %s %q at %g,%g", n.ID, n.Label, n.Col, n.Row))
		}
		for _, g := range d.Model.Groups {
			out = append(out, fmt.Sprintf("group %s of %s", g.ID, strings.Join(g.Nodes, " ")))
		}
		for _, e := range d.Model.Events {
			fields := fieldsOf(e)
			switch e.Kind {
			case deck.EventMessage:
				out = append(out, fmt.Sprintf("message %s %q (%s) %s -> %s, fields %s", e.ID, deck.PlainText(e.Label), e.Protocol, e.From, e.To, fields))
			case deck.EventProcess:
				out = append(out, fmt.Sprintf("process %s at %s, fields %s", e.ID, e.Actor, fields))
			case deck.EventRelation:
				out = append(out, fmt.Sprintf("relation %s %s -> %s", e.ID, e.From, e.To))
			}
		}
	}
	for _, s := range d.Slides {
		if s.Scene == nil {
			continue
		}
		sc := s.Scene
		out = append(out, fmt.Sprintf("slide %d: scene %s, %s of %s, %s", s.Number, sc.ID, sc.View, strings.Join(sc.Nodes, " "), sc.Basis))
		for _, st := range sc.Steps {
			out = append(out, fmt.Sprintf("scene %s step %s: events %s, active %s, focus %s, zoom %s, detail %s, checkpoint %v",
				sc.ID, st.ID, strings.Join(st.Events, " "), st.Active, strings.Join(st.Focus, " "), strings.Join(st.Zoom, " "), strings.Join(st.Detail, " "), st.Checkpoint))
		}
	}
	return out
}

// compareStructure returns the lines that differ, in the order of the
// first list.
func compareStructure(a, b []string) []string {
	var out []string
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			out = append(out, fmt.Sprintf("\n  %s\n  %s", x, y))
		}
	}
	return out
}

var (
	// literalName is the name of a header, an AVP or a parameter, such
	// as Server-Name or Call-ID: the same in every language. A name in
	// words, such as "what it stores", is translated.
	literalName = regexp.MustCompile(`^[A-Za-z0-9]+(-[A-Za-z0-9]+)+$|^[A-Z][A-Za-z0-9]*[A-Z][A-Za-z0-9]*$`)
	// literalValue are the parts of a value that no translation changes:
	// a name of a code such as DIAMETER_FIRST_REGISTRATION and a number
	// in parentheses such as (2001).
	literalValue = regexp.MustCompile(`[A-Z][A-Z0-9]*(_[A-Z0-9]+)+|\(\d+\)`)
)

// fieldsOf lists what the languages share of the fields of an event:
// how many there are, the names that are headers or AVPs, and the
// inline code, the names of codes and the numbers of the values.
func fieldsOf(e deck.Event) string {
	var parts []string
	for _, f := range e.Fields {
		var name string
		if literalName.MatchString(f.Name) {
			name = f.Name + ":"
		}
		var literal []string
		for _, span := range f.Value {
			if span.Code {
				literal = append(literal, "`"+span.Text+"`")
			} else {
				literal = append(literal, literalValue.FindAllString(span.Text, -1)...)
			}
		}
		// A translation may put the parts of a value in another order.
		slices.Sort(literal)
		parts = append(parts, "["+strings.TrimSpace(name+" "+strings.Join(literal, " "))+"]")
	}
	return fmt.Sprintf("%d %s", len(e.Fields), strings.Join(parts, " "))
}
