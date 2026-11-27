package layout

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// A Diagram is a scene with every shape placed: the boxes of its nodes,
// the frames of its groups, a line or a box for each event, and for each
// step the part of the diagram that the view shows. The HTML draws it
// as an SVG and moves the view from step to step; the pptx draws the
// shapes of a step as native shapes. Both read the same positions, so a
// node stays where it is from one step, one scene and one output to the
// next.
//
// The unit is the diagram unit. The writers scale it to their room.
type Diagram struct {
	View    deck.View
	Bounds  Box
	Nodes   []NodeShape
	Groups  []GroupShape
	Events  []EventShape
	Lines   []LifelineShape
	Cameras []Box
}

// Box is a rectangle of a diagram.
type Box struct{ X, Y, W, H float64 }

// Center is the middle of the box.
func (b Box) Center() (float64, float64) { return b.X + b.W/2, b.Y + b.H/2 }

func (b Box) union(o Box) Box {
	if b.W == 0 && b.H == 0 {
		return o
	}
	x, y := min(b.X, o.X), min(b.Y, o.Y)
	return Box{x, y, max(b.X+b.W, o.X+o.W) - x, max(b.Y+b.H, o.Y+o.H) - y}
}

func (b Box) grow(dx, dy float64) Box { return Box{b.X - dx, b.Y - dy, b.W + 2*dx, b.H + 2*dy} }

// NodeShape is the box of a node.
type NodeShape struct {
	ID    string
	Box   Box
	Label string
	Role  []deck.Span
}

// GroupShape is the frame around the nodes of a group, with its name in
// its bottom left corner.
type GroupShape struct {
	ID     string
	Box    Box
	Label  []deck.Span
	LabelX float64
	LabelY float64
}

// LifelineShape is the head and the line of a node in a ladder.
type LifelineShape struct {
	ID     string
	Head   Box
	Label  string
	X      float64
	Y1, Y2 float64
}

// EventShape is the line of a message or a relation, from (X1, Y1) to
// the arrowhead at (X2, Y2), or the box of a process. The label sits
// with its center at (LabelX, LabelY). Number is the step that first
// makes the event the active one, 0 for none.
type EventShape struct {
	ID     string
	Nodes  []string
	Kind   deck.EventKind
	Family string
	X1, Y1 float64
	X2, Y2 float64
	Box    Box
	Label  string
	Note   string
	Proto  string
	// The label is centered at (LabelX, LabelY) and is LabelW wide and
	// LabelH tall. On a map it has two lines, the name over the note and
	// the protocol, so that it fits between two nodes; on a ladder one.
	LabelX float64
	LabelY float64
	LabelW float64
	LabelH float64
	Number int
}

// Sizes of the diagrams, in diagram units. A map node is a box on a
// grid; a ladder has a column for each node and a row for each event.
const (
	diagramMargin = 36.0
	mapCellW      = 280.0
	mapCellH      = 160.0
	mapNodeW      = 150.0
	mapNodeH      = 64.0
	mapLane       = 8.0
	mapLaneStep   = 14.0
	ladderColW    = 168.0
	ladderHeadW   = 132.0
	ladderHeadH   = 40.0
	ladderRowH    = 42.0
	ladderTop     = 30.0

	// DiagramLabelSize is the size of the label of an event; the role
	// of a node is a little smaller than its name.
	DiagramLabelSize = 15.0
	DiagramSubSize   = 11.5
	DiagramNodeSize  = 19.0
	DiagramRoleSize  = 12.5
	DiagramGroupSize = 13.0
	processPadX      = 12.0
	processH         = 30.0
	processMaxW      = 220.0

	// LadderRows is the number of rows that a view of a ladder shows at
	// most; a longer ladder scrolls with its steps.
	LadderRows = 10
)

// Family is the kind of line an event gets: the protocols of a family
// share a colour and a dash, and the label names the protocol, so that
// neither the colour nor the dash alone tells them apart.
func Family(e deck.Event) string {
	switch e.Kind {
	case deck.EventProcess:
		return "process"
	case deck.EventRelation:
		return "relation"
	}
	p := strings.ToUpper(strings.Fields(e.Protocol + " x")[0])
	switch p {
	case "SIP":
		return "sip"
	case "DIAMETER", "CX", "SH", "RX", "GX", "SGD", "S6A", "S6D", "RO", "RF", "DX":
		return "diameter"
	case "DNS":
		return "dns"
	case "HTTP", "HTTPS", "UT", "XCAP", "REST":
		return "http"
	case "RTP", "RTCP", "MSRP":
		return "media"
	}
	return "other"
}

// Families lists the families in the order of a legend.
var Families = []string{"sip", "diameter", "dns", "http", "media", "other", "relation", "process"}

// Draw places a scene.
func Draw(m *deck.Model, s *deck.Scene) *Diagram {
	if s.View == deck.ViewLadder {
		return drawLadder(m, s)
	}
	return drawMap(m, s)
}

func eventShape(m *deck.Model, id string, numbers map[string]int) (deck.Event, EventShape) {
	e, _ := m.Event(id)
	shape := EventShape{ID: id, Nodes: e.Nodes(), Kind: e.Kind, Family: Family(e), Label: deck.PlainText(e.Label),
		Note: deck.PlainText(e.Note), Number: numbers[id]}
	if e.Kind == deck.EventMessage {
		shape.Proto = e.Protocol
	}
	return e, shape
}

// LabelText is the first line of the label of an event: the number of
// its step and its name.
func (e EventShape) LabelText() string {
	if e.Number > 0 {
		return numberMark(e.Number) + " " + e.Label
	}
	return e.Label
}

// SubText is the second line of the label of a message: its note and
// its protocol, such as "(2nd) Cx".
func (e EventShape) SubText() string {
	return strings.TrimSpace(e.Note + " " + e.Proto)
}

// numberMark is the number of a step as the diagrams print it.
func numberMark(n int) string {
	if n >= 1 && n <= 20 {
		return string(rune('①' + n - 1))
	}
	return fmt.Sprintf("(%d)", n)
}

// LabelLines reports whether the label of an event has a second line.
func (e EventShape) LabelLines() int {
	if e.Kind == deck.EventMessage && e.SubText() != "" || e.Kind == deck.EventRelation && e.Note != "" {
		return 2
	}
	return 1
}

func sizeLabel(e *EventShape, oneLine bool) {
	title := textWidth(e.LabelText(), DiagramLabelSize, false, true)
	sub := textWidth(e.SubText(), DiagramSubSize, false, false)
	if oneLine {
		e.LabelW = title + 12
		if e.SubText() != "" {
			e.LabelW += sub + 6
		}
		e.LabelH = DiagramLabelSize * 1.4
		return
	}
	e.LabelW = max(title, sub) + 14
	e.LabelH = DiagramLabelSize*1.3 + 4
	if e.LabelLines() == 2 {
		e.LabelH += DiagramSubSize * 1.3
	}
}

// LabelBox is the box of the label of an event.
func (e EventShape) LabelBox() Box {
	return Box{e.LabelX - e.LabelW/2, e.LabelY - e.LabelH/2, e.LabelW, e.LabelH}
}

func drawMap(m *deck.Model, s *deck.Scene) *Diagram {
	d := &Diagram{View: deck.ViewMap}
	// The grid is that of the whole model, so that a node keeps its
	// place in every scene of the deck.
	minCol, minRow := math.Inf(1), math.Inf(1)
	for _, n := range m.Nodes {
		minCol, minRow = min(minCol, n.Col), min(minRow, n.Row)
	}
	boxes := map[string]Box{}
	for _, id := range s.Nodes {
		n, _ := m.Node(id)
		cx := diagramMargin + (n.Col-minCol+0.5)*mapCellW
		cy := diagramMargin + (n.Row-minRow+0.5)*mapCellH
		b := Box{cx - mapNodeW/2, cy - mapNodeH/2, mapNodeW, mapNodeH}
		boxes[id] = b
		d.Nodes = append(d.Nodes, NodeShape{ID: id, Box: b, Label: n.Label, Role: n.Role})
		d.Bounds = d.Bounds.union(b)
	}
	for _, g := range m.Groups {
		var frame Box
		for _, id := range g.Nodes {
			if b, ok := boxes[id]; ok {
				frame = frame.union(b)
			}
		}
		if frame.W == 0 {
			continue
		}
		frame = Box{frame.X - 24, frame.Y - 22, frame.W + 48, frame.H + 54}
		d.Groups = append(d.Groups, GroupShape{ID: g.ID, Box: frame, Label: g.Label, LabelX: frame.X + 12, LabelY: frame.Y + frame.H - 12})
		d.Bounds = d.Bounds.union(frame)
	}
	numbers := s.FirstActive()
	// Labels and the boxes of the processes keep clear of the nodes and
	// of each other. Each is placed at the first free place of a short
	// list: a process below, above, right or left of its node; a label
	// at the middle of its line, then further along it, then further out.
	var taken []Box
	for _, b := range boxes {
		taken = append(taken, b.grow(4, 4))
	}
	free := func(b Box) bool {
		for _, t := range taken {
			if b.overlaps(t) {
				return false
			}
		}
		return true
	}
	cover := func(b Box) float64 {
		area := 0.0
		for _, t := range taken {
			if b.overlaps(t) {
				area += (min(b.X+b.W, t.X+t.W) - max(b.X, t.X)) * (min(b.Y+b.H, t.Y+t.H) - max(b.Y, t.Y))
			}
		}
		return area
	}
	shapes := map[string]EventShape{}
	for _, id := range s.Events() {
		e, shape := eventShape(m, id, numbers)
		if e.Kind != deck.EventProcess {
			continue
		}
		b := boxes[e.Actor]
		w := min(processMaxW, textWidth(shape.LabelText(), DiagramLabelSize, false, false)+2*processPadX)
		cx, cy := b.Center()
		candidates := []Box{
			{cx - w/2, b.Y + b.H + 10, w, processH},
			{cx - w/2, b.Y - 10 - processH, w, processH},
			{b.X + b.W + 10, cy - processH/2, w, processH},
			{b.X - 10 - w, cy - processH/2, w, processH},
		}
		for extra := 1.0; extra <= 3; extra++ {
			candidates = append(candidates, Box{cx - w/2, b.Y + b.H + 10 + extra*(processH+6), w, processH})
		}
		shape.Box = candidates[0]
		for _, c := range candidates {
			if free(c) {
				shape.Box = c
				break
			}
		}
		taken = append(taken, shape.Box.grow(3, 3))
		shape.LabelX, shape.LabelY = shape.Box.Center()
		shape.LabelW, shape.LabelH = w, processH
		shapes[id] = shape
	}
	lanes := map[[2]string]int{}
	for _, id := range s.Events() {
		e, shape := eventShape(m, id, numbers)
		if e.Kind == deck.EventProcess {
			shape = shapes[id]
			d.Events = append(d.Events, shape)
			d.Bounds = d.Bounds.union(shape.Box)
			continue
		}
		a, b := boxes[e.From], boxes[e.To]
		ax, ay := a.Center()
		bx, by := b.Center()
		// Both directions of a pair of nodes get a lane of their own, on
		// either side of the line between the centers: a request and
		// its answer never share a line.
		forward := ax < bx || (ax == bx && ay < by)
		ux, uy := bx-ax, by-ay
		if !forward {
			ux, uy = -ux, -uy
		}
		n := math.Hypot(ux, uy)
		ux, uy = ux/n, uy/n
		nx, ny := uy, -ux // the side of the forward lane
		side := 1.0
		key := [2]string{e.From, e.To}
		if !forward {
			side = -1
		}
		k := lanes[key]
		lanes[key]++
		offset := side * (mapLane + float64(k)*mapLaneStep)
		ox, oy := nx*offset, ny*offset
		x1, y1 := clipToBox(ax+ox, ay+oy, bx+ox, by+oy, a.grow(3, 3))
		x2, y2 := clipToBox(bx+ox, by+oy, ax+ox, ay+oy, b.grow(5, 5))
		shape.X1, shape.Y1, shape.X2, shape.Y2 = x1, y1, x2, y2
		sizeLabel(&shape, false)
		// The label sits beside its lane, on the outer side, far enough
		// that it does not cover the line; the labels of more lanes in
		// one direction stack outwards.
		reach := labelReach(shape.LabelW, shape.LabelH, nx, ny)
		lift := mapLane + 4 + reach + float64(k)*(2*reach+4)
		place := func(t, extra, toward float64) {
			px, py := x1+(x2-x1)*t, y1+(y2-y1)*t
			shape.LabelX, shape.LabelY = px+nx*toward*(lift+extra), py+ny*toward*(lift+extra)
		}
		// The outer side first; where a node is in the way there, the
		// other side of the line.
		// Where no place is free, the one that covers least.
		best, bestCover := [3]float64{0.5, 0, side}, math.Inf(1)
	search:
		for _, toward := range []float64{side, -side} {
			for _, extra := range []float64{0, 14, 28, 42} {
				for _, t := range []float64{0.5, 0.4, 0.6, 0.32, 0.68, 0.25, 0.75} {
					place(t, extra, toward)
					if c := cover(shape.LabelBox()); c < bestCover {
						best, bestCover = [3]float64{t, extra, toward}, c
						if c == 0 {
							break search
						}
					}
				}
			}
		}
		place(best[0], best[1], best[2])
		taken = append(taken, shape.LabelBox().grow(2, 2))
		d.Events = append(d.Events, shape)
		d.Bounds = d.Bounds.union(Box{min(x1, x2), min(y1, y2), math.Abs(x2 - x1), math.Abs(y2 - y1)})
		d.Bounds = d.Bounds.union(shape.LabelBox())
	}
	d.Bounds = d.Bounds.grow(diagramMargin/2, diagramMargin/2)
	for _, step := range s.Steps {
		d.Cameras = append(d.Cameras, d.camera(step, boxes))
	}
	return d
}

// labelReach is how far the center of a label of width w and height h
// has to sit from a line, along the normal (nx, ny), for the label to
// clear it.
func labelReach(w, h, nx, ny float64) float64 {
	return math.Abs(nx)*w/2 + math.Abs(ny)*h/2
}

// clipToBox returns the point where the segment from (x1, y1) inside the
// box to (x2, y2) outside it leaves the box.
func clipToBox(x1, y1, x2, y2 float64, b Box) (float64, float64) {
	dx, dy := x2-x1, y2-y1
	t := 1.0
	if dx > 0 {
		t = min(t, (b.X+b.W-x1)/dx)
	} else if dx < 0 {
		t = min(t, (b.X-x1)/dx)
	}
	if dy > 0 {
		t = min(t, (b.Y+b.H-y1)/dy)
	} else if dy < 0 {
		t = min(t, (b.Y-y1)/dy)
	}
	return x1 + dx*t, y1 + dy*t
}

// camera is the part of a map that a step shows: the whole diagram, or
// the nodes it zooms to with the events between them.
func (d *Diagram) camera(step deck.Step, boxes map[string]Box) Box {
	if len(step.Zoom) == 0 {
		return d.Bounds
	}
	var view Box
	for _, id := range step.Zoom {
		view = view.union(boxes[id])
	}
	for _, e := range d.Events {
		if !slices.Contains(step.Events, e.ID) {
			continue
		}
		if e.Kind == deck.EventProcess {
			if view.contains(e.Box.Center()) {
				view = view.union(e.Box)
			}
			continue
		}
		if view.contains(e.X1, e.Y1) && view.contains(e.X2, e.Y2) {
			view = view.union(e.LabelBox())
		}
	}
	view = view.grow(70, 56)
	// A view that is much taller than wide, or the other way round,
	// wastes the room of a 16:9 screen; it is widened around its
	// middle.
	const aspect = 1.6
	if view.W < view.H*aspect {
		cx := view.X + view.W/2
		view.W = view.H * aspect
		view.X = cx - view.W/2
	} else if view.H < view.W/aspect*0.6 {
		cy := view.Y + view.H/2
		view.H = view.W / aspect * 0.6
		view.Y = cy - view.H/2
	}
	return view
}

func (b Box) overlaps(o Box) bool {
	return b.X < o.X+o.W && o.X < b.X+b.W && b.Y < o.Y+o.H && o.Y < b.Y+b.H
}

func (b Box) contains(x, y float64) bool {
	return x >= b.X-1 && x <= b.X+b.W+1 && y >= b.Y-1 && y <= b.Y+b.H+1
}

func drawLadder(m *deck.Model, s *deck.Scene) *Diagram {
	d := &Diagram{View: deck.ViewLadder}
	xs := map[string]float64{}
	for i, id := range s.Nodes {
		n, _ := m.Node(id)
		x := diagramMargin + (float64(i)+0.5)*ladderColW
		xs[id] = x
		head := Box{x - ladderHeadW/2, ladderTop, ladderHeadW, ladderHeadH}
		d.Lines = append(d.Lines, LifelineShape{ID: id, Head: head, Label: n.Label, X: x, Y1: ladderTop + ladderHeadH})
		d.Bounds = d.Bounds.union(head)
	}
	numbers := s.FirstActive()
	y := ladderTop + ladderHeadH + ladderRowH*0.9
	for _, id := range s.Events() {
		e, shape := eventShape(m, id, numbers)
		if e.Kind == deck.EventProcess {
			x := xs[e.Actor]
			w := min(processMaxW, textWidth(shape.LabelText(), DiagramLabelSize, false, false)+2*processPadX)
			shape.Box = Box{x - w/2, y - processH/2, w, processH}
			shape.LabelX, shape.LabelY = x, y
			shape.LabelW, shape.LabelH = w, processH
		} else {
			shape.X1, shape.Y1, shape.X2, shape.Y2 = xs[e.From], y, xs[e.To], y
			sizeLabel(&shape, true)
			shape.LabelX, shape.LabelY = (xs[e.From]+xs[e.To])/2, y-shape.LabelH/2-1
		}
		d.Events = append(d.Events, shape)
		y += ladderRowH
	}
	for i := range d.Lines {
		d.Lines[i].Y2 = y
	}
	d.Bounds = d.Bounds.union(Box{0, 0, diagramMargin*2 + float64(len(s.Nodes))*ladderColW, y + diagramMargin/2})
	for _, step := range s.Steps {
		d.Cameras = append(d.Cameras, d.ladderCamera(step))
	}
	return d
}

// ladderCamera shows the heads of the lifelines and the rows of a ladder
// down to the last one that the step draws, at most LadderRows of them:
// the ladder grows as the steps add rows, and scrolls when it is long.
// The writers draw the heads at the top of the view, wherever it is, so
// that a scrolled ladder still names its lifelines.
func (d *Diagram) ladderCamera(step deck.Step) Box {
	last := 0
	for i, e := range d.Events {
		if slices.Contains(step.Events, e.ID) {
			last = i
		}
	}
	first := max(0, last-LadderRows+1)
	rows := max(last-first+1, 6)
	head := ladderTop + ladderHeadH + ladderRowH*0.9
	top := float64(first) * ladderRowH
	height := head + float64(rows)*ladderRowH
	return Box{d.Bounds.X, top, d.Bounds.W, min(height, d.Bounds.Y+d.Bounds.H-top)}
}
