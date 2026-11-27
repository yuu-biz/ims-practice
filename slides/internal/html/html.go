// Package html writes a deck as one self-contained HTML file. The file
// needs no network: the styles, the script and the data of the scenes
// are inside it, and the fonts are those of the machine.
//
// In a browser one slide shows at a time, scaled to the window. A slide
// of blocks is drawn from the boxes that package layout placed, the same
// boxes that the pptx holds. A slide with a scene is a stage: the
// diagram, the text of the current step beside it, and its longer
// explanation behind a button. The learner steps through the scene; the
// address names the scene and the step (#select/uaa), so a reload or a
// link opens the same state, and a slide of blocks keeps its number
// (#12). On a narrow screen the slides flow instead of scaling, and a
// reading view and the printout show every slide one after the other,
// a scene at each of its checkpoints.
package html

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// Options are what the HTML needs beyond the deck.
type Options struct {
	// Repository is the address of the repository tree, if the links
	// to the guides are to be absolute.
	Repository string
	// Translation is the address of the same deck in the other
	// language, relative to this file, if there is one.
	Translation     string
	TranslationLang string
}

// Write writes the HTML of a deck.
func Write(out io.Writer, d *deck.Deck, pages []layout.Page) error {
	return WriteWith(out, d, pages, Options{})
}

// sceneData is what the script knows of a slide: its id in the address,
// and for a scene, the complete state of each step.
type sceneData struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Steps []stepData `json:"steps"`
}

type stepData struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Events []string   `json:"events"`
	Active string     `json:"active"`
	Focus  []string   `json:"focus"`
	Detail []string   `json:"detail"`
	Camera [4]float64 `json:"camera"`
}

// WriteWith writes the HTML of a deck with options.
func WriteWith(out io.Writer, d *deck.Deck, pages []layout.Page, o Options) error {
	w := writer{d: d, o: o, words: words[d.Lang]}
	byslide := map[int]layout.Page{}
	for _, p := range pages {
		if p.Step == "" {
			byslide[p.Slide] = p
		}
	}
	diagrams := map[int]*layout.Diagram{}
	for _, s := range d.Slides {
		if s.Scene != nil {
			diagrams[s.Number] = layout.Draw(d.Model, s.Scene)
		}
	}
	b := &w.b
	fmt.Fprintf(b, "<!DOCTYPE html>\n<html lang=\"%s\">\n<head>\n<meta charset=\"utf-8\">\n"+
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<title>%s</title>\n<style>\n%s</style>\n</head>\n<body>\n",
		d.Lang, html.EscapeString(d.Title), style())
	b.WriteString(`<div id="app">` + "\n")
	fmt.Fprintf(b, `<a class="skip" id="skip" href="#reading">%s</a>`+"\n", w.words["skip"])
	b.WriteString(`<main id="viewport"><div id="stage">` + "\n")
	var data []sceneData
	for i, s := range d.Slides {
		if s.Scene != nil {
			w.stageScene(s, diagrams[s.Number], i, len(d.Slides))
			data = append(data, w.sceneData(s, diagrams[s.Number]))
		} else {
			w.stagePage(s, byslide[s.Number], i, len(d.Slides))
			data = append(data, sceneData{ID: fmt.Sprint(s.Number), Title: deck.PlainText(s.Title), Steps: []stepData{{ID: ""}}})
		}
	}
	b.WriteString("</div></main>\n")
	w.bar()
	fmt.Fprintf(b, `<aside id="drawer" hidden aria-labelledby="drawer-title"><div class="drawer-head"><h2 id="drawer-title"></h2><button type="button" id="drawer-close" aria-label="%s">×</button></div><div id="drawer-body"></div></aside>`+"\n", w.words["close"])
	b.WriteString(`<p id="live" class="sr" aria-live="polite"></p>` + "\n</div>\n")
	w.reading(byslide, diagrams)
	payload, err := json.Marshal(map[string]any{"scenes": data, "words": w.words})
	if err != nil {
		return err
	}
	// JSON inside a script element: "</" would end the element.
	b.WriteString(`<script type="application/json" id="data">` + strings.ReplaceAll(string(payload), "</", `<\/`) + "</script>\n")
	b.WriteString("<script>\n" + script + "</script>\n</body>\n</html>\n")
	_, err = io.WriteString(out, b.String())
	return err
}

type writer struct {
	d     *deck.Deck
	o     Options
	words map[string]string
	b     strings.Builder
}

func (w *writer) sceneData(s deck.Slide, diagram *layout.Diagram) sceneData {
	data := sceneData{ID: s.Scene.ID, Title: deck.PlainText(s.Title)}
	for k, st := range s.Scene.Steps {
		c := diagram.Cameras[k]
		data.Steps = append(data.Steps, stepData{ID: st.ID, Title: deck.PlainText(st.Title), Events: nonNil(st.Events), Active: st.Active,
			Focus: nonNil(st.Focus), Detail: nonNil(st.Detail), Camera: [4]float64{round2(c.X), round2(c.Y), round2(c.W), round2(c.H)}})
	}
	return data
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Wide reports a diagram that is much wider than tall: a map of nodes in
// a row or two. The stage then draws it across the whole width, with the
// text of the step below it, instead of beside it.
func Wide(d *layout.Diagram) bool {
	return d.View == deck.ViewMap && d.Bounds.W > 2.1*d.Bounds.H
}

// counter is the place of a slide in the deck, such as "03 / 18".
func counter(i, n int) string { return fmt.Sprintf("%02d / %02d", i+1, n) }

// stagePage writes a slide of blocks: the boxes of its page, and its
// flowing form for a narrow screen.
func (w *writer) stagePage(s deck.Slide, page layout.Page, i, n int) {
	b := &w.b
	// The ids of the sections start with "slide-", so that no id of a
	// scene can be the id of a control of the page.
	fmt.Fprintf(b, `<section class="slide page %s" id="slide-%d" data-index="%d" aria-label="%s" hidden>`+"\n", page.Layout, s.Number, i, html.EscapeString(deck.PlainText(s.Title)))
	fmt.Fprintf(b, `<div class="fixed" style="background:#%s">`+"\n", page.Background)
	// The number of the page is its place in the pptx; on the stage a
	// slide shows its place among the slides, as the scenes do.
	var shown []layout.Element
	for _, e := range page.Elements {
		if t, ok := e.(*layout.TextBox); ok && t.Role == layout.RoleNumber {
			c := *t
			c.Paragraphs = []layout.Paragraph{t.Paragraphs[0]}
			c.Paragraphs[0].Runs = []layout.Run{{Text: fmt.Sprintf("%d / %d", i+1, n)}}
			e = &c
		}
		shown = append(shown, e)
	}
	elements(b, shown)
	b.WriteString("</div>\n")
	b.WriteString(`<div class="flow">`)
	fmt.Fprintf(b, `<p class="kicker">%s</p><h2>`, counter(i, n))
	flowSpans(b, s.Title, w.o.Repository)
	b.WriteString("</h2>")
	for _, column := range s.Columns {
		flowBlocks(b, column, w.o.Repository)
	}
	b.WriteString("</div>\n</section>\n")
}

// stageScene writes a slide with a scene: its header, the diagram, the
// panel with the text of each step and the fields of each event, and its
// longer explanation, which the drawer shows.
func (w *writer) stageScene(s deck.Slide, diagram *layout.Diagram, i, n int) {
	b := &w.b
	scene := s.Scene
	class := "scene"
	if Wide(diagram) {
		class += " wide"
	}
	fmt.Fprintf(b, `<section class="slide %s" id="slide-%s" data-index="%d" aria-label="%s" hidden>`+"\n", class, scene.ID, i, html.EscapeString(deck.PlainText(s.Title)))
	fmt.Fprintf(b, `<header class="head"><p class="kicker">%s</p><h2>`, counter(i, n))
	flowSpans(b, s.Title, w.o.Repository)
	b.WriteString("</h2>")
	if len(scene.Lead) > 0 {
		b.WriteString(`<p class="lead">`)
		flowSpans(b, scene.Lead, w.o.Repository)
		b.WriteString("</p>")
	}
	b.WriteString("</header>\n")
	b.WriteString(`<div class="figure">`)
	diagramSVG(b, w.d, s, diagram, 0, "d-"+scene.ID, diagram.Cameras[0])
	if diagram.View == deck.ViewMap {
		// A small map of the whole shows where a close-up is. A ladder
		// that scrolls needs none.
		fmt.Fprintf(b, `<svg class="minimap" viewBox="%s" aria-hidden="true" hidden><use href="#d-%s-world"/><rect class="cam"/></svg>`, viewBox(diagram.Bounds), scene.ID)
	}
	b.WriteString(`<div class="under">`)
	fmt.Fprintf(b, `<p class="caption">%s</p>`, html.EscapeString(layout.BasisWords[w.d.Lang][scene.Basis]))
	legend(b, w.d.Lang, diagram)
	b.WriteString("</div></div>\n")
	b.WriteString(`<div class="panel">`)
	fmt.Fprintf(b, `<p class="where"><span class="count"></span></p>`)
	for k, st := range scene.Steps {
		hidden := " hidden"
		if k == 0 {
			hidden = ""
		}
		fmt.Fprintf(b, `<div class="step" data-step="%s"%s><h3><span class="mark">%s</span> `, st.ID, hidden, numberMark(k+1))
		flowSpans(b, st.Title, w.o.Repository)
		b.WriteString("</h3>")
		w.stepText(st)
		b.WriteString("</div>")
	}
	b.WriteString(`<div class="detail">`)
	for _, id := range scene.Events() {
		e, _ := w.d.Model.Event(id)
		if len(e.Fields) == 0 {
			continue
		}
		hidden := " hidden"
		if contains(scene.Steps[0].Detail, id) {
			hidden = ""
		}
		fmt.Fprintf(b, `<div class="fields" data-id="%s"%s>`, id, hidden)
		w.fields(e)
		b.WriteString("</div>")
	}
	b.WriteString("</div>")
	if len(s.Columns) > 0 && len(s.Columns[0]) > 0 {
		fmt.Fprintf(b, `<button type="button" class="more" aria-controls="drawer">%s <kbd>D</kbd></button>`, w.words["more"])
	}
	b.WriteString("</div>\n")
	if len(s.Columns) > 0 && len(s.Columns[0]) > 0 {
		b.WriteString(`<div class="body" hidden>`)
		flowBlocks(b, s.Columns[0], w.o.Repository)
		b.WriteString("</div>\n")
	}
	b.WriteString("</section>\n")
}

func (w *writer) stepText(st deck.Step) {
	b := &w.b
	b.WriteString("<p>")
	for i, l := range st.Text {
		if len(l) == 0 {
			b.WriteString("</p><p>")
			continue
		}
		if i > 0 && len(st.Text[i-1]) > 0 {
			b.WriteString("<br>")
		}
		flowSpans(b, l, w.o.Repository)
	}
	b.WriteString("</p>")
}

func (w *writer) fields(e deck.Event) {
	b := &w.b
	fmt.Fprintf(b, `<h4>%s</h4><dl>`, html.EscapeString(layout.EventHeading(w.d.Model, e)))
	for _, f := range e.Fields {
		fmt.Fprintf(b, `<dt>%s</dt><dd>`, html.EscapeString(f.Name))
		flowSpans(b, f.Value, w.o.Repository)
		b.WriteString("</dd>")
	}
	b.WriteString("</dl>")
}

func contains(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

func numberMark(n int) string {
	if n >= 1 && n <= 20 {
		return string(rune('①' + n - 1))
	}
	return fmt.Sprintf("(%d)", n)
}

// bar writes the controls under the stage.
func (w *writer) bar() {
	b := &w.b
	b.WriteString(`<nav id="bar" aria-label="` + w.words["controls"] + `">`)
	fmt.Fprintf(b, `<button type="button" id="prev" aria-label="%s" title="%s (←)">◀</button>`, w.words["prev"], w.words["prev"])
	fmt.Fprintf(b, `<label class="sr" for="jump">%s</label><select id="jump">`, w.words["jump"])
	for i, s := range w.d.Slides {
		fmt.Fprintf(b, `<option value="%d">%d. %s</option>`, i, i+1, html.EscapeString(deck.PlainText(s.Title)))
	}
	b.WriteString("</select>")
	fmt.Fprintf(b, `<ol id="steps" aria-label="%s"></ol>`, w.words["steps"])
	fmt.Fprintf(b, `<button type="button" id="next" aria-label="%s" title="%s (→)">▶</button>`, w.words["next"], w.words["next"])
	b.WriteString(`<span class="spacer"></span>`)
	fmt.Fprintf(b, `<button type="button" id="motion" aria-pressed="false" title="M">%s</button>`, w.words["motion"])
	fmt.Fprintf(b, `<button type="button" id="read" title="R">%s</button>`, w.words["read"])
	if w.o.Translation != "" {
		fmt.Fprintf(b, `<a id="translation" href="%s" hreflang="%s" lang="%s">%s</a>`, html.EscapeString(w.o.Translation), w.o.TranslationLang, w.o.TranslationLang, html.EscapeString(words[w.o.TranslationLang]["language"]))
	}
	b.WriteString("</nav>\n")
}

// reading writes the reading view: every slide in turn, a scene at each
// of its checkpoints with the text of the steps up to it, then its
// longer explanation. It is also what the printer prints.
func (w *writer) reading(pages map[int]layout.Page, diagrams map[int]*layout.Diagram) {
	b := &w.b
	fmt.Fprintf(b, `<article id="reading" hidden aria-label="%s">`+"\n", w.words["read"])
	fmt.Fprintf(b, `<header class="reading-head"><h1>%s</h1><button type="button" id="read-close">%s</button></header>`+"\n", html.EscapeString(w.d.Title), w.words["back"])
	for i, s := range w.d.Slides {
		fmt.Fprintf(b, `<section class="read" id="read-%d"><p class="kicker">%s</p><h2>`, i, counter(i, len(w.d.Slides)))
		flowSpans(b, s.Title, w.o.Repository)
		b.WriteString("</h2>")
		if s.Scene == nil {
			for _, column := range s.Columns {
				flowBlocks(b, column, w.o.Repository)
			}
			b.WriteString("</section>\n")
			continue
		}
		scene := s.Scene
		if len(scene.Lead) > 0 {
			b.WriteString(`<p class="lead">`)
			flowSpans(b, scene.Lead, w.o.Repository)
			b.WriteString("</p>")
		}
		diagram := diagrams[s.Number]
		from := 0
		for k, st := range scene.Steps {
			if !st.Checkpoint {
				continue
			}
			camera := diagram.Bounds
			if diagram.View == deck.ViewLadder {
				camera = diagram.Cameras[k]
			}
			b.WriteString(`<figure class="checkpoint">`)
			diagramSVG(b, w.d, s, diagram, k, fmt.Sprintf("r-%s-%s", scene.ID, st.ID), camera)
			fmt.Fprintf(b, `<figcaption>%s</figcaption></figure>`, html.EscapeString(layout.BasisWords[w.d.Lang][scene.Basis]))
			b.WriteString(`<ol class="read-steps">`)
			var details []string
			for j := from; j <= k; j++ {
				step := scene.Steps[j]
				fmt.Fprintf(b, `<li value="%d"><b>`, j+1)
				flowSpans(b, step.Title, w.o.Repository)
				b.WriteString("</b>")

				w.stepText(step)
				b.WriteString("</li>")
				for _, id := range step.Detail {
					if !contains(details, id) {
						details = append(details, id)
					}
				}
			}
			b.WriteString("</ol>")
			for _, id := range details {
				e, _ := w.d.Model.Event(id)
				if len(e.Fields) > 0 {
					b.WriteString(`<div class="fields">`)
					w.fields(e)
					b.WriteString("</div>")
				}
			}
			from = k + 1
		}
		if len(s.Columns) > 0 && len(s.Columns[0]) > 0 {
			fmt.Fprintf(b, `<h3 class="details">%s</h3>`, w.words["more"])
			flowBlocks(b, s.Columns[0], w.o.Repository)
		}
		b.WriteString("</section>\n")
	}
	b.WriteString("</article>\n")
}
