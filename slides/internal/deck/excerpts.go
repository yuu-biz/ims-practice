package deck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CheckExcerpts reports the excerpts of a deck that are no longer in the
// file they name. An excerpt is a code block right below a paragraph that
// is nothing but a link to a file, or right below another excerpt of the
// same file in the same language, such as two "go" blocks; a block in
// another language, such as the "sh" command that runs the excerpt, ends
// the excerpts of the file. The lines of an excerpt have to be lines of
// that file, one after another, as they are. A code change that the deck
// did not follow would otherwise leave the slide showing code that is
// not there.
//
// A link to a file that does not exist is left to CheckLinks.
func CheckExcerpts(d *Deck) []error {
	var problems []error
	files := map[string][]string{}
	for _, slide := range d.Slides {
		for _, column := range slide.Columns {
			target, info := "", ""
			for _, b := range column {
				switch b := b.(type) {
				case Paragraph:
					target, info = linkedFile(b), ""
				case Code:
					if target == "" || (info != "" && b.Info != info) {
						target = ""
						continue
					}
					info = b.Info
					file := filepath.Join(filepath.Dir(d.Path), filepath.FromSlash(target))
					lines, ok := files[file]
					if !ok {
						lines = readLines(file)
						files[file] = lines
					}
					if lines != nil && !contains(lines, b.Lines) {
						problems = append(problems, fmt.Errorf("%s: slide %d: the excerpt that starts %q is not in %s, line for line", d.Path, slide.Number, firstLine(b.Lines), target))
					}
				default:
					target, info = "", ""
				}
			}
		}
	}
	return problems
}

// linkedFile returns the file that a paragraph of one link names, or ""
// for any other paragraph.
func linkedFile(p Paragraph) string {
	if len(p.Lines) != 1 || len(p.Lines[0]) != 1 {
		return ""
	}
	href := p.Lines[0][0].Href
	if href == "" || strings.Contains(href, "://") || strings.Contains(href, "#") {
		return ""
	}
	return href
}

// readLines returns the lines of a file the way the parser keeps the
// lines of a code block: tabs as four spaces, without trailing blanks.
// It returns nil for a file that cannot be read.
func readLines(file string) []string {
	text, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(strings.TrimRight(l, " \t"), "\t", "    ")
	}
	return lines
}

// contains reports whether part is a run of consecutive lines of whole.
func contains(whole, part []string) bool {
	if len(part) == 0 {
		return true
	}
	for i := 0; i+len(part) <= len(whole); i++ {
		if slices.Equal(whole[i:i+len(part)], part) {
			return true
		}
	}
	return false
}

func firstLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}
