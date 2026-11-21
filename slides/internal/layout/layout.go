package layout

import (
	"fmt"
	"math"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// OverflowError is a slide whose content does not fit, even at the
// smallest scale. Nothing on a slide is cut off or drawn outside its box
// in silence: the build fails, and the slide has to be split or
// shortened.
type OverflowError struct {
	Path   string
	Slide  int
	Title  string
	Reason string
}

func (e *OverflowError) Error() string {
	return fmt.Sprintf("%s: slide %d %q: %s", e.Path, e.Slide, e.Title, e.Reason)
}

// Deck lays out every slide of a deck. It returns the pages of the
// slides that fit and an error for each slide that does not.
func Deck(d *deck.Deck) ([]Page, []error) {
	var pages []Page
	var problems []error
	for _, s := range d.Slides {
		page, err := Slide(d, s)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		pages = append(pages, page)
	}
	return pages, problems
}

// Slide lays out one slide of a deck.
func Slide(d *deck.Deck, s deck.Slide) (Page, error) {
	overflow := func(format string, args ...any) error {
		return &OverflowError{Path: d.Path, Slide: s.Number, Title: deck.PlainText(s.Title), Reason: fmt.Sprintf(format, args...)}
	}
	page := Page{Number: s.Number, Layout: s.Layout, Background: ColorWhite, Scale: 1}
	switch s.Layout {
	case deck.LayoutTitle:
		return titleSlide(d, s, page), nil
	case deck.LayoutSection:
		return sectionSlide(d, s, page), nil
	}

	accent := ColorAccent
	tag := ""
	switch s.Layout {
	case deck.LayoutExercise:
		accent, tag = ColorExercise, tagWords[d.Lang]["exercise"]
	case deck.LayoutAnswers:
		accent, tag = ColorAnswers, tagWords[d.Lang]["answers"]
	}

	titleWidth := SlideWidth - 2*MarginX
	if tag != "" {
		tagWidth := textWidth(tag, sizeTag, false, true) + 24
		page.Elements = append(page.Elements,
			&Rect{X: SlideWidth - MarginX - tagWidth, Y: TitleTop + 10, W: tagWidth, H: 26, Fill: accent, Round: true},
			&TextBox{X: SlideWidth - MarginX - tagWidth, Y: TitleTop + 10, W: tagWidth, H: 26, Anchor: AnchorMiddle,
				Paragraphs: []Paragraph{{Runs: []Run{{Text: tag}}, Size: sizeTag, LineHeight: lineHeight(sizeTag, lineHeightText), Align: AlignCenter, Color: ColorWhite, Bold: true}}})
		titleWidth -= tagWidth + 16
	}
	title, titleHeight := heading(s.Title, titleWidth, accent)
	page.Elements = append(page.Elements, title)
	ruleY := TitleTop + titleHeight + 6
	page.Elements = append(page.Elements, &Line{X1: MarginX, Y1: ruleY, X2: SlideWidth - MarginX, Y2: ruleY, Color: accent, Width: 2.25})
	page.Elements = append(page.Elements, footer(d, s, ColorMuted)...)

	top := ruleY + 16
	left, width := MarginX, SlideWidth-2*MarginX
	if s.Layout == deck.LayoutSummary {
		left, width = MarginX+24, width-40
		top += 12
	}
	var body []Element
	var height float64
	var last error
	fitted := false
	for _, scale := range scales {
		b := builder{scale: scale}
		var err error
		if s.Layout == deck.LayoutTwoColumns {
			body, height, err = b.columns(s.Columns, left, top, width)
		} else {
			body, height, err = b.stack(s.Columns[0], left, top, width)
		}
		bottom := BodyBottom
		if s.Layout == deck.LayoutSummary {
			bottom -= 12
		}
		switch {
		case err != nil:
			last = err
		case top+height > bottom:
			last = fmt.Errorf("the content is %.0f points too tall", top+height-bottom)
		default:
			fitted = true
			page.Scale = scale
		}
		if fitted {
			break
		}
	}
	if !fitted {
		return page, overflow("%v at %.0f%% of the text size", last, scales[len(scales)-1]*100)
	}
	if s.Layout == deck.LayoutSummary {
		page.Elements = append(page.Elements,
			&Rect{X: MarginX, Y: top - 14, W: SlideWidth - 2*MarginX, H: height + 28, Fill: ColorPanelFill},
			&Rect{X: MarginX, Y: top - 14, W: 5, H: height + 28, Fill: accent})
	}
	page.Elements = append(page.Elements, body...)
	return page, nil
}

// lineHeight is the height of a line of text of a size, in whole
// points: a browser rounds a line to whole pixels, and a line height
// that is a whole number keeps the HTML as tall as the measure.
func lineHeight(size, factor float64) float64 {
	return math.Ceil(size * factor)
}

// heading is the title of a content slide. A title that does not fit on
// one line gets a smaller size, and then a second line.
func heading(spans []deck.Span, width float64, color string) (*TextBox, float64) {
	runs := runsOf([][]deck.Span{spans})
	size := SizeHeading
	if countLines(runs, size, width) > 1 {
		size = SizeHeading * 0.85
	}
	lines := min(countLines(runs, size, width), 2)
	height := float64(lines) * lineHeight(size, lineHeightTitle)
	return &TextBox{Role: RoleTitle, X: MarginX, Y: TitleTop, W: width, H: height, Anchor: AnchorBottom,
		Paragraphs: []Paragraph{{Runs: runs, Size: size, LineHeight: lineHeight(size, lineHeightTitle), Color: color, Bold: true}}}, height
}

func footer(d *deck.Deck, s deck.Slide, color string) []Element {
	text := func(role Role, x, w float64, align Align, value string) *TextBox {
		return &TextBox{Role: role, X: x, Y: FooterTop, W: w, H: FooterHeight, Anchor: AnchorMiddle,
			Paragraphs: []Paragraph{{Runs: []Run{{Text: value}}, Size: SizeFooter, LineHeight: lineHeight(SizeFooter, lineHeightText), Align: align, Color: color}}}
	}
	return []Element{
		text(RoleFooter, MarginX, 640, AlignLeft, d.Footer),
		text(RoleNumber, SlideWidth-MarginX-120, 120, AlignRight, fmt.Sprint(s.Number)),
	}
}

// titleSlide is the first slide: the title of the session, a short rule
// and the subtitle, beside a band of the accent colour.
func titleSlide(d *deck.Deck, s deck.Slide, page Page) Page {
	const left, width = 80.0, SlideWidth - 160
	title := runsOf([][]deck.Span{s.Title})
	size := SizeTitleSlide
	if countLines(title, size, width) > 2 {
		size *= 0.85
	}
	titleHeight := float64(countLines(title, size, width)) * lineHeight(size, lineHeightTitle)
	subtitle := paragraphsOf(s.Columns[0], SizeSubtitle, ColorMuted)
	subtitleHeight := paragraphsHeight(subtitle, width)
	top := math.Round((SlideHeight-titleHeight-28-subtitleHeight)/2) - 16
	page.Elements = append(page.Elements,
		&Rect{X: 0, Y: 0, W: 16, H: SlideHeight, Fill: ColorAccent},
		&TextBox{Role: RoleTitle, X: left, Y: top, W: width, H: titleHeight, Anchor: AnchorBottom,
			Paragraphs: []Paragraph{{Runs: title, Size: size, LineHeight: lineHeight(size, lineHeightTitle), Color: ColorAccent, Bold: true}}},
		&Line{X1: left, Y1: top + titleHeight + 12, X2: left + 120, Y2: top + titleHeight + 12, Color: ColorAccent, Width: 3},
		&TextBox{Role: RoleSubtitle, X: left, Y: top + titleHeight + 28, W: width, H: subtitleHeight, Paragraphs: subtitle},
	)
	page.Elements = append(page.Elements, footer(d, s, ColorMuted)[0])
	return page
}

// sectionSlide opens a part of a session, on the accent colour.
func sectionSlide(d *deck.Deck, s deck.Slide, page Page) Page {
	const left, width = 80.0, SlideWidth - 160
	page.Background = ColorAccent
	title := runsOf([][]deck.Span{s.Title})
	size := SizeSection
	titleHeight := float64(countLines(title, size, width)) * lineHeight(size, lineHeightTitle)
	subtitle := paragraphsOf(s.Columns[0], SizeSubtitle, ColorOnAccent)
	subtitleHeight := paragraphsHeight(subtitle, width)
	top := math.Round((SlideHeight-titleHeight-20-subtitleHeight)/2) - 10
	page.Elements = append(page.Elements,
		&TextBox{Role: RoleTitle, X: left, Y: top, W: width, H: titleHeight, Anchor: AnchorBottom,
			Paragraphs: []Paragraph{{Runs: title, Size: size, LineHeight: lineHeight(size, lineHeightTitle), Color: ColorWhite, Bold: true}}})
	if len(subtitle) > 0 {
		page.Elements = append(page.Elements,
			&TextBox{Role: RoleSubtitle, X: left, Y: top + titleHeight + 20, W: width, H: subtitleHeight, Paragraphs: subtitle})
	}
	page.Elements = append(page.Elements, footer(d, s, ColorOnAccent)...)
	return page
}

// paragraphsOf turns the paragraphs of a title or a section slide into
// paragraphs of one size, a line of the source to a paragraph.
func paragraphsOf(blocks []deck.Block, size float64, color string) []Paragraph {
	var out []Paragraph
	for _, b := range blocks {
		for i, l := range b.(deck.Paragraph).Lines {
			before := 0.0
			if i == 0 && len(out) > 0 {
				before = size * 0.6
			}
			out = append(out, Paragraph{Runs: runsOf([][]deck.Span{l}), Size: size, LineHeight: lineHeight(size, lineHeightText), SpaceBefore: before, Color: color})
		}
	}
	return out
}

func paragraphsHeight(paragraphs []Paragraph, width float64) float64 {
	h := 0.0
	for _, p := range paragraphs {
		h += p.SpaceBefore + float64(countLines(p.Runs, p.Size, width-p.Indent))*p.LineHeight
	}
	return h
}

// runsOf turns lines of spans into runs, with a break between lines.
func runsOf(lines [][]deck.Span) []Run {
	var runs []Run
	for i, l := range lines {
		if i > 0 {
			runs = append(runs, Run{Break: true})
		}
		for _, span := range l {
			runs = append(runs, Run{Text: span.Text, Bold: span.Bold, Code: span.Code, Href: span.Href})
		}
	}
	return runs
}

// builder places the blocks of a slide at one scale of the text.
type builder struct {
	scale float64
}

func (b builder) size(base float64) float64 { return math.Round(base*b.scale*2) / 2 }

// stack places blocks from top to bottom and returns their height.
func (b builder) stack(blocks []deck.Block, x, y, width float64) ([]Element, float64, error) {
	var out []Element
	top := y
	for i, blk := range blocks {
		if i > 0 {
			y += blockGap * b.scale
		}
		elements, h, err := b.block(blk, x, y, width)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, elements...)
		y += h
	}
	return out, y - top, nil
}

func (b builder) columns(columns [][]deck.Block, x, y, width float64) ([]Element, float64, error) {
	w := (width - columnGap) / 2
	var out []Element
	height := 0.0
	for i, column := range columns {
		elements, h, err := b.stack(column, x+float64(i)*(w+columnGap), y, w)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, elements...)
		height = max(height, h)
	}
	return out, height, nil
}

func (b builder) block(blk deck.Block, x, y, width float64) ([]Element, float64, error) {
	switch blk := blk.(type) {
	case deck.Paragraph:
		return b.text(b.paragraphLines(blk.Lines, SizeBody, ColorText), x, y, width)
	case deck.Quote:
		paragraphs := b.paragraphLines(blk.Lines, sizeQuote, ColorMuted)
		elements, h, err := b.text(paragraphs, x+14, y, width-14)
		if err != nil {
			return nil, 0, err
		}
		bar := &Line{X1: x + 2, Y1: y, X2: x + 2, Y2: y + h, Color: ColorAccent, Width: 3.5}
		return append([]Element{bar}, elements...), h, nil
	case deck.List:
		return b.text(b.list(blk, 0, SizeBody), x, y, width)
	case deck.Code:
		return b.code(blk, x, y, width)
	case deck.Table:
		return b.table(blk, x, y, width)
	case deck.Ladder:
		return b.ladder(blk, x, y, width)
	}
	return nil, 0, fmt.Errorf("a block of type %T has no layout", blk)
}

// paragraphLines makes a paragraph of each line, as in the source.
func (b builder) paragraphLines(lines [][]deck.Span, base float64, color string) []Paragraph {
	size := b.size(base)
	out := make([]Paragraph, len(lines))
	for i, l := range lines {
		out[i] = Paragraph{Runs: runsOf([][]deck.Span{l}), Size: size, LineHeight: lineHeight(size, lineHeightText), Color: color}
	}
	return out
}

// list makes a paragraph of each item, with the marker hanging to the
// left of the text. A nested list is indented one step further.
func (b builder) list(l deck.List, level int, base float64) []Paragraph {
	size := b.size(base)
	step := 1.2 * size
	if l.Ordered {
		step = 1.6 * size
	}
	at := float64(level) * 1.2 * b.size(SizeBody)
	var out []Paragraph
	for i, item := range l.Items {
		p := Paragraph{
			Runs: runsOf(item.Lines), Size: size, LineHeight: lineHeight(size, lineHeightText),
			Indent: at + step, MarkerAt: at, Color: ColorText,
		}
		switch {
		case l.Ordered:
			p.Marker = fmt.Sprintf("%d.", l.Start+i)
			p.ListStart = l.Start
		case level == 0:
			p.Marker = "•"
		default:
			p.Marker = "–"
		}
		if len(out) > 0 {
			p.SpaceBefore = math.Round(size * 0.3)
		}
		out = append(out, p)
		if item.Children != nil {
			children := b.list(*item.Children, level+1, base*0.9)
			for k := range children {
				children[k].SpaceBefore = math.Round(size * 0.15)
			}
			out = append(out, children...)
		}
	}
	return out
}

func (b builder) text(paragraphs []Paragraph, x, y, width float64) ([]Element, float64, error) {
	for _, p := range paragraphs {
		if longest := longestPiece(p.Runs, p.Size); longest > (width-p.Indent)*fillLimit && !hasCode(p.Runs) {
			// A word that is wider than its line is cut by the
			// applications; inline code, such as an address, may be.
			return nil, 0, fmt.Errorf("a word is %.0f points wider than its line", longest-(width-p.Indent)*fillLimit)
		}
	}
	h := paragraphsHeight(paragraphs, width)
	return []Element{&TextBox{X: x, Y: y, W: width, H: h, Paragraphs: paragraphs}}, h, nil
}

func hasCode(runs []Run) bool {
	for _, r := range runs {
		if r.Code || r.Href != "" {
			return true
		}
	}
	return false
}

// code places a fenced block in a monospace font. Its lines are never
// wrapped: a block that is too wide gets a smaller size, down to
// minSizeCode.
func (b builder) code(c deck.Code, x, y, width float64) ([]Element, float64, error) {
	const inset = 8.0
	widest := 0.0
	for _, l := range c.Lines {
		widest = max(widest, columns(l))
	}
	size := b.size(sizeCode)
	if widest > 0 {
		size = min(size, math.Floor((width-2*inset)/(widest*monoWidth)*2)/2)
	}
	if size < minSizeCode {
		return nil, 0, fmt.Errorf("a code block of %.0f columns is too wide even at %.0f points", widest, minSizeCode)
	}
	paragraphs := make([]Paragraph, len(c.Lines))
	for i, l := range c.Lines {
		paragraphs[i] = Paragraph{Runs: []Run{{Text: l}}, Size: size, LineHeight: lineHeight(size, lineHeightCode), Color: ColorText, Mono: true}
	}
	h := float64(len(c.Lines))*lineHeight(size, lineHeightCode) + 2*inset
	return []Element{&TextBox{X: x, Y: y, W: width, H: h, Inset: inset, Fill: ColorCodeFill, Line: ColorCodeLine, NoWrap: true, Paragraphs: paragraphs}}, h, nil
}

// table decides the widths of the columns and the heights of the rows.
// A column gets the room that its widest cell needs on one line when the
// table fits; otherwise every column keeps its longest word and the rest
// of the width goes to the columns in proportion to what they lack. The
// table takes the whole width of its box.
func (b builder) table(t deck.Table, x, y, width float64) ([]Element, float64, error) {
	size := b.size(sizeTable)
	pad := Padding{X: math.Round(size * 0.45), Y: math.Round(size * 0.25)}
	rowLine := lineHeight(size, lineHeightText)
	cell := func(spans deck.Cell, header bool, align deck.Align) Paragraph {
		runs := runsOf([][]deck.Span{spans})
		if header {
			for i := range runs {
				runs[i].Bold = true
			}
		}
		return Paragraph{Runs: runs, Size: size, LineHeight: rowLine, Color: ColorText, Bold: header, Align: Align(align)}
	}
	rows := [][]Paragraph{}
	header := make([]Paragraph, len(t.Header))
	for i, c := range t.Header {
		header[i] = cell(c, true, t.Align[i])
	}
	rows = append(rows, header)
	for _, r := range t.Rows {
		row := make([]Paragraph, len(r))
		for i, c := range r {
			row[i] = cell(c, false, t.Align[i])
		}
		rows = append(rows, row)
	}

	n := len(t.Header)
	natural := make([]float64, n)
	least := make([]float64, n)
	for _, row := range rows {
		for i, p := range row {
			natural[i] = max(natural[i], runsWidth(p.Runs, size)/fillLimit+2*pad.X+1)
			least[i] = max(least[i], min(longestPiece(p.Runs, size), 12*size)/fillLimit+2*pad.X+1)
		}
	}
	widths := make([]float64, n)
	sumNatural, sumLeast := 0.0, 0.0
	for i := range n {
		sumNatural += natural[i]
		sumLeast += least[i]
	}
	switch {
	case sumNatural <= width:
		for i := range n {
			widths[i] = natural[i] + (width-sumNatural)*natural[i]/sumNatural
		}
	case sumLeast <= width:
		lack := sumNatural - sumLeast
		for i := range n {
			widths[i] = least[i] + (width-sumLeast)*(natural[i]-least[i])/lack
		}
	default:
		return nil, 0, fmt.Errorf("a table is %.0f points too wide", sumLeast-width)
	}

	table := &Table{X: x, Y: y, W: width, Columns: widths, Padding: pad}
	for k, row := range rows {
		lines := 1
		for i, p := range row {
			lines = max(lines, countLines(p.Runs, size, widths[i]-2*pad.X))
		}
		h := float64(lines)*rowLine + 2*pad.Y
		table.Rows = append(table.Rows, TableRow{Height: h, Header: k == 0, Cells: row})
		table.H += h
	}
	return []Element{table}, table.H, nil
}
