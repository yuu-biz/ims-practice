package deck

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CheckLinks reports the links of a deck to a file that does not exist,
// and to a heading of a Markdown file that does not exist. The decks
// point at the steps of the guides by heading, and a renamed heading
// would break such a link without a sound.
func CheckLinks(d *Deck) []error {
	var problems []error
	anchors := map[string]map[string]bool{}
	for _, slide := range d.Slides {
		for _, href := range Links(slide) {
			if strings.Contains(href, "://") || strings.HasPrefix(href, "mailto:") {
				continue
			}
			target, fragment, _ := strings.Cut(href, "#")
			file := filepath.Join(filepath.Dir(d.Path), filepath.FromSlash(target))
			if _, err := os.Stat(file); err != nil {
				problems = append(problems, fmt.Errorf("%s: slide %d: %s does not exist", d.Path, slide.Number, target))
				continue
			}
			if fragment == "" || !strings.HasSuffix(target, ".md") {
				continue
			}
			if anchors[file] == nil {
				found, err := headingAnchors(file)
				if err != nil {
					problems = append(problems, err)
					continue
				}
				anchors[file] = found
			}
			name, err := url.PathUnescape(fragment)
			if err != nil || !anchors[file][name] {
				problems = append(problems, fmt.Errorf("%s: slide %d: no heading #%s in %s", d.Path, slide.Number, fragment, target))
			}
		}
	}
	return problems
}

// Links returns the addresses that a slide links to, in order.
func Links(slide Slide) []string {
	var links []string
	for _, spans := range Lines(slide) {
		for _, span := range spans {
			if span.Href != "" {
				links = append(links, span.Href)
			}
		}
	}
	return links
}

// Lines returns every line of text of a slide, in order: the title and
// the lines of its paragraphs, quotes, list items and table cells. Code
// blocks and ladder diagrams are not among them.
func Lines(slide Slide) [][]Span {
	lines := [][]Span{slide.Title}
	for _, column := range slide.Columns {
		for _, b := range column {
			switch b := b.(type) {
			case Paragraph:
				lines = append(lines, b.Lines...)
			case Quote:
				lines = append(lines, b.Lines...)
			case List:
				for _, item := range b.Items {
					lines = append(lines, item.Lines...)
					if item.Children != nil {
						for _, child := range item.Children.Items {
							lines = append(lines, child.Lines...)
						}
					}
				}
			case Table:
				lines = append(lines, cellLines(b.Header)...)
				for _, row := range b.Rows {
					lines = append(lines, cellLines(row)...)
				}
			}
		}
	}
	return lines
}

func cellLines(cells []Cell) [][]Span {
	lines := make([][]Span, len(cells))
	for i, c := range cells {
		lines[i] = c
	}
	return lines
}

var (
	headingLine = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	notInAnchor = regexp.MustCompile(`[^\p{L}\p{M}\p{N}\-_ ]`)
)

// headingAnchors returns the anchors that the repository host gives the
// headings of a Markdown file: lower case, without anything but letters,
// marks, digits, "-", "_" and spaces, and with each space turned into
// "-". A second heading with the same anchor gets "-1", a third "-2".
func headingAnchors(file string) (map[string]bool, error) {
	text, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	seen := map[string]int{}
	anchors := map[string]bool{}
	fenced := false
	for _, l := range strings.Split(string(text), "\n") {
		// A guide indents the fences inside its lists.
		if trimmed := strings.TrimLeft(l, " \t"); strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
		}
		m := headingLine.FindStringSubmatch(strings.TrimRight(l, "\r"))
		if fenced || m == nil {
			continue
		}
		slug := strings.ReplaceAll(notInAnchor.ReplaceAllString(strings.ToLower(strings.TrimSpace(m[1])), ""), " ", "-")
		count := seen[slug]
		seen[slug] = count + 1
		if count == 0 {
			anchors[slug] = true
		} else {
			anchors[fmt.Sprintf("%s-%d", slug, count)] = true
		}
	}
	return anchors, nil
}
