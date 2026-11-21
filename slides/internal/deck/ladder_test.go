package deck

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLadder(t *testing.T) {
	source := "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n## S\n\n```ladder\n" +
		"UE, P-CSCF, S-CSCF, HSS\n" +
		"UE -> P-CSCF -> S-CSCF: REGISTER // 1 - 3\n" +
		"S-CSCF <-> HSS: MAR, MAA\n" +
		"UE <-- P-CSCF <-- S-CSCF: 401 Unauthorized\n" +
		"note UE, HSS: registered: yes\n" +
		"```\n"
	d, err := Parse("x.md", source)
	if err != nil {
		t.Fatal(err)
	}
	l := d.Slides[1].Columns[0][0].(Ladder)
	want := []LadderRow{
		{Path: []int{0, 1, 2}, Label: "REGISTER", Remark: "1 - 3"},
		{Path: []int{2, 3}, Both: true, Label: "MAR, MAA"},
		{Path: []int{2, 1, 0}, Dashed: true, Label: "401 Unauthorized"},
		{Note: true, Path: []int{0, 3}, Label: "registered: yes"},
	}
	if !reflect.DeepEqual(l.Participants, []string{"UE", "P-CSCF", "S-CSCF", "HSS"}) || !reflect.DeepEqual(l.Rows, want) {
		t.Errorf("ladder = %+v", l)
	}
}

func TestParseLadderRejects(t *testing.T) {
	front := "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n## S\n\n```ladder\nA, B, C\n"
	tests := map[string]string{
		"A -> D: x\n":        `"D" is not a participant`,
		"A -> B --> C: x\n":  "one kind",
		"A <-> B <-> C: x\n": "joins two",
		"A B: x\n":           "has an arrow",
		"A -> B\n":           `"A -> B: label"`,
		"A -> A: x\n":        "from one participant to another",
		"B -> C -> A: x\n":   "turns back",
	}
	for row, message := range tests {
		_, err := Parse("x.md", front+row+"```\n")
		if err == nil || !strings.Contains(err.Error(), message) {
			t.Errorf("%q: error = %v, want one with %q", row, err, message)
		}
		if err != nil && !strings.Contains(err.Error(), "x.md:15:") {
			t.Errorf("%q: error = %v, want it on line 15", row, err)
		}
	}
}
