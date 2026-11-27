package deck

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The info words of the fenced blocks of a visual deck.
const (
	fenceModel = "ims-model"
	fenceScene = "ims-scene"
)

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	yamlLineError = regexp.MustCompile(`line (\d+)`)
)

// The source of the model and of a scene, as YAML. Every key is known;
// a mistyped key fails instead of passing unnoticed.
type rawModel struct {
	Nodes  []rawNode  `yaml:"nodes"`
	Groups []rawGroup `yaml:"groups"`
	Events []rawEvent `yaml:"events"`
}

type rawNode struct {
	ID    string    `yaml:"id"`
	Label string    `yaml:"label"`
	Role  string    `yaml:"role"`
	At    []float64 `yaml:"at"`
}

type rawGroup struct {
	ID    string   `yaml:"id"`
	Label string   `yaml:"label"`
	Nodes []string `yaml:"nodes"`
}

type rawEvent struct {
	ID     string              `yaml:"id"`
	Msg    string              `yaml:"msg"`
	Do     string              `yaml:"do"`
	Rel    string              `yaml:"rel"`
	From   string              `yaml:"from"`
	To     string              `yaml:"to"`
	At     string              `yaml:"at"`
	Proto  string              `yaml:"proto"`
	Note   string              `yaml:"note"`
	Fields []map[string]string `yaml:"fields"`
}

type rawScene struct {
	ID    string    `yaml:"id"`
	View  string    `yaml:"view"`
	Nodes []string  `yaml:"nodes"`
	Lead  string    `yaml:"lead"`
	Basis string    `yaml:"basis"`
	Steps []rawStep `yaml:"steps"`
}

type rawStep struct {
	ID         string    `yaml:"id"`
	Title      string    `yaml:"title"`
	Text       string    `yaml:"text"`
	Show       []string  `yaml:"show"`
	Hide       []string  `yaml:"hide"`
	Only       *[]string `yaml:"only"`
	Active     *string   `yaml:"active"`
	Focus      *[]string `yaml:"focus"`
	Zoom       []string  `yaml:"zoom"`
	Detail     *[]string `yaml:"detail"`
	Checkpoint bool      `yaml:"checkpoint"`
}

// decodeYAML reads a fenced block as YAML into out. line is the number
// of the first line of the block, so that an error names the line of the
// deck.
func (p *parser) decodeYAML(body []line, out any) error {
	text := make([]string, len(body))
	for i, l := range body {
		text[i] = l.text
	}
	first := 0
	if len(body) > 0 {
		first = body[0].number
	}
	dec := yaml.NewDecoder(strings.NewReader(strings.Join(text, "\n")))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		number := first
		msg := err.Error()
		if m := yamlLineError.FindStringSubmatch(msg); m != nil {
			n, _ := strconv.Atoi(m[1])
			number = first + n - 1
		}
		return p.errorf(number, "%s", strings.TrimPrefix(msg, "yaml: "))
	}
	return nil
}

// fencedBody returns the lines inside a fenced block that opens at
// lines[0] with the info word, and the lines after the block.
func (p *parser) fencedBody(lines []line, info string) ([]line, []line, error) {
	m := fenceOpening.FindStringSubmatch(lines[0].text)
	if m == nil || m[2] != info {
		return nil, nil, p.errorf(lines[0].number, "a %s block is expected here", info)
	}
	for j := 1; j < len(lines); j++ {
		if nextFence(m[1], lines[j].text) == "" {
			return lines[1:j], lines[j+1:], nil
		}
	}
	return nil, nil, p.errorf(lines[0].number, "the fenced block is not closed")
}

// inline parses one line of text of the model or of a scene.
func (p *parser) inline(number int, text string) ([]Span, error) {
	if strings.Contains(text, "\n") {
		return nil, p.errorf(number, "%q is one line", text)
	}
	spans, err := parseInline(strings.TrimSpace(text))
	if err != nil {
		return nil, p.errorf(number, "%v", err)
	}
	return spans, nil
}

// model parses the block of the model, the first part of a visual deck.
func (p *parser) model(lines []line) (*Model, error) {
	lines = trimBlank(lines)
	if len(lines) == 0 {
		return nil, p.errorf(1, "a visual deck starts with an %s block after its front matter", fenceModel)
	}
	body, rest, err := p.fencedBody(lines, fenceModel)
	if err != nil {
		return nil, err
	}
	if len(trimBlank(rest)) > 0 {
		return nil, p.errorf(rest[0].number, `the %s block stands alone; a "---" line ends it`, fenceModel)
	}
	var raw rawModel
	if err := p.decodeYAML(body, &raw); err != nil {
		return nil, err
	}
	at := lines[0].number
	m := &Model{Line: at}
	ids := map[string]string{}
	claim := func(id, what string) error {
		if !idPattern.MatchString(id) {
			return p.errorf(at, "%s id %q: an id is lowercase letters, digits and single hyphens", what, id)
		}
		if other, ok := ids[id]; ok {
			return p.errorf(at, "%s id %q is already the id of a %s", what, id, other)
		}
		ids[id] = what
		return nil
	}
	for _, n := range raw.Nodes {
		if err := claim(n.ID, "node"); err != nil {
			return nil, err
		}
		if n.Label == "" || len(n.At) != 2 {
			return nil, p.errorf(at, "node %s needs a label and at: [column, row]", n.ID)
		}
		role, err := p.inline(at, n.Role)
		if err != nil {
			return nil, err
		}
		m.Nodes = append(m.Nodes, Node{ID: n.ID, Label: n.Label, Role: role, Col: n.At[0], Row: n.At[1]})
	}
	known := func(id string) bool { return ids[id] == "node" }
	for _, g := range raw.Groups {
		if err := claim(g.ID, "group"); err != nil {
			return nil, err
		}
		label, err := p.inline(at, g.Label)
		if err != nil {
			return nil, err
		}
		for _, n := range g.Nodes {
			if !known(n) {
				return nil, p.errorf(at, "group %s: no node %q", g.ID, n)
			}
		}
		if len(g.Nodes) == 0 {
			return nil, p.errorf(at, "group %s has no nodes", g.ID)
		}
		m.Groups = append(m.Groups, Group{ID: g.ID, Label: label, Nodes: g.Nodes})
	}
	for _, r := range raw.Events {
		e, err := p.event(at, r, known)
		if err != nil {
			return nil, err
		}
		if err := claim(e.ID, "event"); err != nil {
			return nil, err
		}
		m.Events = append(m.Events, e)
	}
	if len(m.Nodes) == 0 {
		return nil, p.errorf(at, "the model has no nodes")
	}
	return m, nil
}

func (p *parser) event(at int, r rawEvent, known func(string) bool) (Event, error) {
	e := Event{ID: r.ID, Protocol: r.Proto}
	fail := func(format string, args ...any) (Event, error) {
		return Event{}, p.errorf(at, "event %s: %s", r.ID, fmt.Sprintf(format, args...))
	}
	kinds := 0
	for _, s := range []string{r.Msg, r.Do, r.Rel} {
		if s != "" {
			kinds++
		}
	}
	if kinds != 1 {
		return fail("an event is one of msg (a message), do (a process) and rel (a relation)")
	}
	var label string
	switch {
	case r.Msg != "":
		e.Kind, label = EventMessage, r.Msg
		if r.Proto == "" {
			return fail("a message names its protocol, such as proto: SIP")
		}
	case r.Do != "":
		e.Kind, label = EventProcess, r.Do
	default:
		e.Kind, label = EventRelation, r.Rel
	}
	if e.Kind == EventProcess {
		if r.At == "" || r.From != "" || r.To != "" {
			return fail("a process happens at one node: at, without from and to")
		}
		if !known(r.At) {
			return fail("no node %q", r.At)
		}
		e.Actor = r.At
	} else {
		if r.At != "" {
			return fail("a message and a relation go from one node to another: from and to, without at")
		}
		if !known(r.From) || !known(r.To) {
			return fail("from %q and to %q have to be nodes", r.From, r.To)
		}
		if r.From == r.To {
			return fail("from and to are the same node")
		}
		e.From, e.To = r.From, r.To
	}
	var err error
	if e.Label, err = p.inline(at, label); err != nil {
		return Event{}, err
	}
	if e.Note, err = p.inline(at, r.Note); err != nil {
		return Event{}, err
	}
	for _, f := range r.Fields {
		if len(f) != 1 {
			return fail(`a field is one "name: value"`)
		}
		for name, value := range f {
			if name == "" || value == "" {
				return fail("a field has a name and a value")
			}
			spans, err := p.inline(at, value)
			if err != nil {
				return Event{}, err
			}
			e.Fields = append(e.Fields, Field{Name: name, Value: spans})
		}
	}
	return e, nil
}

// scene parses the scene block that opens the body of a slide, and
// returns the scene and the lines after the block.
func (p *parser) scene(d *Deck, lines []line, ids map[string]bool) (*Scene, []line, error) {
	body, rest, err := p.fencedBody(lines, fenceScene)
	if err != nil {
		return nil, nil, err
	}
	at := lines[0].number
	if d.Model == nil {
		return nil, nil, p.errorf(at, `a scene needs the model of a visual deck ("format: visual" in the front matter)`)
	}
	var raw rawScene
	if err := p.decodeYAML(body, &raw); err != nil {
		return nil, nil, err
	}
	fail := func(format string, args ...any) error {
		return p.errorf(at, "scene %s: %s", raw.ID, fmt.Sprintf(format, args...))
	}
	if !idPattern.MatchString(raw.ID) {
		return nil, nil, fail("a scene has an id of lowercase letters, digits and single hyphens")
	}
	if ids[raw.ID] {
		return nil, nil, fail("the id is taken by another scene, node, group or event")
	}
	ids[raw.ID] = true
	s := &Scene{Line: at, ID: raw.ID, View: View(raw.View), Basis: Basis(raw.Basis)}
	if s.View == "" {
		s.View = ViewMap
	}
	if s.View != ViewMap && s.View != ViewLadder {
		return nil, nil, fail("view is map or ladder")
	}
	if s.Basis == "" {
		s.Basis = BasisRepository
	}
	if !slices.Contains([]Basis{BasisRepository, BasisStandard, BasisIllustration}, s.Basis) {
		return nil, nil, fail("basis is repository, standard or illustration")
	}
	if s.Lead, err = p.inline(at, raw.Lead); err != nil {
		return nil, nil, err
	}
	s.Nodes = raw.Nodes
	for i, n := range s.Nodes {
		if _, ok := d.Model.Node(n); !ok {
			return nil, nil, fail("no node %q", n)
		}
		if slices.Contains(s.Nodes[:i], n) {
			return nil, nil, fail("node %s is listed twice", n)
		}
	}
	if len(raw.Steps) == 0 {
		return nil, nil, fail("a scene has at least one step")
	}
	var drawn []string
	stepIDs := map[string]bool{}
	for i, r := range raw.Steps {
		step, next, err := p.step(d.Model, s, r, drawn)
		if err != nil {
			return nil, nil, fail("step %s: %v", r.ID, err)
		}
		if stepIDs[step.ID] {
			return nil, nil, fail("two steps are %s", step.ID)
		}
		stepIDs[step.ID] = true
		if i == len(raw.Steps)-1 {
			step.Checkpoint = true
		}
		s.Steps = append(s.Steps, step)
		drawn = next
	}
	if len(s.Nodes) == 0 {
		s.Nodes = defaultNodes(d.Model, s)
	}
	for _, step := range s.Steps {
		for _, id := range step.Events {
			e, _ := d.Model.Event(id)
			if s.View == ViewLadder && e.Kind == EventRelation {
				return nil, nil, fail("a ladder draws messages and processes, not the relation %s", id)
			}
			for _, n := range e.Nodes() {
				if !slices.Contains(s.Nodes, n) {
					return nil, nil, fail("event %s needs node %s, which the scene does not draw", id, n)
				}
			}
		}
		for _, n := range slices.Concat(step.Focus, step.Zoom) {
			if !slices.Contains(s.Nodes, n) {
				return nil, nil, fail("step %s: the scene does not draw node %s", step.ID, n)
			}
		}
	}
	return s, rest, nil
}

// defaultNodes are the nodes of a scene that lists none: every node of
// the model on a map, so that the whole network stays in sight, and on a
// ladder the nodes that its events touch, in the order of the model.
func defaultNodes(m *Model, s *Scene) []string {
	used := map[string]bool{}
	for _, id := range s.Events() {
		e, _ := m.Event(id)
		for _, n := range e.Nodes() {
			used[n] = true
		}
	}
	var out []string
	for _, n := range m.Nodes {
		if s.View == ViewMap || used[n.ID] {
			out = append(out, n.ID)
		}
	}
	return out
}

// step resolves one step of the source against the events drawn before
// it, and returns the complete step and the events it draws.
func (p *parser) step(m *Model, s *Scene, r rawStep, before []string) (Step, []string, error) {
	if !idPattern.MatchString(r.ID) {
		return Step{}, nil, errors.New("a step has an id of lowercase letters, digits and single hyphens")
	}
	step := Step{ID: r.ID, Checkpoint: r.Checkpoint}
	var err error
	if r.Title == "" {
		return Step{}, nil, errors.New("a step has a title")
	}
	if step.Title, err = parseInline(strings.TrimSpace(r.Title)); err != nil {
		return Step{}, nil, err
	}
	for _, text := range strings.Split(strings.TrimSpace(r.Text), "\n") {
		spans, err := parseInline(strings.TrimSpace(text))
		if err != nil {
			return Step{}, nil, err
		}
		step.Text = append(step.Text, spans)
	}
	if len(step.Text) == 1 && len(step.Text[0]) == 0 {
		return Step{}, nil, errors.New("a step has a text that says what it shows")
	}
	known := func(id string) error {
		if _, ok := m.Event(id); !ok {
			return fmt.Errorf("no event %q", id)
		}
		return nil
	}
	drawn := slices.Clone(before)
	var added []string
	if r.Only != nil {
		drawn = nil
		for _, id := range *r.Only {
			if err := known(id); err != nil {
				return Step{}, nil, err
			}
			drawn = append(drawn, id)
		}
		added = slices.Clone(drawn)
	}
	for _, id := range r.Hide {
		i := slices.Index(drawn, id)
		if i < 0 {
			return Step{}, nil, fmt.Errorf("hides %s, which is not drawn", id)
		}
		drawn = slices.Delete(drawn, i, i+1)
	}
	for _, id := range r.Show {
		if err := known(id); err != nil {
			return Step{}, nil, err
		}
		if slices.Contains(drawn, id) {
			return Step{}, nil, fmt.Errorf("shows %s, which is already drawn", id)
		}
		drawn = append(drawn, id)
		added = append(added, id)
	}
	if len(drawn) != len(slices.Compact(slices.Sorted(slices.Values(drawn)))) {
		return Step{}, nil, errors.New("draws an event twice")
	}
	step.Events = drawn
	if r.Active != nil {
		step.Active = *r.Active
		if step.Active != "" && !slices.Contains(drawn, step.Active) {
			return Step{}, nil, fmt.Errorf("the active event %s is not drawn", step.Active)
		}
	} else {
		// The active event is the last message or process that the step
		// adds: what happens in this step.
		for _, id := range added {
			if e, _ := m.Event(id); e.Kind != EventRelation {
				step.Active = id
			}
		}
	}
	if r.Focus != nil {
		step.Focus = *r.Focus
	} else if step.Active != "" {
		e, _ := m.Event(step.Active)
		step.Focus = e.Nodes()
	}
	step.Zoom = r.Zoom
	if r.Detail != nil {
		step.Detail = *r.Detail
		for _, id := range step.Detail {
			if !slices.Contains(drawn, id) {
				return Step{}, nil, fmt.Errorf("shows the detail of %s, which is not drawn", id)
			}
		}
	} else if step.Active != "" {
		if e, _ := m.Event(step.Active); len(e.Fields) > 0 {
			step.Detail = []string{step.Active}
		}
	}
	return step, drawn, nil
}
