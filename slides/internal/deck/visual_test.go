package deck

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// visualSample is a small visual deck: two nodes, a request and its
// answer, a process, and a scene that steps through them.
const visualSample = "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n" +
	"```ims-model\n" +
	"nodes:\n" +
	"  - {id: a, label: A, role: the first, at: [0, 0]}\n" +
	"  - {id: b, label: B, role: the second, at: [1, 0]}\n" +
	"events:\n" +
	"  - {id: req, msg: REQ, from: a, to: b, proto: SIP, fields: [{Request-URI: \"`sip:b`\"}]}\n" +
	"  - {id: work, do: thinks, at: b}\n" +
	"  - {id: ans, msg: 200 OK, from: b, to: a, proto: SIP}\n" +
	"```\n\n---\n\n# T\n\n---\n\n## A scene\n\n" +
	"```ims-scene\n" +
	"id: flow\n" +
	"steps:\n" +
	"  - {id: send, title: Send, show: [req], text: A sends.}\n" +
	"  - {id: think, title: Think, show: [work], zoom: [b], text: B thinks.}\n" +
	"  - {id: answer, title: Answer, hide: [work], show: [ans], text: B answers.}\n" +
	"  - {id: again, title: Again, only: [req], active: '', text: From the start.}\n" +
	"```\n\nThe longer explanation, with [a link](guide.md).\n"

func TestStepsAreComplete(t *testing.T) {
	d, err := Parse("visual.md", visualSample)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Slides) != 2 || d.Slides[1].Scene == nil || d.Slides[1].Layout != LayoutScene {
		t.Fatalf("slides = %+v", d.Slides)
	}
	steps := d.Slides[1].Scene.Steps
	type state struct {
		events []string
		active string
		focus  []string
		detail []string
	}
	want := []state{
		{[]string{"req"}, "req", []string{"a", "b"}, []string{"req"}},
		{[]string{"req", "work"}, "work", []string{"b"}, nil},
		{[]string{"req", "ans"}, "ans", []string{"b", "a"}, nil},
		{[]string{"req"}, "", nil, nil},
	}
	for i, st := range steps {
		got := state{st.Events, st.Active, st.Focus, st.Detail}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("step %s = %+v, want %+v", st.ID, got, want[i])
		}
	}
	if !steps[3].Checkpoint || steps[0].Checkpoint {
		t.Error("the last step, and only it, is a checkpoint")
	}
	if got := d.Slides[1].Scene.Nodes; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("a map draws every node by default, got %v", got)
	}
	if got := d.Slides[1].Scene.FirstActive(); got["req"] != 1 || got["ans"] != 3 {
		t.Errorf("FirstActive = %v", got)
	}
}

func TestVisualMistakesFail(t *testing.T) {
	cases := map[string][2]string{
		"unknown key":          {"id: flow\n", "id: flow\nzom: [b]\n"},
		"unknown event":        {"show: [ans]", "show: [nosuch]"},
		"shown twice":          {"only: [req]", "only: [req, req]"},
		"hidden but not shown": {"hide: [work]", "hide: [ans]"},
		"active not drawn":     {"active: ''", "active: ans"},
		"no title":             {"title: Send, ", ""},
		"bad id":               {"id: flow\n", "id: Flow\n"},
		"node not drawn":       {"id: flow\n", "id: flow\nnodes: [a]\n"},
		"two kinds":            {"{id: work, do: thinks, at: b}", "{id: work, do: thinks, msg: X, at: b}"},
		"message to itself":    {"from: b, to: a", "from: b, to: b"},
		"same id":              {"{id: work,", "{id: req,"},
		"scene outside":        {"format: visual\n", ""},
		"scene in the body":    {"\nThe longer explanation", "\n```ims-scene\nid: x\n```\n\nThe longer explanation"},
	}
	for name, c := range cases {
		source := strings.Replace(visualSample, c[0], c[1], 1)
		if source == visualSample {
			t.Fatalf("%s: the sample has no %q", name, c[0])
		}
		if _, err := Parse("visual.md", source); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestYAMLErrorNamesTheLineOfTheDeck(t *testing.T) {
	source := strings.Replace(visualSample, "  - {id: think,", "  - {id: think: oops,", 1)
	_, err := Parse("visual.md", source)
	if err == nil {
		t.Fatal("no error")
	}
	line := 0
	for i, l := range strings.Split(source, "\n") {
		if strings.Contains(l, "oops") {
			line = i + 1
		}
	}
	if e, ok := err.(*Error); !ok || e.Line < line-1 || e.Line > line+1 {
		t.Errorf("error %v, want one near line %d", err, line)
	}
}

// TestChecksReadTheScenes makes sure that the text of a scene goes
// through the checks as the text of a slide does: a link in the text of
// a step, in a field of an event and in the longer explanation.
func TestChecksReadTheScenes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte("# Guide\n\n## A step\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(visualSample, "text: A sends.", "text: '[step](guide.md#no-step)'", 1)
	source = strings.Replace(source, "\"`sip:b`\"", "'[field](missing.md)'", 1)
	source = strings.Replace(source, "[a link](guide.md)", "[a link](guide.md#a-step)", 1)
	d, err := Parse(filepath.Join(dir, "deck.md"), source)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range CheckLinks(d) {
		got = append(got, p.Error())
	}
	if len(got) != 2 || !strings.Contains(strings.Join(got, "\n"), "#no-step") || !strings.Contains(strings.Join(got, "\n"), "missing.md") {
		t.Errorf("problems = %v, want the link of the step and the link of the field", got)
	}
}
