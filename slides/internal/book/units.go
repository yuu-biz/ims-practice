package book

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Unit is one thing that the book explains, followed across the chapter,
// the scenes, the code, the tests and the specifications that explain
// or show it. The units are a record for the authors: when a file of a
// unit changes, the explanation of the unit is read again.
type Unit struct {
	ID       string            `yaml:"id"`
	Question map[string]string `yaml:"question"`
	// Setup is the configuration that the explanation is about, such as
	// the softphone that registers with SIP Digest on deploy/compose.
	Setup string `yaml:"setup"`
	// Chapter is the name of the chapter that explains the unit.
	Chapter string `yaml:"chapter"`
	// Scenes are <deck>/<scene> of the decks that show the unit.
	Scenes []string `yaml:"scenes"`
	// Code and Tests are <file>:<name> of the functions, the types and
	// the tests that the chapter explains or quotes.
	Code  []string `yaml:"code"`
	Tests []string `yaml:"tests"`
	// Standards are the documents, their versions and the clauses that
	// the chapter states, as checked in the documents.
	Standards []string `yaml:"standards"`
	// Evidence says what the explanation rests on: code that the author
	// read, tests that the author ran, a capture, or an illustration.
	Evidence []string `yaml:"evidence"`
	// Verified is the commit of the code that the author checked the
	// explanation against.
	Verified string `yaml:"verified"`
}

var evidence = []string{"code", "test", "capture", "illustration"}

// LoadUnits reads the record of the units.
func LoadUnits(path string) ([]Unit, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record struct {
		Units []Unit `yaml:"units"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(text))
	decoder.KnownFields(true)
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return record.Units, nil
}

// CheckUnits reports a unit that names a chapter, a scene, a function, a
// type or a test that does not exist, or that misses what each unit
// has to say.
func CheckUnits(b *Book, units []Unit) []error {
	var problems []error
	seen := map[string]bool{}
	decks := deckCache{root: b.Root}
	for _, u := range units {
		where := "unit " + u.ID
		if u.ID == "" || seen[u.ID] {
			problems = append(problems, fmt.Errorf("%s: a unit has an id of its own", where))
		}
		seen[u.ID] = true
		for _, lang := range Languages {
			if strings.TrimSpace(u.Question[lang]) == "" {
				problems = append(problems, fmt.Errorf("%s: no question in %s", where, lang))
			}
		}
		if u.Setup == "" || u.Verified == "" || len(u.Evidence) == 0 {
			problems = append(problems, fmt.Errorf("%s: setup, evidence and verified are required", where))
		}
		for _, e := range u.Evidence {
			if !slices.Contains(evidence, e) {
				problems = append(problems, fmt.Errorf("%s: evidence is one of %s, not %q", where, strings.Join(evidence, ", "), e))
			}
		}
		if !slices.Contains(b.Config.Chapters, u.Chapter) {
			problems = append(problems, fmt.Errorf("%s: no chapter %q", where, u.Chapter))
		}
		for _, s := range u.Scenes {
			name, scene, ok := strings.Cut(s, "/")
			d, err := decks.get(Languages[0], name)
			if !ok || err != nil {
				problems = append(problems, fmt.Errorf("%s: no deck for the scene %q", where, s))
				continue
			}
			found := false
			for _, slide := range d.Slides {
				found = found || (slide.Scene != nil && slide.Scene.ID == scene)
			}
			if !found {
				problems = append(problems, fmt.Errorf("%s: %s has no scene %s", where, DeckPath(Languages[0], name), scene))
			}
		}
		for _, ref := range append(slices.Clone(u.Code), u.Tests...) {
			if err := checkDefinition(b.Root, ref); err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", where, err))
			}
		}
	}
	return problems
}

// checkDefinition reports a <file>:<name> whose file does not define a
// function, a method, a type, a variable or a constant of that name.
func checkDefinition(root, ref string) error {
	file, name, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("%q is <file>:<name>", ref)
	}
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return fmt.Errorf("%s does not exist", file)
	}
	q := regexp.QuoteMeta(name)
	definition := regexp.MustCompile(`(?m)^(func (\([^)]*\) )?` + q + `[\[(]|type ` + q + `\b|\t` + q + `\s+(=|[A-Za-z*\[])|(var|const) ` + q + `\b)`)
	if !definition.Match(text) {
		return fmt.Errorf("%s defines no %s", file, name)
	}
	return nil
}

// Stale lists, for each unit, the files of its code and its tests that
// changed since the commit that the unit was verified against, as git
// tells. The chapter and the decks change with the book itself, so they
// are left out. A unit whose commit git does not have is listed with
// that reason, since nothing can be said about it.
func Stale(root string, units []Unit) []string {
	var out []string
	for _, u := range units {
		var code []string
		for _, ref := range append(slices.Clone(u.Code), u.Tests...) {
			file, _, _ := strings.Cut(ref, ":")
			code = append(code, file)
		}
		slices.Sort(code)
		code = slices.Compact(code)
		if err := exec.Command("git", "-C", root, "cat-file", "-e", u.Verified+"^{commit}").Run(); err != nil {
			out = append(out, fmt.Sprintf("%s: git does not have the commit %s that it was verified against", u.ID, u.Verified))
			continue
		}
		args := append([]string{"-C", root, "diff", "--name-only", u.Verified, "--"}, code...)
		changed, err := exec.Command("git", args...).Output()
		if err != nil {
			out = append(out, fmt.Sprintf("%s: git diff failed: %v", u.ID, err))
			continue
		}
		if files := strings.Fields(string(changed)); len(files) > 0 {
			out = append(out, fmt.Sprintf("%s: %s changed since %s; read the chapter %s again", u.ID, strings.Join(files, ", "), u.Verified[:min(12, len(u.Verified))], u.Chapter))
		}
	}
	return out
}
