package yamledit

import (
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	const src = `# Top.
node: x
a:
  # Old comment
  # of b.
  b: 172.28.0.1
  c: [172.28.0.2]
  d:
    e: 1
  items:
    - name: one
      url: http://172.28.0.3
list:
- 1
- 2
# Head of z.
z: last
`
	tests := []struct {
		name    string
		changes []Change
		want    string
	}{
		{"value of a scalar", []Change{{Key: "a.b", Value: "b.svc"}},
			"  b: b.svc\n"},
		{"comment replaced", []Change{{Key: "a.b", Comment: "New."}},
			"a:\n  # New.\n  b: 172.28.0.1\n"},
		{"comment inserted with a value", []Change{{Key: "a.c", Comment: "Two\nlines.", Value: []string{"${IP}"}}},
			"  b: 172.28.0.1\n  # Two\n  # lines.\n  c:\n    - ${IP}\n  d:\n"},
		{"block mapping removed", []Change{{Key: "a.d", Remove: true, Comment: "No d."}},
			"  c: [172.28.0.2]\n  # No d.\n  items:\n"},
		{"key in an item", []Change{{Key: "a.items.0.url", Value: "http://one.svc"}},
			"    - name: one\n      url: http://one.svc\nlist:\n"},
		{"compact sequence removed", []Change{{Key: "list", Remove: true}},
			"      url: http://172.28.0.3\n# Head of z.\nz: last\n"},
		{"key added after the last key of a mapping", []Change{{Key: "a.f", Add: true, Comment: "New f.", Value: "x"}},
			"      url: http://172.28.0.3\n  # New f.\n  f: x\nlist:\n"},
		{"two changes", []Change{{Key: "a.b", Value: "b.svc"}, {Key: "z", Comment: "Z."}},
			"  b: b.svc\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Apply([]byte(src), tt.changes)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("got\n%s\nwant it to contain\n%s", out, tt.want)
			}
			if !strings.HasPrefix(string(out), "# Top.\nnode: x\n") {
				t.Errorf("the lines before the change moved:\n%s", out)
			}
		})
	}

	for _, c := range []Change{
		{Key: "a.nothing", Value: "x"},
		{Key: "a.items.0", Value: "x"},
		{Key: "a.items.0.name", Comment: "The first key of an item"},
		{Key: "a.b", Remove: true, Value: "x", Comment: "x"},
		{Key: "a.b", Add: true, Value: "x"},
		{Key: "a.g", Add: true},
		{Key: "nothing.g", Add: true, Value: "x"},
		{Key: "g", Add: true, Value: "x"},
		{Key: "a.items.0.extra", Add: true, Value: "y"},
	} {
		if _, err := Apply([]byte(src), []Change{c, {Key: "a.b", Value: "y"}}); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

// Two keys added at the end of one mapping, or of a mapping and of its
// last key, would go to the same line. No order is promised, so Apply
// refuses. A mapping in flow style has no line to add to.
func TestApplyRefusesAmbiguousAdds(t *testing.T) {
	const src = "p:\n  q:\n    r: 1\nflow: {a: 1}\n"
	for name, cs := range map[string][]Change{
		"one mapping":       {{Key: "p.x", Add: true, Value: "1"}, {Key: "p.y", Add: true, Value: "2"}},
		"nested and parent": {{Key: "p.q.y", Add: true, Value: "1"}, {Key: "p.y", Add: true, Value: "2"}},
		"flow style":        {{Key: "flow.b", Add: true, Value: "2"}},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := Apply([]byte(src), cs); err == nil {
				t.Errorf("no error:\n%s", out)
			} else {
				t.Log(err)
			}
		})
	}
}
