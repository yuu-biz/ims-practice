package book

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/html"
)

// Check reports what is wrong with the chapters: a link to a file or a
// heading that does not exist, an excerpt that is not in its file, a
// figure that names no step of a scene or that is not the drawing of
// that step, a figure file that no chapter shows, a table of contents
// that leaves a chapter out, and two languages that do not show the
// same figures and the same code in the same order.
func Check(b *Book) []error {
	var problems []error
	decks := deckCache{root: b.Root}
	for _, lang := range Languages {
		shown := map[string]bool{}
		for _, c := range b.Chapters[lang] {
			problems = append(problems, checkLinks(c)...)
			problems = append(problems, checkExcerpts(c, b.Root)...)
			for _, f := range c.Figures {
				if err := checkFigure(c, f, &decks); err != nil {
					problems = append(problems, err)
				}
				shown[filepath.Base(f.Target)] = true
			}
		}
		files, _ := filepath.Glob(filepath.Join(b.Dir, lang, "figures", "*"))
		for _, f := range files {
			if !shown[filepath.Base(f)] {
				problems = append(problems, fmt.Errorf("%s: no chapter shows this figure; remove it", f))
			}
		}
		problems = append(problems, checkContents(b, lang)...)
	}
	problems = append(problems, checkTranslations(b)...)
	return problems
}

// checkLinks reports the links to a file or a heading that does not exist.
func checkLinks(c *Chapter) []error {
	var problems []error
	anchors := map[string]map[string]bool{}
	for _, l := range c.Links {
		if strings.Contains(l.Target, "://") || strings.HasPrefix(l.Target, "mailto:") {
			continue
		}
		target, fragment, _ := strings.Cut(l.Target, "#")
		file := c.Path
		if target != "" {
			file = filepath.Join(filepath.Dir(c.Path), filepath.FromSlash(target))
		}
		if _, err := os.Stat(file); err != nil {
			problems = append(problems, fmt.Errorf("%s:%d: %s does not exist", c.Path, l.Line, target))
			continue
		}
		if fragment == "" || !strings.HasSuffix(file, ".md") {
			continue
		}
		if anchors[file] == nil {
			found, err := deck.HeadingAnchors(file)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			anchors[file] = found
		}
		name, err := url.PathUnescape(fragment)
		if err != nil || !anchors[file][name] {
			problems = append(problems, fmt.Errorf("%s:%d: no heading #%s in %s", c.Path, l.Line, fragment, file))
		}
	}
	return problems
}

// checkExcerpts reports the excerpts that are not in their file, line
// for line.
func checkExcerpts(c *Chapter, root string) []error {
	var problems []error
	for _, e := range c.Excerpts {
		lines := deck.ReadLines(filepath.Join(root, filepath.FromSlash(e.File)))
		if lines == nil {
			continue // a link to a file that does not exist is reported with the links
		}
		if !deck.ContainsRun(lines, e.Code) {
			problems = append(problems, fmt.Errorf("%s:%d: the excerpt that starts %q is not in %s, line for line", c.Path, e.Line, firstLine(e.Code), e.File))
		}
	}
	return problems
}

func firstLine(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// checkFigure reports a figure that names no step of a scene, or whose
// file is not the drawing of that step as the deck is now.
func checkFigure(c *Chapter, f Figure, decks *deckCache) error {
	if f.DeckName == "" {
		return fmt.Errorf("%s:%d: the image %s is not a figure; a figure is figures/<deck>.<scene>.<step>.svg", c.Path, f.Line, f.Target)
	}
	if strings.TrimSpace(f.Alt) == "" {
		return fmt.Errorf("%s:%d: the figure %s has no text; say in it what the figure shows", c.Path, f.Line, f.Target)
	}
	want, err := Draw(decks, c.Lang, f)
	if err != nil {
		return fmt.Errorf("%s:%d: %w", c.Path, f.Line, err)
	}
	have, err := os.ReadFile(filepath.Join(filepath.Dir(c.Path), filepath.FromSlash(f.Target)))
	if err != nil || !bytes.Equal(have, want) {
		return fmt.Errorf("%s:%d: %s is not the drawing of the step as the deck is now; run \"go run ./cmd/slides figures\" in slides/", c.Path, f.Line, f.Target)
	}
	return nil
}

// Draw draws a figure from its deck.
func Draw(decks *deckCache, lang string, f Figure) ([]byte, error) {
	d, err := decks.get(lang, f.DeckName)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := html.WriteFigure(&out, d, f.SceneID, f.StepID); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// WriteFigures draws every figure of the book into its file, and removes
// the figure files that no chapter shows.
func WriteFigures(b *Book) ([]string, error) {
	decks := deckCache{root: b.Root}
	var written []string
	for _, lang := range Languages {
		dir := filepath.Join(b.Dir, lang, "figures")
		keep := map[string]bool{}
		for _, c := range b.Chapters[lang] {
			for _, f := range c.Figures {
				if f.DeckName == "" {
					continue
				}
				data, err := Draw(&decks, lang, f)
				if err != nil {
					return written, fmt.Errorf("%s:%d: %w", c.Path, f.Line, err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return written, err
				}
				path := filepath.Join(filepath.Dir(c.Path), filepath.FromSlash(f.Target))
				keep[filepath.Clean(path)] = true
				if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
					continue
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					return written, err
				}
				written = append(written, path)
			}
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*"))
		for _, f := range files {
			// Only a file named as a figure is one that this drew.
			if figureFile.MatchString(filepath.Base(f)) && !keep[filepath.Clean(f)] {
				if err := os.Remove(f); err != nil {
					return written, err
				}
				written = append(written, "removed "+f)
			}
		}
	}
	return written, nil
}

// checkContents reports a chapter that the table of contents of its
// language, README.md, does not link to.
func checkContents(b *Book, lang string) []error {
	readme := filepath.Join(b.Dir, lang, "README.md")
	c, err := readChapter(readme, lang, "README", b.Root)
	if err != nil {
		return []error{err}
	}
	problems := checkLinks(c)
	linked := map[string]bool{}
	for _, l := range c.Links {
		target, _, _ := strings.Cut(l.Target, "#")
		linked[target] = true
	}
	for _, name := range b.Config.Chapters {
		if !linked[name+".md"] {
			problems = append(problems, fmt.Errorf("%s does not link to the chapter %s.md", readme, name))
		}
	}
	return problems
}

// checkTranslations reports two languages of a chapter that do not show
// the same figures and quote the same code in the same order: the words
// are free to differ, what they explain is not.
func checkTranslations(b *Book) []error {
	var problems []error
	first := Languages[0]
	for i, c := range b.Chapters[first] {
		for _, lang := range Languages[1:] {
			if i >= len(b.Chapters[lang]) {
				continue
			}
			o := b.Chapters[lang][i]
			if a, z := figureNames(c), figureNames(o); !slices.Equal(a, z) {
				problems = append(problems, fmt.Errorf("%s and %s show different figures:\n  %s\n  %s", c.Path, o.Path, strings.Join(a, " "), strings.Join(z, " ")))
			}
			if a, z := excerptKeys(c), excerptKeys(o); !slices.Equal(a, z) {
				problems = append(problems, fmt.Errorf("%s and %s quote different code:\n  %s\n  %s", c.Path, o.Path, strings.Join(a, " | "), strings.Join(z, " | ")))
			}
		}
	}
	return problems
}

func figureNames(c *Chapter) []string {
	var out []string
	for _, f := range c.Figures {
		out = append(out, filepath.Base(f.Target))
	}
	return out
}

func excerptKeys(c *Chapter) []string {
	var out []string
	for _, e := range c.Excerpts {
		out = append(out, e.File+": "+firstLine(e.Code)+fmt.Sprintf(" (%d lines)", len(e.Code)))
	}
	return out
}

// deckCache reads each deck once.
type deckCache struct {
	root  string
	decks map[string]*deck.Deck
}

func (c *deckCache) get(lang, name string) (*deck.Deck, error) {
	path := DeckPath(lang, name)
	if d, ok := c.decks[path]; ok {
		return d, nil
	}
	d, err := deck.ReadFile(filepath.Join(c.root, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	if c.decks == nil {
		c.decks = map[string]*deck.Deck{}
	}
	c.decks[path] = d
	return d, nil
}
