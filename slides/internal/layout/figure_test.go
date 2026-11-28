package layout

import (
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

const figureSource = "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n" +
	"```ims-model\n" +
	"nodes:\n" +
	"  - {id: a, label: A, at: [0, 0]}\n" +
	"  - {id: b, label: B, at: [1, 0]}\n" +
	"  - {id: c, label: C, at: [1, 1]}\n" +
	"events:\n" +
	"  - {id: req, msg: REQ, from: a, to: b, proto: SIP, fields: [{Request-URI: \"`sip:b`\"}, {Via: a}]}\n" +
	"  - {id: ask, msg: ASK, from: b, to: c, proto: Cx}\n" +
	"  - {id: tell, msg: TELL, from: c, to: b, proto: Cx}\n" +
	"  - {id: work, do: thinks, at: b}\n" +
	"  - {id: ans, msg: 200 OK, from: b, to: a, proto: SIP}\n" +
	"```\n\n---\n\n# T\n\n---\n\n## A map\n\n" +
	"```ims-scene\n" +
	"id: flow\n" +
	"steps:\n" +
	"  - {id: send, title: Send, show: [req], text: A sends.}\n" +
	"  - {id: ask, title: Ask, only: [ask, tell], active: ask, text: B asks C.}\n" +
	"  - {id: answer, title: Answer, show: [work, ans], detail: [], text: B answers.}\n" +
	"```\n\n---\n\n## A ladder\n\n" +
	"```ims-scene\n" +
	"id: lad\n" +
	"view: ladder\n" +
	"steps:\n" +
	"  - {id: one, title: One, show: [req], detail: [], text: A sends.}\n" +
	"  - {id: two, title: Two, show: [ask, tell], text: B asks C.}\n" +
	"  - {id: three, title: Three, show: [ans], text: B answers.}\n" +
	"```\n"

func figureDeck(t *testing.T) *deck.Deck {
	t.Helper()
	d, err := deck.Parse("figure.md", figureSource)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func sceneOf(t *testing.T, d *deck.Deck, id string) *deck.Scene {
	t.Helper()
	for _, s := range d.Slides {
		if s.Scene != nil && s.Scene.ID == id {
			return s.Scene
		}
	}
	t.Fatalf("the deck has no scene %s", id)
	return nil
}

func figure(t *testing.T, d *deck.Deck, sceneID, stepID string) *FigureShapes {
	t.Helper()
	f, err := Figure(d.Model, sceneOf(t, d, sceneID), stepID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// shapeNames are the names of the shapes of a figure, those inside its
// groups included.
func shapeNames(elements []Element) map[string]bool {
	names := map[string]bool{}
	var walk func([]Element)
	walk = func(list []Element) {
		for _, e := range list {
			switch e := e.(type) {
			case *Group:
				walk(e.Elements)
			case *TextBox:
				names[e.Name] = true
			case *Line:
				names[e.Name] = true
			case *Rect:
				names[e.Name] = true
			}
		}
	}
	walk(elements)
	return names
}

// TestFigureOfAnUnknownStepIsAnError makes sure that a figure that names
// a step the scene does not have fails instead of drawing some other step.
func TestFigureOfAnUnknownStepIsAnError(t *testing.T) {
	d := figureDeck(t)
	if _, err := Figure(d.Model, sceneOf(t, d, "flow"), "nosuch"); err == nil {
		t.Error("a figure of a step that does not exist was drawn")
	}
}

// TestFigureDrawsOnlyTheEventsOfItsStep makes sure that a figure shows
// the diagram as its step leaves it: the events of that step, and none
// that the step hides or that a later step adds.
func TestFigureDrawsOnlyTheEventsOfItsStep(t *testing.T) {
	d := figureDeck(t)
	names := shapeNames(figure(t, d, "flow", "ask").Elements)
	for _, want := range []string{"event ask", "event tell", "node a", "node b", "node c"} {
		if !names[want] {
			t.Errorf("the figure of step ask has no shape %q", want)
		}
	}
	for _, absent := range []string{"event req", "event work", "event ans"} {
		if names[absent] {
			t.Errorf("the figure of step ask draws %q, which the step does not show", absent)
		}
	}
	if names := shapeNames(figure(t, d, "flow", "send").Elements); !names["event req"] || names["event ans"] {
		t.Error("the figure of step send does not draw req alone")
	}
}

// TestFigureDetailIsATableUnderTheDiagram makes sure that the fields of
// the event that a step details are on the figure, in a table below the
// diagram, and that the figure grows to hold them.
func TestFigureDetailIsATableUnderTheDiagram(t *testing.T) {
	d := figureDeck(t)
	detailed := figure(t, d, "flow", "send")
	plain := figure(t, d, "flow", "answer")
	var diagram *Group
	var tables []*Table
	for _, e := range detailed.Elements {
		switch e := e.(type) {
		case *Group:
			diagram = e
		case *Table:
			tables = append(tables, e)
		}
	}
	if diagram == nil || len(tables) != 1 {
		t.Fatalf("the figure has diagram %v and %d tables, want a diagram and one table", diagram != nil, len(tables))
	}
	table := tables[0]
	if table.Y < diagram.Y+diagram.H {
		t.Errorf("the table starts at %v, above the bottom of the diagram at %v", table.Y, diagram.Y+diagram.H)
	}
	if text := Text(Page{Elements: []Element{table}}); len(table.Rows) != 2 || !strings.Contains(text, "Request-URI") || !strings.Contains(text, "sip:b") {
		t.Errorf("the table does not hold the two fields of req: %q", text)
	}
	if table.Y+table.H > detailed.H+0.01 {
		t.Errorf("the table ends at %v, below the figure of height %v", table.Y+table.H, detailed.H)
	}
	for _, e := range plain.Elements {
		if _, ok := e.(*Table); ok {
			t.Error("the figure of a step without detail has a table")
		}
	}
	if detailed.H <= plain.H {
		t.Errorf("the figure with fields is %v high, no higher than the one without at %v", detailed.H, plain.H)
	}
}

// TestLadderFigureEndsAtItsLastRow makes sure that a ladder figure is cut
// below the last message that its step draws, so that a figure of an
// early step does not carry the empty rows of the later ones.
func TestLadderFigureEndsAtItsLastRow(t *testing.T) {
	d := figureDeck(t)
	one := figure(t, d, "lad", "one")
	two := figure(t, d, "lad", "two")
	three := figure(t, d, "lad", "three")
	if !(one.H < two.H && two.H < three.H) {
		t.Errorf("the heights of the steps are %v, %v, %v; want each later step taller", one.H, two.H, three.H)
	}
	if one.W != three.W {
		t.Errorf("the widths of the steps are %v and %v; a ladder keeps its width", one.W, three.W)
	}
}

// TestWrapTextKeepsTheText makes sure that a text too long for its box
// is cut into several lines that, put back together, are the text:
// nothing is lost or repeated, in Japanese or in English.
func TestWrapTextKeepsTheText(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("登録の要求は P-CSCF から I-CSCF を通って S-CSCF に届きます。", 4),
		strings.Repeat("The request goes from the P-CSCF through the I-CSCF to the S-CSCF. ", 4),
	} {
		lines := WrapText([]Run{{Text: text}}, 16, 240)
		if len(lines) < 3 {
			t.Errorf("%d lines for %q, want several", len(lines), text)
		}
		joined := strings.ReplaceAll(strings.Join(lines, ""), " ", "")
		if want := strings.ReplaceAll(text, " ", ""); joined != want {
			t.Errorf("the lines\n%q\nare not the text\n%q", joined, want)
		}
	}
}

// TestWrapTextLeavesAShortTextOnOneLine makes sure that a text that fits
// its box is not broken.
func TestWrapTextLeavesAShortTextOnOneLine(t *testing.T) {
	lines := WrapText([]Run{{Text: "REGISTER "}, {Text: "sip:ims", Code: true}}, 16, 400)
	if len(lines) != 1 || lines[0] != "REGISTER sip:ims" {
		t.Errorf("lines = %q, want the text on one line", lines)
	}
}

// TestWrapTextBreaksAtABreak makes sure that a break in the runs starts
// a new line even when the text would fit on one.
func TestWrapTextBreaksAtABreak(t *testing.T) {
	lines := WrapText([]Run{{Text: "first"}, {Break: true}, {Text: "second"}}, 16, 400)
	if len(lines) != 2 || lines[0] != "first" || lines[1] != "second" {
		t.Errorf("lines = %q, want first and second on two lines", lines)
	}
}
