package html

import (
	"fmt"
	"html"
	"io"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// Room of a figure, in diagram units: around the diagram, and for the
// legend and the caption under it.
const (
	figureMargin  = 16.0
	figureLegendH = 30.0
	figureNoteH   = 26.0
	figureNote    = 13.0
)

// WriteFigure writes one step of a scene as an SVG file that stands on
// its own: no style sheet, no script and no font file, so that a book
// shows it as an image and a converter makes a PNG of it. Under the
// diagram it names the kinds of line that it draws, and says what the
// diagram is drawn from, as the caption of a checkpoint does.
func WriteFigure(w io.Writer, d *deck.Deck, sceneID, stepID string) error {
	i := slices.IndexFunc(d.Slides, func(s deck.Slide) bool { return s.Scene != nil && s.Scene.ID == sceneID })
	if i < 0 {
		return fmt.Errorf("%s has no scene %s", d.Path, sceneID)
	}
	slide := d.Slides[i]
	f, err := layout.Figure(d.Model, slide.Scene, stepID)
	if err != nil {
		return fmt.Errorf("%s: %w", d.Path, err)
	}
	dw, dh := f.W, f.H
	width := dw + 2*figureMargin
	height := dh + 2*figureMargin + figureLegendH + figureNoteH
	var b strings.Builder
	title := deck.PlainText(slide.Title)
	if k := slices.IndexFunc(slide.Scene.Steps, func(st deck.Step) bool { return st.ID == stepID }); k >= 0 {
		title += ": " + deck.PlainText(slide.Scene.Steps[k].Title)
	}
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" font-family="%s" role="img">`,
		num(width), num(height), num(width), num(height), html.EscapeString(layout.CSSSans))
	fmt.Fprintf(&b, "\n<title>%s</title>\n", html.EscapeString(title))
	fmt.Fprintf(&b, `<rect width="%s" height="%s" fill="#%s"/>`+"\n", num(width), num(height), layout.ColorStage)
	fmt.Fprintf(&b, `<g transform="translate(%s %s)">`, num(figureMargin), num(figureMargin))
	drawElements(&b, f.Elements)
	b.WriteString("</g>\n")
	y := figureMargin + dh + figureMargin
	figureLegend(&b, d.Lang, f.Families, figureMargin, y+figureLegendH/2)
	fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="%s" fill="#%s">%s</text>`+"\n",
		num(figureMargin), num(y+figureLegendH+figureNote), num(figureNote), layout.ColorMuted,
		html.EscapeString(layout.BasisWords[d.Lang][slide.Scene.Basis]))
	b.WriteString("</svg>\n")
	_, err = io.WriteString(w, b.String())
	return err
}

// figureLegend draws, in a row, a sample of each kind of line that the
// figure has, with its name: on paper the colour of a line alone does not
// say whether it is SIP or Diameter.
func figureLegend(b *strings.Builder, lang string, families []string, x, y float64) {
	for _, f := range families {
		line := &layout.Line{X1: x, Y1: y, X2: x + 30, Y2: y, Color: layout.FamilyColors[f], Width: 2,
			Dash: f == "diameter" || f == "relation", Dot: f == "dns"}
		drawElements(b, []layout.Element{line})
		name := words[lang]["family-"+f]
		fmt.Fprintf(b, `<text x="%s" y="%s" font-size="%s" fill="#%s">%s</text>`, num(x+36), num(y+4.5), num(figureNote), layout.ColorInk, html.EscapeString(name))
		x += 36 + float64(len([]rune(name)))*figureNote*0.62 + 24
	}
	b.WriteString("\n")
}
