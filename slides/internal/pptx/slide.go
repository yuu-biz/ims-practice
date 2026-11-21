package pptx

import (
	"fmt"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// slideWriter writes one slide. It hands out the shape identifiers and
// collects the hyperlinks, which become relationships of the slide.
type slideWriter struct {
	lang  string
	b     strings.Builder
	id    int
	links []string
}

// slide returns the XML of a slide and the targets of its hyperlinks, in
// the order of their relationship identifiers rId2, rId3, ...; rId1 is
// the slide layout.
func slide(lang string, page layout.Page) (string, []string) {
	w := &slideWriter{lang: lang, id: 1}
	w.b.WriteString(xmlHeader + `<p:sld xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"><p:cSld>`)
	if page.Background != layout.ColorWhite {
		w.b.WriteString(background(page.Background))
	}
	w.b.WriteString(`<p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
		`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)
	w.elements(page, page.Elements)
	w.b.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
	return w.b.String(), w.links
}

func (w *slideWriter) elements(page layout.Page, elements []layout.Element) {
	for _, e := range elements {
		switch e := e.(type) {
		case *layout.TextBox:
			w.textBox(page, e)
		case *layout.Table:
			w.table(e)
		case *layout.Line:
			w.line(e)
		case *layout.Rect:
			w.rect(e)
		case *layout.Group:
			w.group(page, e)
		}
	}
}

// group writes p:grpSp. The child coordinates are those of the slide:
// the child offset and extent of the group equal its own.
func (w *slideWriter) group(page layout.Page, g *layout.Group) {
	id := w.nextID()
	fmt.Fprintf(&w.b, `<p:grpSp><p:nvGrpSpPr><p:cNvPr id="%d" name="Group %d"/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>`, id, id)
	fmt.Fprintf(&w.b, `<p:grpSpPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/><a:chOff x="%d" y="%d"/><a:chExt cx="%d" cy="%d"/></a:xfrm></p:grpSpPr>`,
		emu(g.X), emu(g.Y), emu(g.W), emu(g.H), emu(g.X), emu(g.Y), emu(g.W), emu(g.H))
	w.elements(page, g.Elements)
	w.b.WriteString(`</p:grpSp>`)
}

// layoutOf is the number of the slide layout that a slide uses.
func layoutOf(page layout.Page) int {
	switch page.Layout {
	case deck.LayoutTitle:
		return layoutTitle
	case deck.LayoutSection:
		return layoutSection
	}
	return layoutOther
}

func (w *slideWriter) nextID() int {
	w.id++
	return w.id
}

func xfrm(x, y, width, height float64) string {
	return fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, emu(x), emu(y), emu(width), emu(height))
}

func fillAndLine(fill, line string) string {
	s := `<a:noFill/>`
	if fill != "" {
		s = solidFill(fill)
	}
	if line != "" {
		s += `<a:ln w="9525">` + solidFill(line) + `</a:ln>`
	} else {
		s += `<a:ln><a:noFill/></a:ln>`
	}
	return s
}

// placeholderOf is the p:ph of a text box with a role, and the name of
// its shape.
func placeholderOf(page layout.Page, role layout.Role) (string, string) {
	switch role {
	case layout.RoleTitle:
		if page.Layout == deck.LayoutTitle {
			return `<p:ph type="ctrTitle"/>`, "Title"
		}
		return `<p:ph type="title"/>`, "Title"
	case layout.RoleSubtitle:
		if page.Layout == deck.LayoutTitle {
			return fmt.Sprintf(`<p:ph type="subTitle" idx="%d"/>`, indexBody), "Subtitle"
		}
		return fmt.Sprintf(`<p:ph type="body" idx="%d"/>`, indexBody), "Text"
	case layout.RoleFooter:
		return fmt.Sprintf(`<p:ph type="ftr" sz="quarter" idx="%d"/>`, indexFooter), "Footer"
	case layout.RoleNumber:
		return fmt.Sprintf(`<p:ph type="sldNum" sz="quarter" idx="%d"/>`, indexNumber), "Slide Number"
	}
	return "", "Text"
}

func (w *slideWriter) textBox(page layout.Page, t *layout.TextBox) {
	id := w.nextID()
	ph, name := placeholderOf(page, t.Role)
	if ph != "" {
		fmt.Fprintf(&w.b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>%s</p:nvPr></p:nvSpPr>`, id, name, id, ph)
	} else {
		fmt.Fprintf(&w.b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s %d"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`, id, name, id)
	}
	w.b.WriteString(`<p:spPr>` + xfrm(t.X, t.Y, t.W, t.H) + `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>` + fillAndLine(t.Fill, t.Line) + `</p:spPr>`)
	wrap := "square"
	if t.NoWrap {
		wrap = "none"
	}
	anchor := map[layout.Anchor]string{layout.AnchorTop: "t", layout.AnchorMiddle: "ctr", layout.AnchorBottom: "b"}[t.Anchor]
	inset := emu(t.Inset)
	fmt.Fprintf(&w.b, `<p:txBody><a:bodyPr wrap="%s" lIns="%d" tIns="%d" rIns="%d" bIns="%d" anchor="%s" rtlCol="0"><a:noAutofit/></a:bodyPr><a:lstStyle/>`,
		wrap, inset, inset, inset, inset, anchor)
	for _, p := range t.Paragraphs {
		w.paragraph(p, t.Role == layout.RoleNumber)
	}
	w.b.WriteString(`</p:txBody></p:sp>`)
}

// paragraph writes a:p. Every paragraph sets its size, colour, spacing
// and bullet itself, so that it looks the same whatever the master says.
func (w *slideWriter) paragraph(p layout.Paragraph, slideNumber bool) {
	algn := map[layout.Align]string{layout.AlignLeft: "l", layout.AlignCenter: "ctr", layout.AlignRight: "r"}[p.Align]
	fmt.Fprintf(&w.b, `<a:p><a:pPr marL="%d" indent="%d" algn="%s">`, emu(p.Indent), emu(p.MarkerAt-p.Indent), algn)
	fmt.Fprintf(&w.b, `<a:lnSpc><a:spcPts val="%d"/></a:lnSpc><a:spcBef><a:spcPts val="%d"/></a:spcBef>`, hundredths(p.LineHeight), hundredths(p.SpaceBefore))
	switch {
	case p.ListStart > 0:
		w.b.WriteString(`<a:buClr>` + srgb(p.Color) + `</a:buClr><a:buFont typeface="+mj-lt"/>`)
		fmt.Fprintf(&w.b, `<a:buAutoNum type="arabicPeriod" startAt="%d"/>`, p.ListStart)
	case p.Marker != "":
		w.b.WriteString(`<a:buClr>` + srgb(layout.ColorAccent) + `</a:buClr><a:buFont typeface="Arial"/><a:buChar char="` + escape(p.Marker) + `"/>`)
	default:
		w.b.WriteString(`<a:buNone/>`)
	}
	w.b.WriteString(`</a:pPr>`)
	if slideNumber {
		text := ""
		for _, r := range p.Runs {
			text += r.Text
		}
		w.b.WriteString(`<a:fld id="` + slideNumberField + `" type="slidenum">` + w.runProperties(p, layout.Run{}, "a:rPr") + `<a:t>` + escape(text) + `</a:t></a:fld>`)
	} else {
		for _, r := range p.Runs {
			if r.Break {
				w.b.WriteString(`<a:br>` + w.runProperties(p, layout.Run{}, "a:rPr") + `</a:br>`)
				continue
			}
			if r.Text == "" {
				continue
			}
			w.b.WriteString(`<a:r>` + w.runProperties(p, r, "a:rPr") + `<a:t>` + escape(r.Text) + `</a:t></a:r>`)
		}
	}
	w.b.WriteString(w.runProperties(p, layout.Run{}, "a:endParaRPr") + `</a:p>`)
}

// runProperties is a:rPr (or a:endParaRPr) for a run of a paragraph.
func (w *slideWriter) runProperties(p layout.Paragraph, r layout.Run, element string) string {
	lang := "en-US"
	alt := ""
	if w.lang == "ja" {
		lang, alt = "ja-JP", ` altLang="en-US"`
	}
	bold := ""
	if p.Bold || r.Bold {
		bold = ` b="1"`
	}
	size := p.Size
	if r.Code && !p.Mono {
		size *= layout.CodeScale
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<%s lang="%s"%s sz="%d"%s dirty="0">`, element, lang, alt, hundredths(size), bold)
	color := p.Color
	if r.Href != "" {
		color = layout.ColorLink
	}
	b.WriteString(solidFill(color))
	if p.Mono || r.Code {
		b.WriteString(`<a:latin typeface="` + layout.FontMono + `"/><a:ea typeface="+mn-ea"/><a:cs typeface="` + layout.FontMono + `"/>`)
	} else {
		b.WriteString(`<a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/><a:cs typeface="+mn-cs"/>`)
	}
	if r.Href != "" {
		w.links = append(w.links, uriReference(r.Href))
		fmt.Fprintf(&b, `<a:hlinkClick r:id="rId%d"/>`, len(w.links)+1)
	}
	b.WriteString(`</` + element + `>`)
	return b.String()
}

func (w *slideWriter) table(t *layout.Table) {
	id := w.nextID()
	fmt.Fprintf(&w.b, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="Table %d"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`, id, id)
	fmt.Fprintf(&w.b, `<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm>`, emu(t.X), emu(t.Y), emu(t.W), emu(t.H))
	w.b.WriteString(`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1"/><a:tblGrid>`)
	for _, c := range t.Columns {
		fmt.Fprintf(&w.b, `<a:gridCol w="%d"/>`, emu(c))
	}
	w.b.WriteString(`</a:tblGrid>`)
	noLine := `<a:noFill/>`
	rule := solidFill(layout.ColorRule)
	for k, row := range t.Rows {
		fmt.Fprintf(&w.b, `<a:tr h="%d">`, emu(row.Height))
		for _, cell := range row.Cells {
			w.b.WriteString(`<a:tc><a:txBody><a:bodyPr/><a:lstStyle/>`)
			w.paragraph(cell, false)
			fmt.Fprintf(&w.b, `</a:txBody><a:tcPr marL="%d" marR="%d" marT="%d" marB="%d">`, emu(t.Padding.X), emu(t.Padding.X), emu(t.Padding.Y), emu(t.Padding.Y))
			top, bottom, width := rule, rule, 9525
			if k == 0 {
				top = noLine
			}
			if row.Header {
				bottom, width = solidFill(layout.ColorAccent), 19050
			}
			fmt.Fprintf(&w.b, `<a:lnL w="9525">%s</a:lnL><a:lnR w="9525">%s</a:lnR><a:lnT w="9525">%s</a:lnT><a:lnB w="%d">%s</a:lnB>`, noLine, noLine, top, width, bottom)
			if row.Header {
				w.b.WriteString(solidFill(layout.ColorHeaderFill))
			} else {
				w.b.WriteString(`<a:noFill/>`)
			}
			w.b.WriteString(`</a:tcPr></a:tc>`)
		}
		w.b.WriteString(`</a:tr>`)
	}
	w.b.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
}

// line writes a straight connector. DrawingML draws a line from the top
// left to the bottom right of its box; a line that runs the other way is
// flipped.
func (w *slideWriter) line(l *layout.Line) {
	id := w.nextID()
	x, y := min(l.X1, l.X2), min(l.Y1, l.Y2)
	flip := ""
	if l.X2 < l.X1 {
		flip += ` flipH="1"`
	}
	if l.Y2 < l.Y1 {
		flip += ` flipV="1"`
	}
	fmt.Fprintf(&w.b, `<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="%d" name="Line %d"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr>`, id, id)
	fmt.Fprintf(&w.b, `<p:spPr><a:xfrm%s><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="straightConnector1"><a:avLst/></a:prstGeom>`,
		flip, emu(x), emu(y), emu(max(l.X1, l.X2)-x), emu(max(l.Y1, l.Y2)-y))
	fmt.Fprintf(&w.b, `<a:ln w="%d">%s`, emu(l.Width), solidFill(l.Color))
	if l.Dash {
		w.b.WriteString(`<a:prstDash val="dash"/>`)
	}
	if l.HeadStart {
		w.b.WriteString(`<a:headEnd type="triangle" w="med" len="med"/>`)
	}
	if l.HeadEnd {
		w.b.WriteString(`<a:tailEnd type="triangle" w="med" len="med"/>`)
	}
	w.b.WriteString(`</a:ln></p:spPr></p:cxnSp>`)
}

func (w *slideWriter) rect(r *layout.Rect) {
	id := w.nextID()
	geometry := "rect"
	if r.Round {
		geometry = "roundRect"
	}
	fmt.Fprintf(&w.b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Shape %d"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>`, id, id)
	w.b.WriteString(`<p:spPr>` + xfrm(r.X, r.Y, r.W, r.H) + `<a:prstGeom prst="` + geometry + `"><a:avLst/></a:prstGeom>` + fillAndLine(r.Fill, r.Line) + `</p:spPr></p:sp>`)
}
