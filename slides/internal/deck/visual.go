package deck

// A visual deck (front matter "format: visual") describes, once, the
// nodes of the network or of the code and every signal, step of
// processing and dependency between them: its model. A slide may then
// be a scene: a diagram of some of the nodes and the steps that a
// learner goes through on it, one at a time, each step showing exactly
// the events it lists. The text below the scene block of a slide is the
// longer explanation that the learner can open next to the diagram.
//
// The format is described in slides/README.md.

// FormatVisual is the value of the front matter key "format" of a deck
// with a model and scenes.
const FormatVisual = "visual"

// Model is what a visual deck shows: its nodes, the frames that group
// them, and the events between them.
type Model struct {
	Line   int
	Nodes  []Node
	Groups []Group
	Events []Event
}

// Node is a node of the network, a package of the code, a state: a box
// of the diagrams. Col and Row place it on the grid of the map; the
// writers turn them into positions of their own.
type Node struct {
	ID       string
	Label    string
	Role     []Span
	Col, Row float64
}

// Group is a frame drawn around nodes that belong together, such as the
// home network.
type Group struct {
	ID    string
	Label []Span
	Nodes []string
}

// EventKind tells a message between two nodes from the processing in one
// node and from a relation that is not a signal at all, such as an
// import of a package.
type EventKind int

const (
	// EventMessage is one signal sent from one node to another: one hop
	// of one request or one response.
	EventMessage EventKind = iota
	// EventProcess is what one node does with what it received.
	EventProcess
	// EventRelation is a lasting relation from one node to another, such
	// as "imports" or "reads". It has no direction in time.
	EventRelation
)

// Event is one message, process or relation. A message keeps the name of
// the signal in Label, the same in every language; a process and a
// relation describe themselves in the language of the deck.
type Event struct {
	ID       string
	Kind     EventKind
	Label    []Span
	From, To string
	Actor    string
	Protocol string
	Note     []Span
	Fields   []Field
}

// Field is one header, AVP or value of a message, or one input or result
// of a process, that the detail of a step shows.
type Field struct {
	Name  string
	Value []Span
}

// Nodes returns the nodes that an event touches: both ends of a message
// and of a relation, the node of a process.
func (e Event) Nodes() []string {
	if e.Kind == EventProcess {
		return []string{e.Actor}
	}
	return []string{e.From, e.To}
}

// View is the kind of diagram of a scene.
type View string

const (
	// ViewMap places the nodes on their grid and draws each event between
	// them: where a signal goes, what depends on what.
	ViewMap View = "map"
	// ViewLadder draws a lifeline for each node and one row for each
	// event, in the order in which the steps show them: who talks to
	// whom, and when.
	ViewLadder View = "ladder"
)

// Basis says what a diagram shows: the behaviour of this implementation
// as the guides describe it, what a specification says, or an example
// made up to explain something. The writers print it under the diagram,
// so that no drawing passes for a capture.
type Basis string

const (
	BasisRepository   Basis = "repository"
	BasisStandard     Basis = "standard"
	BasisIllustration Basis = "illustration"
)

// Scene is the diagram of a slide and the steps through it.
type Scene struct {
	Line  int
	ID    string
	View  View
	Nodes []string
	Lead  []Span
	Basis Basis
	Steps []Step
}

// Step is one state of a scene. It is complete: it lists every event
// that is drawn, so that opening a step directly shows what stepping to
// it shows. The source may write a step as a change of the one before;
// the parser resolves it.
type Step struct {
	ID    string
	Title []Span
	Text  [][]Span
	// Events are the events drawn, in the order in which they were
	// added. Active, if set, is the one that this step is about.
	Events []string
	Active string
	// Focus are the nodes drawn strongest. Zoom, if set, are the nodes
	// that the view closes in on; otherwise it shows the whole diagram.
	Focus []string
	Zoom  []string
	// Detail are the events whose fields the step shows.
	Detail []string
	// Checkpoint marks the states that the pptx and the printed page
	// keep. The last step of a scene is always one.
	Checkpoint bool
}

// Event returns the event of the model with the id, if any.
func (m *Model) Event(id string) (Event, bool) {
	for _, e := range m.Events {
		if e.ID == id {
			return e, true
		}
	}
	return Event{}, false
}

// Node returns the node of the model with the id, if any.
func (m *Model) Node(id string) (Node, bool) {
	for _, n := range m.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// FirstActive returns, for each event of a scene, the number of the step
// (from 1) that first makes it the active event. The diagrams print the
// number next to the event, and the pptx numbers the text of the steps
// the same way, so that a reader of a still page can tell the order.
func (s *Scene) FirstActive() map[string]int {
	numbers := map[string]int{}
	for i, step := range s.Steps {
		if step.Active != "" {
			if _, ok := numbers[step.Active]; !ok {
				numbers[step.Active] = i + 1
			}
		}
	}
	return numbers
}

// Sections returns the blocks of a slide that the checks read: the
// columns, and for a scene, its text turned into blocks. A check that
// reads Sections sees every link, every piece of inline code and every
// address of a scene, as it sees those of a column. Each section is
// read on its own, as a column is.
func (d *Deck) Sections(s Slide) [][]Block {
	sections := append([][]Block{}, s.Columns...)
	if d.Model != nil && s.Number == 1 {
		// The text of the model itself, the roles of the nodes and the
		// names of the frames, is checked with the first slide.
		var lines [][]Span
		for _, n := range d.Model.Nodes {
			lines = append(lines, n.Role)
		}
		for _, g := range d.Model.Groups {
			lines = append(lines, g.Label)
		}
		sections = append(sections, []Block{Paragraph{Lines: lines}})
	}
	if s.Scene == nil || d.Model == nil {
		return sections
	}
	var text []Block
	if len(s.Scene.Lead) > 0 {
		text = append(text, Paragraph{Lines: [][]Span{s.Scene.Lead}})
	}
	for _, step := range s.Scene.Steps {
		text = append(text, Paragraph{Lines: append([][]Span{step.Title}, step.Text...)})
	}
	sections = append(sections, text)
	// The model is in the deck once, but each scene that draws an event
	// shows its fields; they are checked with the first slide that
	// shows them.
	for _, id := range sceneEvents(s.Scene) {
		e, ok := d.Model.Event(id)
		if !ok {
			continue
		}
		var blocks []Block
		lines := [][]Span{e.Label}
		if len(e.Note) > 0 {
			lines = append(lines, e.Note)
		}
		blocks = append(blocks, Paragraph{Lines: lines})
		if len(e.Fields) > 0 {
			t := Table{Header: []Cell{{{Text: "field"}}, {{Text: "value"}}}, Align: []Align{AlignLeft, AlignLeft}}
			for _, f := range e.Fields {
				t.Rows = append(t.Rows, []Cell{{{Text: f.Name}}, f.Value})
			}
			blocks = append(blocks, t)
		}
		sections = append(sections, blocks)
	}
	return sections
}

func sceneEvents(s *Scene) []string {
	seen := map[string]bool{}
	var out []string
	for _, step := range s.Steps {
		for _, id := range step.Events {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// Events returns the events that a scene draws, each once, in the
// order of their first step: the rows of a ladder.
func (s *Scene) Events() []string { return sceneEvents(s) }
