package deck

import (
	"strings"
	"testing"
)

// TestHeadingsHaveNoFullStop makes sure that a Japanese heading, of a
// slide or of a step, that cuts its subject apart with a full stop
// fails, and that one in inline code or in an English deck does not.
func TestHeadingsHaveNoFullStop(t *testing.T) {
	source := func(lang, heading, step string) string {
		return "---\ntitle: T\nfooter: F\nlang: " + lang + "\nformat: visual\n---\n\n```ims-model\nnodes:\n" +
			"  - {id: a, label: A, at: [0, 0]}\n  - {id: b, label: B, at: [1, 0]}\nevents:\n" +
			"  - {id: req, msg: REQ, from: a, to: b, proto: SIP}\n```\n\n---\n\n# T\n\n---\n\n## " + heading + "\n\n" +
			"```ims-scene\nid: s\nsteps:\n  - {id: x, title: " + step + ", show: [req], text: Y}\n```\n"
	}
	for _, c := range []struct {
		lang, heading, step string
		want                int
	}{
		{"ja", "CAT（着信側が選ぶ呼び出し音）", "UE 2 が呼び出しを始める", 0},
		{"ja", "CAT。着信側が選ぶ呼び出し音", "UE 2 が呼び出しを始める", 1},
		{"ja", "呼び出し音", "話中。UE 2 が 486 で断る", 1},
		{"ja", "`a。b` を読む", "読む", 0},
		{"en", "CAT. The tone", "Alerting. Then", 0},
	} {
		d, err := Parse(c.lang+".md", source(c.lang, c.heading, c.step))
		if err != nil {
			t.Fatal(err)
		}
		if got := CheckHeadings(d); len(got) != c.want {
			t.Errorf("%s %q %q: %d problems, want %d: %v", c.lang, c.heading, c.step, len(got), c.want, got)
		} else if c.want > 0 && !strings.Contains(got[0].Error(), "full stop") {
			t.Errorf("the problem does not say why: %v", got[0])
		}
	}
}
