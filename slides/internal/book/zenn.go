package book

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// blobURL is the address of the tree of the repository at a commit, as
// -repo-url gives it.
var blobURL = regexp.MustCompile(`^https://[^/]+/[^/]+/[^/]+/blob/([0-9a-f]{7,40})$`)

// Zenn writes the chapters of one language as a book of Zenn under out:
// books/<slug>/config.yaml and a file for each chapter, and the figures
// under images/<slug>/. The book is not published: config.yaml says
// published: false, and nothing is sent anywhere.
//
// Zenn reads PNG, not SVG, so the figures are written as SVG next to the
// PNG name that the chapters point at, for book-images.js to draw; it
// also draws the cover from cover.svg.
//
// Links to other chapters become links to them in the book. Links to
// other files of the repository become links to them at the commit of
// repository, an address such as
// https://github.com/<owner>/<repository>/blob/<commit>, so that the
// book points at the code that it quotes. The excerpts are checked
// against the files at that commit, not against the working tree.
func Zenn(b *Book, lang, out, repository string) ([]string, error) {
	m := blobURL.FindStringSubmatch(repository)
	if m == nil {
		return nil, fmt.Errorf("-repo-url is the address of the tree at a commit, such as https://github.com/<owner>/<repository>/blob/<commit>; it is %q", repository)
	}
	commit := m[1]
	chapters := b.Chapters[lang]
	if len(chapters) == 0 {
		return nil, fmt.Errorf("the book has no chapters in %q", lang)
	}
	var problems []string
	for _, c := range chapters {
		for _, e := range c.Excerpts {
			text, err := exec.Command("git", "-C", b.Root, "show", commit+":"+e.File).Output()
			if err != nil {
				return nil, fmt.Errorf("cannot read %s at %s: %w", e.File, commit, err)
			}
			if !deck.ContainsRun(deck.CodeLines(string(text)), e.Code) {
				problems = append(problems, fmt.Sprintf("%s:%d: the excerpt that starts %q is not in %s at %s", c.Path, e.Line, firstLine(e.Code), e.File, commit))
			}
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	slug := b.Config.Slug
	if lang != Languages[0] {
		slug += "-" + lang
	}
	bookDir := filepath.Join(out, "books", slug)
	imageDir := filepath.Join(out, "images", slug)
	for _, dir := range []string{bookDir, imageDir} {
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	var written []string
	write := func(p, text string) error {
		written = append(written, p)
		return os.WriteFile(p, []byte(text), 0o644)
	}
	var config strings.Builder
	fmt.Fprintf(&config, "title: %s\nsummary: %s\ntopics: [%s]\npublished: false\nprice: 0\nchapters:\n",
		strconv.Quote(b.Config.Title[lang]), strconv.Quote(b.Config.Summary[lang]), strings.Join(b.Config.Topics, ", "))
	for _, c := range chapters {
		fmt.Fprintf(&config, "  - %s\n", c.Name)
	}
	if err := write(filepath.Join(bookDir, "config.yaml"), config.String()); err != nil {
		return nil, err
	}
	if err := write(filepath.Join(bookDir, "cover.svg"), cover(b.Config.Title[lang])); err != nil {
		return nil, err
	}
	decks := deckCache{root: b.Root}
	names := map[string]bool{}
	for _, c := range chapters {
		names[c.Name] = true
	}
	for _, c := range chapters {
		for _, f := range c.Figures {
			data, err := Draw(&decks, lang, f)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", c.Path, f.Line, err)
			}
			if err := write(filepath.Join(imageDir, filepath.Base(f.Target)), string(data)); err != nil {
				return nil, err
			}
		}
		text := zennChapter(b, c, slug, repository, names)
		if err := write(filepath.Join(bookDir, c.Name+".md"), text); err != nil {
			return nil, err
		}
	}
	return written, nil
}

// zennChapter is the text of a chapter for Zenn: its title in the front
// matter instead of the heading, and its links made to work in the book.
func zennChapter(b *Book, c *Chapter, slug, repository string, chapters map[string]bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "---\ntitle: %s\n---\n", strconv.Quote(c.Title))
	fence := ""
	titled := false
	for _, line := range c.Lines {
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) && strings.Trim(strings.TrimSpace(line), fence[:1]) == "" {
				fence = ""
			}
			out.WriteString(line + "\n")
			continue
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil {
			fence = m[1]
			out.WriteString(line + "\n")
			continue
		}
		if !titled && strings.HasPrefix(line, "# ") {
			titled = true
			continue
		}
		out.WriteString(rewriteLinks(line, func(target string, image bool) string {
			if image {
				if fm := figurePattern.FindStringSubmatch(target); fm != nil {
					return "/images/" + slug + "/" + strings.TrimSuffix(path.Base(target), ".svg") + ".png"
				}
			}
			return zennTarget(b, c, target, repository, chapters)
		}) + "\n")
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}

// zennTarget is where a link of a chapter points in the book.
func zennTarget(b *Book, c *Chapter, target, repository string, chapters map[string]bool) string {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
		return target
	}
	file, fragment, _ := strings.Cut(target, "#")
	if fragment != "" {
		fragment = "#" + fragment
	}
	if name := strings.TrimSuffix(file, ".md"); !strings.Contains(file, "/") && chapters[name] {
		return name + fragment
	}
	return strings.TrimRight(repository, "/") + "/" + repositoryPath(c.Path, file, b.Root) + fragment
}

// rewriteLinks replaces the target of each link and image of a line. A
// link that starts inside inline code is text that the reader sees as
// it is, and stays; a link whose text has code in it is a link.
func rewriteLinks(line string, to func(target string, image bool) string) string {
	spans := inlineCodeSpan.FindAllStringIndex(line, -1)
	var out strings.Builder
	last := 0
	for _, m := range anyLink.FindAllStringSubmatchIndex(line, -1) {
		inCode := false
		for _, s := range spans {
			inCode = inCode || (s[0] < m[0] && m[0] < s[1])
		}
		if inCode {
			continue
		}
		out.WriteString(line[last:m[0]])
		out.WriteString(line[m[2]:m[3]] + "[" + line[m[4]:m[5]] + "](" + to(line[m[6]:m[7]], m[3] > m[2]) + ")")
		last = m[1]
	}
	out.WriteString(line[last:])
	return out.String()
}

var inlineCodeSpan = regexp.MustCompile("(`+)[^`].*?(`+)")

var anyLink = regexp.MustCompile(`(!?)\[([^\]]*)\]\(([^)\s]+)\)`)

// cover is a plain cover of the book, 500 × 700 as Zenn asks: the title
// and nothing else.
func cover(title string) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="500" height="700" viewBox="0 0 500 700">`)
	b.WriteString(`<rect width="500" height="700" fill="#F6F5F1"/><rect x="0" y="0" width="500" height="14" fill="#2F6FA3"/>`)
	words := strings.Fields(title)
	if len(words) <= 1 {
		words = splitRunes(title, 10)
	}
	var lines []string
	cur := ""
	for _, w := range words {
		if len([]rune(cur+w)) > 12 && cur != "" {
			lines = append(lines, strings.TrimSpace(cur))
			cur = ""
		}
		cur += w + " "
	}
	lines = append(lines, strings.TrimSpace(cur))
	lines = slices.DeleteFunc(lines, func(s string) bool { return s == "" })
	for i, l := range lines {
		fmt.Fprintf(&b, `<text x="40" y="%d" font-size="34" font-weight="bold" fill="#1D2B36" font-family="Noto Sans CJK JP, sans-serif">%s</text>`, 220+i*52, html.EscapeString(l))
	}
	b.WriteString(`<text x="40" y="640" font-size="18" fill="#5C6670" font-family="Noto Sans CJK JP, sans-serif">ims-practice</text></svg>`)
	return b.String()
}

func splitRunes(s string, n int) []string {
	r := []rune(s)
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return append(out, string(r))
}
