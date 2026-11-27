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
