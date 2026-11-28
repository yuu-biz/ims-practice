// Command slides builds the decks and the book: it reads each source
// under decks/<language>/, lays out its slides, checks them, and writes a
// pptx file and an HTML file for each deck. It also reads the chapters of
// the book under docs/book/, checks them, and draws their figures from
// the scenes of the decks. Run it in the directory slides/:
//
//	go run ./cmd/slides check     # check every deck and the book
//	go run ./cmd/slides build     # check, then write dist/<language>/
//	go run ./cmd/slides figures   # draw the figures of the book again
//	go run ./cmd/slides -repo-url https://github.com/<owner>/<repository>/blob/<commit> zenn
//	                              # write the book for Zenn to dist/zenn/
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

	"github.com/yuu-biz/ims-practice/slides/internal/book"
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
	bookDir := flags.String("book", "../docs/book", "the directory of the book, one subdirectory per language")
	unitsFile := flags.String("units", "../docs/book/units.yaml", "the record of what each part of the book explains and rests on")
	lang := flags.String("lang", "ja", "the language of the book that zenn writes")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "usage: slides [flags] check|build|figures|zenn\n")
		flags.PrintDefaults()
	}
	flags.Parse(os.Args[1:])
	command := flags.Arg(0)
	if flags.NArg() != 1 || !slices.Contains([]string{"check", "build", "figures", "zenn"}, command) {
		flags.Usage()
		os.Exit(2)
	}
	root := filepath.Join(*decks, "..", "..")
	switch command {
	case "figures":
		b, problems := book.Load(*bookDir, root)
		exitOn(problems)
		written, err := book.WriteFigures(b)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%d figure files written or removed\n", len(written))
		for _, w := range written {
			fmt.Println(w)
		}
		return
	case "zenn":
		b, problems := book.Load(*bookDir, root)
		exitOn(problems)
		exitOn(book.Check(b))
		written, err := book.Zenn(b, *lang, filepath.Join(*out, "zenn"), *repository)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(strings.Join(written, "\n"))
		fmt.Printf("then, in slides/: node book-images.js %s\n", filepath.Join(*out, "zenn"))
		return
	}
	all, problems := load(*decks)
	b, bookProblems := book.Load(*bookDir, root)
	problems = append(problems, bookProblems...)
	var units []book.Unit
	if b != nil {
		problems = append(problems, book.Check(b)...)
		var err error
		if units, err = book.LoadUnits(*unitsFile); err != nil {
			problems = append(problems, err)
		} else {
			problems = append(problems, book.CheckUnits(b, units)...)
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		fmt.Fprintf(os.Stderr, "%d problems\n", len(problems))
		os.Exit(1)
	}
	if command == "check" {
		for _, d := range all {
			fmt.Println(summary(d))
		}
		fmt.Printf("%d decks: every slide parses, fits, links to what exists, quotes code that is in its file, names commands that the repository takes, and gives the nodes the addresses of the Compose files\n", len(all))
		fmt.Printf("%d chapters in each of %s: every link reaches its file and heading, every excerpt is in its file, every figure is the drawing of its step, and the languages show the same figures and code\n",
			len(b.Config.Chapters), strings.Join(book.Languages, ", "))
		for _, s := range book.Stale(root, units) {
			fmt.Println("to read again: " + s)
		}
		return
	}
	for _, d := range all {
		if *repository != "" {
			absoluteLinks(d.pages, *repository)
		}
		written, err := build(d, all, *out, *repository)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(strings.Join(written, "\n"))
	}
}

// exitOn prints the problems and stops, if there are any.
func exitOn(problems []error) {
	if len(problems) == 0 {
		return
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	fmt.Fprintf(os.Stderr, "%d problems\n", len(problems))
	os.Exit(1)
}

// build writes the outputs of one deck and returns their paths.
func build(d laidOut, all []laidOut, out, repository string) ([]string, error) {
	dir := filepath.Join(out, d.deck.Lang)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(d.deck.Path), ".md")
	options := html.Options{Repository: repository}
	// The HTML links to the same deck in the other language, at the
	// same scene and step: the ids are the same in both.
	for _, other := range all {
		if other.deck.Lang != d.deck.Lang && filepath.Base(other.deck.Path) == filepath.Base(d.deck.Path) {
			options.Translation = "../" + other.deck.Lang + "/" + name + ".html"
			options.TranslationLang = other.deck.Lang
		}
	}
	writers := []struct {
		extension string
		write     func(io.Writer, *deck.Deck, []layout.Page) error
	}{
		{".pptx", pptx.Write},
		{".html", func(w io.Writer, d *deck.Deck, pages []layout.Page) error {
			return html.WriteWith(w, d, pages, options)
		}},
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
		problems = append(problems, deck.CheckHeadings(d)...)
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
			for _, p := range compareStructure(structureOf(first), structureOf(other)) {
				problems = append(problems, fmt.Errorf("%s and %s differ in their model or their scenes: %s", first.Path, other.Path, p))
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

// repositoryRoot is how a deck reaches the root of the repository.
const repositoryRoot = html.RepositoryRoot

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
