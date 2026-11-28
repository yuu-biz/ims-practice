package html

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

const figureSource = "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n" +
	"```ims-model\n" +
	"nodes:\n" +
	"  - {id: a, label: A, at: [0, 0]}\n" +
	"  - {id: b, label: B, at: [1, 0]}\n" +
	"  - {id: c, label: C, at: [1, 1]}\n" +
	"events:\n" +
	"  - {id: req, msg: REQ, from: a, to: b, proto: SIP, fields: [{Request-URI: \"`sip:b`\"}]}\n" +
	"  - {id: ask, msg: ASK, from: b, to: c, proto: Cx}\n" +
	"  - {id: tell, msg: TELL, from: c, to: b, proto: Cx}\n" +
	"```\n\n---\n\n# T\n\n---\n\n## Routing <here>\n\n" +
	"```ims-scene\n" +
	"id: flow\n" +
	"basis: standard\n" +
	"steps:\n" +
	"  - {id: send, title: Send, show: [req], text: A sends.}\n" +
	"  - {id: ask, title: Ask, only: [ask, tell], active: ask, text: B asks C.}\n" +
	"```\n"

func writeFigure(t *testing.T, sceneID, stepID string) []byte {
	t.Helper()
	d, err := deck.Parse("figure.md", figureSource)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteFigure(&out, d, sceneID, stepID); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// svgDocument is what an XML reader finds in a figure: its root, every
// element and attribute name, and its text.
type svgDocument struct {
	root       xml.StartElement
	elements   map[string]bool
	attributes map[string]bool
	text       string
	title      string
}

func readSVG(t *testing.T, data []byte) svgDocument {
	t.Helper()
	doc := svgDocument{elements: map[string]bool{}, attributes: map[string]bool{}}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var text strings.Builder
	inTitle := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("the figure is not well-formed XML: %v\n%s", err, data)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if doc.root.Name.Local == "" {
				doc.root = token.Copy()
			}
			doc.elements[token.Name.Local] = true
			for _, a := range token.Attr {
				doc.attributes[a.Name.Local] = true
			}
			inTitle = token.Name.Local == "title"
		case xml.EndElement:
			inTitle = false
		case xml.CharData:
			if inTitle {
				doc.title += string(token)
			}
			text.WriteString(string(token))
			text.WriteString("\n")
		}
	}
	doc.text = text.String()
	return doc
}

func attribute(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// TestFigureIsAnSVGOfItsOwn makes sure that a figure is an SVG file that
// an image viewer or a converter reads alone: well-formed, with its
// namespace and size, and with no style sheet, script or class that
// would need the page of the slides to look right.
func TestFigureIsAnSVGOfItsOwn(t *testing.T) {
	doc := readSVG(t, writeFigure(t, "flow", "send"))
	if doc.root.Name.Local != "svg" || doc.root.Name.Space != "http://www.w3.org/2000/svg" {
		t.Errorf("the root is %v, want svg in the SVG namespace", doc.root.Name)
	}
	if attribute(doc.root, "width") == "" || attribute(doc.root, "height") == "" {
		t.Error("the root has no width or height")
	}
	if doc.elements["style"] || doc.elements["script"] {
		t.Error("the figure has a style sheet or a script")
	}
	if doc.attributes["class"] {
		t.Error("the figure has a class, which only a style sheet gives a look")
	}
}

// TestFigureNamesItsSceneAndStep makes sure that the title of a figure
// names the slide of the scene and the step that it shows.
func TestFigureNamesItsSceneAndStep(t *testing.T) {
	doc := readSVG(t, writeFigure(t, "flow", "ask"))
	if !strings.Contains(doc.title, "Routing <here>") || !strings.Contains(doc.title, "Ask") {
		t.Errorf("the title is %q, want the heading of the scene and the title of the step", doc.title)
	}
}

// TestFigureLegendNamesTheProtocolsItDraws makes sure that under the
// diagram the figure names the kinds of line that it draws, and only
// those: on paper the colour alone does not say which protocol it is.
func TestFigureLegendNamesTheProtocolsItDraws(t *testing.T) {
	send := readSVG(t, writeFigure(t, "flow", "send"))
	if !strings.Contains(send.text, "SIP\n") || strings.Contains(send.text, "Diameter\n") {
		t.Errorf("the figure of a SIP request does not name SIP alone:\n%s", send.text)
	}
	ask := readSVG(t, writeFigure(t, "flow", "ask"))
	if !strings.Contains(ask.text, "Diameter\n") || strings.Contains(ask.text, "SIP\n") {
		t.Errorf("the figure of a Cx exchange does not name Diameter alone:\n%s", ask.text)
	}
}

// TestFigureSaysWhatItIsDrawnFrom makes sure that a figure carries the
// words that say what its diagram rests on, as a slide of the scene does.
func TestFigureSaysWhatItIsDrawnFrom(t *testing.T) {
	doc := readSVG(t, writeFigure(t, "flow", "send"))
	if want := layout.BasisWords["en"][deck.BasisStandard]; !strings.Contains(doc.text, want) {
		t.Errorf("the figure does not say %q:\n%s", want, doc.text)
	}
}

// TestFigureLeavesOutTheEventsOfOtherSteps makes sure that a figure
// shows its own step: the labels of the messages of another step are
// not on it.
func TestFigureLeavesOutTheEventsOfOtherSteps(t *testing.T) {
	doc := readSVG(t, writeFigure(t, "flow", "ask"))
	if !strings.Contains(doc.text, "ASK") || !strings.Contains(doc.text, "TELL") {
		t.Errorf("the figure of step ask does not draw its messages:\n%s", doc.text)
	}
	if strings.Contains(doc.text, "REQ") {
		t.Errorf("the figure of step ask draws REQ, which the step does not show:\n%s", doc.text)
	}
}

// TestFigureOfAnUnknownSceneOrStepIsAnError makes sure that a figure that
// names a scene or a step the deck does not have fails, and writes
// nothing.
func TestFigureOfAnUnknownSceneOrStepIsAnError(t *testing.T) {
	d, err := deck.Parse("figure.md", figureSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"nosuch", "send"}, {"flow", "nosuch"}} {
		var out bytes.Buffer
		if err := WriteFigure(&out, d, c[0], c[1]); err == nil {
			t.Errorf("a figure of scene %s step %s was written", c[0], c[1])
		}
		if out.Len() > 0 {
			t.Errorf("a failed figure of scene %s step %s wrote %d bytes", c[0], c[1], out.Len())
		}
	}
}

// TestFigureIsTheSameEachTime makes sure that drawing a figure twice
// gives the same bytes, so that a figure file in the repository changes
// only when its step does.
func TestFigureIsTheSameEachTime(t *testing.T) {
	if !bytes.Equal(writeFigure(t, "flow", "send"), writeFigure(t, "flow", "send")) {
		t.Error("two drawings of the same step differ")
	}
}
