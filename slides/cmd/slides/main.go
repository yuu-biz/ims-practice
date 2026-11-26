// Command slides builds the study-session decks: it reads each source
// under decks/<language>/, lays out its slides, checks them, and writes a
// pptx file and an HTML file for each deck. Run it in the directory slides/:
//
//	go run ./cmd/slides check   # parse, lay out and check every deck
//	go run ./cmd/slides build   # check, then write dist/<language>/
//
// The format of a deck and the checks are described in slides/README.md.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/facts"
	"github.com/yuu-biz/ims-practice/slides/internal/html"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
	"github.com/yuu-biz/ims-practice/slides/internal/pptx"
)

func main() {
	flags := flag.NewFlagSet("slides", flag.ExitOnError)
	decks := flags.String("decks", "decks", "the directory of the deck sources, one subdirectory per language")
	out := flags.String("out", "dist", "the directory that build writes to, one subdirectory per language")
	repository := flags.String("repo-url", "", "the address of the repository tree, such as https://<host>/<owner>/<repository>/blob/<commit>;\n"+
		"build makes the links to the guides absolute with it, for decks that are opened outside a checkout")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "usage: slides [flags] check|build\n")
		flags.PrintDefaults()
	}
	flags.Parse(os.Args[1:])
	if flags.NArg() != 1 || (flags.Arg(0) != "check" && flags.Arg(0) != "build") {
		flags.Usage()
		os.Exit(2)
	}
	all, problems := load(*decks)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		fmt.Fprintf(os.Stderr, "%d problems\n", len(problems))
		os.Exit(1)
	}
	if flags.Arg(0) == "check" {
		for _, d := range all {
			fmt.Println(summary(d))
		}
		fmt.Printf("%d decks: every slide parses, fits, links to what exists, quotes code that is in its file, names commands that the repository takes, and gives the nodes the addresses of the Compose files\n", len(all))
		return
	}
	for _, d := range all {
		if *repository != "" {
			absoluteLinks(d.pages, *repository)
		}
		written, err := build(d, *out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(strings.Join(written, "\n"))
	}
}

// build writes the outputs of one deck and returns their paths.
func build(d laidOut, out string) ([]string, error) {
	dir := filepath.Join(out, d.deck.Lang)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(d.deck.Path), ".md")
	writers := []struct {
		extension string
		write     func(io.Writer, *deck.Deck, []layout.Page) error
	}{
		{".pptx", pptx.Write},
		{".html", html.Write},
	}
	var written []string
	for _, w := range writers {
		path := filepath.Join(dir, name+w.extension)
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		if err := w.write(f, d.deck, d.pages); err != nil {
			f.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	return written, nil
}

// summary names the slides of a deck whose text was made smaller to
// fit, so that an author sees which slides are full.
func summary(d laidOut) string {
	var shrunk []string
	for _, page := range d.pages {
		if page.Scale < 1 {
			shrunk = append(shrunk, fmt.Sprintf("%d (%.0f%%)", page.Number, page.Scale*100))
		}
	}
	line := fmt.Sprintf("%s: %d slides", d.deck.Path, len(d.pages))
	if len(shrunk) > 0 {
		line += ", smaller text on " + strings.Join(shrunk, ", ")
	}
	return line
}

// laidOut is a deck with the pages of its slides.
type laidOut struct {
	deck  *deck.Deck
	pages []layout.Page
}

// load reads every deck under dir, lays it out and checks it. It returns
// the decks in the order of their paths and every problem found.
func load(dir string) ([]laidOut, []error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.md"))
	if err != nil {
		return nil, []error{err}
	}
	if len(paths) == 0 {
		return nil, []error{fmt.Errorf("no deck under %s", dir)}
	}
	slices.Sort(paths)
	// The decks are in slides/decks, two levels below the root of the
	// repository, whose commands they name.
	repository := facts.NewRepository(filepath.Join(dir, "..", ".."))
	var out []laidOut
	var problems []error
	for _, path := range paths {
		d, err := deck.ReadFile(path)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if language := filepath.Base(filepath.Dir(path)); d.Lang != language {
			problems = append(problems, fmt.Errorf("%s: lang is %s, but the deck is under %s/", path, d.Lang, language))
		}
		pages, overflows := layout.Deck(d)
		problems = append(problems, overflows...)
		problems = append(problems, deck.CheckLinks(d)...)
		problems = append(problems, deck.CheckExcerpts(d)...)
		problems = append(problems, repository.Check(d)...)
		out = append(out, laidOut{deck: d, pages: pages})
	}
	problems = append(problems, checkTranslations(out)...)
	return out, problems
}

// checkTranslations makes sure that each deck exists in every language,
// with the same slides in the same order: the same number of slides and
// the same layout on each.
func checkTranslations(decks []laidOut) []error {
	byName := map[string][]*deck.Deck{}
	languages := map[string]bool{}
	for _, d := range decks {
		name := filepath.Base(d.deck.Path)
		byName[name] = append(byName[name], d.deck)
		languages[d.deck.Lang] = true
	}
	var problems []error
	for name, versions := range byName {
		if len(versions) != len(languages) {
			problems = append(problems, fmt.Errorf("%s exists in %d of the %d languages", name, len(versions), len(languages)))
			continue
		}
		first := versions[0]
		for _, other := range versions[1:] {
			if a, b := layoutsOf(first), layoutsOf(other); a != b {
				problems = append(problems, fmt.Errorf("%s and %s differ in their slides:\n  %s\n  %s", first.Path, other.Path, a, b))
			}
		}
	}
	slices.SortFunc(problems, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return problems
}

func layoutsOf(d *deck.Deck) string {
	names := make([]string, len(d.Slides))
	for i, s := range d.Slides {
		names[i] = string(s.Layout)
	}
	return fmt.Sprintf("%d slides: %s", len(d.Slides), strings.Join(names, " "))
}

// repositoryRoot is how a deck at decks/<language>/<name>.md reaches the
// root of the repository. A built file sits as deep, at
// dist/<language>/<name>.pptx, so the relative links work inside a
// checkout; a copy elsewhere, such as a CI artifact, needs them absolute.
const repositoryRoot = "../../../"

func absoluteLinks(pages []layout.Page, repository string) {
	repository = strings.TrimRight(repository, "/") + "/"
	fix := func(runs []layout.Run) {
		for i := range runs {
			if strings.HasPrefix(runs[i].Href, repositoryRoot) {
				runs[i].Href = repository + strings.TrimPrefix(runs[i].Href, repositoryRoot)
			}
		}
	}
	for _, page := range pages {
		for _, e := range page.Elements {
			switch e := e.(type) {
			case *layout.TextBox:
				for _, p := range e.Paragraphs {
					fix(p.Runs)
				}
			case *layout.Table:
				for _, row := range e.Rows {
					for _, p := range row.Cells {
						fix(p.Runs)
					}
				}
			}
		}
	}
}
