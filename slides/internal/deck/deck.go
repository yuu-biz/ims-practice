// Package deck reads the source of a study-session deck: a Markdown-like
// text file with a front matter, slides separated by "---", and a small
// set of block types. The format is described in slides/README.md.
//
// The source holds everything that is shown. There are no speaker notes
// and no HTML comments: what a presenter or a learner needs is on a
// slide or in the README of the decks.
package deck

// Deck is one source file: one study session in one language.
type Deck struct {
	Path   string
	Title  string
	Footer string
	Lang   string
	// Format is "" for a deck of slides only, FormatVisual for a deck
	// with a model and scenes.
	Format string
	Model  *Model
	Slides []Slide
}

// Layout names the arrangement of a slide. Both writers draw a layout
// the same way, from the boxes that package layout computes.
type Layout string

const (
	LayoutTitle      Layout = "title"
	LayoutSection    Layout = "section"
	LayoutContent    Layout = "content"
	LayoutTwoColumns Layout = "two-columns"
	LayoutSummary    Layout = "summary"
	// LayoutScene is a slide with a scene. It is not named in a layout
	// line: a scene block at the top of the body makes it.
	LayoutScene Layout = "scene"
)

// Layouts lists the layouts that a "[layout: ...]" line may name.
var Layouts = []Layout{
	LayoutTitle, LayoutSection, LayoutContent, LayoutTwoColumns,
	LayoutSummary,
}

// Slide is one slide. Columns has two entries for the two-columns layout
// and one for every other layout.
type Slide struct {
	Number  int
	Line    int
	Layout  Layout
	Title   []Span
	Columns [][]Block
	// Scene is the diagram of the slide, in a visual deck. Columns then
	// hold the longer explanation of the scene.
	Scene *Scene
}

// Span is a run of text with one style. Href is set for a link.
type Span struct {
	Text string
	Bold bool
	Code bool
	Href string
}

// Block is one of Paragraph, List, Table, Code, Quote and Ladder.
type Block interface{ block() }

// Paragraph keeps the lines of the source: a line break in the source
// is a line break on the slide.
type Paragraph struct {
	Lines [][]Span
}

// List is a bulleted or a numbered list. An item may hold one nested
// list.
type List struct {
	Ordered bool
	Start   int
	Items   []Item
}

// Item is one entry of a list. A continuation line in the source is a
// line break in the item.
type Item struct {
	Lines    [][]Span
	Children *List
}

// Align is the alignment of a table column.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// Table is a table with one header row.
type Table struct {
	Header []Cell
	Align  []Align
	Rows   [][]Cell
}

// Cell is the text of one table cell.
type Cell []Span

// Code is a fenced block shown in a monospace font, line for line.
// Info is the word after the opening fence, such as "go" or "sh". A
// fenced block with the info word "ladder" is a Ladder instead.
type Code struct {
	Info  string
	Lines []string
}

// Quote is a block quote. The decks use it to point to the guide and to
// the specifications.
type Quote struct {
	Lines [][]Span
}

func (Paragraph) block() {}
func (List) block()      {}
func (Table) block()     {}
func (Code) block()      {}
func (Quote) block()     {}

// PlainText joins the text of spans without their styles.
func PlainText(spans []Span) string {
	text := ""
	for _, span := range spans {
		text += span.Text
	}
	return text
}
