package deck

import (
	"fmt"
	"strings"
)

// CheckHeadings lists the headings of a Japanese deck, of a slide or of
// a step of a scene, that hold a full stop. A heading names what the
// slide shows; one that cuts a subject and a detail apart with a full
// stop, such as 「CAT。着信側が選ぶ呼び出し音」, reads as two sentences
// pressed together. Put the detail in parentheses, or say both in one
// phrase. Inline code is not read.
func CheckHeadings(d *Deck) []error {
	if d.Lang != "ja" {
		return nil
	}
	var problems []error
	stop := func(spans []Span) (string, bool) {
		var b strings.Builder
		for _, s := range spans {
			if !s.Code {
				b.WriteString(s.Text)
			}
		}
		text := strings.TrimRight(b.String(), "。")
		return PlainText(spans), strings.Contains(text, "。")
	}
	for _, slide := range d.Slides {
		if text, bad := stop(slide.Title); bad {
			problems = append(problems, fmt.Errorf("%s:%d: the heading %q has a full stop in it; name the subject in one phrase, with a detail in parentheses", d.Path, slide.Line, text))
		}
		if slide.Scene == nil {
			continue
		}
		for _, step := range slide.Scene.Steps {
			if text, bad := stop(step.Title); bad {
				problems = append(problems, fmt.Errorf("%s:%d: the title %q of step %s has a full stop in it; name the step in one phrase", d.Path, slide.Line, text, step.ID))
			}
		}
	}
	return problems
}
