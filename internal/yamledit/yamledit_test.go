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
	} {
		if _, err := Apply([]byte(src), []Change{c, {Key: "a.b", Value: "y"}}); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}
