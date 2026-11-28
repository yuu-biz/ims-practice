package book

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func demoDeck(lang string) string {
	return "---\ntitle: Demo\nfooter: F\nlang: " + lang + "\nformat: visual\n---\n\n" +
		"```ims-model\n" +
		"nodes:\n" +
		"  - {id: a, label: A, at: [0, 0]}\n" +
		"  - {id: b, label: B, at: [1, 0]}\n" +
		"events:\n" +
		"  - {id: req, msg: REQ, from: a, to: b, proto: SIP, fields: [{Request-URI: \"`sip:b`\"}]}\n" +
		"  - {id: ans, msg: 200 OK, from: b, to: a, proto: SIP}\n" +
		"```\n\n---\n\n# Demo\n\n---\n\n## A request\n\n" +
		"```ims-scene\n" +
		"id: flow\n" +
		"steps:\n" +
		"  - {id: send, title: Send, show: [req], text: A sends.}\n" +
		"  - {id: answer, title: Answer, show: [ans], text: B answers.}\n" +
		"```\n"
}

const code = "package x\n\n" +
	"// Hello greets.\n" +
	"func Hello() string {\n" +
	"\treturn \"hello\"\n" +
	"}\n"

// chapterOne shows a figure, quotes the code and links to a heading of
// the second chapter.
func chapterOne(title string) string {
	return "# " + title + "\n\n" +
		"Text.\n\n" +
		"![A sends a request](figures/demo.flow.send.svg)\n\n" +
		"[internal/x/x.go](../../../internal/x/x.go)\n\n" +
		"```go\n" +
		"func Hello() string {\n" +
		"\treturn \"hello\"\n" +
		"```\n\n" +
		"See [the details](two.md#details).\n"
}

func chapterTwo(title string) string {
	return "# " + title + "\n\nText.\n\n## Details\n\nMore text.\n"
}

const bookYAML = "slug: demo-book-of-tests\n" +
	"title:\n  ja: デモの本\n  en: A demo book\n" +
	"summary:\n  ja: 要約\n  en: A summary\n" +
	"topics: [ims, sip]\n" +
	"chapters:\n  - one\n  - two\n"

const unitsYAML = "units:\n" +
	"  - id: hello\n" +
	"    question:\n      ja: なぜ挨拶するのか\n      en: Why does it greet\n" +
	"    setup: a demo\n" +
	"    chapter: one\n" +
	"    scenes: [demo/flow]\n" +
	"    code: [internal/x/x.go:Hello]\n" +
	"    evidence: [code]\n" +
	"    verified: 0000000000000000000000000000000000000000\n"

// repository builds a repository with a book that is right, its figures
// drawn, and returns its root.
func repository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"internal/x/x.go":         code,
		"slides/decks/ja/demo.md": demoDeck("ja"),
		"slides/decks/en/demo.md": demoDeck("en"),
		"docs/book/book.yaml":     bookYAML,
		"docs/book/units.yaml":    unitsYAML,
		"docs/book/ja/README.md":  "# デモの本\n\n- [一つ目](one.md)\n- [二つ目](two.md)\n",
		"docs/book/en/README.md":  "# A demo book\n\n- [One](one.md)\n- [Two](two.md)\n",
		"docs/book/ja/one.md":     chapterOne("一つ目"),
		"docs/book/en/one.md":     chapterOne("One"),
		"docs/book/ja/two.md":     chapterTwo("二つ目"),
		"docs/book/en/two.md":     chapterTwo("Two"),
	}
	for name, text := range files {
		write(t, root, name, text)
	}
	b := load(t, root)
	if _, err := WriteFigures(b); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, root, name, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func load(t *testing.T, root string) *Book {
	t.Helper()
	b, problems := Load(filepath.Join(root, "docs", "book"), root)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return b
}

// problems are what Load and Check report on the book of root.
func problems(t *testing.T, root string) []string {
	t.Helper()
	b, loaded := Load(filepath.Join(root, "docs", "book"), root)
	if b == nil {
		t.Fatal(loaded)
	}
	var out []string
	for _, err := range append(loaded, Check(b)...) {
		out = append(out, err.Error())
	}
	return out
}

// reportsOnly fails unless got is one problem that says each of want.
func reportsOnly(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("problems = %q, want one that says %q", got, want)
	}
	reports(t, got, want...)
}

// reports fails unless one of got says each of want.
func reports(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, p := range got {
		found := true
		for _, w := range want {
			found = found && strings.Contains(p, w)
		}
		if found {
			return
		}
	}
	t.Errorf("problems = %q, want one that says %q", got, want)
}

// TestRightBookHasNoProblems makes sure that a book whose figures are
// drawn, whose excerpts are in their files and whose links lead
// somewhere passes the check.
func TestRightBookHasNoProblems(t *testing.T) {
	root := repository(t)
	if got := problems(t, root); len(got) > 0 {
		t.Errorf("problems = %q, want none", got)
	}
	for _, lang := range Languages {
		if _, err := os.Stat(filepath.Join(root, "docs", "book", lang, "figures", "demo.flow.send.svg")); err != nil {
			t.Errorf("the figure of %s was not written: %v", lang, err)
		}
	}
}

// TestCheckReportsWhatIsWrong makes sure that each way a book can go
// wrong is reported, and nothing else: a book that a reader would find
// broken does not pass.
func TestCheckReportsWhatIsWrong(t *testing.T) {
	ja := filepath.Join("docs", "book", "ja")
	t.Run("an excerpt line that is not in the file", func(t *testing.T) {
		root := repository(t)
		chapter := filepath.Join(root, ja, "one.md")
		write(t, root, filepath.Join(ja, "one.md"), strings.Replace(read(t, chapter), `return "hello"`, `return "bye"`, 1))
		reportsOnly(t, problems(t, root), filepath.Join(ja, "one.md")+":9:", "func Hello() string {", "not in internal/x/x.go")
	})
	// A fence with more than a language after it, as Zenn writes a file
	// name, or indented as in a list, still opens a code block: an
	// excerpt under it is checked, and the prose after it is read.
	for _, fence := range []string{"```go:x.go", "```go title=x", "   ```go"} {
		t.Run("an excerpt under the fence "+fence, func(t *testing.T) {
			root := repository(t)
			chapter := filepath.Join(root, ja, "one.md")
			text := strings.Replace(read(t, chapter), "```go\n", fence+"\n", 1)
			text = strings.Replace(text, `return "hello"`, `return "bye"`, 1)
			write(t, root, filepath.Join(ja, "one.md"), text+"\nSee [nothing](nowhere.md).\n")
			got := problems(t, root)
			reports(t, got, "not in internal/x/x.go")
			reports(t, got, "nowhere.md does not exist")
		})
	}
	t.Run("a figure file that differs from its drawing", func(t *testing.T) {
		root := repository(t)
		write(t, root, filepath.Join(ja, "figures", "demo.flow.send.svg"), "<svg xmlns=\"http://www.w3.org/2000/svg\"/>\n")
		reportsOnly(t, problems(t, root), filepath.Join(ja, "one.md")+":5:", "demo.flow.send.svg is not the drawing")
	})
	t.Run("a figure file that no chapter shows", func(t *testing.T) {
		root := repository(t)
		write(t, root, filepath.Join(ja, "figures", "demo.flow.answer.svg"), "<svg/>\n")
		reportsOnly(t, problems(t, root), "demo.flow.answer.svg", "no chapter shows")
	})
	t.Run("a link to a heading that does not exist", func(t *testing.T) {
		root := repository(t)
		chapter := filepath.Join(root, ja, "one.md")
		write(t, root, filepath.Join(ja, "one.md"), strings.Replace(read(t, chapter), "two.md#details", "two.md#nowhere", 1))
		reportsOnly(t, problems(t, root), filepath.Join(ja, "one.md")+":14:", "no heading #nowhere")
	})
	t.Run("a link to a file that does not exist", func(t *testing.T) {
		root := repository(t)
		chapter := filepath.Join(root, ja, "one.md")
		write(t, root, filepath.Join(ja, "one.md"), strings.Replace(read(t, chapter), "two.md#details", "gone.md", 1))
		reportsOnly(t, problems(t, root), filepath.Join(ja, "one.md")+":14:", "gone.md does not exist")
	})
	t.Run("an English chapter that shows another figure", func(t *testing.T) {
		root := repository(t)
		chapter := filepath.Join(root, "docs", "book", "en", "one.md")
		write(t, root, filepath.Join("docs", "book", "en", "one.md"), strings.Replace(read(t, chapter), "demo.flow.send.svg", "demo.flow.answer.svg", 1))
		// The figure files follow the chapters, so that the only thing
		// left wrong is that the languages show different figures.
		if _, err := WriteFigures(load(t, root)); err != nil {
			t.Fatal(err)
		}
		reportsOnly(t, problems(t, root), "show different figures", "demo.flow.send.svg", "demo.flow.answer.svg")
	})
	t.Run("a table of contents that leaves a chapter out", func(t *testing.T) {
		root := repository(t)
		write(t, root, filepath.Join("docs", "book", "en", "README.md"), "# A demo book\n\n- [One](one.md)\n")
		reportsOnly(t, problems(t, root), filepath.Join("en", "README.md"), "does not link to the chapter two.md")
	})
	t.Run("a chapter file that book.yaml does not list", func(t *testing.T) {
		root := repository(t)
		write(t, root, filepath.Join(ja, "three.md"), chapterTwo("三つ目"))
		reportsOnly(t, problems(t, root), filepath.Join(ja, "three.md"), "not a chapter of book.yaml")
	})
	t.Run("a figure of a scene that does not exist", func(t *testing.T) {
		root := repository(t)
		chapter := filepath.Join(root, ja, "one.md")
		write(t, root, filepath.Join(ja, "one.md"), strings.Replace(read(t, chapter), "demo.flow.send.svg", "demo.nosuch.send.svg", 1))
		reports(t, problems(t, root), filepath.Join(ja, "one.md")+":5:", "no scene nosuch")
	})
	t.Run("a figure of a step that does not exist", func(t *testing.T) {
		root := repository(t)
		chapter := filepath.Join(root, ja, "one.md")
		write(t, root, filepath.Join(ja, "one.md"), strings.Replace(read(t, chapter), "demo.flow.send.svg", "demo.flow.nosuch.svg", 1))
		reports(t, problems(t, root), filepath.Join(ja, "one.md")+":5:", "no step nosuch")
	})
}

// TestCheckUnitsReportsAMissingDefinition makes sure that a unit that
// names a function its file does not define is reported, and that a
// unit whose names all exist is not.
func TestCheckUnitsReportsAMissingDefinition(t *testing.T) {
	root := repository(t)
	b := load(t, root)
	units, err := LoadUnits(filepath.Join(root, "docs", "book", "units.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := CheckUnits(b, units); len(got) > 0 {
		t.Errorf("problems = %v, want none", got)
	}
	units[0].Code = append(units[0].Code, "internal/x/x.go:Goodbye")
	got := CheckUnits(b, units)
	if len(got) != 1 || !strings.Contains(got[0].Error(), "unit hello") || !strings.Contains(got[0].Error(), "defines no Goodbye") {
		t.Errorf("problems = %v, want one that says internal/x/x.go defines no Goodbye", got)
	}
}

// git runs git in root, with a name and an address of its own so that
// it does not need the configuration of the machine.
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	base := []string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// committed is a repository whose files are all in one commit, and
// that commit.
func committed(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := repository(t)
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "book")
	return root, git(t, root, "rev-parse", "HEAD")
}

// TestZennWritesTheBook makes sure that the book of Zenn has the chapters
// in order and unpublished, each chapter titled in its front matter, and
// its links made to work there: figures to the images of the book, code
// to the file at the commit, chapters to their names.
func TestZennWritesTheBook(t *testing.T) {
	root, commit := committed(t)
	out := t.TempDir()
	repo := "https://github.com/o/r/blob/" + commit
	if _, err := Zenn(load(t, root), "ja", out, repo); err != nil {
		t.Fatal(err)
	}
	config := read(t, filepath.Join(out, "books", "demo-book-of-tests", "config.yaml"))
	if !strings.Contains(config, "published: false") || !strings.Contains(config, "chapters:\n  - one\n  - two\n") {
		t.Errorf("config.yaml is not unpublished with the chapters one and two in order:\n%s", config)
	}
	one := read(t, filepath.Join(out, "books", "demo-book-of-tests", "one.md"))
	if !strings.HasPrefix(one, "---\ntitle: \"一つ目\"\n---\n") {
		t.Errorf("the chapter does not start with its title in front matter:\n%s", one)
	}
	for _, line := range strings.Split(one, "\n") {
		if strings.HasPrefix(line, "# ") {
			t.Errorf("the chapter keeps its heading %q, which Zenn shows a second time", line)
		}
	}
	for _, want := range []string{
		"![A sends a request](/images/demo-book-of-tests/demo.flow.send.png)",
		"[internal/x/x.go](" + repo + "/internal/x/x.go)",
		"[the details](two#details)",
		"```go\nfunc Hello() string {\n\treturn \"hello\"\n```",
	} {
		if !strings.Contains(one, want) {
			t.Errorf("the chapter has no %q:\n%s", want, one)
		}
	}
	figure := read(t, filepath.Join(root, "docs", "book", "ja", "figures", "demo.flow.send.svg"))
	if got := read(t, filepath.Join(out, "images", "demo-book-of-tests", "demo.flow.send.svg")); got != figure {
		t.Error("the image of the book is not the figure of the chapter")
	}
}

// TestZennChecksTheExcerptsAtTheCommit makes sure that the book is not
// written when an excerpt is in the working tree but not in the commit
// that its code links point at.
func TestZennChecksTheExcerptsAtTheCommit(t *testing.T) {
	root, commit := committed(t)
	write(t, root, "internal/x/x.go", strings.Replace(code, `return "hello"`, `return "hi"`, 1))
	for _, lang := range Languages {
		name := filepath.Join("docs", "book", lang, "one.md")
		write(t, root, name, strings.Replace(read(t, filepath.Join(root, name)), `return "hello"`, `return "hi"`, 1))
	}
	if got := problems(t, root); len(got) > 0 {
		t.Fatalf("problems = %q; the excerpt is meant to match the working tree", got)
	}
	out := t.TempDir()
	_, err := Zenn(load(t, root), "ja", out, "https://github.com/o/r/blob/"+commit)
	if err == nil || !strings.Contains(err.Error(), "not in internal/x/x.go at "+commit) {
		t.Errorf("err = %v, want the excerpt that is not in internal/x/x.go at the commit", err)
	}
	if _, err := os.Stat(filepath.Join(out, "books")); err == nil {
		t.Error("a book was written although its excerpts do not hold")
	}
}

// TestZennNeedsACommit makes sure that the address of the repository
// names a commit: without one the code links would follow a branch and
// stop matching the excerpts.
func TestZennNeedsACommit(t *testing.T) {
	root := repository(t)
	b := load(t, root)
	for _, repo := range []string{"https://github.com/o/r", "https://github.com/o/r/blob/main"} {
		if _, err := Zenn(b, "ja", t.TempDir(), repo); err == nil {
			t.Errorf("Zenn took %q, which names no commit", repo)
		}
	}
}

// TestStaleListsUnitsWhoseCodeChanged makes sure that a unit is listed
// once a commit changes a file of its code, and not before.
func TestStaleListsUnitsWhoseCodeChanged(t *testing.T) {
	root, commit := committed(t)
	units, err := LoadUnits(filepath.Join(root, "docs", "book", "units.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := Stale(root, units); len(got) != 1 || !strings.Contains(got[0], "hello") || !strings.Contains(got[0], "does not have the commit") {
		t.Errorf("Stale = %q, want the unit hello, whose commit git does not have", got)
	}
	units[0].Verified = commit
	if got := Stale(root, units); len(got) > 0 {
		t.Errorf("Stale = %q with no change, want nothing", got)
	}
	write(t, root, "internal/x/x.go", code+"\nfunc Goodbye() {}\n")
	git(t, root, "commit", "-q", "-am", "change the code")
	got := Stale(root, units)
	if len(got) != 1 || !strings.Contains(got[0], "hello") || !strings.Contains(got[0], "internal/x/x.go") || !strings.Contains(got[0], "one") {
		t.Errorf("Stale = %q, want the unit hello, whose internal/x/x.go changed", got)
	}
}

// TestLoadRefusesNamesThatZennDoesNotTake makes sure that a slug or a
// chapter name that Zenn would refuse, or that would write outside the
// book, stops the book before anything is written.
func TestLoadRefusesNamesThatZennDoesNotTake(t *testing.T) {
	for _, change := range [][2]string{
		{"slug: demo-book-of-tests", "slug: \"\""},
		{"slug: demo-book-of-tests", "slug: ../../outside-of-it"},
		{"  - two\n", "  - one\n"},
	} {
		root := repository(t)
		path := filepath.Join("docs", "book", "book.yaml")
		write(t, root, path, strings.Replace(read(t, filepath.Join(root, path)), change[0], change[1], 1))
		if _, problems := Load(filepath.Join(root, "docs", "book"), root); len(problems) == 0 {
			t.Errorf("book.yaml with %q loads without a problem", change[1])
		}
	}
}

// TestWriteFiguresKeepsWhatIsNoFigure makes sure that drawing the
// figures again removes a figure that no chapter shows, and nothing else.
func TestWriteFiguresKeepsWhatIsNoFigure(t *testing.T) {
	root := repository(t)
	dir := filepath.Join("docs", "book", "ja", "figures")
	write(t, root, filepath.Join(dir, "README.md"), "kept\n")
	write(t, root, filepath.Join(dir, "demo.gone.step.svg"), "<svg/>\n")
	if _, err := WriteFigures(load(t, root)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, dir, "README.md")); err != nil {
		t.Errorf("the README in figures/ is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, dir, "demo.gone.step.svg")); err == nil {
		t.Error("a figure that no chapter shows is still there")
	}
}

// TestZennLeavesInlineCodeAsItIs makes sure that the Markdown of a link
// that a chapter shows as inline code reaches the reader as written,
// while a link whose text is code is made to work in the book.
func TestZennLeavesInlineCodeAsItIs(t *testing.T) {
	root, _ := committed(t)
	path := filepath.Join("docs", "book", "ja", "two.md")
	quoted := "Write `[the file](../../../internal/x/x.go)` to link it, as [`x.go`](../../../internal/x/x.go) does.\n"
	write(t, root, path, read(t, filepath.Join(root, path))+"\n"+quoted)
	git(t, root, "commit", "-q", "-am", "quote")
	commit := git(t, root, "rev-parse", "HEAD")
	out := t.TempDir()
	if _, err := Zenn(load(t, root), "ja", out, "https://github.com/o/r/blob/"+commit); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(out, "books", "demo-book-of-tests", "two.md"))
	want := "Write `[the file](../../../internal/x/x.go)` to link it, as [`x.go`](https://github.com/o/r/blob/" + commit + "/internal/x/x.go) does."
	if !strings.Contains(got, want) {
		t.Errorf("the chapter has no line %q:\n%s", want, got)
	}
}

// TestCheckUnitsWantsAFixedCommitAndADefinition makes sure that a unit
// verified against a name that moves, such as HEAD, is reported, and
// that a struct field of the same name is no definition of a function.
func TestCheckUnitsWantsAFixedCommitAndADefinition(t *testing.T) {
	root := repository(t)
	write(t, root, "internal/x/y.go", "package x\n\ntype T struct {\n\tGoodbye string\n}\n")
	b := load(t, root)
	units, err := LoadUnits(filepath.Join(root, "docs", "book", "units.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	units[0].Verified = "HEAD"
	reports(t, errorStrings(CheckUnits(b, units)), "verified is the 40 hexadecimal digits")
	units[0].Verified = strings.Repeat("0", 40)
	units[0].Code = append(units[0].Code, "internal/x/y.go:Goodbye")
	reports(t, errorStrings(CheckUnits(b, units)), "internal/x/y.go defines no Goodbye")
}

func errorStrings(errs []error) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}
