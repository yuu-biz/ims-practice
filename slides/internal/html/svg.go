package html

import (
	"fmt"
	"html"
	"math"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// num writes a coordinate of an SVG with two decimals at most.
func num(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func viewBox(b layout.Box) string {
	return num(b.X) + " " + num(b.Y) + " " + num(b.W) + " " + num(b.H)
}

// diagramSVG writes the SVG of a scene as its step k leaves it. Every
// node and every event of the scene is in it, each with its id, and the
// state of the step is in the classes: the script of the page moves from
// step to step by changing them, so the same markup is the still figure
// of the reading view and the first state of the stage.
//
// prefix makes the ids of the SVG unique in the page, which holds the
// same scene more than once.
func diagramSVG(b *strings.Builder, d *deck.Deck, s deck.Slide, diagram *layout.Diagram, k int, prefix string, camera layout.Box) {
	scene := s.Scene
	step := scene.Steps[k]
	fmt.Fprintf(b, `<svg class="diagram view-%s" viewBox="%s" preserveAspectRatio="xMidYMid meet" role="img" aria-label="%s">`,
		diagram.View, viewBox(camera), html.EscapeString(deck.PlainText(s.Title)))
	b.WriteString("<defs>")
	for _, family := range layout.Families {
		color := layout.FamilyColors[family]
		fmt.Fprintf(b, `<marker id="%s-arrow-%s" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#%s"/></marker>`,
			prefix, family, color)
	}
	b.WriteString("</defs>")
	fmt.Fprintf(b, `<g class="world" id="%s-world">`, prefix)
	for _, g := range diagram.Groups {
		fmt.Fprintf(b, `<g class="group" data-id="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="14"/><text x="%s" y="%s">%s</text></g>`,
			g.ID, num(g.Box.X), num(g.Box.Y), num(g.Box.W), num(g.Box.H), num(g.LabelX), num(g.LabelY), spansText(g.Label))
	}
	touched := map[string]bool{}
	for _, e := range diagram.Events {
		if slices.Contains(step.Events, e.ID) {
			for _, n := range e.Nodes {
				touched[n] = true
			}
		}
	}
	nodeState := func(id string) string {
		switch {
		case slices.Contains(step.Focus, id):
			return "focus"
		case len(step.Focus) > 0 && !touched[id]:
			return "dim"
		}
		return "plain"
	}
	for _, l := range diagram.Lines {
		fmt.Fprintf(b, `<line class="lifeline" data-id="%s" x1="%s" y1="%s" x2="%s" y2="%s"/>`, l.ID, num(l.X), num(l.Y1), num(l.X), num(l.Y2))
	}
	for _, n := range diagram.Nodes {
		cx, cy := n.Box.Center()
		labelY := cy + layout.DiagramNodeSize*0.35
		if len(n.Role) > 0 {
			labelY = cy - 2
		}
		size := ""
		if n.NameSize > 0 && n.NameSize < layout.DiagramNodeSize {
			size = fmt.Sprintf(` style="font-size:%spx"`, num(n.NameSize))
		}
		fmt.Fprintf(b, `<g class="node" data-id="%s" data-state="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="10"/><text class="name" x="%s" y="%s"%s>%s</text>`,
			n.ID, nodeState(n.ID), num(n.Box.X), num(n.Box.Y), num(n.Box.W), num(n.Box.H), num(cx), num(labelY), size, html.EscapeString(n.Label))
		if len(n.Role) > 0 {
			fmt.Fprintf(b, `<text class="role" x="%s" y="%s">%s</text>`, num(cx), num(cy+18), spansText(n.Role))
		}
		b.WriteString("</g>")
	}
	for _, e := range diagram.Events {
		state := "off"
		if slices.Contains(step.Events, e.ID) {
			state = "done"
			if e.ID == step.Active {
				state = "active"
			}
		}
		fmt.Fprintf(b, `<g class="event %s" data-id="%s" data-nodes="%s" data-state="%s">`, e.Family, e.ID, strings.Join(e.Nodes, " "), state)
		if e.Kind == deck.EventProcess {
			cx, cy := e.Box.Center()
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="8"/><text class="label" x="%s" y="%s">%s</text>`,
				num(e.Box.X), num(e.Box.Y), num(e.Box.W), num(e.Box.H), num(cx), num(cy+5), html.EscapeString(e.LabelText()))
		} else {
			length := math.Hypot(e.X2-e.X1, e.Y2-e.Y1)
			marker := fmt.Sprintf(` marker-end="url(#%s-arrow-%s)"`, prefix, e.Family)
			if e.Undirected {
				marker = ""
			}
			fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" pathLength="%s"%s/>`,
				num(e.X1), num(e.Y1), num(e.X2), num(e.Y2), num(math.Round(length)), marker)
			box := e.LabelBox()
			fmt.Fprintf(b, `<rect class="pill" x="%s" y="%s" width="%s" height="%s" rx="9"/>`,
				num(box.X), num(box.Y), num(box.W), num(box.H))
			if e.LabelLines() == 2 && diagram.View == deck.ViewMap {
				fmt.Fprintf(b, `<text class="label" x="%s" y="%s">%s</text><text class="sub" x="%s" y="%s">%s</text>`,
					num(e.LabelX), num(box.Y+2+layout.DiagramLabelSize), html.EscapeString(e.LabelText()),
					num(e.LabelX), num(box.Y+box.H-5), html.EscapeString(e.SubText()))
			} else {
				fmt.Fprintf(b, `<text class="label" x="%s" y="%s">%s`, num(e.LabelX), num(e.LabelY+5), html.EscapeString(e.LabelText()))
				if sub := e.SubText(); sub != "" {
					fmt.Fprintf(b, `<tspan class="sub"> %s</tspan>`, html.EscapeString(sub))
				}
				b.WriteString("</text>")
			}
		}
		b.WriteString("</g>")
	}
	b.WriteString("</g>")
	if diagram.View == deck.ViewLadder {
		// The heads of the lifelines stay at the top of the view; the
		// script moves them with it.
		fmt.Fprintf(b, `<g class="heads" transform="translate(0 %s)">`, num(camera.Y))
		// A band behind the heads hides the rows that the view has
		// scrolled past.
		fmt.Fprintf(b, `<rect class="band" x="%s" y="0" width="%s" height="%s"/>`, num(diagram.Bounds.X), num(diagram.Bounds.W), num(diagram.Lines[0].Head.Y+diagram.Lines[0].Head.H+10))
		for _, l := range diagram.Lines {
			cx, cy := l.Head.Center()
			nameY := cy + 6
			if len(l.Role) > 0 {
				nameY = cy - 1
			}
			fmt.Fprintf(b, `<g class="node" data-id="%s" data-state="%s"><rect x="%s" y="%s" width="%s" height="%s" rx="8"/><text class="name" x="%s" y="%s">%s</text>`,
				l.ID, nodeState(l.ID), num(l.Head.X), num(l.Head.Y), num(l.Head.W), num(l.Head.H), num(cx), num(nameY), html.EscapeString(l.Label))
			if len(l.Role) > 0 {
				size := ""
				if l.RoleSize < layout.DiagramRoleSize {
					size = fmt.Sprintf(` style="font-size:%spx"`, num(l.RoleSize))
				}
				fmt.Fprintf(b, `<text class="role" x="%s" y="%s"%s>%s</text>`, num(cx), num(cy+16), size, spansText(l.Role))
			}
			b.WriteString("</g>")
		}
		b.WriteString("</g>")
	}
	b.WriteString("</svg>")
}

// spansText is the text of spans, escaped, without their styles: the
// text of an SVG has no links.
func spansText(spans []deck.Span) string {
	return html.EscapeString(deck.PlainText(spans))
}

// legend lists the kinds of line that a diagram draws.
func legend(b *strings.Builder, lang string, diagram *layout.Diagram) {
	var families []string
	for _, e := range diagram.Events {
		if !slices.Contains(families, e.Family) {
			families = append(families, e.Family)
		}
	}
	slices.SortFunc(families, func(a, c string) int { return slices.Index(layout.Families, a) - slices.Index(layout.Families, c) })
	b.WriteString(`<ul class="legend">`)
	for _, f := range families {
		fmt.Fprintf(b, `<li class="%s"><svg viewBox="0 0 28 10" aria-hidden="true"><line x1="1" y1="5" x2="27" y2="5"/></svg>%s</li>`, f, html.EscapeString(words[lang]["family-"+f]))
	}
	b.WriteString("</ul>")
}

// elementsSVG draws placed elements of the layout, such as a ladder of
// a slide, as an SVG of their own: the reading view flows its text and
// keeps the diagrams as pictures that scale with the page.
func elementsSVG(b *strings.Builder, elements []layout.Element, width, height float64) {
	fmt.Fprintf(b, `<svg class="shapes" viewBox="0 0 %s %s" role="img">`, num(width), num(height+2))
	var draw func([]layout.Element)
	draw = func(list []layout.Element) {
		for _, e := range list {
			switch e := e.(type) {
			case *layout.Group:
				draw(e.Elements)
			case *layout.Rect:
				fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="#%s"%s/>`, num(e.X), num(e.Y), num(e.W), num(e.H), e.Fill, stroke(e.Line))
			case *layout.Line:
				dash := ""
				if e.Dash {
					dash = fmt.Sprintf(` stroke-dasharray="%s %s"`, num(4*e.Width), num(3*e.Width))
				}
				fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#%s" stroke-width="%s"%s/>`, num(e.X1), num(e.Y1), num(e.X2), num(e.Y2), e.Color, num(e.Width), dash)
				head := func(x1, y1, x2, y2 float64) {
					length := max(3*e.Width, 5)
					n := max(1e-9, math.Hypot(x2-x1, y2-y1))
					ux, uy := (x2-x1)/n, (y2-y1)/n
					bx, by := x2-ux*length, y2-uy*length
					ox, oy := -uy*length/2, ux*length/2
					fmt.Fprintf(b, `<polygon points="%s,%s %s,%s %s,%s" fill="#%s"/>`, num(x2), num(y2), num(bx+ox), num(by+oy), num(bx-ox), num(by-oy), e.Color)
				}
				if e.HeadEnd {
					head(e.X1, e.Y1, e.X2, e.Y2)
				}
				if e.HeadStart {
					head(e.X2, e.Y2, e.X1, e.Y1)
				}
			case *layout.TextBox:
				if e.Fill != "" || e.Line != "" {
					fill := "none"
					if e.Fill != "" {
						fill = "#" + e.Fill
					}
					fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"%s/>`, num(e.X), num(e.Y), num(e.W), num(e.H), fill, stroke(e.Line))
				}
				y := e.Y + e.Inset
				total := 0.0
				for _, p := range e.Paragraphs {
					total += p.LineHeight
				}
				if e.Anchor == layout.AnchorMiddle {
					y = e.Y + (e.H-total)/2
				}
				for _, p := range e.Paragraphs {
					x, anchor := e.X+e.Inset+p.Indent, "start"
					switch p.Align {
					case layout.AlignCenter:
						x, anchor = e.X+e.W/2, "middle"
					case layout.AlignRight:
						x, anchor = e.X+e.W-e.Inset, "end"
					}
					weight := ""
					if p.Bold {
						weight = ` font-weight="bold"`
					}
					text := ""
					for _, r := range p.Runs {
						text += r.Text
					}
					fmt.Fprintf(b, `<text x="%s" y="%s" font-size="%s" fill="#%s" text-anchor="%s"%s>%s</text>`,
						num(x), num(y+p.LineHeight*0.75), num(p.Size), p.Color, anchor, weight, html.EscapeString(text))
					y += p.LineHeight
				}
			}
		}
	}
	draw(elements)
	b.WriteString("</svg>")
}

func stroke(color string) string {
	if color == "" {
		return ""
	}
	return ` stroke="#` + color + `"`
}
