package layout

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// A scene becomes still pages: one for each checkpoint, and one for its
// longer explanation. The page of a checkpoint draws the diagram as the
// checkpoint leaves it and, beside or below it, the text of every step
// since the checkpoint before, numbered as the diagram numbers the
// events, and the fields of their events. A reader of the pptx, who
// cannot step, reads there what the HTML shows one step at a time.

// The sizes of the text of a page of a checkpoint, before it is scaled
// down to fit.
const (
	sizeStepTitle = 14.0
	sizeStepText  = 13.0
	sizeFieldHead = 11.5
	sizeField     = 10.5
	sizeLead      = 15.0
	sizeCaption   = 10.0
	sideWidth     = 300.0
	paneGap       = 18.0
	// The diagram is scaled down to fit its room, but not below this:
	// smaller, the labels of the events would be too small to read.
	minDiagramScale = 0.5
)

// scenePages lays out the pages of a slide with a scene.
func scenePages(d *deck.Deck, s deck.Slide) ([]Page, error) {
	diagram := Draw(d.Model, s.Scene)
	if len(diagram.Problems) > 0 {
		return nil, &OverflowError{Path: d.Path, Slide: s.Number, Title: deck.PlainText(s.Title), Reason: "scene " + s.Scene.ID + ": " + strings.Join(diagram.Problems, "; ")}
	}
	var pages []Page
	from := 0
	for k, step := range s.Scene.Steps {
		if !step.Checkpoint {
			continue
		}
		cp, err := checkpointPages(d, s, diagram, from, k)
		if err != nil {
			return nil, err
		}
		pages = append(pages, cp...)
		from = k + 1
	}
	if len(s.Columns) > 0 && len(s.Columns[0]) > 0 {
		page, err := Slide(d, s)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, nil
}

// checkpointPages lays out the page of the checkpoint at step k, with the
// text of the steps from from to k. A diagram that is too large to
// share a page with its text gets a page of its own, and the text the
// page after it; both belong to the checkpoint.
func checkpointPages(d *deck.Deck, s deck.Slide, diagram *Diagram, from, k int) ([]Page, error) {
	scene := s.Scene
	step := scene.Steps[k]
	page := Page{Slide: s.Number, Step: step.ID, Layout: deck.LayoutContent, Background: ColorWhite, Scale: 1}
	title, titleHeight := heading(s.Title, SlideWidth-2*MarginX, ColorAccent)
	page.Elements = append(page.Elements, title)
	ruleY := TitleTop + titleHeight + 6
	page.Elements = append(page.Elements, &Line{X1: MarginX, Y1: ruleY, X2: SlideWidth - MarginX, Y2: ruleY, Color: ColorAccent, Width: 2.25})
	page.Elements = append(page.Elements, footer(d, s, ColorMuted)...)
	top := ruleY + 10
	if len(scene.Lead) > 0 {
		lead := []Paragraph{{Runs: runsOf([][]deck.Span{scene.Lead}), Size: sizeLead, LineHeight: lineHeight(sizeLead, lineHeightText), Color: ColorMuted}}
		h := paragraphsHeight(lead, SlideWidth-2*MarginX)
		page.Elements = append(page.Elements, &TextBox{X: MarginX, Y: top, W: SlideWidth - 2*MarginX, H: h, Paragraphs: lead})
		top += h + 8
	}
	caption := []Paragraph{{Runs: []Run{{Text: BasisWords[d.Lang][scene.Basis]}}, Size: sizeCaption, LineHeight: lineHeight(sizeCaption, lineHeightText), Color: ColorMuted}}
	captionY := BodyBottom - lineHeight(sizeCaption, lineHeightText)
	page.Elements = append(page.Elements, &TextBox{X: MarginX, Y: captionY, W: SlideWidth - 2*MarginX, H: lineHeight(sizeCaption, lineHeightText), Paragraphs: caption})
	bottom := captionY - 6
	camera := diagram.Bounds
	if diagram.View == deck.ViewLadder {
		camera = diagram.Cameras[k]
	}

	// A still page of a ladder shows LadderRows rows. Every row that the
	// steps of the page add has to be among them, or the page would tell
	// of a message that it does not draw.
	if diagram.View == deck.ViewLadder {
		for _, st := range scene.Steps[from : k+1] {
			for _, e := range diagram.Events {
				if slices.Contains(st.Events, e.ID) && hiddenRow(e, camera) {
					if prev := from - 1; prev < 0 || !slices.Contains(scene.Steps[prev].Events, e.ID) {
						return nil, &OverflowError{Path: d.Path, Slide: s.Number, Title: deck.PlainText(s.Title),
							Reason: fmt.Sprintf("the checkpoint %s of the ladder adds more rows than a page shows; make an earlier step a checkpoint", step.ID)}
					}
				}
			}
		}
	}
	numbers := scene.FirstActive()
	var details []string
	for _, st := range scene.Steps[from : k+1] {
		for _, id := range st.Detail {
			if !slices.Contains(details, id) {
				details = append(details, id)
			}
		}
	}
	width := SlideWidth - 2*MarginX
	height := bottom - top
	for _, scale := range scales {
		b := builder{scale: scale}
		steps := b.stepParagraphs(scene, from, k, numbers)
		fields := b.fields(d.Model, details)
		best, bestScale := []Element(nil), 0.0
		// Below the diagram: the steps on the left, the fields on the
		// right, when the diagram is wide.
		{
			stepsW, fieldsW := width, 0.0
			if len(fields) > 0 {
				stepsW = math.Round(width * 0.54)
				fieldsW = width - stepsW - paneGap
			}
			stepsH := paragraphsHeight(steps, stepsW)
			fieldsH := fieldsHeight(fields, fieldsW)
			textH := max(stepsH, fieldsH)
			room := height - textH - paneGap
			scaleD := min(width/camera.W, room/camera.H, 1)
			if room > 0 && scaleD >= minDiagramScale && scaleD > bestScale {
				bestScale = scaleD
				x := MarginX + (width-camera.W*scaleD)/2
				best = drawDiagram(diagram, scene, step, camera, scaleD, x, top)
				y := top + camera.H*scaleD + paneGap
				best = append(best, &TextBox{X: MarginX, Y: y, W: stepsW, H: stepsH, Paragraphs: steps})
				best = append(best, placeFields(fields, MarginX+stepsW+paneGap, y, fieldsW)...)
			}
		}
		// Beside the diagram: the steps and then the fields in a column
		// on the right, when the diagram is tall.
		{
			textW := sideWidth
			textH := paragraphsHeight(steps, textW) + fieldsHeight(fields, textW)
			if len(fields) > 0 {
				textH += 10
			}
			roomW := width - textW - paneGap
			scaleD := min(roomW/camera.W, height/camera.H, 1)
			if textH <= height && scaleD >= minDiagramScale && scaleD > bestScale {
				bestScale = scaleD
				y := top + (height-camera.H*scaleD)/2
				best = drawDiagram(diagram, scene, step, camera, scaleD, MarginX, y)
				textX := MarginX + roomW + paneGap
				stepsH := paragraphsHeight(steps, textW)
				best = append(best, &TextBox{X: textX, Y: top, W: textW, H: stepsH, Paragraphs: steps})
				best = append(best, placeFields(fields, textX, top+stepsH+10, textW)...)
			}
		}
		if best != nil {
			page.Scale = scale
			page.Elements = append(page.Elements, best...)
			return []Page{page}, nil
		}
	}
	if scaleD := min(width/camera.W, height/camera.H, 1); scaleD >= minDiagramScale {
		figure, text := page, page
		figure.Elements = slices.Clone(page.Elements)
		text.Elements = slices.Clone(page.Elements)
		figure.Elements = append(figure.Elements, drawDiagram(diagram, scene, step, camera, scaleD, MarginX+(width-camera.W*scaleD)/2, top)...)
		for _, scale := range scales {
			b := builder{scale: scale}
			steps := b.stepParagraphs(scene, from, k, numbers)
			fields := b.fields(d.Model, details)
			stepsW, fieldsW := width, 0.0
			if len(fields) > 0 {
				stepsW = math.Round(width * 0.54)
				fieldsW = width - stepsW - paneGap
			}
			stepsH := paragraphsHeight(steps, stepsW)
			if max(stepsH, fieldsHeight(fields, fieldsW)) > height {
				continue
			}
			text.Scale = scale
			text.Elements = append(text.Elements, &TextBox{X: MarginX, Y: top, W: stepsW, H: stepsH, Paragraphs: steps})
			text.Elements = append(text.Elements, placeFields(fields, MarginX+stepsW+paneGap, top, fieldsW)...)
			return []Page{figure, text}, nil
		}
	}
	return nil, &OverflowError{Path: d.Path, Slide: s.Number, Title: deck.PlainText(s.Title),
		Reason: fmt.Sprintf("the checkpoint %s does not fit: the diagram and the text of its steps need more room than a page has; make an earlier step a checkpoint, or show fewer fields", step.ID)}
}

// stepParagraphs are the steps from from to k: the number and the title
// of each, then its text.
func (b builder) stepParagraphs(scene *deck.Scene, from, k int, numbers map[string]int) []Paragraph {
	var out []Paragraph
	titleSize, textSize := b.size(sizeStepTitle), b.size(sizeStepText)
	for i := from; i <= k; i++ {
		st := scene.Steps[i]
		runs := []Run{{Text: numberMark(i+1) + " "}}
		runs = append(runs, runsOf([][]deck.Span{st.Title})...)
		before := 0.0
		if len(out) > 0 {
			before = textSize * 0.7
		}
		out = append(out, Paragraph{Runs: runs, Size: titleSize, LineHeight: lineHeight(titleSize, lineHeightText), SpaceBefore: before, Color: ColorAccent, Bold: true})
		for _, l := range st.Text {
			out = append(out, Paragraph{Runs: runsOf([][]deck.Span{l}), Size: textSize, LineHeight: lineHeight(textSize, lineHeightText), Color: ColorText})
		}
	}
	return out
}

// eventFields is the heading and the table of the fields of one event.
type eventFields struct {
	head Paragraph
	rows [][2]Paragraph
}

func (b builder) fields(m *deck.Model, ids []string) []eventFields {
	var out []eventFields
	headSize, size := b.size(sizeFieldHead), b.size(sizeField)
	for _, id := range ids {
		e, _ := m.Event(id)
		if len(e.Fields) == 0 {
			continue
		}
		f := eventFields{head: Paragraph{Runs: []Run{{Text: EventHeading(m, e)}}, Size: headSize, LineHeight: lineHeight(headSize, lineHeightText), Color: ColorInk, Bold: true}}
		for _, field := range e.Fields {
			// The name is code, measured and drawn as code is in a line of
			// text: a little smaller than the text, in the monospace font.
			name := Paragraph{Runs: []Run{{Text: field.Name, Code: true}}, Size: size, LineHeight: lineHeight(size, lineHeightText), Color: ColorMuted}
			value := Paragraph{Runs: runsOf([][]deck.Span{field.Value}), Size: size, LineHeight: lineHeight(size, lineHeightText), Color: ColorText}
			f.rows = append(f.rows, [2]Paragraph{name, value})
		}
		out = append(out, f)
	}
	return out
}

// EventHeading names an event and its ends, as the detail of a step
// heads its fields: "UAA (Cx) HSS → I-CSCF".
func EventHeading(m *deck.Model, e deck.Event) string {
	label := deck.PlainText(e.Label)
	if e.Kind == deck.EventProcess {
		n, _ := m.Node(e.Actor)
		return n.Label + ": " + label
	}
	if e.Protocol != "" {
		label += " (" + e.Protocol + ")"
	}
	from, _ := m.Node(e.From)
	to, _ := m.Node(e.To)
	return label + "  " + from.Label + " → " + to.Label
}

const fieldNameShare = 0.42

func fieldsHeight(fields []eventFields, width float64) float64 {
	if width <= 0 {
		return 0
	}
	h := 0.0
	for i, f := range fields {
		if i > 0 {
			h += 8
		}
		h += paragraphsHeight([]Paragraph{f.head}, width) + 2
		for _, row := range f.rows {
			h += rowHeight(row, width)
		}
	}
	return h
}

func rowHeight(row [2]Paragraph, width float64) float64 {
	nameW := width*fieldNameShare - 8
	valueW := width*(1-fieldNameShare) - 8
	lines := max(countLines(row[0].Runs, row[0].Size, nameW), countLines(row[1].Runs, row[1].Size, valueW))
	return float64(lines)*row[1].LineHeight + 6
}

func placeFields(fields []eventFields, x, y, width float64) []Element {
	var out []Element
	for i, f := range fields {
		if i > 0 {
			y += 8
		}
		h := paragraphsHeight([]Paragraph{f.head}, width)
		out = append(out, &TextBox{X: x, Y: y, W: width, H: h, Paragraphs: []Paragraph{f.head}})
		y += h + 2
		t := &Table{X: x, Y: y, W: width, Columns: []float64{width * fieldNameShare, width * (1 - fieldNameShare)}, Padding: Padding{X: 4, Y: 3}}
		for _, row := range f.rows {
			rh := rowHeight(row, width)
			t.Rows = append(t.Rows, TableRow{Height: rh, Cells: []Paragraph{row[0], row[1]}})
			t.H += rh
		}
		out = append(out, t)
		y += t.H
	}
	return out
}

// The sizes of the text of a diagram on a page, in points. They follow
// the scale of the diagram, but no further down than these.
const (
	minNodeSize  = 11.0
	minRoleSize  = 8.0
	minLabelSize = 9.0
)

// drawDiagram draws a diagram as a step of it leaves it: the part of it
// in camera, scaled by k, with its top left corner at (x, y). Everything
// is a native shape; a node is one box with its text, so that it moves
// as one and its text can be edited.
func drawDiagram(diagram *Diagram, scene *deck.Scene, step deck.Step, camera Box, k, x, y float64) []Element {
	at := func(px, py float64) (float64, float64) { return x + (px-camera.X)*k, y + (py-camera.Y)*k }
	box := func(b Box) (float64, float64, float64, float64) {
		bx, by := at(b.X, b.Y)
		return bx, by, b.W * k, b.H * k
	}
	g := &Group{Name: "diagram " + scene.ID, X: x, Y: y, W: camera.W * k, H: camera.H * k}
	nodeSize, roleSize, labelSize := max(DiagramNodeSize*k, minNodeSize), max(DiagramRoleSize*k, minRoleSize), max(DiagramLabelSize*k, minLabelSize)
	focused := func(id string) bool { return slices.Contains(step.Focus, id) }
	touched := map[string]bool{}
	for _, id := range step.Events {
		for _, e := range diagram.Events {
			if e.ID == id {
				for _, n := range e.Nodes {
					touched[n] = true
				}
			}
		}
	}
	for _, gr := range diagram.Groups {
		bx, by, bw, bh := box(gr.Box)
		lx, ly := at(gr.LabelX, gr.LabelY)
		g.Elements = append(g.Elements,
			&Rect{Name: "group " + gr.ID, X: bx, Y: by, W: bw, H: bh, Fill: ColorStage, Line: ColorGroupLine, Round: true, Dash: true},
			&TextBox{Name: "group label " + gr.ID, X: lx, Y: ly - roleSize, W: bw - (lx - bx), H: roleSize * 1.4, Anchor: AnchorMiddle,
				Paragraphs: []Paragraph{{Runs: runsOf([][]deck.Span{gr.Label}), Size: roleSize, LineHeight: lineHeight(roleSize, lineHeightText), Color: ColorMuted}}})
	}
	if diagram.View == deck.ViewLadder {
		for _, l := range diagram.Lines {
			lx, ly1 := at(l.X, l.Y1)
			_, ly2 := at(l.X, min(l.Y2, camera.Y+camera.H))
			hx, _, hw, hh := box(l.Head)
			// The heads stay at the top of the view, as in the HTML.
			hy := y + ladderTop*k
			ly1 = hy + hh
			fill, line, color := ColorNodeFill, ColorNodeLine, ColorInk
			if focused(l.ID) {
				fill, line = ColorFocusFill, ColorFocusLine
			}
			g.Elements = append(g.Elements,
				&Line{Name: "lifeline " + l.ID, X1: lx, Y1: ly1, X2: lx, Y2: ly2, Color: ColorDimLine, Width: 1},
				&TextBox{Name: "node " + l.ID, X: hx, Y: hy, W: hw, H: hh, Fill: fill, Line: line, Round: true, Anchor: AnchorMiddle,
					Paragraphs: []Paragraph{{Runs: []Run{{Text: l.Label}}, Size: nodeSize * 0.8, LineHeight: lineHeight(nodeSize*0.8, lineHeightTitle), Color: color, Bold: true, Align: AlignCenter}}})
		}
	}
	for _, n := range diagram.Nodes {
		bx, by, bw, bh := box(n.Box)
		fill, line, color, width := ColorNodeFill, ColorNodeLine, ColorInk, 0.75
		switch {
		case focused(n.ID):
			fill, line, width = ColorFocusFill, ColorFocusLine, 2
		case !touched[n.ID] && len(step.Focus) > 0:
			line, color = ColorDimLine, ColorDimText
		}
		nameSize := nodeSize
		if n.NameSize > 0 {
			nameSize = math.Round(nodeSize*n.NameSize/DiagramNodeSize*2) / 2
		}
		paragraphs := []Paragraph{{Runs: []Run{{Text: n.Label}}, Size: nameSize, LineHeight: lineHeight(nameSize, lineHeightTitle), Color: color, Bold: true, Align: AlignCenter}}
		if len(n.Role) > 0 {
			paragraphs = append(paragraphs, Paragraph{Runs: runsOf([][]deck.Span{n.Role}), Size: roleSize, LineHeight: lineHeight(roleSize, lineHeightText), Color: ColorMuted, Align: AlignCenter})
		}
		g.Elements = append(g.Elements, &TextBox{Name: "node " + n.ID, X: bx, Y: by, W: bw, H: bh, Inset: 2, Fill: fill, Line: line, LineWidth: width, Round: true, Anchor: AnchorMiddle, Paragraphs: paragraphs})
	}
	for _, e := range diagram.Events {
		if !slices.Contains(step.Events, e.ID) {
			continue
		}
		active := e.ID == step.Active
		color := FamilyColors[e.Family]
		labelColor := ColorInk
		// A message that a later step has passed is drawn lighter; a
		// relation lasts and keeps its colour.
		if !active && e.Kind != deck.EventRelation {
			color = tint(color, 0.45)
			labelColor = ColorMuted
		}
		// On a scrolled ladder the rows above the view, and those under
		// the heads at its top, are not drawn, as in the HTML.
		if diagram.View == deck.ViewLadder && hiddenRow(e, camera) {
			continue
		}
		if e.Kind == deck.EventProcess {
			bx, by, bw, bh := box(e.Box)
			lineW := 0.75
			if active {
				lineW = 1.75
			}
			g.Elements = append(g.Elements, &TextBox{Name: "event " + e.ID, X: bx, Y: by, W: bw, H: bh, Inset: 2, Fill: ColorProcessFill, Line: ColorProcessLine, LineWidth: lineW, Round: true, Anchor: AnchorMiddle,
				Paragraphs: []Paragraph{{Runs: []Run{{Text: e.LabelText()}}, Size: labelSize * 0.9, LineHeight: lineHeight(labelSize*0.9, lineHeightText), Color: labelColor, Bold: active, Align: AlignCenter}}})
			continue
		}
		x1, y1 := at(e.X1, e.Y1)
		x2, y2 := at(e.X2, e.Y2)
		width := 1.25
		if active {
			width = 2.25
		}
		l := &Line{Name: "event " + e.ID, X1: x1, Y1: y1, X2: x2, Y2: y2, Color: color, Width: width, HeadEnd: !e.Undirected,
			Dash: e.Family == "diameter" || e.Family == "relation", Dot: e.Family == "dns"}
		g.Elements = append(g.Elements, l)
		subSize := labelSize * DiagramSubSize / DiagramLabelSize
		paragraphs := []Paragraph{{Runs: []Run{{Text: e.LabelText()}}, Size: labelSize, LineHeight: lineHeight(labelSize, 1.2), Color: labelColor, Bold: active, Align: AlignCenter}}
		if sub := e.SubText(); sub != "" {
			if diagram.View == deck.ViewMap {
				paragraphs = append(paragraphs, Paragraph{Runs: []Run{{Text: sub}}, Size: subSize, LineHeight: lineHeight(subSize, 1.2), Color: ColorMuted, Align: AlignCenter})
			} else {
				paragraphs[0].Runs = append(paragraphs[0].Runs, Run{Text: " " + sub})
			}
		}
		lb := e.LabelBox()
		lx, ly := at(lb.X, lb.Y)
		ratio := labelSize / (DiagramLabelSize * k)
		w, h := lb.W*k*ratio+4, lb.H*k*ratio
		cx := lx + lb.W*k/2
		cy := ly + lb.H*k/2
		g.Elements = append(g.Elements, &TextBox{Name: "label " + e.ID, X: cx - w/2, Y: cy - h/2, W: w, H: h, Anchor: AnchorMiddle,
			Paragraphs: paragraphs})
	}
	return []Element{g}
}

// hiddenRow reports a row of a ladder that a view does not show: above
// it, under the heads of the lifelines at its top, or below it.
func hiddenRow(e EventShape, camera Box) bool {
	y := e.Y1
	if e.Kind == deck.EventProcess {
		y = e.LabelY
	}
	return y < camera.Y+ladderTop+ladderHeadH+8 || !camera.contains(e.LabelX, e.LabelY)
}

// tint mixes a colour with white: share 0 keeps it, 1 makes it white.
func tint(hex string, share float64) string {
	var r, g, b int
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	mix := func(c int) int { return int(math.Round(float64(c) + (255-float64(c))*share)) }
	return fmt.Sprintf("%02X%02X%02X", mix(r), mix(g), mix(b))
}
