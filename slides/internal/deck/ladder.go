package deck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Ladder is a sequence diagram, written in a fenced block with the info
// word "ladder":
//
//	UE, P-CSCF, S-CSCF, HSS
//	UE -> P-CSCF -> S-CSCF: REGISTER // 1 - 3
//	S-CSCF <-> HSS: MAR, MAA
//	UE <-- P-CSCF <-- S-CSCF: 401
//	note UE, HSS: the UE is registered
//
// The first line names the participants, from left to right. Each other
// line is a row: a message, or a note across some of the participants.
// A message passes the participants in the order written: "->" is a
// request, "-->" a response, drawn dashed, and "<-" and "<--" are the
// same from right to left; "<->" is a request and its answer between two
// participants, such as a Diameter pair. The text after "//" goes to the
// right of the diagram, for the numbers of the steps in the guide.
type Ladder struct {
	Participants []string
	Rows         []LadderRow
}

// LadderRow is a message or a note. For a message, Path holds the
// participants it passes, in order; for a note, the first and the last
// participant it spans.
type LadderRow struct {
	Note   bool
	Path   []int
	Dashed bool
	Both   bool
	Label  string
	Remark string
}

func (Ladder) block() {}

var arrow = regexp.MustCompile(`\s*(<->|<--|-->|<-|->)\s*`)

// parseLadder reads the lines of a ladder block. first is the number of
// the first line in the source, for the messages.
func (p *parser) parseLadder(lines []string, first int) (Ladder, error) {
	l := Ladder{}
	index := map[string]int{}
	for k, text := range lines {
		number := first + k
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if l.Participants == nil {
			for _, name := range strings.Split(text, ",") {
				name = strings.TrimSpace(name)
				if name == "" || index[name] > 0 {
					return l, p.errorf(number, "the participants are names separated by commas, each once")
				}
				l.Participants = append(l.Participants, name)
				index[name] = len(l.Participants)
			}
			if len(l.Participants) < 2 {
				return l, p.errorf(number, "a ladder has two participants or more")
			}
			continue
		}
		row, err := parseRow(text, index)
		if err != nil {
			return l, p.errorf(number, "%v", err)
		}
		l.Rows = append(l.Rows, row)
	}
	if len(l.Rows) == 0 {
		return l, p.errorf(first, "a ladder has its participants on the first line and at least one row")
	}
	return l, nil
}

func parseRow(text string, index map[string]int) (LadderRow, error) {
	row := LadderRow{}
	head, label, ok := strings.Cut(text, ":")
	if !ok {
		return row, fmt.Errorf(`a row is "A -> B: label" or "note A, B: text"`)
	}
	label, row.Remark, _ = strings.Cut(label, " // ")
	row.Label = strings.TrimSpace(label)
	row.Remark = strings.TrimSpace(row.Remark)
	participant := func(name string) (int, error) {
		i := index[strings.TrimSpace(name)]
		if i == 0 {
			return 0, fmt.Errorf("%q is not a participant", strings.TrimSpace(name))
		}
		return i - 1, nil
	}

	if rest, isNote := strings.CutPrefix(head, "note "); isNote {
		row.Note = true
		names := strings.Split(rest, ",")
		if len(names) > 2 {
			return row, fmt.Errorf("a note spans from one participant to another: note A, B: text")
		}
		for _, name := range names {
			i, err := participant(name)
			if err != nil {
				return row, err
			}
			row.Path = append(row.Path, i)
		}
		if len(row.Path) == 1 {
			row.Path = append(row.Path, row.Path[0])
		}
		if row.Path[0] > row.Path[1] {
			row.Path[0], row.Path[1] = row.Path[1], row.Path[0]
		}
		return row, nil
	}

	names := arrow.Split(head, -1)
	arrows := arrow.FindAllStringSubmatch(head, -1)
	if len(arrows) == 0 {
		return row, fmt.Errorf(`a message has an arrow: "->", "-->", "<-", "<--" or "<->"`)
	}
	kind := arrows[0][1]
	for _, a := range arrows {
		if a[1] != kind {
			return row, fmt.Errorf("the arrows of one row are all of one kind")
		}
	}
	for _, name := range names {
		i, err := participant(name)
		if err != nil {
			return row, err
		}
		row.Path = append(row.Path, i)
	}
	row.Dashed = strings.Contains(kind, "--")
	row.Both = kind == "<->"
	if strings.HasPrefix(kind, "<") && !row.Both {
		slices.Reverse(row.Path)
	}
	if row.Both && len(row.Path) != 2 {
		return row, fmt.Errorf(`"<->" joins two participants`)
	}
	for k := 1; k < len(row.Path); k++ {
		if row.Path[k] == row.Path[k-1] {
			return row, fmt.Errorf("a message goes from one participant to another")
		}
		// The arrows of a row lie on one line, so a message that turns
		// back would be drawn over itself.
		if k > 1 && (row.Path[k] > row.Path[k-1]) != (row.Path[k-1] > row.Path[k-2]) {
			return row, fmt.Errorf("a message that turns back takes a row for each direction")
		}
	}
	return row, nil
}
