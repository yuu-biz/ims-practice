package layout

import "github.com/yuu-biz/ims-practice/slides/internal/deck"

// Page is one slide with everything on it placed. Elements are drawn in
// order, the later ones on top.
type Page struct {
	Number     int
	Layout     deck.Layout
	Background string
	// Scale is the share of the full text size that the content of the
	// slide got to fit, 1 when nothing was shrunk.
	Scale    float64
	Elements []Element
}

// Element is one of *TextBox, *Table, *Line, *Rect and *Group.
type Element interface{ element() }

// Role says what a text box is for. The pptx writer makes the title, the
// subtitle, the footer and the slide number placeholders of the slide
// layout, so that PowerPoint shows the titles in its outline and the
// footer in its header and footer settings.
type Role int

const (
	RoleBody Role = iota
	RoleTitle
	RoleSubtitle
	RoleFooter
	RoleNumber
)

// Run is text of one style. Break ends the line without ending the
// paragraph.
type Run struct {
	Text  string
	Bold  bool
	Code  bool
	Href  string
	Break bool
}

// Paragraph is a paragraph of a text box or of a table cell. The line
// height is exact, so that a line takes the same room in every font and
// in both outputs.
type Paragraph struct {
	Runs        []Run
	Size        float64
	LineHeight  float64
	SpaceBefore float64
	// Indent is the left edge of the text. A Marker, such as a bullet
	// or "3.", hangs in the room to the left of it, from MarkerAt. An
	// item of a numbered list has ListStart, the number of the first
	// item of its list, so that the pptx can number it as PowerPoint
	// numbers a list.
	Indent    float64
	MarkerAt  float64
	Marker    string
	ListStart int
	Align     Align
	Color     string
	Bold      bool
	Mono      bool
}

// Align is the horizontal alignment of a paragraph.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// Anchor is where the text sits in its box, vertically.
type Anchor int

const (
	AnchorTop Anchor = iota
	AnchorMiddle
	AnchorBottom
)

// TextBox is a box of paragraphs. Inset is the room between the edge of
// the box and its text, on every side. NoWrap keeps each paragraph on
// one line, for code.
type TextBox struct {
	Role       Role
	X, Y, W, H float64
	Inset      float64
	Fill, Line string
	Anchor     Anchor
	NoWrap     bool
	Paragraphs []Paragraph
}

// Table is a table with its column widths and row heights decided.
type Table struct {
	X, Y, W, H float64
	Columns    []float64
	Rows       []TableRow
	Padding    Padding
}

// Padding is the room inside a table cell.
type Padding struct{ X, Y float64 }

// TableRow is one row. The first row of a table is its header.
type TableRow struct {
	Height float64
	Header bool
	Cells  []Paragraph
}

// Line is a straight line, with an arrowhead at either end if asked.
type Line struct {
	X1, Y1, X2, Y2 float64
	Color          string
	Width          float64
	Dash           bool
	HeadStart      bool
	HeadEnd        bool
}

// Rect is a filled rectangle, such as a band or a panel. Round gives it
// rounded corners.
type Rect struct {
	X, Y, W, H float64
	Fill, Line string
	Round      bool
}

// Group holds elements that belong together, such as the shapes of a
// ladder diagram. The box is the one around all of them.
type Group struct {
	X, Y, W, H float64
	Elements   []Element
}

func (*TextBox) element() {}
func (*Group) element()   {}
func (*Table) element()   {}
func (*Line) element()    {}
func (*Rect) element()    {}
