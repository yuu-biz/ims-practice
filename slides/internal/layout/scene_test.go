package layout

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

const sceneSource = "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n" +
	"```ims-model\n" +
	"nodes:\n" +
	"  - {id: a, label: A, role: the first, at: [0, 0]}\n" +
	"  - {id: b, label: B, role: the second, at: [1, 0]}\n" +
	"  - {id: c, label: C, at: [1, 1]}\n" +
	"events:\n" +
	"  - {id: req, msg: REQ, from: a, to: b, proto: SIP, fields: [{Request-URI: \"`sip:b`\"}]}\n" +
	"  - {id: ask, msg: ASK, from: b, to: c, proto: Cx}\n" +
	"  - {id: tell, msg: TELL, from: c, to: b, proto: Cx}\n" +
	"  - {id: work, do: thinks, at: b}\n" +
	"  - {id: ans, msg: 200 OK, from: b, to: a, proto: SIP}\n" +
	"```\n\n---\n\n# T\n\n---\n\n## A scene\n\n" +
	"```ims-scene\n" +
	"id: flow\n" +
	"lead: What happens.\n" +
	"steps:\n" +
	"  - {id: send, title: Send, show: [req], checkpoint: true, text: A sends.}\n" +
	"  - {id: ask, title: Ask, show: [ask, tell], active: ask, text: B asks C.}\n" +
	"  - {id: think, title: Think, show: [work], text: B thinks.}\n" +
	"  - {id: answer, title: Answer, show: [ans], text: B answers.}\n" +
	"```\n\nThe longer explanation.\n"

func TestSceneGivesAPagePerCheckpointAndOneForItsText(t *testing.T) {
	d, err := deck.Parse("scene.md", sceneSource)
	if err != nil {
		t.Fatal(err)
	}
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if len(pages) != 4 {
		t.Fatalf("%d pages, want the title, two checkpoints and the text", len(pages))
	}
	if pages[1].Step != "send" || pages[2].Step != "answer" || pages[3].Step != "" || pages[3].Slide != 2 {
		t.Errorf("pages = %+v", []Page{pages[1], pages[2], pages[3]})
	}
	for i, p := range pages {
		if p.Number != i+1 {
			t.Errorf("page %d has number %d", i+1, p.Number)
		}
	}
	// The second checkpoint keeps the text of the three steps since the
	// first, numbered as the diagram numbers the events.
	text := Text(pages[2])
	for _, want := range []string{"② Ask", "B asks C.", "③ Think", "④ Answer", "B answers.", "What happens."} {
		if !strings.Contains(text, want) {
			t.Errorf("the page of the checkpoint has no %q:\n%s", want, text)
		}
	}
	if !strings.Contains(Text(pages[3]), "Details") || !strings.Contains(Text(pages[3]), "The longer explanation.") {
		t.Error("the page of the text has no tag or no text")
	}
}

// TestDiagramIsNativeShapes makes sure that a diagram is drawn as shapes
// that PowerPoint can edit, each named after its node or event: a node
// is one box with its text, a message a line with an arrowhead.
func TestDiagramIsNativeShapes(t *testing.T) {
	d, err := deck.Parse("scene.md", sceneSource)
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := Deck(d)
	var group *Group
	for _, e := range pages[2].Elements {
		if g, ok := e.(*Group); ok {
			group = g
		}
	}
	if group == nil || group.Name != "diagram flow" {
		t.Fatal("the page has no group of the diagram")
	}
	names := map[string]Element{}
	for _, e := range group.Elements {
		switch e := e.(type) {
		case *TextBox:
			names[e.Name] = e
		case *Line:
			names[e.Name] = e
		}
	}
	for _, n := range []string{"node a", "node b", "node c", "event req", "label req", "event ans", "event work"} {
		if names[n] == nil {
			t.Errorf("no shape %q", n)
		}
	}
	if l, ok := names["event ans"].(*Line); !ok || !l.HeadEnd || l.Width <= names["event req"].(*Line).Width {
		t.Error("the active message is not the strongest arrow")
	}
	if l, ok := names["event ask"].(*Line); !ok || !l.Dash {
		t.Error("a Diameter message is not dashed")
	}
	if box := names["node a"].(*TextBox); !box.Round || !strings.Contains(Text(Page{Elements: []Element{box}}), "the first") {
		t.Error("a node is not a rounded box with its name and role")
	}
}

func TestLabelsKeepClearOfNodes(t *testing.T) {
	d, err := deck.Parse("scene.md", sceneSource)
	if err != nil {
		t.Fatal(err)
	}
	diagram := Draw(d.Model, d.Slides[1].Scene)
	for _, e := range diagram.Events {
		for _, n := range diagram.Nodes {
			if e.Kind != deck.EventProcess && e.LabelBox().overlaps(n.Box) {
				t.Errorf("the label of %s covers node %s", e.ID, n.ID)
			}
		}
		for _, o := range diagram.Events {
			if o.ID < e.ID && e.LabelBox().overlaps(o.LabelBox()) {
				t.Errorf("the labels of %s and %s overlap", e.ID, o.ID)
			}
		}
	}
}

// TestLargeDiagramGetsAPageOfItsOwn makes sure that a map too large to
// share a page with the text of its steps is drawn on a page of its
// own, followed by a page with the text, both of the same checkpoint.
func TestLargeDiagramGetsAPageOfItsOwn(t *testing.T) {
	var nodes, steps strings.Builder
	for c := 0; c < 5; c++ {
		for r := 0; r < 4; r++ {
			fmt.Fprintf(&nodes, "  - {id: n%d%d, label: N%d%d, at: [%d, %d]}\n", c, r, c, r, c, r)
		}
	}
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&steps, "  - {id: s%d, title: Step %d, focus: [n0%d], text: A line of text that says what the step shows on the map.}\n", i, i, i)
	}
	source := "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n```ims-model\nnodes:\n" + nodes.String() + "```\n\n---\n\n# T\n\n---\n\n## Big\n\n```ims-scene\nid: big\nsteps:\n" + steps.String() + "```\n"
	d, err := deck.Parse("big.md", source)
	if err != nil {
		t.Fatal(err)
	}
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if len(pages) != 3 || pages[1].Step != "s3" || pages[2].Step != "s3" {
		t.Fatalf("pages = %d, want the title and two pages of checkpoint s3", len(pages))
	}
	if !strings.Contains(Text(pages[2]), "Step 0") || strings.Contains(Text(pages[1]), "Step 0") {
		t.Error("the text of the steps is not on the second page alone")
	}
}

// TestReferencePointHasNoArrowhead makes sure that a relation with a
// protocol, an interface between two nodes, is drawn in the style of
// its protocol and without an arrowhead.
func TestReferencePointHasNoArrowhead(t *testing.T) {
	source := "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n```ims-model\nnodes:\n" +
		"  - {id: a, label: A, at: [0, 0]}\n  - {id: b, label: B, at: [1, 0]}\nevents:\n" +
		"  - {id: cx, rel: Cx, from: a, to: b, proto: Diameter}\n  - {id: uses, rel: uses, from: a, to: b}\n```\n\n---\n\n# T\n\n---\n\n## S\n\n" +
		"```ims-scene\nid: s\nsteps:\n  - {id: x, title: X, show: [cx, uses], text: Y}\n```\n"
	d, err := deck.Parse("ref.md", source)
	if err != nil {
		t.Fatal(err)
	}
	diagram := Draw(d.Model, d.Slides[1].Scene)
	for _, e := range diagram.Events {
		switch e.ID {
		case "cx":
			if !e.Undirected || e.Family != "diameter" || e.SubText() != "Diameter" {
				t.Errorf("cx = %+v", e)
			}
		case "uses":
			if e.Undirected || e.Family != "relation" {
				t.Errorf("uses = %+v", e)
			}
		}
	}
}

// TestLinesThatNoStepDrawsTogetherShareALane makes sure that two
// messages of one direction between two nodes take lanes side by side
// only when a step draws them together, so that a scene that clears the
// map with only does not push a later label out onto another node.
func TestLinesThatNoStepDrawsTogetherShareALane(t *testing.T) {
	source := "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n```ims-model\nnodes:\n" +
		"  - {id: a, label: A, at: [0, 0]}\n  - {id: b, label: B, at: [1, 0]}\nevents:\n" +
		"  - {id: first, msg: ONE, from: a, to: b, proto: SIP}\n" +
		"  - {id: second, msg: TWO, from: a, to: b, proto: SIP}\n" +
		"  - {id: third, msg: THREE, from: a, to: b, proto: SIP}\n```\n\n---\n\n# T\n\n---\n\n## S\n\n" +
		"```ims-scene\nid: s\nsteps:\n" +
		"  - {id: x, title: X, only: [first], text: Y}\n" +
		"  - {id: y, title: Y, only: [second], text: Y}\n" +
		"  - {id: z, title: Z, show: [third], text: Y}\n```\n"
	d, err := deck.Parse("lanes.md", source)
	if err != nil {
		t.Fatal(err)
	}
	diagram := Draw(d.Model, d.Slides[1].Scene)
	y := map[string]float64{}
	for _, e := range diagram.Events {
		y[e.ID] = e.Y1
	}
	if y["first"] != y["second"] {
		t.Errorf("first and second are never drawn together but take lanes at %v and %v", y["first"], y["second"])
	}
	if y["second"] == y["third"] {
		t.Errorf("second and third are drawn together but share the lane at %v", y["second"])
	}
	if len(diagram.Problems) > 0 {
		t.Error(diagram.Problems)
	}
}

// TestLadderHeadsSayTheRole makes sure that the head of a lifeline says
// the role of its node, so that one AS in two roles makes two heads
// that a reader can tell apart, and that the pptx writes it too.
func TestLadderHeadsSayTheRole(t *testing.T) {
	source := "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n```ims-model\nnodes:\n" +
		"  - {id: s, label: S-CSCF, at: [0, 0]}\n" +
		"  - {id: as, label: MMTel AS, role: the user's services, at: [1, 0]}\n" +
		"  - {id: focus, label: MMTel AS, role: conference focus, at: [2, 0]}\nevents:\n" +
		"  - {id: inv, msg: INVITE, from: s, to: focus, proto: SIP}\n```\n\n---\n\n# T\n\n---\n\n## S\n\n" +
		"```ims-scene\nid: l\nview: ladder\nnodes: [s, as, focus]\nsteps:\n  - {id: x, title: X, show: [inv], text: Y}\n```\n"
	d, err := deck.Parse("heads.md", source)
	if err != nil {
		t.Fatal(err)
	}
	diagram := Draw(d.Model, d.Slides[1].Scene)
	roles := map[string]string{}
	for _, l := range diagram.Lines {
		roles[l.ID] = deck.PlainText(l.Role)
	}
	if roles["as"] != "the user's services" || roles["focus"] != "conference focus" || roles["s"] != "" {
		t.Errorf("roles = %v", roles)
	}
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	found := false
	for _, e := range pages[1].Elements {
		if g, ok := e.(*Group); ok {
			for _, e := range g.Elements {
				if box, ok := e.(*TextBox); ok && box.Name == "node focus" {
					found = strings.Contains(Text(Page{Elements: []Element{box}}), "conference focus")
				}
			}
		}
	}
	if !found {
		t.Error("the head of focus on the page does not say its role")
	}
}

// TestS6cIsDiameter makes sure that the interface of the SMS router and
// the HSS is drawn in the style of Diameter.
func TestS6cIsDiameter(t *testing.T) {
	if f := Family(deck.Event{Kind: deck.EventMessage, Protocol: "S6c"}); f != "diameter" {
		t.Errorf("S6c is %s", f)
	}
}

// TestLadderLabelWiderThanTheLadderFails makes sure that a label of a
// message that reaches past the sides of a ladder fails the check: the
// view would cut it, and the message would show without its name.
func TestLadderLabelWiderThanTheLadderFails(t *testing.T) {
	source := func(name string) string {
		return "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n```ims-model\nnodes:\n" +
			"  - {id: a, label: A, at: [0, 0]}\n  - {id: b, label: B, at: [1, 0]}\nevents:\n" +
			"  - {id: m, msg: " + name + ", from: a, to: b, proto: SIP}\n```\n\n---\n\n# T\n\n---\n\n## S\n\n" +
			"```ims-scene\nid: l\nview: ladder\nsteps:\n  - {id: x, title: X, show: [m], text: Y}\n```\n"
	}
	for name, want := range map[string]bool{
		"INVITE": false,
		"ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST": true,
	} {
		d, err := deck.Parse("wide.md", source(name))
		if err != nil {
			t.Fatal(err)
		}
		diagram := Draw(d.Model, d.Slides[1].Scene)
		if got := len(diagram.Problems) > 0; got != want {
			t.Errorf("%s: problems %v, want some: %v", name, diagram.Problems, want)
		}
	}
}
