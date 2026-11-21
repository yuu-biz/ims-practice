// Package html writes a deck as one self-contained HTML file from the
// boxes that package layout placed, the same boxes that the pptx holds.
// The file needs no network: the styles and the script are inside it,
// and the fonts are those of the machine.
//
// In a browser one slide shows at a time, scaled to the window. The
// arrow keys, Page Up and Page Down, Space, Home and End move between the
// slides, and "f" switches to full screen. Printing gives one slide per
// page of 10 by 5.625 inches, the size of the pptx slides.
package html

import (
	"fmt"
	"html"
	"io"
	"math"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

// Write writes the HTML of a deck.
func Write(out io.Writer, d *deck.Deck, pages []layout.Page) error {
	var b strings.Builder
	fmt.Fprintf(&b, "<!DOCTYPE html>\n<html lang=\"%s\">\n<head>\n<meta charset=\"utf-8\">\n"+
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<title>%s</title>\n<style>\n%s</style>\n</head>\n<body>\n<main>\n",
		d.Lang, html.EscapeString(d.Title), style())
	for _, page := range pages {
		fmt.Fprintf(&b, `<section id="%d" class="slide %s" style="background:#%s">`+"\n", page.Number, page.Layout, page.Background)
		elements(&b, page.Elements)
		b.WriteString("</section>\n")
	}
	b.WriteString("</main>\n<script>\n" + script + "</script>\n</body>\n</html>\n")
	_, err := io.WriteString(out, b.String())
	return err
}

func elements(b *strings.Builder, list []layout.Element) {
	for _, e := range list {
		switch e := e.(type) {
		case *layout.TextBox:
			textBox(b, e)
		case *layout.Table:
			table(b, e)
		case *layout.Line:
			line(b, e)
		case *layout.Rect:
			rect(b, e)
		case *layout.Group:
			// The elements of a group are placed on the slide; the group
			// adds no offset of its own.
			elements(b, e.Elements)
		}
	}
}

// px writes a length of the layout, in points, as CSS pixels: a slide is
// 960 by 540 pixels before it is scaled to the window.
func px(points float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", points), "0"), ".") + "px"
}

func position(x, y, w, h float64) string {
	return fmt.Sprintf("left:%s;top:%s;width:%s;height:%s", px(x), px(y), px(w), px(h))
}

func textBox(b *strings.Builder, t *layout.TextBox) {
	classes := "box"
	if t.NoWrap {
		classes += " nowrap"
	}
	classes += map[layout.Anchor]string{layout.AnchorTop: "", layout.AnchorMiddle: " middle", layout.AnchorBottom: " bottom"}[t.Anchor]
	style := position(t.X, t.Y, t.W, t.H) + ";padding:" + px(t.Inset)
	if t.Fill != "" {
		style += ";background:#" + t.Fill
	}
	if t.Line != "" {
		style += ";outline:1px solid #" + t.Line + ";outline-offset:-1px"
	}
	fmt.Fprintf(b, `<div class="%s" style="%s">`, classes, style)
	for _, p := range t.Paragraphs {
		paragraph(b, p)
	}
	b.WriteString("</div>\n")
}

func paragraph(b *strings.Builder, p layout.Paragraph) {
	style := fmt.Sprintf("font-size:%s;line-height:%s;color:#%s", px(p.Size), px(p.LineHeight), p.Color)
	if p.SpaceBefore > 0 {
		style += ";margin-top:" + px(p.SpaceBefore)
	}
	if p.Indent > 0 {
		style += ";padding-left:" + px(p.Indent)
	}
	if p.Align != layout.AlignLeft {
		style += ";text-align:" + map[layout.Align]string{layout.AlignCenter: "center", layout.AlignRight: "right"}[p.Align]
	}
	if p.Bold {
		style += ";font-weight:bold"
	}
	class := ""
	if p.Mono {
		class = ` class="mono"`
	}
	fmt.Fprintf(b, `<p%s style="%s">`, class, style)
	if p.Marker != "" {
		markerClass := "marker"
		if p.ListStart == 0 {
			markerClass += " bullet"
		}
		fmt.Fprintf(b, `<span class="%s" style="left:%s">%s</span>`, markerClass, px(p.MarkerAt), html.EscapeString(p.Marker))
	}
	runs(b, p.Runs)
	if n := len(p.Runs); n == 0 || p.Runs[n-1].Break || p.Runs[n-1].Text == "" {
		// An empty line, such as one in a code block, keeps its height.
		b.WriteString("<br>")
	}
	b.WriteString("</p>")
}

func runs(b *strings.Builder, runs []layout.Run) {
	for _, r := range runs {
		if r.Break {
			b.WriteString("<br>")
			continue
		}
		text := html.EscapeString(r.Text)
		if r.Code {
			text = "<code>" + text + "</code>"
		}
		if r.Bold {
			text = "<b>" + text + "</b>"
		}
		if r.Href != "" {
			text = `<a href="` + html.EscapeString(r.Href) + `">` + text + "</a>"
		}
		b.WriteString(text)
	}
}

func table(b *strings.Builder, t *layout.Table) {
	fmt.Fprintf(b, `<table style="left:%s;top:%s;width:%s"><colgroup>`, px(t.X), px(t.Y), px(t.W))
	for _, c := range t.Columns {
		fmt.Fprintf(b, `<col style="width:%s">`, px(c))
	}
	b.WriteString("</colgroup>")
	padding := px(t.Padding.Y) + " " + px(t.Padding.X)
	for _, row := range t.Rows {
		cell := "td"
		if row.Header {
			cell = "th"
		}
		fmt.Fprintf(b, `<tr style="height:%s">`, px(row.Height))
		for _, p := range row.Cells {
			align := map[layout.Align]string{layout.AlignLeft: "left", layout.AlignCenter: "center", layout.AlignRight: "right"}[p.Align]
			fmt.Fprintf(b, `<%s style="padding:%s;text-align:%s;font-size:%s;line-height:%s">`, cell, padding, align, px(p.Size), px(p.LineHeight))
			runs(b, p.Runs)
			fmt.Fprintf(b, "</%s>", cell)
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>\n")
}

// line draws a line in an SVG of its own, so that it keeps its place in
// the order of the elements. The SVG has room around the line for the
// arrowheads.
func line(b *strings.Builder, l *layout.Line) {
	const room = 8.0
	x, y := min(l.X1, l.X2)-room, min(l.Y1, l.Y2)-room
	w, h := max(l.X1, l.X2)-x+room, max(l.Y1, l.Y2)-y+room
	fmt.Fprintf(b, `<svg class="line" style="%s" viewBox="%.2f %.2f %.2f %.2f">`, position(x, y, w, h), x, y, w, h)
	dash := ""
	if l.Dash {
		dash = fmt.Sprintf(` stroke-dasharray="%.1f %.1f"`, 4*l.Width, 3*l.Width)
	}
	fmt.Fprintf(b, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="#%s" stroke-width="%.2f"%s/>`, l.X1, l.Y1, l.X2, l.Y2, l.Color, l.Width, dash)
	if l.HeadEnd {
		arrowhead(b, l.X1, l.Y1, l.X2, l.Y2, l)
	}
	if l.HeadStart {
		arrowhead(b, l.X2, l.Y2, l.X1, l.Y1, l)
	}
	b.WriteString("</svg>\n")
}

// arrowhead draws a filled triangle at (x2, y2), pointing away from
// (x1, y1), of the size of PowerPoint's medium arrowhead: three times
// the width of the line.
func arrowhead(b *strings.Builder, x1, y1, x2, y2 float64, l *layout.Line) {
	length := max(3*l.Width, 5)
	dx, dy := x2-x1, y2-y1
	n := max(1e-9, math.Hypot(dx, dy))
	ux, uy := dx/n, dy/n
	bx, by := x2-ux*length, y2-uy*length
	ox, oy := -uy*length/2, ux*length/2
	fmt.Fprintf(b, `<polygon points="%.2f,%.2f %.2f,%.2f %.2f,%.2f" fill="#%s"/>`, x2, y2, bx+ox, by+oy, bx-ox, by-oy, l.Color)
}

func rect(b *strings.Builder, r *layout.Rect) {
	style := position(r.X, r.Y, r.W, r.H) + ";background:#" + r.Fill
	if r.Line != "" {
		style += ";outline:1px solid #" + r.Line + ";outline-offset:-1px"
	}
	if r.Round {
		style += ";border-radius:" + px(min(r.W, r.H)/4)
	}
	fmt.Fprintf(b, `<div class="rect" style="%s"></div>`+"\n", style)
}
