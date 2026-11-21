package deck

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Error is a mistake in a deck source, with the line where it is.
type Error struct {
	Path string
	Line int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Msg) }

// ReadFile reads and parses a deck source.
func ReadFile(path string) (*Deck, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, string(source))
}

// The languages a deck may be written in. The writers pick the fonts and
// the words of the layouts, such as "Answers", by the language.
var languages = []string{"en", "ja"}

var (
	layoutLine   = regexp.MustCompile(`^\[layout: ([a-z-]+)\]$`)
	columnLine   = "[column]"
	listMarker   = regexp.MustCompile(`^( *)([-*]|[0-9]+\.) +(.*)$`)
	fenceOpening = regexp.MustCompile("^(```+|~~~+) *([A-Za-z0-9_-]*) *$")
)

// line is a line of the source with its number, counted from 1.
type line struct {
	number int
	text   string
}

// Parse parses the source of a deck. path is used in error messages.
func Parse(path, source string) (*Deck, error) {
	p := parser{path: path}
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	numbered := make([]line, len(lines))
	for i, text := range lines {
		numbered[i] = line{number: i + 1, text: strings.TrimRight(text, " \t")}
	}
	d := &Deck{Path: path}
	body, err := p.frontMatter(d, numbered)
	if err != nil {
		return nil, err
	}
	for i, slideLines := range p.splitSlides(body) {
		slide, err := p.slide(i+1, slideLines)
		if err != nil {
			return nil, err
		}
		d.Slides = append(d.Slides, slide)
	}
	return d, nil
}

type parser struct {
	path string
}

func (p *parser) errorf(number int, format string, args ...any) error {
	return &Error{Path: p.path, Line: number, Msg: fmt.Sprintf(format, args...)}
}

// frontMatter reads the lines between the first two "---" lines. Every
// key is required, and no other key is allowed, so that a mistyped key
// does not pass unnoticed.
func (p *parser) frontMatter(d *Deck, lines []line) ([]line, error) {
	if len(lines) == 0 || lines[0].text != "---" {
		return nil, p.errorf(1, `the deck must start with a front matter between two "---" lines`)
	}
	seen := map[string]bool{}
	for i := 1; i < len(lines); i++ {
		l := lines[i]
		if l.text == "---" {
			for _, key := range []string{"title", "footer", "lang"} {
				if !seen[key] {
					return nil, p.errorf(l.number, "the front matter has no %q", key)
				}
			}
			return lines[i+1:], nil
		}
		key, value, ok := strings.Cut(l.text, ":")
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return nil, p.errorf(l.number, `a front matter line is "key: value"`)
		}
		switch key {
		case "title":
			d.Title = value
		case "footer":
			d.Footer = value
		case "lang":
			if !slices.Contains(languages, value) {
				return nil, p.errorf(l.number, "lang is one of %s", strings.Join(languages, ", "))
			}
			d.Lang = value
		default:
			return nil, p.errorf(l.number, "unknown front matter key %q (title, footer and lang are known)", key)
		}
		seen[key] = true
	}
	return nil, p.errorf(lines[0].number, `the front matter has no closing "---"`)
}

// splitSlides cuts the body at the "---" lines outside fenced blocks.
func (p *parser) splitSlides(body []line) [][]line {
	var slides [][]line
	var current []line
	fence := ""
	for _, l := range body {
		if fence == "" && l.text == "---" {
			slides = append(slides, current)
			current = nil
			continue
		}
		fence = nextFence(fence, l.text)
		current = append(current, l)
	}
	return append(slides, current)
}

// nextFence returns the fence that is open after the line: the opening
// fence while inside a fenced block, "" outside one.
func nextFence(open, text string) string {
	if open == "" {
		if m := fenceOpening.FindStringSubmatch(text); m != nil {
			return m[1]
		}
		return ""
	}
	if strings.HasPrefix(text, open) && strings.TrimLeft(text, open[:1]) == "" {
		return ""
	}
	return open
}

func (p *parser) slide(number int, lines []line) (Slide, error) {
	first := 0
	if len(lines) > 0 {
		first = lines[0].number
	}
	s := Slide{Number: number, Line: first}
	if err := p.rejectComments(lines); err != nil {
		return s, err
	}
	lines = trimBlank(lines)
	if len(lines) == 0 {
		return s, p.errorf(first, "slide %d is empty", number)
	}
	s.Line = lines[0].number

	hint := Layout("")
	if m := layoutLine.FindStringSubmatch(lines[0].text); m != nil {
		hint = Layout(m[1])
		if !slices.Contains(Layouts, hint) {
			return s, p.errorf(lines[0].number, "unknown layout %q", m[1])
		}
		lines = trimBlank(lines[1:])
		if len(lines) == 0 {
			return s, p.errorf(s.Line, "slide %d has a layout and nothing else", number)
		}
	}

	heading := lines[0]
	level := 0
	switch {
	case strings.HasPrefix(heading.text, "# "):
		level = 1
	case strings.HasPrefix(heading.text, "## "):
		level = 2
	default:
		return s, p.errorf(heading.number, `a slide starts with its heading: "# " for the title and the section slides, "## " for the others`)
	}
	title, err := parseInline(strings.TrimSpace(heading.text[level+1:]))
	if err != nil {
		return s, p.errorf(heading.number, "%v", err)
	}
	s.Title = title

	// The heading decides the title and the section slides: the first
	// slide with "#" is the title slide, any later one opens a section.
	// The hint picks among the other layouts.
	switch {
	case level == 1 && number == 1:
		s.Layout = LayoutTitle
	case level == 1:
		s.Layout = LayoutSection
	case number == 1:
		return s, p.errorf(heading.number, `the first slide is the title slide, with a heading of one "#"`)
	case hint == LayoutTitle || hint == LayoutSection:
		return s, p.errorf(heading.number, `layout %s takes a heading with one "#"`, hint)
	case hint != "":
		s.Layout = hint
	default:
		s.Layout = LayoutContent
	}
	if hint != "" && hint != s.Layout {
		return s, p.errorf(heading.number, `a heading with one "#" makes a %s slide; layout %s takes "##"`, s.Layout, hint)
	}

	columns := [][]line{nil}
	for _, l := range lines[1:] {
		if l.text == columnLine {
			columns = append(columns, nil)
			continue
		}
		columns[len(columns)-1] = append(columns[len(columns)-1], l)
	}
	if s.Layout == LayoutTwoColumns && len(columns) != 2 {
		return s, p.errorf(heading.number, "a two-columns slide has one %q line between its two columns", columnLine)
	}
	if s.Layout != LayoutTwoColumns && len(columns) != 1 {
		return s, p.errorf(heading.number, "%q is only for the two-columns layout", columnLine)
	}
	for _, column := range columns {
		blocks, err := p.blocks(column)
		if err != nil {
			return s, err
		}
		if s.Layout == LayoutTitle || s.Layout == LayoutSection {
			for _, b := range blocks {
				if _, ok := b.(Paragraph); !ok {
					return s, p.errorf(heading.number, "a %s slide holds only its heading and paragraphs", s.Layout)
				}
			}
		}
		s.Columns = append(s.Columns, blocks)
	}
	return s, nil
}

// rejectComments fails on an HTML comment outside a fenced block. Such a
// comment would be a speaker note or a hidden directive, and the decks
// carry neither: everything a reader needs is on the slides.
func (p *parser) rejectComments(lines []line) error {
	fence := ""
	for _, l := range lines {
		before := fence
		fence = nextFence(fence, l.text)
		if before != "" || fence != "" {
			continue
		}
		if strings.Contains(stripCodeSpans(l.text), "<!--") {
			return p.errorf(l.number, "an HTML comment: the decks have no speaker notes; put the text on a slide or in the README of the decks")
		}
	}
	return nil
}

// stripCodeSpans removes the inline code of a line, where "<!--" is text.
func stripCodeSpans(text string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(text, '`')
		if start < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:start])
		end := strings.IndexByte(text[start+1:], '`')
		if end < 0 {
			return b.String()
		}
		text = text[start+1+end+1:]
	}
}

func trimBlank(lines []line) []line {
	for len(lines) > 0 && lines[0].text == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1].text == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// blocks parses the body of a slide or of one column.
func (p *parser) blocks(lines []line) ([]Block, error) {
	var blocks []Block
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case l.text == "":
			i++
		case fenceOpening.MatchString(l.text):
			block, next, err := p.fenced(lines, i)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
			i = next
		case strings.HasPrefix(l.text, "|"):
			block, next, err := p.table(lines, i)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
			i = next
		case strings.HasPrefix(l.text, ">"):
			block, next, err := p.quote(lines, i)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
			i = next
		case listMarker.MatchString(l.text):
			if strings.HasPrefix(l.text, " ") {
				return nil, p.errorf(l.number, "a list starts without indentation")
			}
			block, next, err := p.list(lines, i)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
			i = next
		case strings.HasPrefix(l.text, "#"):
			return nil, p.errorf(l.number, "a slide has one heading, at its top")
		default:
			block, next, err := p.paragraph(lines, i)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
			i = next
		}
	}
	return blocks, nil
}

func (p *parser) fenced(lines []line, i int) (Block, int, error) {
	open := lines[i]
	m := fenceOpening.FindStringSubmatch(open.text)
	fence, info := m[1], m[2]
	var body []string
	for j := i + 1; j < len(lines); j++ {
		if nextFence(fence, lines[j].text) == "" {
			if info == "ladder" {
				ladder, err := p.parseLadder(body, open.number+1)
				return ladder, j + 1, err
			}
			return Code{Info: info, Lines: body}, j + 1, nil
		}
		body = append(body, strings.ReplaceAll(lines[j].text, "\t", "    "))
	}
	return nil, 0, p.errorf(open.number, "the fenced block is not closed")
}

func (p *parser) table(lines []line, i int) (Block, int, error) {
	if i+1 >= len(lines) {
		return nil, 0, p.errorf(lines[i].number, "a table needs a header row and a delimiter row")
	}
	header := splitRow(lines[i].text)
	delimiter := splitRow(lines[i+1].text)
	if len(delimiter) != len(header) {
		return nil, 0, p.errorf(lines[i+1].number, "the delimiter row has %d cells, the header %d", len(delimiter), len(header))
	}
	t := Table{}
	for _, d := range delimiter {
		d = strings.TrimSpace(d)
		if strings.Trim(d, ":-") != "" || !strings.Contains(d, "-") {
			return nil, 0, p.errorf(lines[i+1].number, `a delimiter cell is "---", ":---", ":---:" or "---:"`)
		}
		switch {
		case strings.HasPrefix(d, ":") && strings.HasSuffix(d, ":"):
			t.Align = append(t.Align, AlignCenter)
		case strings.HasSuffix(d, ":"):
			t.Align = append(t.Align, AlignRight)
		default:
			t.Align = append(t.Align, AlignLeft)
		}
	}
	cells := func(l line) ([]Cell, error) {
		raw := splitRow(l.text)
		if len(raw) != len(header) {
			return nil, p.errorf(l.number, "the row has %d cells, the header %d", len(raw), len(header))
		}
		row := make([]Cell, len(raw))
		for k, text := range raw {
			spans, err := parseInline(strings.ReplaceAll(strings.TrimSpace(text), `\|`, "|"))
			if err != nil {
				return nil, p.errorf(l.number, "%v", err)
			}
			row[k] = spans
		}
		return row, nil
	}
	var err error
	if t.Header, err = cells(lines[i]); err != nil {
		return nil, 0, err
	}
	j := i + 2
	for ; j < len(lines) && strings.HasPrefix(lines[j].text, "|"); j++ {
		row, err := cells(lines[j])
		if err != nil {
			return nil, 0, err
		}
		t.Rows = append(t.Rows, row)
	}
	if j < len(lines) && lines[j].text != "" {
		return nil, 0, p.errorf(lines[j].number, "a blank line ends a table")
	}
	return t, j, nil
}

// splitRow cuts a table row at the "|" that are not escaped as "\|".
// The escape stays in the cell; it means "|" there, inline code included,
// as in the tables of GitHub.
func splitRow(text string) []string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "|")
	if strings.HasSuffix(text, "|") && !strings.HasSuffix(text, `\|`) {
		text = text[:len(text)-1]
	}
	var cells []string
	var b strings.Builder
	for k := 0; k < len(text); k++ {
		switch {
		case text[k] == '\\' && k+1 < len(text) && text[k+1] == '|':
			b.WriteString(`\|`)
			k++
		case text[k] == '|':
			cells = append(cells, b.String())
			b.Reset()
		default:
			b.WriteByte(text[k])
		}
	}
	return append(cells, b.String())
}

func (p *parser) quote(lines []line, i int) (Block, int, error) {
	q := Quote{}
	j := i
	for ; j < len(lines) && strings.HasPrefix(lines[j].text, ">"); j++ {
		spans, err := parseInline(strings.TrimSpace(strings.TrimPrefix(lines[j].text, ">")))
		if err != nil {
			return nil, 0, p.errorf(lines[j].number, "%v", err)
		}
		q.Lines = append(q.Lines, spans)
	}
	if j < len(lines) && lines[j].text != "" {
		return nil, 0, p.errorf(lines[j].number, "a blank line ends a block quote")
	}
	return q, j, nil
}

// list parses a list with at most one level of nesting. A nested item is
// indented by two or more spaces; a line that is indented and has no
// marker continues the item right above it.
func (p *parser) list(lines []line, i int) (Block, int, error) {
	top := &List{}
	j := i
	for ; j < len(lines) && lines[j].text != ""; j++ {
		l := lines[j]
		m := listMarker.FindStringSubmatch(l.text)
		indent := len(l.text) - len(strings.TrimLeft(l.text, " "))
		switch {
		case m != nil && indent == 0:
			if err := p.addItem(top, m, l); err != nil {
				return nil, 0, err
			}
		case m != nil:
			if len(top.Items) == 0 {
				return nil, 0, p.errorf(l.number, "a nested item needs an item above it")
			}
			parent := &top.Items[len(top.Items)-1]
			if parent.Children == nil {
				parent.Children = &List{}
			}
			if err := p.addItem(parent.Children, m, l); err != nil {
				return nil, 0, err
			}
		case indent > 0:
			spans, err := parseInline(strings.TrimSpace(l.text))
			if err != nil {
				return nil, 0, p.errorf(l.number, "%v", err)
			}
			// The line continues the item above it, nested or not.
			item := &top.Items[len(top.Items)-1]
			if item.Children != nil {
				item = &item.Children.Items[len(item.Children.Items)-1]
			}
			item.Lines = append(item.Lines, spans)
		default:
			return nil, 0, p.errorf(l.number, "a blank line ends a list")
		}
	}
	return *top, j, nil
}

func (p *parser) addItem(list *List, m []string, l line) error {
	ordered := m[2] != "-" && m[2] != "*"
	if len(list.Items) == 0 {
		list.Ordered = ordered
		if ordered {
			list.Start, _ = strconv.Atoi(strings.TrimSuffix(m[2], "."))
		}
	} else if list.Ordered != ordered {
		return p.errorf(l.number, "a list is either bulleted or numbered")
	}
	spans, err := parseInline(m[3])
	if err != nil {
		return p.errorf(l.number, "%v", err)
	}
	list.Items = append(list.Items, Item{Lines: [][]Span{spans}})
	return nil
}

func (p *parser) paragraph(lines []line, i int) (Block, int, error) {
	para := Paragraph{}
	j := i
	for ; j < len(lines); j++ {
		l := lines[j]
		if l.text == "" || fenceOpening.MatchString(l.text) || strings.HasPrefix(l.text, "|") ||
			strings.HasPrefix(l.text, ">") || listMarker.MatchString(l.text) || strings.HasPrefix(l.text, "#") {
			break
		}
		spans, err := parseInline(strings.TrimSpace(l.text))
		if err != nil {
			return nil, 0, p.errorf(l.number, "%v", err)
		}
		para.Lines = append(para.Lines, spans)
	}
	return para, j, nil
}
