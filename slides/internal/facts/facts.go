// Package facts checks the commands that the decks tell a learner to
// run against the repository: the services and profiles of the Compose
// files, the subcommands and flags of the UE simulator, the HTTP routes
// that a curl reaches, and the scripts. A command that the code no
// longer takes fails the check, as a link to a renamed heading does.
//
// Each fact is read from where the learner meets it: the Compose files
// as they are, the subcommands and flags from "ue SUBCOMMAND -h", and the
// routes from the source of the package that serves them. The slides
// tool imports none of the packages of the nodes.
package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// Repository is what the commands of the decks may name. Each part is
// read the first time a command needs it, so that a deck without
// commands, such as one in a test, needs nothing of the repository.
type Repository struct {
	root    string
	compose map[string]*composeFile
	ue      *ueCommand
	routes  map[string][]route
	failed  map[string]bool
}

// NewRepository returns the facts of the repository at root.
func NewRepository(root string) *Repository {
	return &Repository{root: root, compose: map[string]*composeFile{}, routes: map[string][]route{}, failed: map[string]bool{}}
}

// Check reports each command of a deck that names a service, a profile,
// a subcommand, a flag, a route or a script that the repository does
// not have.
func (r *Repository) Check(d *deck.Deck) []error {
	var problems []error
	for _, c := range commands(d) {
		for _, p := range r.checkCommand(c.words) {
			problems = append(problems, fmt.Errorf("%s: slide %d: %s: %s", d.Path, c.slide, strings.Join(c.words, " "), p))
		}
	}
	for _, s := range scripts(d) {
		if p := r.checkScript(s.path); p != "" {
			problems = append(problems, fmt.Errorf("%s: slide %d: %s", d.Path, s.slide, p))
		}
	}
	return problems
}

// command is one simple command of a deck, split into words as a shell
// splits it.
type command struct {
	slide int
	words []string
}

// commands returns the commands of the shell blocks (```sh) of a deck
// and of its inline code. A line that ends with a backslash goes on in
// the next line; &&, ||, | and ; separate commands.
func commands(d *deck.Deck) []command {
	var out []command
	add := func(slide int, line string) {
		for _, words := range splitCommands(shellWords(line)) {
			out = append(out, command{slide, words})
		}
	}
	for _, slide := range d.Slides {
		for _, column := range slide.Columns {
			for _, b := range column {
				code, ok := b.(deck.Code)
				if !ok || code.Info != "sh" {
					continue
				}
				line := ""
				for _, l := range code.Lines {
					if strings.HasSuffix(l, `\`) {
						line += strings.TrimSuffix(l, `\`) + " "
						continue
					}
					add(slide.Number, line+l)
					line = ""
				}
				add(slide.Number, line)
			}
		}
		for _, spans := range deck.Lines(slide) {
			for _, span := range spans {
				if span.Code {
					add(slide.Number, span.Text)
				}
			}
		}
	}
	return out
}

// shellWords splits a line into words as a POSIX shell does, as far as
// the decks need: single and double quotes, the backslash, a comment
// from a word that starts with #, and the operators &&, ||, | and ;.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord := false
	end := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			end()
		case c == '#' && !inWord:
			return words
		case c == '\'':
			inWord = true
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				j = len(line) - i - 1
			}
			word.WriteString(line[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inWord = true
			for i++; i < len(line) && line[i] != '"'; i++ {
				if line[i] == '\\' && i+1 < len(line) && strings.IndexByte(`"\$`+"`", line[i+1]) >= 0 {
					i++
				}
				word.WriteByte(line[i])
			}
		case c == '\\' && i+1 < len(line):
			inWord = true
			i++
			word.WriteByte(line[i])
		case c == '&' && strings.HasPrefix(line[i:], "&&"), c == '|' && strings.HasPrefix(line[i:], "||"):
			end()
			words = append(words, line[i:i+2])
			i++
		case c == '|' || c == ';':
			end()
			words = append(words, string(c))
		default:
			inWord = true
			word.WriteByte(c)
		}
	}
	end()
	return words
}

func splitCommands(words []string) [][]string {
	var out [][]string
	start := 0
	for i := 0; i <= len(words); i++ {
		if i == len(words) || words[i] == "&&" || words[i] == "||" || words[i] == "|" || words[i] == ";" {
			if i > start {
				out = append(out, words[start:i])
			}
			start = i + 1
		}
	}
	return out
}

var environmentAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// checkCommand returns what is wrong with one command, or nothing for a
// command that this package does not check.
func (r *Repository) checkCommand(words []string) []string {
	for len(words) > 0 && environmentAssignment.MatchString(words[0]) {
		words = words[1:]
	}
	switch {
	case len(words) >= 2 && words[0] == "docker" && words[1] == "compose":
		return r.checkCompose(words[2:])
	case len(words) >= 1 && words[0] == "curl":
		return r.checkCurl(words[1:])
	case len(words) >= 1 && words[0] == "ue":
		return r.checkUE(words[1:])
	case len(words) >= 3 && words[0] == "go" && words[1] == "run" && strings.TrimSuffix(words[2], "/") == "./cmd/ue":
		return r.checkUE(words[3:])
	}
	return nil
}

// once runs read, which reads a part of the repository and keeps it,
// unless read failed for the same part before. Only the first failure
// is returned as a problem, not one for every command that needs it.
func (r *Repository) once(what string, read func() error) (ok bool, problems []string) {
	if r.failed[what] {
		return false, nil
	}
	if err := read(); err != nil {
		r.failed[what] = true
		return false, []string{err.Error()}
	}
	return true, nil
}

// script is a path under scripts/ that a deck names.
type script struct {
	slide int
	path  string
}

var scriptPath = regexp.MustCompile(`(?:^|[^\w./-])(scripts/[\w.*/-]*)`)

// scripts returns the paths under scripts/ in the shell blocks and the
// inline code of a deck.
func scripts(d *deck.Deck) []script {
	var out []script
	find := func(slide int, text string) {
		for _, m := range scriptPath.FindAllStringSubmatch(text, -1) {
			out = append(out, script{slide, strings.TrimRight(m[1], ".")})
		}
	}
	for _, slide := range d.Slides {
		for _, column := range slide.Columns {
			for _, b := range column {
				if code, ok := b.(deck.Code); ok && code.Info == "sh" {
					for _, l := range code.Lines {
						find(slide.Number, l)
					}
				}
			}
		}
		for _, spans := range deck.Lines(slide) {
			for _, span := range spans {
				if span.Code {
					find(slide.Number, span.Text)
				}
			}
		}
	}
	return out
}

// checkScript returns what is wrong with a path under scripts/: a file
// or a directory that does not exist, or a pattern such as
// scripts/e2e-*.sh that matches nothing.
func (r *Repository) checkScript(path string) string {
	full := filepath.Join(r.root, filepath.FromSlash(path))
	if strings.Contains(path, "*") {
		if matches, _ := filepath.Glob(full); len(matches) == 0 {
			return path + " matches no file"
		}
		return ""
	}
	if _, err := os.Stat(full); err != nil {
		return path + " does not exist"
	}
	return ""
}
