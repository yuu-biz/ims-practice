package main

import (
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

func visual(t *testing.T, lang, role, to, step string) *deck.Deck {
	t.Helper()
	source := "---\ntitle: T\nfooter: F\nlang: " + lang + "\nformat: visual\n---\n\n" +
		"```ims-model\nnodes:\n  - {id: a, label: A, role: " + role + ", at: [0, 0]}\n  - {id: b, label: B, at: [1, 0]}\n  - {id: c, label: C, at: [2, 0]}\n" +
		"events:\n  - {id: req, msg: REQ, from: a, to: " + to + ", proto: SIP}\n```\n\n---\n\n# T\n\n---\n\n## S\n\n" +
		"```ims-scene\nid: s\nsteps:\n  - {id: " + step + ", title: X, show: [req], text: Y}\n```\n"
	d, err := deck.Parse(lang+".md", source)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestStructureComparesWhatTheLanguagesShare makes sure that the words
// may differ between the languages and the nodes, the events and the
// steps may not.
func TestStructureComparesWhatTheLanguagesShare(t *testing.T) {
	en := visual(t, "en", "the first", "b", "go")
	if p := compareStructure(structureOf(en), structureOf(visual(t, "ja", "最初", "b", "go"))); len(p) != 0 {
		t.Errorf("translated words differ: %v", p)
	}
	if p := compareStructure(structureOf(en), structureOf(visual(t, "ja", "最初", "c", "go"))); len(p) == 0 || !strings.Contains(p[0], "a -> c") {
		t.Errorf("another receiver passes: %v", p)
	}
	if p := compareStructure(structureOf(en), structureOf(visual(t, "ja", "最初", "b", "went"))); len(p) == 0 {
		t.Error("another step passes")
	}
}

// TestStructureComparesLiteralFields makes sure that a header name and
// the codes and numbers of a value are the same in both languages, and
// that the words of a field are not compared.
func TestStructureComparesLiteralFields(t *testing.T) {
	field := func(name, value string) []string {
		e := deck.Event{ID: "x", Kind: deck.EventMessage, Label: []deck.Span{{Text: "UAA"}}, From: "a", To: "b", Protocol: "Cx",
			Fields: []deck.Field{{Name: name, Value: []deck.Span{{Text: value}}}}}
		return []string{fieldsOf(e)}
	}
	if p := compareStructure(field("Experimental-Result", "DIAMETER_FIRST_REGISTRATION (2001)"), field("Experimental-Result", "初回の DIAMETER_FIRST_REGISTRATION (2001)")); len(p) != 0 {
		t.Errorf("translated words differ: %v", p)
	}
	if p := compareStructure(field("what it stores", "this S-CSCF"), field("記録すること", "この S-CSCF")); len(p) != 0 {
		t.Errorf("translated names differ: %v", p)
	}
	for _, other := range [][]string{
		field("Server-Name", "DIAMETER_FIRST_REGISTRATION (2001)"),
		field("Experimental-Result", "DIAMETER_SUBSEQUENT_REGISTRATION (2001)"),
		field("Experimental-Result", "DIAMETER_FIRST_REGISTRATION (2002)"),
	} {
		if p := compareStructure(field("Experimental-Result", "DIAMETER_FIRST_REGISTRATION (2001)"), other); len(p) == 0 {
			t.Errorf("%v passes", other)
		}
	}
}
