package layout

import (
	"fmt"
	"slices"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// Figure draws one step of a scene as a still figure of its own, for a
// page that is not a slide, such as a chapter of the book. It draws the
// diagram as the step leaves it: the events of that step, its main
// event, its nodes in focus. The step does not have to be a checkpoint,
// and the figure does not depend on the steps before it.
//
// A map is drawn whole, as on the pages of the pptx; a ladder from its
// first row to the last row that the step draws, without scrolling. The
// steps are not numbered on the figure: the text beside it is the
// chapter, not the list of the steps of the scene. Under the diagram are
// the fields of the events that the step details, as on its page of the
// pptx: on paper, the values that a step shows have to be there too.
func Figure(m *deck.Model, s *deck.Scene, stepID string) (*FigureShapes, error) {
	k := slices.IndexFunc(s.Steps, func(st deck.Step) bool { return st.ID == stepID })
	if k < 0 {
		return nil, fmt.Errorf("scene %s has no step %s", s.ID, stepID)
	}
	diagram := Draw(m, s)
	if len(diagram.Problems) > 0 {
		return nil, fmt.Errorf("scene %s: %v", s.ID, diagram.Problems)
	}
	for i := range diagram.Events {
		diagram.Events[i].Number = 0
	}
	step := s.Steps[k]
	camera := diagram.Bounds
	if diagram.View == deck.ViewLadder {
		last := 0
		for i, e := range diagram.Events {
			if slices.Contains(step.Events, e.ID) {
				last = i
			}
		}
		head := ladderTop + ladderHeadH + ladderRowH*0.9
		camera.Y = 0
		camera.H = min(head+float64(last+1)*ladderRowH, diagram.Bounds.Y+diagram.Bounds.H)
	}
	f := &FigureShapes{Elements: drawDiagram(diagram, s, step, camera, 1, 0, 0), W: camera.W, H: camera.H}
	if fields := (builder{scale: figureFieldScale}).fields(m, step.Detail); len(fields) > 0 {
		top := camera.H + figureFieldGap
		f.Elements = append(f.Elements, placeFields(fields, 0, top, camera.W)...)
		f.H = top + fieldsHeight(fields, camera.W)
	}
	for _, e := range diagram.Events {
		if e.Kind != deck.EventProcess && slices.Contains(step.Events, e.ID) && !slices.Contains(f.Families, e.Family) {
			f.Families = append(f.Families, e.Family)
		}
	}
	slices.SortFunc(f.Families, func(a, b string) int { return slices.Index(Families, a) - slices.Index(Families, b) })
	return f, nil
}

// The fields under a figure are drawn larger than on a page of the pptx,
// since a figure is scaled down to the width of a column of text.
const (
	figureFieldScale = 1.5
	figureFieldGap   = 20.0
)

// FigureShapes is a still figure: its shapes, its size in diagram units,
// and the families of the lines that it draws, for its legend.
type FigureShapes struct {
	Elements []Element
	W, H     float64
	Families []string
}
