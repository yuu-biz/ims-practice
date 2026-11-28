package layout

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

func TestCountLines(t *testing.T) {
	tests := []struct {
		name  string
		runs  []Run
		width float64
		want  int
	}{
		// "aaaa " is 4 * 0.556 + 0.278 = 2.5 em, 50 points at 20 points.
		{"fits", []Run{{Text: "aaaa aaaa"}}, 100, 1},
		{"breaks at the space", []Run{{Text: "aaaa aaaa"}}, 80, 2},
		{"explicit break", []Run{{Text: "a"}, {Break: true}, {Text: "b"}}, 500, 2},
		// Ten kanji are 200 points at 20 points; a line of 100 holds 4,
		// because a line is filled to 97% of its box.
		{"kanji break anywhere", []Run{{Text: "漢字漢字漢字漢字漢字"}}, 100, 3},
		{"a long word is cut", []Run{{Text: strings.Repeat("m", 30), Code: true}}, 100, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countLines(tt.runs, 20, tt.width); got != tt.want {
				t.Errorf("countLines = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNoLineStartStaysWithTheCharacterBefore(t *testing.T) {
	// "字。" may not break before "。", so the two are one piece.
	ps := pieces([]Run{{Text: "漢字。次"}}, 10)
	if len(ps) != 3 || ps[1].width != 20 {
		t.Errorf("pieces = %+v, want 漢, 字。 and 次", ps)
	}
	ps = pieces([]Run{{Text: "「字」"}}, 10)
	if len(ps) != 1 {
		t.Errorf("pieces = %+v, want one piece", ps)
	}
}

func TestJapanesePunctuationStaysWithTheLatinWord(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"メソッド、Release", 3}, // メ, ソッ, ド、Release
		{"P-CSCF、UE、S-CSCF", 1},
		{"AAR（#786）", 1},
		{"字 UE", 3}, // a space breaks
		{"字UE", 2},  // a Latin word after a kanji may begin a line
	}
	for _, tt := range tests {
		var words []float64
		for _, p := range pieces([]Run{{Text: tt.text}}, 10) {
			if !p.space {
				words = append(words, p.width)
			}
		}
		if got := len(words) + strings.Count(tt.text, " "); got != tt.want {
			t.Errorf("%q: %d pieces, want %d", tt.text, got, tt.want)
		}
	}
}

func parse(t *testing.T, body string) *deck.Deck {
	t.Helper()
	d, err := deck.Parse("test.md", "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\nsub\n\n---\n\n"+body)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestContentFitsInsideTheFrame(t *testing.T) {
	d := parse(t, "[layout: summary]\n\n## Questions\n\n1. One?\n2. Two?\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n```\ncode\n```\n\n> Guide: x\n")
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, e := range pages[1].Elements {
		x, y, w, h := bounds(e)
		if x < 0 || y < 0 || x+w > SlideWidth+0.01 || y+h > SlideHeight+0.01 {
			t.Errorf("%T at %.0f,%.0f %.0fx%.0f leaves the slide", e, x, y, w, h)
		}
		if box, ok := e.(*TextBox); ok && box.Role == RoleBody && y+h > BodyBottom {
			t.Errorf("a text box ends at %.0f, below the body", y+h)
		}
	}
}

func bounds(e Element) (x, y, w, h float64) {
	switch e := e.(type) {
	case *TextBox:
		return e.X, e.Y, e.W, e.H
	case *Table:
		return e.X, e.Y, e.W, e.H
	case *Rect:
		return e.X, e.Y, e.W, e.H
	case *Group:
		return e.X, e.Y, e.W, e.H
	case *Line:
		return math.Min(e.X1, e.X2), math.Min(e.Y1, e.Y2), math.Abs(e.X2 - e.X1), math.Abs(e.Y2 - e.Y1)
	}
	return 0, 0, 0, 0
}

func TestTooTallFails(t *testing.T) {
	d := parse(t, "## Long\n\n"+strings.Repeat("- An item of the list.\n", 30))
	_, problems := Deck(d)
	var overflow *OverflowError
	if len(problems) != 1 || !errors.As(problems[0], &overflow) || overflow.Slide != 2 {
		t.Fatalf("problems = %v, want slide 2 overflowing", problems)
	}
}

func TestSlideShrinksBeforeItFails(t *testing.T) {
	// Fifteen items do not fit at the full size, but at a smaller one.
	d := parse(t, "## Long\n\n"+strings.Repeat("- An item of the list.\n", 15))
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var sizes []float64
	for _, e := range pages[1].Elements {
		if box, ok := e.(*TextBox); ok && box.Role == RoleBody && len(box.Paragraphs) > 1 {
			sizes = append(sizes, box.Paragraphs[0].Size)
		}
	}
	if len(sizes) != 1 || sizes[0] >= SizeBody {
		t.Errorf("sizes of the list = %v, want one smaller than %v", sizes, SizeBody)
	}
}

func TestWideCodeGetsSmaller(t *testing.T) {
	d := parse(t, "## Code\n\n```\n"+strings.Repeat("x", 120)+"\n```\n")
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, e := range pages[1].Elements {
		if box, ok := e.(*TextBox); ok && box.NoWrap {
			size := box.Paragraphs[0].Size
			if size >= sizeCode || 120*monoWidth*size > box.W-2*box.Inset {
				t.Errorf("code at %v points in a box of %v", size, box.W)
			}
			return
		}
	}
	t.Error("no code box")
}

func TestTooWideCodeFails(t *testing.T) {
	d := parse(t, "## Code\n\n```\n"+strings.Repeat("x", 300)+"\n```\n")
	if _, problems := Deck(d); len(problems) != 1 {
		t.Errorf("problems = %v, want one", problems)
	}
}

func TestTableTakesTheWholeWidth(t *testing.T) {
	d := parse(t, "## Table\n\n| Node | What it does |\n| --- | --- |\n| P-CSCF | "+strings.Repeat("Many words in a long cell. ", 8)+"|\n")
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, e := range pages[1].Elements {
		if table, ok := e.(*Table); ok {
			sum := 0.0
			for _, w := range table.Columns {
				sum += w
			}
			if math.Abs(sum-table.W) > 0.01 || table.W != SlideWidth-2*MarginX {
				t.Errorf("columns sum to %v in a table of %v", sum, table.W)
			}
			if table.Rows[1].Height <= table.Rows[0].Height {
				t.Errorf("the long row is %v high, the header %v", table.Rows[1].Height, table.Rows[0].Height)
			}
			return
		}
	}
	t.Error("no table")
}

// Text returns the text of a page.
func Text(page Page) string {
	var b strings.Builder
	for _, e := range page.Elements {
		switch e := e.(type) {
		case *TextBox:
			for _, p := range e.Paragraphs {
				for _, r := range p.Runs {
					b.WriteString(r.Text)
				}
				b.WriteByte('\n')
			}
		case *Table:
			for _, row := range e.Rows {
				for _, p := range row.Cells {
					for _, r := range p.Runs {
						b.WriteString(r.Text)
					}
					b.WriteByte('\t')
				}
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}
