package layout

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// ladder draws a sequence diagram from shapes: a box with the name of
// each participant, a lifeline under it, an arrow for each message with
// its label above it, and a box for each note. The steps of the guide
// go into a column on the right. Every part is a native shape, grouped,
// so that the diagram moves as one in PowerPoint and can be ungrouped
// and edited there.
func (b builder) ladder(l deck.Ladder, x, y, width float64) ([]Element, float64, error) {
	size := b.size(sizeLadder)
	line := lineHeight(size, lineHeightText)
	rowHeight := line + 0.6*size + 4
	headerHeight := line + 8

	remarkWidth := 0.0
	for _, row := range l.Rows {
		if row.Remark != "" {
			remarkWidth = max(remarkWidth, textWidth(row.Remark, size, false, false)/fillLimit+12)
		}
	}
	if remarkWidth > width*0.35 {
		return nil, 0, fmt.Errorf("a remark of the ladder is wider than a third of the slide")
	}
	area := width - remarkWidth
	column := area / float64(len(l.Participants))
	center := func(i int) float64 { return x + column*(float64(i)+0.5) }
	plain := func(text string, color string, bold bool, align Align) Paragraph {
		return Paragraph{Runs: []Run{{Text: text}}, Size: size, LineHeight: line, Color: color, Bold: bold, Align: align}
	}

	group := &Group{X: x, Y: y, W: width}
	bottom := y + headerHeight + 6 + float64(len(l.Rows))*rowHeight + 4
	for i, name := range l.Participants {
		// The boxes are as wide as each other where the columns allow,
		// and as wide as their names otherwise.
		w := max(textWidth(name, size, false, true)/fillLimit+16, min(column*0.7, 7*size))
		if w > column-4 {
			return nil, 0, fmt.Errorf("the participant %q is wider than its column of the ladder", name)
		}
		group.Elements = append(group.Elements,
			&Line{X1: center(i), Y1: y + headerHeight, X2: center(i), Y2: bottom, Color: ColorLifeline, Width: 1},
			&TextBox{X: center(i) - w/2, Y: y, W: w, H: headerHeight, Anchor: AnchorMiddle, Fill: ColorHeaderFill, Line: ColorAccent,
				Paragraphs: []Paragraph{plain(name, ColorAccent, true, AlignCenter)}})
	}

	for k, row := range l.Rows {
		top := y + headerHeight + 6 + float64(k)*rowHeight
		arrowY := top + rowHeight - 0.3*size - 2
		labelWidth := textWidth(row.Label, size, false, false)/fillLimit + 8
		if labelWidth > area {
			return nil, 0, fmt.Errorf("the label %q is wider than the ladder", row.Label)
		}
		if row.Note {
			left := center(row.Path[0]) - column/2 + 4
			right := center(row.Path[1]) + column/2 - 4
			if labelWidth > right-left {
				return nil, 0, fmt.Errorf("the note %q is wider than the participants it spans", row.Label)
			}
			group.Elements = append(group.Elements, &TextBox{X: left, Y: top + 2, W: right - left, H: rowHeight - 4, Anchor: AnchorMiddle,
				Fill: ColorNoteFill, Line: ColorNoteLine, Paragraphs: []Paragraph{plain(row.Label, ColorText, false, AlignCenter)}})
		} else {
			for s := 1; s < len(row.Path); s++ {
				group.Elements = append(group.Elements, &Line{
					X1: center(row.Path[s-1]), Y1: arrowY, X2: center(row.Path[s]), Y2: arrowY,
					Color: ColorText, Width: 1.25, Dash: row.Dashed, HeadEnd: true, HeadStart: row.Both,
				})
			}
			// The label sits above the arrow, in the middle of the way
			// the message goes, and inside the diagram.
			from, to := center(row.Path[0]), center(row.Path[len(row.Path)-1])
			middle := (from + to) / 2
			left := min(max(middle-labelWidth/2, x), x+area-labelWidth)
			group.Elements = append(group.Elements, &TextBox{X: left, Y: arrowY - line - 1, W: labelWidth, H: line, Anchor: AnchorBottom,
				Fill: ColorWhite, Paragraphs: []Paragraph{plain(row.Label, ColorText, false, AlignCenter)}})
		}
		if row.Remark != "" {
			group.Elements = append(group.Elements, &TextBox{X: x + area + 8, Y: arrowY - line, W: remarkWidth - 8, H: line, Anchor: AnchorBottom,
				Paragraphs: []Paragraph{plain(row.Remark, ColorMuted, false, AlignLeft)}})
		}
	}
	group.H = bottom - y
	return []Element{group}, group.H, nil
}

// LadderShapes places a ladder diagram at the top left of a room of the
// width, at the full size of its text, for a writer that draws it
// outside a slide, such as the reading view of the HTML.
func LadderShapes(l deck.Ladder, width float64) ([]Element, float64, error) {
	return builder{scale: 1}.ladder(l, 0, 0, width)
}
