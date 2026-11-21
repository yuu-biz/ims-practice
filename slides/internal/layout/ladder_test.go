package layout

import (
	"strings"
	"testing"
)

func TestLadderIsShapes(t *testing.T) {
	d := parse(t, "## Ladder\n\n```ladder\nUE, P-CSCF, S-CSCF\n"+
		"UE -> P-CSCF -> S-CSCF: REGISTER // 1\n"+
		"UE <-- P-CSCF: 401\n"+
		"P-CSCF <-> S-CSCF: AAR, AAA\n"+
		"note UE, S-CSCF: registered\n```\n")
	pages, problems := Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var group *Group
	for _, e := range pages[1].Elements {
		if g, ok := e.(*Group); ok {
			group = g
		}
	}
	if group == nil {
		t.Fatal("no group")
	}
	var lines []*Line
	var texts []string
	for _, e := range group.Elements {
		x, y, w, h := bounds(e)
		if x < group.X-0.01 || y < group.Y-0.01 || x+w > group.X+group.W+0.01 || y+h > group.Y+group.H+0.01 {
			t.Errorf("%T at %.0f,%.0f %.0fx%.0f is outside the group", e, x, y, w, h)
		}
		switch e := e.(type) {
		case *Line:
			lines = append(lines, e)
		case *TextBox:
			texts = append(texts, e.Paragraphs[0].Runs[0].Text)
		}
	}
	// Three lifelines, two segments of the REGISTER, one 401 and one pair.
	if len(lines) != 7 {
		t.Fatalf("%d lines, want 7", len(lines))
	}
	register, unauthorized, pair := lines[3], lines[5], lines[6]
	if !(register.X2 > register.X1) || register.Dash || !register.HeadEnd || register.HeadStart {
		t.Errorf("REGISTER = %+v", register)
	}
	if !(unauthorized.X2 < unauthorized.X1) || !unauthorized.Dash {
		t.Errorf("401 = %+v", unauthorized)
	}
	if !pair.HeadStart || !pair.HeadEnd {
		t.Errorf("AAR, AAA = %+v", pair)
	}
	if got := strings.Join(texts, ","); got != "UE,P-CSCF,S-CSCF,REGISTER,1,401,AAR, AAA,registered" {
		t.Errorf("texts = %s", got)
	}
}

func TestLadderLabelTooWideFails(t *testing.T) {
	d := parse(t, "## Ladder\n\n```ladder\nA, B\nnote A: "+strings.Repeat("wide ", 20)+"\n```\n")
	if _, problems := Deck(d); len(problems) != 1 || !strings.Contains(problems[0].Error(), "wider than the participants") {
		t.Errorf("problems = %v", problems)
	}
}
