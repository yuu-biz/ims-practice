// Package book reads the chapters of the book under docs/book/, checks
// them, draws their figures from the scenes of the decks, and writes the
// chapters as a book of Zenn.
//
// A chapter is a Markdown file that a reader can read on the repository
// host as it is. The book adds no syntax of its own to it; it reads
// three patterns of plain Markdown:
//
//   - A figure is an image whose path is figures/<deck>.<scene>.<step>.svg:
//     the step of a scene of the deck of the same language, drawn whole.
//     The figure is a file of the repository, so that the chapter shows
//     it; the check fails when it is not the drawing of that step.
//   - An excerpt is a code block right below a paragraph that is nothing
//     but a link to a file, as in the decks: its lines have to be lines
//     of that file, one after another.
//   - A link to another chapter is a link to its file.
package book

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// Book is the book: its settings and its chapters in each language.
type Book struct {
	// Dir is docs/book, and Root the root of the repository.
	Dir, Root string
	Config    Config
	// Chapters holds the chapters of each language, in the order of
	// the book.
	Chapters map[string][]*Chapter
}

// Config is docs/book/book.yaml.
type Config struct {
	// Slug names the book on Zenn.
	Slug string `yaml:"slug"`
	// Title and Summary are given in each language.
	Title   map[string]string `yaml:"title"`
	Summary map[string]string `yaml:"summary"`
	Topics  []string          `yaml:"topics"`
	// Chapters are the names of the chapter files, without ".md", in
	// the order of the book. A chapter has the same name in every
	// language.
	Chapters []string `yaml:"chapters"`
}

// Languages are the languages of the book, each a directory of docs/book.
var Languages = []string{"ja", "en"}

// Chapter is one chapter in one language.
type Chapter struct {
	Path, Lang, Name, Title string
	Lines                   []string
	Figures                 []Figure
	Excerpts                []Excerpt
	Links                   []Link
}

// Figure is an image of a chapter that is a step of a scene.
type Figure struct {
	Line                      int
	Alt, Target               string
	DeckName, SceneID, StepID string
}

// Excerpt is a code block that quotes a file of the repository.
type Excerpt struct {
	Line int
	// File is the path of the file from the root of the repository.
	File, Info string
	Code       []string
}

// Link is a link of a chapter, other than a figure.
type Link struct {
	Line   int
	Target string
}

var (
	imagePattern  = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	bookSlug      = regexp.MustCompile(`^[0-9a-z_-]{12,50}$`)
	chapterName   = regexp.MustCompile(`^[0-9a-z_-]{1,50}$`)
	figureFile    = regexp.MustCompile(`^[0-9a-z-]+\.[0-9a-z-]+\.[0-9a-z-]+\.svg$`)
	linkPattern   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	onlyLink      = regexp.MustCompile(`^\[[^\]]+\]\(([^)\s#]+)\)$`)
	figurePattern = regexp.MustCompile(`^figures/([0-9a-z-]+)\.([0-9a-z-]+)\.([0-9a-z-]+)\.svg$`)
	// A fence opens a code block as Markdown has it: up to three spaces,
	// three or more backticks or tildes, and an info string of any kind,
	// such as "go" or Zenn's "go:main.go"; its first word is the
	// language. A backtick fence has no backtick in its info string.
	fencePattern = regexp.MustCompile("^ {0,3}(```+|~~~+)\\s*([^\\s`:]*)[^`]*$")
	inlineCode   = regexp.MustCompile("`[^`]*`")
)

// Load reads the book under dir, whose repository has its root at root.
// It returns the problems that keep the book from being read.
func Load(dir, root string) (*Book, []error) {
	b := &Book{Dir: dir, Root: root, Chapters: map[string][]*Chapter{}}
	text, err := os.ReadFile(filepath.Join(dir, "book.yaml"))
	if err != nil {
		return nil, []error{err}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(text))
	decoder.KnownFields(true)
	if err := decoder.Decode(&b.Config); err != nil {
		return nil, []error{fmt.Errorf("%s: %w", filepath.Join(dir, "book.yaml"), err)}
	}
	var problems []error
	// The names that Zenn takes: a book of 12 to 50 of a-z, 0-9, "-" and
	// "_", and chapters of the same letters, each once.
	if !bookSlug.MatchString(b.Config.Slug) {
		problems = append(problems, fmt.Errorf("%s: the slug %q is not 12 to 50 of a-z, 0-9, - and _", filepath.Join(dir, "book.yaml"), b.Config.Slug))
	}
	seen := map[string]bool{}
	for _, name := range b.Config.Chapters {
		if !chapterName.MatchString(name) || seen[name] {
			problems = append(problems, fmt.Errorf("%s: the chapter %q is not a name of a-z, 0-9, - and _ that comes once", filepath.Join(dir, "book.yaml"), name))
		}
		seen[name] = true
	}
	if len(problems) > 0 {
		return nil, problems
	}
	for _, lang := range Languages {
		for _, name := range b.Config.Chapters {
			c, err := readChapter(filepath.Join(dir, lang, name+".md"), lang, name, root)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			b.Chapters[lang] = append(b.Chapters[lang], c)
		}
		files, _ := filepath.Glob(filepath.Join(dir, lang, "*.md"))
		for _, f := range files {
			name := strings.TrimSuffix(filepath.Base(f), ".md")
			if name != "README" && !slices.Contains(b.Config.Chapters, name) {
				problems = append(problems, fmt.Errorf("%s is not a chapter of book.yaml", f))
			}
		}
	}
	return b, problems
}

// readChapter reads a chapter and finds its title, figures, excerpts
// and links.
func readChapter(path, lang, name, root string) (*Chapter, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Chapter{Path: path, Lang: lang, Name: name, Lines: strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")}
	fence, info := "", ""
	var block []string
	blockStart := 0
	excerptOf := "" // the file that the paragraph above the next block links to
	previous := ""  // the file of the excerpt right above, for a second block of it
	for i, line := range c.Lines {
		number := i + 1
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) && strings.Trim(strings.TrimSpace(line), fence[:1]) == "" {
				if excerptOf != "" {
					c.Excerpts = append(c.Excerpts, Excerpt{Line: blockStart, File: excerptOf, Info: info, Code: deck.CodeLines(strings.Join(block, "\n"))})
					previous = excerptOf
				}
				fence, block, excerptOf = "", nil, ""
				continue
			}
			block = append(block, line)
			continue
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil {
			fence, info, blockStart = m[1], m[2], number
			// A second block of the same file in the same language
			// continues the excerpt above it.
			if excerptOf == "" && previous != "" && len(c.Excerpts) > 0 && c.Excerpts[len(c.Excerpts)-1].Info == info {
				excerptOf = previous
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		previous = ""
		excerptOf = ""
		if m := onlyLink.FindStringSubmatch(strings.TrimSpace(line)); m != nil && !strings.Contains(m[1], "://") {
			excerptOf = repositoryPath(path, m[1], root)
		}
		if strings.HasPrefix(line, "# ") {
			if c.Title != "" {
				return nil, fmt.Errorf("%s:%d: a chapter has one title, the first heading of level 1", path, number)
			}
			c.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
		outside := inlineCode.ReplaceAllString(line, "")
		for _, m := range imagePattern.FindAllStringSubmatch(outside, -1) {
			f := Figure{Line: number, Alt: m[1], Target: m[2]}
			if fm := figurePattern.FindStringSubmatch(m[2]); fm != nil {
				f.DeckName, f.SceneID, f.StepID = fm[1], fm[2], fm[3]
			}
			c.Figures = append(c.Figures, f)
		}
		for _, m := range linkPattern.FindAllStringSubmatch(imagePattern.ReplaceAllString(outside, ""), -1) {
			c.Links = append(c.Links, Link{Line: number, Target: m[1]})
		}
	}
	if fence != "" {
		return nil, fmt.Errorf("%s:%d: a code block is not closed", path, blockStart)
	}
	if c.Title == "" {
		return nil, fmt.Errorf("%s: a chapter starts with its title, a heading of level 1", path)
	}
	return c, nil
}

// repositoryPath is the path from the root of the repository of a file
// that a chapter at path links to.
func repositoryPath(path, target, root string) string {
	abs, _ := filepath.Abs(filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
	rootAbs, _ := filepath.Abs(root)
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

// DeckPath is the source of the deck of a figure, from the root of the
// repository.
func DeckPath(lang, name string) string {
	return "slides/decks/" + lang + "/" + name + ".md"
}
